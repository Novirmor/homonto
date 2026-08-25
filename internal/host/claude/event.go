// Package claude normalizes Claude Code's hook events into Homonto's
// protocol, and renders the responses Claude understands.
//
// Everything here is translation. No decision is made in this package: the
// guard decides whether a write is allowed, the engines decide what
// happens next, and this code turns one shape of JSON into another. That
// separation is why a second host tool costs a file rather than a fork.
package claude

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// Event names the hook events Homonto installs.
const (
	// EventSessionStart fires when a session begins; the resume probe runs
	// there.
	EventSessionStart = "SessionStart"
	// EventPreToolUse fires before a tool runs; the write guard runs
	// there.
	EventPreToolUse = "PreToolUse"
)

// Event is the hook payload Claude writes to a hook's stdin. Only the
// fields Homonto reads are declared; Claude adds more over time, and a
// strict decode would turn every Claude release into a Homonto outage.
type Event struct {
	SessionID     string          `json:"session_id"`
	HookEventName string          `json:"hook_event_name"`
	CWD           string          `json:"cwd"`
	ToolName      string          `json:"tool_name"`
	ToolInput     json.RawMessage `json:"tool_input"`
}

// Decode reads a hook event.
func Decode(r io.Reader) (Event, error) {
	var e Event
	if err := json.NewDecoder(r).Decode(&e); err != nil {
		return Event{}, fmt.Errorf("claude: decode hook event: %w", err)
	}
	if strings.TrimSpace(e.HookEventName) == "" {
		return Event{}, fmt.Errorf("claude: hook event names no event")
	}
	return e, nil
}

// NormalizeEvent turns a PreToolUse event into a guard request.
//
// The session id is the host's, not a Homonto identity, so it is hashed
// into a stable UUID rather than passed through: the protocol requires a
// canonical identifier, and inventing a fresh one per event would make
// every request look like a new session.
func NormalizeEvent(r io.Reader, root string) (protocol.GuardRequest, error) {
	e, err := Decode(r)
	if err != nil {
		return protocol.GuardRequest{}, err
	}
	if e.HookEventName != EventPreToolUse {
		return protocol.GuardRequest{}, fmt.Errorf(
			"claude: %s is not a write hook event; only %s is guarded", e.HookEventName, EventPreToolUse)
	}
	if strings.TrimSpace(e.ToolName) == "" {
		return protocol.GuardRequest{}, fmt.Errorf("claude: %s event names no tool", EventPreToolUse)
	}
	// Write paths are relative to the event's OWN working directory, not
	// to the control root: the guard resolves them against the working
	// directory it is told, and basing them anywhere else composes the
	// prefix twice.
	base := e.CWD
	if base == "" {
		base = root
	}
	paths, err := writePaths(e.ToolName, e.ToolInput, base)
	if err != nil {
		return protocol.GuardRequest{}, err
	}
	return protocol.GuardRequest{
		Host:             protocol.HostClaude,
		SessionID:        identity.SessionIDFor("claude", e.SessionID),
		Tool:             e.ToolName,
		WorkingDirectory: relativeTo(root, e.CWD),
		WritePaths:       paths,
	}, nil
}

// writeTools maps each writing tool to the tool-input fields that carry a
// path. A tool absent from this table writes no file — and one that starts
// writing files without being added here is caught by the final diff gate,
// which is the half of the boundary that does not depend on the host.
var writeTools = map[string][]string{
	"Write":        {"file_path"},
	"Edit":         {"file_path"},
	"MultiEdit":    {"file_path"},
	"NotebookEdit": {"notebook_path"},
}

// writePaths extracts the workspace-relative paths a tool call intends to
// write.
func writePaths(tool string, input json.RawMessage, base string) ([]string, error) {
	fields, writes := writeTools[tool]
	if !writes {
		return nil, nil
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("claude: %s event carries no tool input", tool)
	}
	var decoded map[string]any
	if err := json.Unmarshal(input, &decoded); err != nil {
		return nil, fmt.Errorf("claude: decode %s input: %w", tool, err)
	}
	seen := map[string]bool{}
	var paths []string
	for _, field := range fields {
		value, _ := decoded[field].(string)
		if strings.TrimSpace(value) == "" {
			continue
		}
		rel := relativeTo(base, value)
		if seen[rel] {
			continue
		}
		seen[rel] = true
		paths = append(paths, rel)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("claude: %s event names no path to write", tool)
	}
	sort.Strings(paths)
	return paths, nil
}

// relativeTo expresses a host-supplied path relative to a base directory.
// A path outside the root is returned as given, so the guard refuses it as
// unclean rather than this package silently rewriting it into something
// that looks acceptable.
func relativeTo(root, path string) string {
	if path == "" {
		return "."
	}
	if !filepath.IsAbs(path) {
		return filepath.ToSlash(filepath.Clean(path))
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

// Decision is the PreToolUse response shape Claude reads.
type Decision struct {
	HookSpecificOutput HookOutput `json:"hookSpecificOutput"`
}

// HookOutput carries the permission verdict.
type HookOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

// RenderDecision turns a guard decision into Claude's response shape.
//
// An allowed write returns "allow" rather than staying silent: saying so
// explicitly means a later hook cannot mistake Homonto's silence for
// absence, and the reason gives an operator something to read when they
// wonder why a write went through.
func RenderDecision(d protocol.GuardDecision) ([]byte, error) {
	verdict := "deny"
	if d.Allow {
		verdict = "allow"
	}
	reason := d.Reason
	if !d.Allow {
		reason = fmt.Sprintf("homonto refused this write (%s): %s", d.Code, d.Reason)
	}
	out, err := json.MarshalIndent(Decision{HookOutput{
		HookEventName:            EventPreToolUse,
		PermissionDecision:       verdict,
		PermissionDecisionReason: reason,
	}}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("claude: encode hook decision: %w", err)
	}
	return out, nil
}

// SessionContext is the SessionStart response shape.
type SessionContext struct {
	HookSpecificOutput SessionOutput `json:"hookSpecificOutput"`
}

// SessionOutput carries the context Claude adds to the session.
type SessionOutput struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// RenderProbe turns a probe response into session context. An idle
// workspace adds nothing: a session that starts with no work in progress
// should not begin with a paragraph about there being none.
func RenderProbe(resp protocol.ProbeResponse) ([]byte, error) {
	if resp.State == protocol.ProbeIdle || resp.State == protocol.ProbeUnavailable {
		return nil, nil
	}
	out, err := json.MarshalIndent(SessionContext{SessionOutput{
		HookEventName:     EventSessionStart,
		AdditionalContext: resp.Message,
	}}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("claude: encode session context: %w", err)
	}
	return out, nil
}
