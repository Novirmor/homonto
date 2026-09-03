package agentfm

import (
	"strings"
	"testing"
)

// A read-only specialist: no edits, no shell, dialogs on, spawns nothing. The
// model is supplied by the render context's Overrides, mirroring a
// [subagents.<name>.<tool>] block in homonto.toml.
const readOnlyReviewer = `---
name: onto-reviewer
description: Use to review a diff; reports findings ranked by severity.
mode: subagent
homonto:
  read_only: true
  bash: false
  dialogs: true
  spawn: []
---
You are a focused code reviewer.
`

// An orchestrator: may edit, may spawn a fixed set, is the OpenCode primary.
const orchestrator = `---
name: onto
description: dispatcher
mode: subagent
homonto:
  primary: true
  steps: 600
  spawn: [onto-implementer, onto-reviewer]
---
Drive the workflow.
`

func ctx() *RenderContext {
	return &RenderContext{Overrides: map[string]ModelSpec{
		"onto-reviewer": {Model: "opus"},
		"onto":          {Model: "opus"},
	}}
}

func mustRender(t *testing.T, content, tool string) string {
	t.Helper()
	out, err := Render("onto-reviewer", []byte(content), tool, ctx())
	if err != nil {
		t.Fatalf("Render(%s): %v", tool, err)
	}
	return string(out)
}

func TestNeedsTransform(t *testing.T) {
	if !NeedsTransform([]byte(readOnlyReviewer)) {
		t.Fatal("homonto block should need transform")
	}
	if NeedsTransform([]byte("---\nname: x\ndescription: y\n---\nbody\n")) {
		t.Fatal("no homonto block should not need transform")
	}
}

