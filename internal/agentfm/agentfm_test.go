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
	needs, err := NeedsTransform([]byte(readOnlyReviewer))
	if err != nil || !needs {
		t.Fatal("homonto block should need transform")
	}
	needs, err = NeedsTransform([]byte("---\nname: x\ndescription: y\n---\nbody\n"))
	if err != nil || needs {
		t.Fatal("no homonto block should not need transform")
	}
	for _, content := range []string{
		"---\nhomonto:\n  bash: [false]\n---\nbody\n",
		"---\nhomonto: null\n---\nbody\n",
		"---\nhomonto:\n  read_onnly: true\n---\nbody\n",
	} {
		if _, err := NeedsTransform([]byte(content)); err == nil {
			t.Fatal("malformed homonto block must fail instead of projecting verbatim")
		}
	}
}

func TestRenderOpenCode_ReadOnlyReviewer(t *testing.T) {
	s := mustRender(t, readOnlyReviewer, "opencode")
	for _, want := range []string{"mode: subagent", `model: "opus"`, "permission:", "  edit: deny", "  bash: deny", "  question: allow", "  task: deny"} {
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

func TestRenderOpenCode_NetworkIntent(t *testing.T) {
	for _, tc := range []struct {
		name, intent, action string
	}{
		{"omitted", "", ""},
		{"allowed", "  network: true\n", "allow"},
		{"denied", "  network: false\n", "deny"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Replace(readOnlyReviewer, "  dialogs: true\n", "  dialogs: true\n"+tc.intent, 1)
			s := mustRender(t, content, "opencode")
			for _, tool := range []string{"webfetch", "websearch"} {
				if tc.action == "" {
					if strings.Contains(s, "  "+tool+":") {
						t.Errorf("omitted network must leave %s unchanged:\n%s", tool, s)
					}
				} else if !strings.Contains(s, "  "+tool+": "+tc.action+"\n") {
					t.Errorf("%s must render %s: %s:\n%s", tc.name, tool, tc.action, s)
				}
			}
			for _, want := range []string{"  edit: deny", "  bash: deny", "  task: deny"} {
				if !strings.Contains(s, want) {
					t.Errorf("network intent must preserve %q:\n%s", want, s)
				}
			}
		})
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

// A request containing composition ("git status; curl ...") also matches a
// prefix allow. Guards follow allows so that request re-asks. This does not
// test how the host parses a shell invocation into permission requests.
func TestRenderOpenCode_BashAllowGuardsCompositionInRequests(t *testing.T) {
	allowlisted := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", `  spawn: [onto-implementer, onto-reviewer]
  bash_allow: ["git status*"]
`, 1)
	s, err := Render("onto", []byte(allowlisted), "opencode", ctx())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`    "*;*": ask`, `    "*&&*": ask`, `    "*||*": ask`, `    "*|*": ask`, `    "*$(*": ask`, "    \"*`*\": ask", `    "*>*": ask`, `    "*<*": ask`} {
		if !strings.Contains(string(s), want) {
			t.Errorf("opencode bash composition guard missing %q:\n%s", want, s)
		}
	}
	allowIdx := strings.Index(string(s), `    "git status*": allow`)
	guardIdx := strings.Index(string(s), `    "*;*": ask`)
	if allowIdx == -1 || guardIdx == -1 {
		t.Fatalf("render must carry the allow and the guard:\n%s", s)
	}
	if allowIdx > guardIdx {
		t.Errorf("guards must follow allows to re-ask composition-bearing requests:\n%s", s)
	}
	// A denied-bash agent carries no allowlist, so it needs no guards.
	if rendered := mustRender(t, readOnlyReviewer, "opencode"); strings.Contains(rendered, `"*;*": ask`) {
		t.Errorf("bash: deny must not render guards:\n%s", rendered)
	}
}

// bash_deny closes the single-command gate-skipping surface: "onto bypass…"
// is one command (no composition for the guards to catch) matching the broad
// "onto *" allow. The deny must render AFTER the allow so it wins.
func TestRenderOpenCode_BashDenyOverridesAllow(t *testing.T) {
	denied := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", `  spawn: [onto-implementer, onto-reviewer]
  bash_allow: ["onto *"]
  bash_deny: ["onto bypass*"]
`, 1)
	s, err := Render("onto", []byte(denied), "opencode", ctx())
	if err != nil {
		t.Fatal(err)
	}
	allowIdx := strings.Index(string(s), `    "onto *": allow`)
	denyIdx := strings.Index(string(s), `    "onto bypass*": deny`)
	if denyIdx == -1 {
		t.Fatalf("bash_deny must render a deny rule:\n%s", s)
	}
	if allowIdx == -1 || allowIdx > denyIdx {
		t.Errorf("deny must follow the allow so last-match-wins denies the gate skip:\n%s", s)
	}
	// bash: deny plus a deny list is incoherent: there is no bash surface.
	contradiction := strings.Replace(readOnlyReviewer, "  bash: false\n", "  bash: false\n  bash_deny: [\"onto bypass*\"]\n", 1)
	if _, err := Render("onto-reviewer", []byte(contradiction), "opencode", ctx()); err == nil {
		t.Fatal("bash: deny with bash_deny entries must error")
	}
	// A deny list alone (no allows) still renders the map: catch-all ask +
	// the deny.
	denyOnly := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", "  spawn: [onto-implementer, onto-reviewer]\n  bash_deny: [\"onto bypass*\"]\n", 1)
	s2, err := Render("onto", []byte(denyOnly), "opencode", ctx())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`    "*": ask`, `    "onto bypass*": deny`} {
		if !strings.Contains(string(s2), want) {
			t.Errorf("deny-only render missing %q:\n%s", want, s2)
		}
	}
}

func TestRenderOpenCode_ExternalDirectoriesAreAgentSpecific(t *testing.T) {
	context := ctx()
	context.ExternalDirectoriesByAgent = map[string][]string{
		"onto": {"/workspace/service-b", "/workspace/service-a", "/workspace/service-a"},
	}
	out, err := Render("onto", []byte(orchestrator), "opencode", context)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"  external_directory:",
		`    "*": deny`,
		`    "/workspace/service-a/**": allow`,
		`    "/workspace/service-b/**": allow`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("external access render missing %q:\n%s", want, out)
		}
	}
	if strings.Count(string(out), `"/workspace/service-a/**": allow`) != 1 {
		t.Errorf("duplicate external path rendered:\n%s", out)
	}

	empty := ctx()
	empty.ExternalDirectoriesByAgent = map[string][]string{"onto": {}}
	out, err = Render("onto", []byte(orchestrator), "opencode", empty)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "  external_directory:\n    \"*\": deny") {
		t.Errorf("mapped agent without repos must deny external directories:\n%s", out)
	}
	if strings.Contains(string(out), `"/**": allow`) {
		t.Errorf("empty external-directory map must not add an allow rule:\n%s", out)
	}

	if rendered := mustRender(t, readOnlyReviewer, "opencode"); strings.Contains(rendered, "external_directory:") {
		t.Errorf("read-only agent must not receive external write access:\n%s", rendered)
	}
	if rendered, err := Render("custom-writer", []byte(orchestrator), "opencode", &RenderContext{
		Overrides:                  map[string]ModelSpec{"custom-writer": {Model: "opus"}},
		ExternalDirectoriesByAgent: context.ExternalDirectoriesByAgent,
	}); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(rendered), "external_directory:") {
		t.Errorf("unmapped writable agent must not receive external access:\n%s", rendered)
	}
}

func TestRender_NoHomontoBlock_Unchanged(t *testing.T) {
	in := "---\nname: x\ndescription: y\nmode: subagent\n---\nbody\n"
	// Native content needs no model override, but its installed name must match.
	out, err := Render("x", []byte(in), "opencode", ctx())
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
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
	if !strings.Contains(string(out), `model: "openai/gpt-5"`) || !strings.Contains(string(out), `variant: "high"`) {
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
