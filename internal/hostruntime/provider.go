// Package hostruntime is the host-neutral draft and publication core behind
// the hosting workflows (ADR 0090). It owns session identity, native-question
// approval correlation, TTL, capacities, invalidation, freshness and
// uncertain-send reconciliation (ADR 0059, ADR 0060). A Provider owns one
// host's transport: target and origin parsing, repository and actor identity,
// destination snapshots, publication and receipt reconciliation.
//
// The core never executes a process or asks for a permission itself. Every
// command and every native permission request goes through the harness shim
// (Call), so the shim's runner limits and the coordinator's host-CLI-only
// posture (ADR 0047) stay where they are.
package hostruntime

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

// Item kinds and review events are host-neutral vocabulary. "pr" names the
// change request on every host (ADR 0089): a GitLab merge request is a pr here.
const (
	KindIssueComment = "issue_comment"
	KindPRComment    = "pr_comment"
	KindPRReview     = "pr_review"

	EventComment        = "COMMENT"
	EventRequestChanges = "REQUEST_CHANGES"
)

// InputItem is one requested publication, exactly as the model supplied it.
type InputItem struct {
	Kind        string `json:"kind"`
	URL         string `json:"url"`
	Body        string `json:"body"`
	BaseOID     string `json:"baseOID"`
	HeadOID     string `json:"headOID"`
	ReviewEvent string `json:"reviewEvent"`
}

// Target is a parsed destination. Type is the provider's path segment for the
// destination kind (GitHub: "issues" or "pull"); Change reports whether it is a
// change request rather than an issue.
type Target struct {
	Host   string
	Repo   string
	Number int64
	Type   string
	Change bool
	URL    string
}

// ID is a host identifier kept as its canonical JSON text, so numeric (GitHub,
// GitLab) and string (Bitbucket UUID) identities compare and render exactly.
type ID string

func (i ID) MarshalJSON() ([]byte, error) { return []byte(i), nil }

// NumberID renders a positive integer identifier.
func NumberID(n int64) ID { b, _ := json.Marshal(n); return ID(b) }

// StringID renders a string identifier.
func StringID(s string) ID { b, _ := json.Marshal(s); return ID(b) }

// Repo is a repository identity. Field order is the preview's JSON order.
type Repo struct {
	Host string `json:"host"`
	ID   ID     `json:"id"`
	Name string `json:"name"`
}

// Actor is the authenticated identity that publishes.
type Actor struct {
	ID    ID     `json:"id"`
	Login string `json:"login"`
}

// Snapshot is the destination state a draft was reviewed against. Publication
// requires a fresh snapshot equal to it, field for field. Extra carries any
// provider-specific freshness evidence.
type Snapshot struct {
	Repo      Repo
	Actor     Actor
	ID        ID
	Number    int64
	State     string // "open" or "closed", normalized by the provider
	Title     string
	Body      *string
	UpdatedAt string
	BaseOID   string
	HeadOID   string
	Locked    bool
	Merged    bool
	Draft     bool
	Extra     string
}

func (s Snapshot) equal(o Snapshot) bool {
	if (s.Body == nil) != (o.Body == nil) || (s.Body != nil && *s.Body != *o.Body) {
		return false
	}
	s.Body, o.Body = nil, nil
	return s == o
}

// Receipt identifies one remote publication.
type Receipt struct {
	ID  ID     `json:"id"`
	URL string `json:"url"`
}

// Item is a staged publication.
type Item struct {
	InputItem
	ItemID   string
	Target   Target
	Snapshot Snapshot
	Payload  string
	Status   string
	Receipt  *Receipt
	Reason   string
}

// Transport runs one host or workspace command through the harness shim.
type Transport interface {
	// Run executes argv (stdin nil means no input) and returns its stdout.
	Run(argv []string, stdin *string) (string, error)
}

// Provider is one host's side of the draft/publication boundary.
type Provider interface {
	// ID names the host in tool and permission names ("github").
	ID() string
	// Label names the host in progress stages and messages ("GitHub").
	Label() string
	// CLI is the host CLI's executable; reads through it report "Reading <Label>".
	CLI() string
	// ParseTarget validates and parses a destination URL.
	ParseTarget(raw string) (Target, error)
	// ValidateItem checks the kind-specific fields of one input against its target.
	ValidateItem(in InputItem, t Target) error
	// Origin parses a source checkout's origin remote into host and repository path.
	Origin(remote string) (host, repo string, err error)
	// Repository resolves a repository's canonical identity.
	Repository(x Transport, host, name string) (Repo, error)
	// Actor resolves the authenticated publisher.
	Actor(x Transport, host string) (Actor, error)
	// Destination reads the target's current state. Repo and Actor are set by the core.
	Destination(x Transport, t Target, r Repo) (Snapshot, error)
	// CanonicalURL rebuilds a target URL from its canonical repository path.
	CanonicalURL(t Target, repo string) string
	// Payload is the request body that publishes an item (body already final).
	Payload(i *Item) (string, error)
	// PublishCommand is the command the publication permission request names.
	PublishCommand(i *Item) []string
	// Publish sends one approved item and validates its receipt.
	Publish(x Transport, i *Item) (Receipt, error)
	// Existing lists remote publications that match an item exactly.
	Existing(x Transport, i *Item) ([]Receipt, error)
}

// Call is one tool invocation's link back to the harness. Every method may
// park the invocation until the shim answers.
type Call interface {
	SessionID() string
	// Run executes a command for this invocation.
	Run(argv []string, stdin *string, timeout time.Duration) (string, error)
	// Ask requests a native harness permission.
	Ask(permission string, patterns []string, metadata map[string]any) error
	// Progress reports a stage; failures and hangs are ignored.
	Progress(stage string)
	// Err reports whether the invocation was aborted.
	Err() error
}

// --- validation helpers shared by providers -----------------------------

func check(ok bool, message string) error {
	if ok {
		return nil
	}
	return errors.New(message)
}

func record(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }

func text(v any) (string, bool) { s, ok := v.(string); return s, ok }

// safeID reports a JSON number that is a positive safe integer.
func safeID(v any) (int64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	if err != nil || f <= 0 || f != math.Trunc(f) || f > (1<<53)-1 {
		return 0, false
	}
	return int64(f), true
}

func boolean(v any) (bool, bool) { b, ok := v.(bool); return b, ok }

func isOID(v any) bool {
	s, ok := v.(string)
	if !ok || len(s) != 40 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// decode parses JSON preserving number text.
func decode(raw string) (any, error) {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	if d.More() {
		return nil, errors.New("unexpected data after JSON document")
	}
	return v, nil
}

// numberEqual compares a decoded JSON value with a canonical ID.
func numberEqual(v any, id ID) bool {
	n, ok := safeID(v)
	return ok && NumberID(n) == id
}

// hostOK accepts a lowercase DNS name with at least one dot that is not an IP.
func hostOK(v string) bool {
	if len(v) > 253 || !strings.Contains(v, ".") {
		return false
	}
	numeric := true
	for _, label := range strings.Split(v, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		allDigits := true
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
			if c < '0' || c > '9' {
				allDigits = false
			}
		}
		numeric = numeric && allDigits
	}
	return !numeric
}

// byteLen is a string's UTF-8 length.
func byteLen(s string) int { return len(s) }
