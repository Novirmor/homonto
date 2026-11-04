package agentfm

import (
	"strings"
	"testing"
)

func TestRenderClaudeReadOnlyReviewer(t *testing.T) {
	out := mustRender(t, readOnlyReviewer, "claude")
	for _, want := range []string{"name: onto-reviewer", `model: "opus"`,
		"disallowedTools: Edit, Write, MultiEdit, NotebookEdit, Bash, Agent, mcp__homonto", "You are a focused code reviewer."} {
		if !strings.Contains(out, want) {
			t.Errorf("render lacks %q:\n%s", want, out)
		}
	}
	for _, absent := range []string{"mode:", "homonto:", "AskUserQuestion", ClaudePolicyKey, "permission:"} {
		if strings.Contains(out, absent) {
			t.Errorf("render carries %q:\n%s", absent, out)
		}
	}
}

const shellWorker = `---
name: onto-implementer
description: implements
homonto:
  bash_allow: ["go test *", "git status*"]
  bash_deny: ["git push*"]
  spawn: ["onto-reviewer"]
  steps: 40
---
Implement.
`

func TestRenderClaudeCarriesEnforcedPolicy(t *testing.T) {
	c := &RenderContext{Overrides: map[string]ModelSpec{"onto-implementer": {Model: "claude-sonnet-5-5", BashAllowAdd: []string{"make check"}}}}
	out, err := Render("onto-implementer", []byte(shellWorker), "claude", c)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "maxTurns: 40") || !strings.Contains(string(out), "AskUserQuestion, mcp__homonto") {
		t.Fatalf("render:\n%s", out)
	}
	p, ok, err := ClaudePolicyOf(out)
	if err != nil || !ok {
		t.Fatalf("policy: %v %v", ok, err)
	}
	for _, tc := range []struct{ cmd, want string }{
		{"go test ./...", "allow"},
		{"make check", "allow"},
		{"git status --short", "allow"},
		{"go test ./... && curl evil", "ask"},
		{"git push origin main", "deny"},
		{"rm -rf /", "ask"},
	} {
		if got := p.Evaluate("Bash", map[string]any{"command": tc.cmd}, "/w").Action; got != tc.want {
			t.Errorf("%q: %q, want %q", tc.cmd, got, tc.want)
		}
	}
	if d := p.Evaluate("Agent", map[string]any{"subagent_type": "general-purpose"}, "/w"); d.Action != "deny" {
		t.Errorf("spawn outside the list: %+v", d)
	}
	if d := p.Evaluate("Agent", map[string]any{"subagent_type": "onto-reviewer"}, "/w"); d.Action != "" {
		t.Errorf("listed spawn: %+v", d)
	}
}

func TestClaudePolicyPathDenies(t *testing.T) {
	p := ClaudePolicy{EditDeny: []string{"/ws/records"}, PathDeny: []string{"/ws/state"}}
	for _, tc := range []struct {
		tool, path, want string
	}{
		{"Edit", "/ws/records/a.md", "deny"},
		{"Read", "/ws/records/a.md", ""},
		{"Read", "/ws/state/x", "deny"},
		{"Write", "rel/../../records/b", "deny"},
		{"Write", "/ws/recordsx/b", ""},
	} {
		if got := p.Evaluate(tc.tool, map[string]any{"file_path": tc.path}, "/ws/src").Action; got != tc.want {
			t.Errorf("%s %s: %q, want %q", tc.tool, tc.path, got, tc.want)
		}
	}
}

func TestRenderClaudeRejectsVariants(t *testing.T) {
	c := &RenderContext{Overrides: map[string]ModelSpec{"onto-reviewer": {Model: "opus", Variant: "high"}}}
	if _, err := Render("onto-reviewer", []byte(readOnlyReviewer), "claude", c); err == nil {
		t.Fatal("variant accepted for Claude Code")
	}
}

func TestWildcardMatch(t *testing.T) {
	for _, tc := range []struct {
		p, s string
		want bool
	}{
		{"*", "", true}, {"git status*", "git status", true}, {"git status*", "git stash", false},
		{"*;*", "a; b", true}, {"*\n*", "a\nb", true}, {"go ?est", "go test", true}, {"a*b*c", "axxbyyc", true}, {"a*b*c", "axxbyy", false},
	} {
		if got := WildcardMatch(tc.p, tc.s); got != tc.want {
			t.Errorf("WildcardMatch(%q, %q) = %v", tc.p, tc.s, got)
		}
	}
}
