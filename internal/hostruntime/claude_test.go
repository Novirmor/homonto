package hostruntime

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/agentfm"
)

type claudeView struct {
	DraftID string `json:"draftID"`
	Items   []struct {
		Status string `json:"status"`
	} `json:"items"`
	Question claudeQuestions `json:"question"`
}

func (f *fixture) stageClaude(c *fakeCall, items ...string) claudeView {
	f.t.Helper()
	f.s.SetDialect(DialectClaude)
	out, err := f.s.Stage(c, f.args(`{"items":[`+strings.Join(items, ",")+`]}`))
	if err != nil {
		f.t.Fatalf("stage: %v", err)
	}
	var v claudeView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		f.t.Fatal(err)
	}
	return v
}

func (f *fixture) answerClaude(d claudeView, toolUseID string, labels ...string) {
	answers := map[string]any{}
	for n, q := range d.Question.Questions {
		label := q.Options[2].Label
		if n < len(labels) {
			label = labels[n]
		}
		answers[q.Question] = label
	}
	f.s.ClaudeAnswered("s1", toolUseID, f.args(marshal(map[string]any{"questions": d.Question.Questions, "answers": answers, "annotations": map[string]any{}}, false)))
}

func TestClaudeQuestionApprovesOnlyTheCorrelatedCall(t *testing.T) {
	f := newFixture(t)
	c := &fakeCall{session: "s1"}
	d := f.stageClaude(c, issueItem)
	q := d.Question.Questions[0]
	if q.Header != "Fake 1" || q.MultiSelect || len(q.Options) != 3 {
		t.Fatalf("AskUserQuestion arguments: %+v", d.Question)
	}
	// An answer for a call that never correlated approves nothing.
	f.answerClaude(d, "toolu_unbound")
	if got := f.publish(c, view{DraftID: d.DraftID}); got[0] != "pending" {
		t.Fatalf("uncorrelated answer approved: %v", got)
	}
	// The model may not answer its own question.
	pre := f.args(marshal(map[string]any{"questions": d.Question.Questions, "answers": map[string]any{q.Question: q.Options[2].Label}}, false))
	if err := f.s.ClaudeBefore("s1", "toolu_1", pre); !errors.Is(err, ErrPreAnswered) {
		t.Fatalf("pre-answered question not refused: %v", err)
	}
	// A tampered question does not correlate.
	tampered := marshal(map[string]any{"questions": []claudeQuestion{{Question: "Approve?", Header: q.Header, Options: q.Options}}}, false)
	if err := f.s.ClaudeBefore("s1", "toolu_2", f.args(tampered)); err != nil {
		t.Fatal(err)
	}
	f.answerClaude(d, "toolu_2")
	if got := f.publish(c, view{DraftID: d.DraftID}); got[0] != "pending" {
		t.Fatalf("tampered question approved: %v", got)
	}
	if err := f.s.ClaudeBefore("s1", "toolu_3", f.args(marshal(d.Question, false))); err != nil {
		t.Fatal(err)
	}
	f.answerClaude(d, "toolu_3")
	if got := f.publish(c, view{DraftID: d.DraftID}); got[0] != "published" || len(f.p.posted) != 1 {
		t.Fatalf("approved draft: %v %v", got, f.p.posted)
	}
}

func TestClaudeFreeTextFailureAndLimits(t *testing.T) {
	f := newFixture(t)
	c := &fakeCall{session: "s1"}
	d := f.stageClaude(c, issueItem, changeItem)
	_ = f.s.ClaudeBefore("s1", "toolu_1", f.args(marshal(d.Question, false)))
	f.answerClaude(d, "toolu_1", "looks fine to me", "Revise")
	if got := f.publish(c, view{DraftID: d.DraftID}); strings.Join(got, ",") != "declined,revision-requested" {
		t.Fatalf("free text must decline: %v", got)
	}
	d = f.stageClaude(c, issueItem)
	_ = f.s.ClaudeBefore("s1", "toolu_2", f.args(marshal(d.Question, false)))
	f.s.ClaudeFailed("s1", "toolu_2")
	if got := f.publish(c, view{DraftID: d.DraftID}); got[0] != "declined" {
		t.Fatalf("rejected dialog must decline: %v", got)
	}
	five := strings.Repeat(issueItem+",", 4) + issueItem
	if _, err := f.s.Stage(c, f.args(`{"items":[`+five+`]}`)); err == nil || !strings.Contains(err.Error(), "1–4 items") {
		t.Fatalf("Claude draft above four items: %v", err)
	}
}

func hookInput(fields map[string]any) *bytes.Reader {
	if _, ok := fields["session_id"]; !ok {
		fields["session_id"] = "ses"
	}
	b, _ := json.Marshal(fields)
	return bytes.NewReader(b)
}

