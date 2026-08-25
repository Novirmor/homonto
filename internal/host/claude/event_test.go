package claude

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

const root = "/work/repo"

func event(t *testing.T, body string) protocol.GuardRequest {
	t.Helper()
	req, err := NormalizeEvent(strings.NewReader(body), root)
	if err != nil {
		t.Fatalf("NormalizeEvent: %v\n%s", err, body)
	}
	return req
}

// TestNormalizeWriteEvents proves each writing tool's path field is read,
// and that the result is a valid guard request.
func TestNormalizeWriteEvents(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		tool  string
		paths []string
	}{
		{"Write", `{"session_id":"s1","hook_event_name":"PreToolUse","cwd":"/work/repo",
			"tool_name":"Write","tool_input":{"file_path":"/work/repo/src/a.go","content":"x"}}`,
			"Write", []string{"src/a.go"}},
		{"Edit", `{"session_id":"s1","hook_event_name":"PreToolUse","cwd":"/work/repo",
			"tool_name":"Edit","tool_input":{"file_path":"/work/repo/src/b.go"}}`,
			"Edit", []string{"src/b.go"}},
		{"MultiEdit", `{"session_id":"s1","hook_event_name":"PreToolUse","cwd":"/work/repo",
			"tool_name":"MultiEdit","tool_input":{"file_path":"/work/repo/src/c.go"}}`,
			"MultiEdit", []string{"src/c.go"}},
		{"NotebookEdit", `{"session_id":"s1","hook_event_name":"PreToolUse","cwd":"/work/repo",
			"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/work/repo/nb.ipynb"}}`,
			"NotebookEdit", []string{"nb.ipynb"}},
		{"a relative path", `{"session_id":"s1","hook_event_name":"PreToolUse","cwd":"/work/repo",
			"tool_name":"Write","tool_input":{"file_path":"src/d.go"}}`,
			"Write", []string{"src/d.go"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := event(t, tt.body)
			if req.Host != protocol.HostClaude || req.Tool != tt.tool {
				t.Fatalf("request = %+v", req)
			}
			if strings.Join(req.WritePaths, ",") != strings.Join(tt.paths, ",") {
				t.Fatalf("write paths = %v, want %v", req.WritePaths, tt.paths)
			}
			if err := req.Validate(); err != nil {
				t.Fatalf("the normalized request is not valid: %v", err)
			}
		})
	}
}

// TestReadingToolsWriteNothing proves a non-writing tool produces a
// request with no write paths, which the guard allows without any
// permission at all.
func TestReadingToolsWriteNothing(t *testing.T) {
	for _, tool := range []string{"Read", "Grep", "Glob", "Bash"} {
		body := `{"session_id":"s1","hook_event_name":"PreToolUse","cwd":"/work/repo",
			"tool_name":"` + tool + `","tool_input":{"command":"ls"}}`
		req := event(t, body)
		if len(req.WritePaths) != 0 {
			t.Errorf("%s produced write paths %v", tool, req.WritePaths)
		}
		if err := req.Validate(); err != nil {
			t.Errorf("%s: the normalized request is not valid: %v", tool, err)
		}
	}
}

// TestSessionIDIsStable proves one host session looks like one session
// across every event it produces — which is what makes a refusal
// traceable back to the session that caused it.
func TestSessionIDIsStable(t *testing.T) {
	body := `{"session_id":"abc-123","hook_event_name":"PreToolUse","cwd":"/work/repo",
		"tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`
	first := event(t, body)
	second := event(t, body)
	if first.SessionID != second.SessionID {
		t.Fatal("the same host session produced two different session ids")
	}
	if err := identity.ValidateUUID(string(first.SessionID)); err != nil {
		t.Fatalf("the derived session id is not canonical: %v", err)
	}
	other := `{"session_id":"def-456","hook_event_name":"PreToolUse","cwd":"/work/repo",
		"tool_name":"Write","tool_input":{"file_path":"src/a.go"}}`
	if event(t, other).SessionID == first.SessionID {
		t.Fatal("two host sessions collapsed into one id")
	}
}

