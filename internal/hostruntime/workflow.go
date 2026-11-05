package hostruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// WorkflowService is the harness-independent workflow core (M4.1): the
// coordinator's read-only homonto_status and homonto_handoff tools and the
// worker permission handback (ADR 0077). Harness-specific checks — session
// scope, the coordinator's identity, native permission prompts — stay in the
// shim; the core validates arguments, runs the read commands through the
// Call, and validates their contracts.
type WorkflowService struct {
	configPath string
	configRoot string
	Handback   *Handback
}

// WorkflowProvider is the binding name a shim initializes the workflow core
// with, in place of a hosting provider.
const WorkflowProvider = "workflow"

// toolLimit bounds a tool result the way the plugin always did.
const toolLimit = 16384

// NewWorkflowService binds the core to the selected config.
func NewWorkflowService(configPath string) (*WorkflowService, error) {
	if !pathOK(configPath) {
		return nil, errors.New("bound absolute configPath is required")
	}
	return &WorkflowService{configPath: configPath, configRoot: filepath.Dir(configPath), Handback: NewHandback(4096)}, nil
}

// WorkflowToolDefinitions are the coordinator's read tools.
func WorkflowToolDefinitions() []map[string]any {
	return []map[string]any{
		{"name": "homonto_status", "description": "Read the selected workflow snapshot. Coordinator only; no arguments.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}, "required": []string{}, "additionalProperties": false}},
		{"name": "homonto_handoff", "description": "Read exact-generation recovery artifacts. Coordinator only.",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{
				"workflow": map[string]any{"type": "string", "enum": []string{"onto", "to"}},
				"change":   map[string]any{"type": "string"},
				"identity": map[string]any{"type": "string"},
			}, "required": []string{"workflow", "change", "identity"}, "additionalProperties": false}},
	}
}

func strictArgs(args any, keys ...string) (map[string]any, error) {
	m, ok := record(args)
	if args == nil && len(keys) == 0 {
		return map[string]any{}, nil
	}
	if !ok || len(m) != len(keys) {
		return nil, errors.New("invalid arguments; required keys: " + orNone(keys))
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return nil, errors.New("invalid arguments; required keys: " + orNone(keys))
		}
	}
	return m, nil
}

func orNone(keys []string) string {
	if len(keys) == 0 {
		return "none"
	}
	return strings.Join(keys, ", ")
}

// Tool returns the handler for a workflow tool name.
func (w *WorkflowService) Tool(name string) func(Call, any) (string, error) {
	switch name {
	case "homonto_status":
		return w.Status
	case "homonto_handoff":
		return w.Handoff
	}
	return nil
}

func (w *WorkflowService) read(c Call, tool string) error {
	return c.Ask("homonto_read", []string{w.configPath}, map[string]any{"tool": tool})
}

// SnapshotArgv is the workflow snapshot command every shim runs.
func (w *WorkflowService) SnapshotArgv() []string {
	return []string{"homonto", "workflow", "snapshot", "--json", "--config", w.configPath}
}

// Status reads the selected workflow snapshot.
func (w *WorkflowService) Status(c Call, args any) (string, error) {
	if _, err := strictArgs(args); err != nil {
		return "", err
	}
	if err := w.read(c, "homonto_status"); err != nil {
		return "", err
	}
	raw, err := c.Run(w.SnapshotArgv(), nil, time.Second)
	if err != nil {
		return "", err
	}
	if _, err := ParseSnapshot(raw, w.configPath); err != nil {
		return "", err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(strings.TrimSpace(raw))); err != nil {
		return "", errors.New("invalid workflow snapshot contract")
	}
	return Bounded(compact.String(), toolLimit), nil
}

var changeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// RecoverableIdentity reports whether a generation selection may be named in a
// handoff command: a safe change name and an identity without controls or
// path separators.
func RecoverableIdentity(change, identity string) bool {
	return changeName.MatchString(change) && change != "archive" && identity != "" && len(identity) <= 1024 &&
		!strings.ContainsFunc(identity, func(r rune) bool { return unicode.IsControl(r) || r == '/' || r == '\\' })
}

// Handoff reads one exact generation's recovery artifacts.
func (w *WorkflowService) Handoff(c Call, args any) (string, error) {
	m, err := strictArgs(args, "workflow", "change", "identity")
	if err != nil {
		return "", err
	}
	workflow, _ := text(m["workflow"])
	change, okC := text(m["change"])
	identity, okI := text(m["identity"])
	if (workflow != "onto" && workflow != "to") || !okC || !okI || !RecoverableIdentity(change, identity) {
		return "", errors.New("invalid workflow selection")
	}
	if err := w.read(c, "homonto_handoff"); err != nil {
		return "", err
	}
	raw, err := c.Run([]string{"homonto", "workflow", "handoff", "--workflow", workflow, "--change", change,
		"--identity", identity, "--json", "--config", w.configPath}, nil, 5*time.Second)
	if err != nil {
		return "", err
	}
	var result struct {
		SchemaVersion int    `json:"schemaVersion"`
		ConfigPath    string `json:"configPath"`
		ConfigRoot    string `json:"configRoot"`
		Change        *struct {
			Workflow string `json:"workflow"`
			Name     string `json:"name"`
			Identity string `json:"identity"`
		} `json:"change"`
	}
	if json.Unmarshal([]byte(raw), &result) != nil || result.SchemaVersion != 1 || result.ConfigPath != w.configPath ||
		result.ConfigRoot != w.configRoot || result.Change == nil || result.Change.Workflow != workflow ||
		result.Change.Name != change || result.Change.Identity != identity {
		return "", errors.New("invalid workflow handoff contract")
	}
	return Bounded(raw, toolLimit), nil
}

// Bounded truncates text to limit UTF-8 bytes, ending with a marker, exactly
// as the shims always have.
func Bounded(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	const marker = "\n[truncated; inspect the workflow snapshot for complete data]"
	size := len(marker)
	var b strings.Builder
	for _, r := range s {
		n := len(string(r))
		if size+n > limit {
			break
		}
		size += n
		b.WriteRune(r)
	}
	return b.String() + marker
}

// HandbackEvent applies one normalized handback notification from a shim.
func (w *WorkflowService) HandbackEvent(kind string, raw json.RawMessage) {
	var p struct {
		SessionID string   `json:"sessionID"`
		ParentID  string   `json:"parentID"`
		Agent     string   `json:"agent"`
		RequestID string   `json:"requestID"`
		Action    string   `json:"action"`
		Resources []string `json:"resources"`
		Reply     string   `json:"reply"`
	}
	if json.Unmarshal(raw, &p) != nil || p.SessionID == "" {
		return
	}
	switch kind {
	case "session":
		w.Handback.Session(p.SessionID, p.ParentID, p.Agent)
	case "asked":
		w.Handback.Asked(p.SessionID, p.RequestID, p.Action, p.Resources)
	case "replied":
		w.Handback.Replied(p.SessionID, p.RequestID, p.Reply)
	case "deleted":
		w.Handback.Deleted(p.SessionID)
	}
}
