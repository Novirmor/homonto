package permevent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// PinnedOpencodeRevision is the upstream OpenCode commit the event contract
// below was verified against (packages/opencode/src/permission/index.ts,
// packages/schema/src/v1/permission.ts, packages/opencode/src/plugin/index.ts).
// A fixture/regression mismatch against a newer revision must update this pin
// deliberately, never silently.
const PinnedOpencodeRevision = "50efc055de282e0e54a87ccebb8e2054cc45efd2"

// Event names as OpenCode spells them.
const (
	EventAsked   = "permission.asked"
	EventReplied = "permission.replied"
)

// Decision values carried by permission.replied.
const (
	ReplyOnce    = "once"
	ReplyAlways  = "always"
	ReplyReject  = "reject"
)

// Asked is the payload of permission.asked: the full request, including the
// bash command under metadata.command and the session the ask belongs to.
type Asked struct {
	Type       string            `json:"type"`
	Properties AskedProperties   `json:"properties"`
}

type AskedProperties struct {
	ID         string            `json:"id"`
	SessionID  string            `json:"sessionID"`
	Permission string            `json:"permission"`
	Patterns   []string          `json:"patterns"`
	Metadata   map[string]any    `json:"metadata,omitempty"`
	Always     []string          `json:"always,omitempty"`
}

// Replied is the payload of permission.replied: the authoritative user
// decision for a previously asked request, on the same session.
type Replied struct {
	Type       string           `json:"type"`
	Properties RepliedProperties `json:"properties"`
}

type RepliedProperties struct {
	SessionID string `json:"sessionID"`
	RequestID string `json:"requestID"`
	Reply     string `json:"reply"`
}

// Decision is one correlated user decision: which command, on which session,
// allowed or denied. Approval is ReplyOnce or ReplyAlways; denial is
// ReplyReject. Replied alone carries no command, so decisions are only ever
// produced by correlating an Asked first — execution telemetry is never a
// substitute.
type Decision struct {
	SessionID  string
	RequestID  string
	Permission string
	Command    string
	Approval   bool
	Always     bool
}

// Correlator folds an ordered event stream into decisions. It fails closed:
// a reply without a matching ask, an unknown event type, or a malformed
// payload is an error, never a silent drop.
type Correlator struct {
	pending map[string]AskedProperties
}

func NewCorrelator() *Correlator {
	return &Correlator{pending: map[string]AskedProperties{}}
}

// Feed consumes one raw event. It returns a Decision when the event completes
// an ask/reply pair, otherwise nil.
func (c *Correlator) Feed(line []byte) (*Decision, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, fmt.Errorf("permevent: malformed event: %w", err)
	}
	switch probe.Type {
	case EventAsked:
		var ev Asked
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("permevent: malformed %s: %w", EventAsked, err)
		}
		p := ev.Properties
		if p.ID == "" || p.SessionID == "" || p.Permission == "" {
			return nil, fmt.Errorf("permevent: %s missing id, sessionID, or permission", EventAsked)
		}
		c.pending[p.ID] = p
		return nil, nil
	case EventReplied:
		var ev Replied
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("permevent: malformed %s: %w", EventReplied, err)
		}
		p := ev.Properties
		ask, ok := c.pending[p.RequestID]
		if !ok {
			return nil, fmt.Errorf("permevent: %s for unknown request %s", EventReplied, p.RequestID)
		}
		delete(c.pending, p.RequestID)
		switch p.Reply {
		case ReplyOnce, ReplyAlways, ReplyReject:
		default:
			return nil, fmt.Errorf("permevent: unknown reply %q", p.Reply)
		}
		if p.SessionID != ask.SessionID {
			return nil, fmt.Errorf("permevent: %s session %s does not match ask session %s", EventReplied, p.SessionID, ask.SessionID)
		}
		return &Decision{
			SessionID:  ask.SessionID,
			RequestID:  ask.RequestID(),
			Permission: ask.Permission,
			Command:    ask.MetadataCommand(),
			Approval:   p.Reply != ReplyReject,
			Always:     p.Reply == ReplyAlways,
		}, nil
	default:
		return nil, fmt.Errorf("permevent: unknown event type %q", probe.Type)
	}
}

// RequestID returns the originating ask's id.
func (a AskedProperties) RequestID() string { return a.ID }

// MetadataCommand extracts metadata.command, the full bash command string.
func (a AskedProperties) MetadataCommand() string {
	if a.Metadata == nil {
		return ""
	}
	if v, ok := a.Metadata["command"].(string); ok {
		return v
	}
	return ""
}

// ParseStream reads newline-delimited events and emits each correlated
// decision. Order is preserved.
func ParseStream(r io.Reader) ([]Decision, error) {
	c := NewCorrelator()
	var out []Decision
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		d, err := c.Feed([]byte(line))
		if err != nil {
			return nil, err
		}
		if d != nil {
			out = append(out, *d)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