func TestRenderOpenCode_ReadOnlyReviewer(t *testing.T) {
	s := mustRender(t, readOnlyReviewer, "opencode")
	for _, want := range []string{"mode: subagent", "model: opus", "permission:", "  edit: deny", "  bash: deny", "  question: allow", "  task: deny"} {
		if !strings.Contains(s, want) {
			t.Errorf("opencode output missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "tools:") || strings.Contains(s, "homonto:") {
		t.Errorf("opencode output must not carry a tools string / homonto:\n%s", s)
	}
}

// A primary agent is an OpenCode concept: the neutral primary flag renders as
// mode: primary, and its iteration budget and delegation topology render
// alongside it.
func TestRenderOpenCode_PrimaryMode(t *testing.T) {
	oc, err := Render("onto", []byte(orchestrator), "opencode", ctx())
	if err != nil {
		t.Fatalf("Render(opencode) primary: %v", err)
	}
	s := string(oc)
	if !strings.Contains(s, "mode: primary") || !strings.Contains(s, "steps: 600") {
		t.Errorf("opencode primary must carry mode: primary + steps:\n%s", s)
	}
	// named spawn → task glob allowlist.
	for _, want := range []string{"  task:", `    "*": deny`, `    "onto-implementer": allow`, `    "onto-reviewer": allow`} {
		if !strings.Contains(s, want) {
			t.Errorf("opencode spawn topology missing %q:\n%s", want, s)
		}
	}
}

// dialogs is enforced BOTH ways in OpenCode: false must render question: deny,
// or the "subagents never prompt" protocol is silently unenforced there.
func TestRenderOpenCode_NoDialogsDeniesQuestion(t *testing.T) {
	silent := strings.Replace(readOnlyReviewer, "  dialogs: true\n", "", 1)
	s := mustRender(t, silent, "opencode")
	if !strings.Contains(s, "  question: deny") {
		t.Errorf("dialogs:false must render question: deny:\n%s", s)
	}
}

func TestRenderOpenCode_BashAllowlist(t *testing.T) {
	allowlisted := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", `  spawn: [onto-implementer, onto-reviewer]
  bash_allow: ["onto *", "git status*", "git commit *"]
`, 1)
	s, err := Render("onto", []byte(allowlisted), "opencode", ctx())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`  bash:`, `    "*": ask`, `    "onto *": allow`, `    "git status*": allow`, `    "git commit *": allow`} {
		if !strings.Contains(string(s), want) {
			t.Errorf("opencode bash allowlist missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(string(s), "bash: deny") {
		t.Errorf("a bash allowlist must not render bash: deny:\n%s", s)
	}
}

func TestRender_NoHomontoBlock_Unchanged(t *testing.T) {
	in := "---\nname: x\ndescription: y\nmode: subagent\n---\nbody\n"
	// A missing homonto block returns before model context is considered.
	if out := mustRender(t, in, "opencode"); out != in {
		t.Errorf("content without a homonto block must be unchanged\n got: %q", out)
	}
}

// The removed adapters are gone for good: Render must reject them like any
// other unknown tool rather than render something for them.
func TestRender_UnknownTool(t *testing.T) {
	for _, tool := range []string{"claude", "codex"} {
		if _, err := Render("onto-reviewer", []byte(readOnlyReviewer), tool, ctx()); err == nil {
			t.Errorf("Render(%s): unknown tool should error", tool)
		}
	}
}

// OpenCode stores a model variant in its own frontmatter field. Combining it
// with the model ID makes OpenCode look for a nonexistent literal model.
func TestRenderOpenCode_VariantUsesSeparateFrontmatterField(t *testing.T) {
	ctx := &RenderContext{Overrides: map[string]ModelSpec{
		"onto-reviewer": {Model: "openai/gpt-5", Variant: "high"},
	}}
	out, err := Render("onto-reviewer", []byte(readOnlyReviewer), "opencode", ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "model: openai/gpt-5") || !strings.Contains(string(out), "variant: high") {
		t.Errorf("opencode must render the model and variant separately:\n%s", out)
	}
	if strings.Contains(string(out), "model: openai/gpt-5#high") {
		t.Errorf("opencode must not suffix the model ID with its variant:\n%s", out)
	}
}

// TestRenderNoModelErrors keeps the present-but-empty override failure distinct
// from the missing-override production backstop below.
func TestRenderNoModelErrors(t *testing.T) {
	for _, tool := range []string{"opencode"} {
		ctx := &RenderContext{Overrides: map[string]ModelSpec{
			"ghost": {Variant: "fast"}, // entry present but no Model
		}}
		_, err := Render("ghost", []byte(readOnlyReviewer), tool, ctx)
		if err == nil {
			t.Fatalf("Render(%s): an override entry with no model must error", tool)
		}
		if !strings.Contains(err.Error(), `"ghost"`) || !strings.Contains(err.Error(), tool) {
			t.Fatalf("Render(%s): error must name the agent and tool, got: %v", tool, err)
		}
		if !strings.Contains(err.Error(), "[subagents.ghost."+tool+"]") {
			t.Fatalf("Render(%s): error must name the block to add, got: %v", tool, err)
		}
	}
}

func TestRenderMissingModelOverrideErrorsWithRenderContext(t *testing.T) {
	for _, tool := range []string{"opencode"} {
		_, err := Render("ghost", []byte(readOnlyReviewer), tool, &RenderContext{Overrides: map[string]ModelSpec{}})
		if err == nil {
			t.Fatalf("Render(%s): a production render context without an override must error", tool)
		}
		if !strings.Contains(err.Error(), `"ghost"`) || !strings.Contains(err.Error(), tool) {
			t.Fatalf("Render(%s): error must name the agent and tool, got: %v", tool, err)
		}
		if !strings.Contains(err.Error(), "[subagents.ghost."+tool+"]") {
			t.Fatalf("Render(%s): error must name the required model block, got: %v", tool, err)
		}
	}
}

func TestRenderNilContextRemainsLenientForCatalogProjection(t *testing.T) {
	out, err := Render("onto-reviewer", []byte(readOnlyReviewer), "opencode", nil)
	if err != nil {
		t.Fatalf("Render with nil context: %v", err)
	}
	if strings.Contains(string(out), "model:") {
		t.Fatalf("nil catalog context must not add a model line:\n%s", out)
	}
}