func TestMalformedEventsAreRefused(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not JSON", `{ not json`},
		{"no event name", `{"session_id":"s1","tool_name":"Write","tool_input":{"file_path":"a.go"}}`},
		{"the wrong event", `{"hook_event_name":"SessionStart","tool_name":"Write","tool_input":{}}`},
		{"no tool", `{"hook_event_name":"PreToolUse","tool_input":{"file_path":"a.go"}}`},
		{"no tool input", `{"hook_event_name":"PreToolUse","tool_name":"Write"}`},
		{"a write with no path", `{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{}}`},
		{"unreadable tool input", `{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":"nope"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NormalizeEvent(strings.NewReader(tt.body), root); err == nil {
				t.Fatal("NormalizeEvent = nil error, want rejection")
			}
		})
	}
}

// TestUnknownFieldsAreTolerated proves a Claude release that adds a field
// does not become a Homonto outage.
func TestUnknownFieldsAreTolerated(t *testing.T) {
	body := `{"session_id":"s1","hook_event_name":"PreToolUse","cwd":"/work/repo",
		"tool_name":"Write","tool_input":{"file_path":"src/a.go"},
		"something_new":{"claude":"added this next release"}}`
	req := event(t, body)
	if len(req.WritePaths) != 1 {
		t.Fatalf("write paths = %v", req.WritePaths)
	}
}

func TestRenderDecision(t *testing.T) {
	allowed, err := RenderDecision(protocol.GuardDecision{
		Allow: true, Reason: "within the issued scope", Code: "allowed",
	})
	if err != nil {
		t.Fatalf("RenderDecision: %v", err)
	}
	var decision Decision
	if err := json.Unmarshal(allowed, &decision); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decision.HookSpecificOutput.PermissionDecision != "allow" {
		t.Fatalf("decision = %+v, want allow", decision.HookSpecificOutput)
	}
	if decision.HookSpecificOutput.HookEventName != EventPreToolUse {
		t.Fatalf("the response names %q", decision.HookSpecificOutput.HookEventName)
	}
	// An allowed write still says why, so an operator has something to
	// read when they wonder how it went through.
	if decision.HookSpecificOutput.PermissionDecisionReason == "" {
		t.Error("an allowed write explains nothing")
	}

	refused, err := RenderDecision(protocol.GuardDecision{
		Allow: false, Reason: "docs/homonto is not in the scope", Code: "outside_scope",
	})
	if err != nil {
		t.Fatalf("RenderDecision: %v", err)
	}
	if err := json.Unmarshal(refused, &decision); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decision.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("decision = %+v, want deny", decision.HookSpecificOutput)
	}
	reason := decision.HookSpecificOutput.PermissionDecisionReason
	for _, want := range []string{"outside_scope", "docs/homonto is not in the scope"} {
		if !strings.Contains(reason, want) {
			t.Errorf("the refusal does not carry %q: %q", want, reason)
		}
	}
}

func TestRenderProbe(t *testing.T) {
	// An idle workspace adds nothing: a session with no work in progress
	// should not begin with a paragraph about there being none.
	for _, state := range []protocol.ProbeState{protocol.ProbeIdle, protocol.ProbeUnavailable} {
		body, err := RenderProbe(protocol.ProbeResponse{
			ProtocolVersion: protocol.CurrentVersion, State: state, Message: "nothing here",
		})
		if err != nil {
			t.Fatalf("RenderProbe(%s): %v", state, err)
		}
		if len(body) != 0 {
			t.Errorf("RenderProbe(%s) = %q, want nothing", state, body)
		}
	}
	body, err := RenderProbe(protocol.ProbeResponse{
		ProtocolVersion: protocol.CurrentVersion,
		State:           protocol.ProbeResumable,
		Message:         "Homonto has one active task",
	})
	if err != nil {
		t.Fatalf("RenderProbe: %v", err)
	}
	var ctx SessionContext
	if err := json.Unmarshal(body, &ctx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ctx.HookSpecificOutput.HookEventName != EventSessionStart {
		t.Fatalf("the response names %q", ctx.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(ctx.HookSpecificOutput.AdditionalContext, "one active task") {
		t.Fatalf("the context does not carry the message: %q", ctx.HookSpecificOutput.AdditionalContext)
	}
}