func TestClaudeHookInjectsContextAndGuardsTools(t *testing.T) {
	snap, _ := json.Marshal(sampleSnapshot(sampleChange("one")))
	var relayed []HookEvent
	deps := HookDeps{
		Snapshot: func(string) (string, error) { return string(snap), nil },
		Relay: func(_, session string, ev HookEvent) (HookReply, error) {
			relayed = append(relayed, ev)
			if ev.Event == "SessionStart" {
				return HookReply{Context: "## Homonto worker permission blocks\n- worker"}, nil
			}
			if ev.ToolName == "AskUserQuestion" {
				return HookReply{Deny: "[question_preanswered] no"}, nil
			}
			return HookReply{}, nil
		},
	}
	var out bytes.Buffer
	if err := RunClaudeHook(hookInput(map[string]any{"hook_event_name": "SessionStart", "source": "compact"}), &out, testConfig, deps); err != nil {
		t.Fatal(err)
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName      string `json:"hookEventName"`
			AdditionalContext  string `json:"additionalContext"`
			PermissionDecision string `json:"permissionDecision"`
			Reason             string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	ctx := got.HookSpecificOutput.AdditionalContext
	if got.HookSpecificOutput.HookEventName != "SessionStart" || !strings.HasPrefix(ctx, ContinuationPolicy) ||
		!strings.Contains(ctx, "## Homonto workflow context") || !strings.HasSuffix(ctx, "- worker") || len(ctx) > ClaudeContextLimit {
		t.Fatalf("session start context: %q", ctx)
	}

	out.Reset()
	deps.Snapshot = func(string) (string, error) { return "", errors.New("no") }
	_ = RunClaudeHook(hookInput(map[string]any{"hook_event_name": "SessionStart", "source": "startup"}), &out, testConfig, deps)
	if !strings.Contains(out.String(), "Workflow observation unavailable") || strings.Contains(out.String(), "execution continuity") {
		t.Fatalf("unavailable snapshot: %s", out.String())
	}

	out.Reset()
	before := len(relayed)
	_ = RunClaudeHook(hookInput(map[string]any{"hook_event_name": "PreToolUse", "agent_id": "a1", "agent_type": "onto-skeptic",
		"tool_name": "mcp__homonto__homonto_status"}), &out, testConfig, deps)
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.HookSpecificOutput.PermissionDecision != "deny" ||
		!strings.Contains(got.HookSpecificOutput.Reason, "[wrong_agent]") || len(relayed) != before {
		t.Fatalf("worker reached a coordinator tool: %s", out.String())
	}

	out.Reset()
	_ = RunClaudeHook(hookInput(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "AskUserQuestion", "tool_use_id": "t"}), &out, testConfig, deps)
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || got.HookSpecificOutput.PermissionDecision != "deny" {
		t.Fatalf("core denial not applied: %s", out.String())
	}

	out.Reset()
	deps.Relay = func(string, string, HookEvent) (HookReply, error) { return HookReply{}, errors.New("no server") }
	if err := RunClaudeHook(hookInput(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash"}), &out, testConfig, deps); err != nil || out.Len() != 0 {
		t.Fatalf("an unavailable core must not block the harness: %v %q", err, out.String())
	}
	if err := RunClaudeHook(strings.NewReader("{}"), &out, testConfig, deps); err == nil {
		t.Fatal("hook without a session accepted")
	}
}

func TestDirectServerHandsBackWorkerRefusals(t *testing.T) {
	s, err := NewDirectServer(strings.NewReader(""), &bytes.Buffer{}, DirectOptions{ConfigPath: testConfig, SessionID: "ses", Dialect: DialectClaude})
	if err != nil {
		t.Fatal(err)
	}
	input := json.RawMessage(`{"command":"git push origin main"}`)
	s.Hook(HookEvent{Event: "PermissionRequest", AgentID: "a1", AgentType: "to-implementer", ToolName: "Bash", ToolInput: input})
	s.Hook(HookEvent{Event: "PermissionRequest", AgentID: "a1", AgentType: "to-implementer", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test ./..."}`)})
	s.Hook(HookEvent{Event: "PostToolUse", AgentID: "a1", ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"go test ./..."}`)})
	s.Hook(HookEvent{Event: "SubagentStop", AgentID: "a1", AgentType: "to-implementer"})
	reply := s.Hook(HookEvent{Event: "PostToolUse", ToolName: "Agent"})
	if !strings.Contains(reply.Context, `- worker "to-implementer" session "a1": "Bash" refused for "git push origin main"`) ||
		strings.Contains(reply.Context, "go test") {
		t.Fatalf("handback: %q", reply.Context)
	}
	if again := s.Hook(HookEvent{Event: "UserPromptSubmit"}); again.Context != "" {
		t.Fatalf("handback delivered twice: %q", again.Context)
	}
	if names := s.Tools(); len(names) != 2 {
		t.Fatalf("workflow-only server tools: %v", names)
	}
}

func TestDirectServerSpeaksMCPAndRelaysHooksOverItsSocket(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s, err := NewDirectServer(inR, outW, DirectOptions{ConfigPath: testConfig, SessionID: "ses", Hosts: []string{"github"}, Dialect: DialectClaude, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	l, err := Listen(testConfig, "ses")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve(l); _ = outW.Close() }()
	p := &peer{t, inW, bufio.NewScanner(outR)}
	p.send(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": ProtocolVersion}})
	if m := p.next(); m["result"].(map[string]any)["serverInfo"].(map[string]any)["name"] != "homonto" {
		t.Fatalf("initialize: %v", m)
	}
	p.send(map[string]any{"method": "notifications/initialized"})
	p.send(map[string]any{"id": 2, "method": "tools/list"})
	tools := p.next()["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 5 || tools[2].(map[string]any)["name"] != "homonto_github_draft" {
		t.Fatalf("tools: %v", tools)
	}
	p.send(map[string]any{"id": 3, "method": "tools/call", "params": map[string]any{"name": "homonto_handoff", "arguments": map[string]any{"workflow": "onto"}}})
	if m := p.next(); m["result"].(map[string]any)["isError"] != true {
		t.Fatalf("bad handoff arguments: %v", m)
	}
	reply, err := RelaySocket(testConfig, "ses", HookEvent{Event: "PreToolUse", ToolName: "AskUserQuestion", ToolUseID: "t",
		ToolInput: json.RawMessage(`{"questions":[],"answers":{"q":"a"}}`)})
	if err != nil || !strings.Contains(reply.Deny, "question_preanswered") {
		t.Fatalf("relay: %+v %v", reply, err)
	}
	_ = inW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := RelaySocket(testConfig, "ses", HookEvent{Event: "Stop"}); err == nil {
		t.Fatal("socket outlived the server")
	}
}

func TestClaudeHookEnforcesTheRunningAgentsPolicy(t *testing.T) {
	dir := t.TempDir()
	policy := agentfm.EncodeClaudePolicy(agentfm.ClaudePolicy{Bash: []agentfm.PolicyRule{{Pattern: "*", Action: "ask"}, {Pattern: "go test*", Action: "allow"}, {Pattern: "git push*", Action: "deny"}}})
	if err := os.WriteFile(filepath.Join(dir, "onto-implementer.md"), []byte("---\nname: onto-implementer\nhomonto_policy: \""+policy+"\"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deps := HookDeps{AgentDirs: []string{dir}, Snapshot: func(string) (string, error) { return "", errors.New("unused") },
		Relay: func(string, string, HookEvent) (HookReply, error) { return HookReply{}, nil }}
	decide := func(agent, command string) string {
		var out bytes.Buffer
		in, _ := json.Marshal(map[string]any{"session_id": "s", "hook_event_name": "PreToolUse", "agent_id": "a", "agent_type": agent,
			"tool_name": "Bash", "tool_input": map[string]any{"command": command}})
		if err := RunClaudeHook(bytes.NewReader(in), &out, testConfig, deps); err != nil {
			t.Fatal(err)
		}
		var got struct {
			HookSpecificOutput struct {
				PermissionDecision string `json:"permissionDecision"`
			} `json:"hookSpecificOutput"`
		}
		_ = json.Unmarshal(out.Bytes(), &got)
		return got.HookSpecificOutput.PermissionDecision
	}
	for cmd, want := range map[string]string{"go test ./...": "allow", "git push origin": "deny", "make": "ask"} {
		if got := decide("onto-implementer", cmd); got != want {
			t.Errorf("%q: %q, want %q", cmd, got, want)
		}
	}
	if got := decide("someone-else", "git push origin"); got != "" {
		t.Errorf("an agent homonto did not render has no policy: %q", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.md"), []byte("---\nhomonto_policy: \"%%%\"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := decide("broken", "ls"); got != "deny" {
		t.Errorf("an unreadable policy must fail closed: %q", got)
	}
}

func TestClaudeHookKeepsRuntimeToolsWithTheCoordinator(t *testing.T) {
	deps := HookDeps{Coordinator: "homonto", Snapshot: func(string) (string, error) { return "", errors.New("unused") },
		Relay: func(string, string, HookEvent) (HookReply, error) { return HookReply{}, nil }}
	for agent, denied := range map[string]bool{"homonto": false, "": false, "general-purpose": true} {
		var out bytes.Buffer
		in, _ := json.Marshal(map[string]any{"session_id": "s", "hook_event_name": "PreToolUse", "agent_type": agent, "tool_name": "mcp__homonto__homonto_status"})
		if err := RunClaudeHook(bytes.NewReader(in), &out, testConfig, deps); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(out.String(), "wrong_agent"); got != denied {
			t.Errorf("main agent %q: denied=%v (%s)", agent, got, out.String())
		}
	}
}
