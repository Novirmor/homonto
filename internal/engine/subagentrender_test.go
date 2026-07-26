package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// opencodeSubagentTOML installs a builtin subagent for OpenCode, whose rendered
// frontmatter is stamped with the per-agent override. %s is the model declared
// in [subagents.onto-reviewer.opencode].
const opencodeSubagentTOML = `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"
targets = ["opencode"]

[subagents.onto-reviewer.opencode]
model = "%s"
`

func writeConfig(t *testing.T, repo, model string) {
	t.Helper()
	body := strings.Replace(opencodeSubagentTOML, "%s", model, 1)
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func renderedModel(t *testing.T, e *Engine, file string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(e.SubagentDir(), file))
	if err != nil {
		t.Fatalf("read rendered subagent %s: %v", file, err)
	}
	for _, ln := range strings.Split(string(data), "\n") {
		if m, ok := strings.CutPrefix(ln, "model: "); ok {
			return m
		}
	}
	return ""
}

// TestApplyRerendersSubagentsWhenModelRouteChanges is the regression guard for
// the stale-render bug: materializeCatalog was gated on the catalog version and
// file existence alone, but a subagent's rendered `model:` comes from the
// config's per-agent override. Editing the override left the catalog version
// untouched, so the gate short-circuited and the projected agent kept its OLD
// model forever — while the tool's own setting.model (re-read from the routes
// each apply) correctly moved. Same config, two different answers.
func TestApplyRerendersSubagentsWhenModelRouteChanges(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()

	writeConfig(t, repo, "first/model-a")
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if got := renderedModel(t, e, "onto-reviewer.opencode.md"); got != "first/model-a" {
		t.Fatalf("after first apply: rendered model = %q, want %q", got, "first/model-a")
	}

	// Change ONLY the per-agent override. The catalog is byte-for-byte identical
	// (same version, same name); the only thing that moved is the override.
	writeConfig(t, repo, "second/model-b")
	e2 := buildEngine(t, home, repo)
	if err := e2.Apply(context.Background(), mustPlan(t, e2)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if got := renderedModel(t, e2, "onto-reviewer.opencode.md"); got != "second/model-b" {
		t.Fatalf("after override change: rendered model = %q, want %q (agent frozen at the old model)", got, "second/model-b")
	}
}

// TestApplyRestoresDeletedRenderedVariant guards the other half of the same
// gate: allSubagentFilesExist checked only the shared <name>.md anchor, never
// the per-tool <name>.<tool>.md variant the adapter actually links. A deleted
// variant left the anchor in place, so the gate short-circuited and apply never
// rewrote it — leaving the tool with a symlink dangling at a file nothing would
// ever recreate, while plan/status/doctor all reported healthy.
func TestApplyRestoresDeletedRenderedVariant(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeConfig(t, repo, "first/model-a")

	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	variant := filepath.Join(e.SubagentDir(), "onto-reviewer.opencode.md")
	if err := os.Remove(variant); err != nil {
		t.Fatal(err)
	}

	e2 := buildEngine(t, home, repo)
	if err := e2.Apply(context.Background(), mustPlan(t, e2)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if _, err := os.Stat(variant); err != nil {
		t.Fatalf("rendered variant not restored by apply: %v", err)
	}
}

// ontoFrameworkTOML installs the onto framework and per-agent override blocks
// for every expanded subagent (each tool they target). onto's `onto` agent is
// OpenCode-primary — agentfm renders no Claude variant for it by design.
const ontoFrameworkTOML = `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.onto.claude]
model = "opus"
[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-explorer.claude]
model = "haiku"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"

[subagents.onto-reviewer.claude]
model = "opus"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-implementer.claude]
model = "sonnet"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"

[subagents.onto-skeptic.claude]
model = "opus"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
`

// A framework's subagents may not be re-declared explicitly (that collision is
// an error), so the per-agent [subagents.<name>.<tool>] blocks above are
// tune-only entries (no source): they tune the framework's agent in place,
// declaring its model — required now that tiers are gone. Changing one agent's
// override must not affect any other agent.
func TestTuneOnlyEntryOverridesFrameworkAgentModel(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	// onto-skeptic gets an effort tune on top of its model block — both
	// declared in one tune-only entry, since TOML rejects duplicate tables.
	doc := strings.Replace(ontoFrameworkTOML,
		"[subagents.onto-skeptic.claude]\nmodel = \"opus\"\n",
		"[subagents.onto-skeptic.claude]\nmodel = \"opus\"\neffort = \"xhigh\"\n", 1)
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	effortOf := func(file string) string {
		data, err := os.ReadFile(filepath.Join(e.SubagentDir(), file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, ln := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(ln, "effort: "); ok {
				return v
			}
		}
		return ""
	}
	if got := effortOf("onto-skeptic.claude.md"); got != "xhigh" {
		t.Errorf("tuned agent effort = %q, want xhigh (the override must apply)", got)
	}
	// onto-reviewer has its own override block; it must stay at no effort (no
	// cross-contamination from onto-skeptic's tune).
	if got := effortOf("onto-reviewer.claude.md"); got != "" {
		t.Errorf("onto-reviewer effort = %q, want empty (each agent has its own block)", got)
	}
	// onto-skeptic renders its declared model (the override is complete on its
	// own — there is no tier to inherit from anymore).
	data, _ := os.ReadFile(filepath.Join(e.SubagentDir(), "onto-skeptic.claude.md"))
	if !strings.Contains(string(data), "model: opus") {
		t.Errorf("onto-skeptic must render its declared model:\n%s", data)
	}
}

// TestDoctorSilentOnPrimaryAgentClaudeVariant guards against a permanent false
// positive: `onto` is an OpenCode-primary agent, so agentfm deliberately renders
// no Claude variant and the adapter deliberately does not project it. doctor
// fell back to the shared anchor, found it, then demanded a Claude link that
// must never exist — reporting `subagent "onto" content present, not linked for
// claude (run apply)` on every healthy workspace. Worse, the advice was a dead
// end: apply correctly does nothing, so the warning could never be cleared.
func TestDoctorSilentOnPrimaryAgentClaudeVariant(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(ontoFrameworkTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	e2 := buildEngine(t, home, repo)
	for _, line := range e2.Doctor() {
		if strings.Contains(line, `subagent "onto"`) && strings.Contains(line, "claude") &&
			strings.HasPrefix(line, "warn:") {
			t.Fatalf("doctor reports an unfixable finding for the primary agent's absent Claude variant: %q", line)
		}
	}

	// The OpenCode side must still be reported healthy — silencing the Claude
	// false positive must not blind doctor to the variant that IS projected.
	var sawOpenCode bool
	for _, line := range e2.Doctor() {
		if strings.Contains(line, `subagent "onto"`) && strings.Contains(line, "opencode") {
			sawOpenCode = true
			if !strings.HasPrefix(line, "ok:") {
				t.Fatalf("opencode side of the primary agent not healthy: %q", line)
			}
		}
	}
	if !sawOpenCode {
		t.Fatal("doctor said nothing about the primary agent's OpenCode projection")
	}
}

// TestSubagentRenderFingerprintDistinguishesRoutes pins the fingerprint's job:
// it must change when an override changes and stay put when nothing does. A
// fingerprint that collided across override sets would silently skip the
// re-render this whole gate exists to trigger.
func TestSubagentRenderFingerprintDistinguishesRoutes(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()

	writeConfig(t, repo, "first/model-a")
	a := buildEngine(t, home, repo).subagentRenderContext()
	// Built independently from the same config: the fingerprint must not depend
	// on map iteration order, or every apply would needlessly re-materialize.
	aAgain := buildEngine(t, home, repo).subagentRenderContext()
	writeConfig(t, repo, "second/model-b")
	b := buildEngine(t, home, repo).subagentRenderContext()

	if renderFingerprint(a) == renderFingerprint(b) {
		t.Fatal("fingerprint collided across different overrides: an override change would not re-render")
	}
	if renderFingerprint(a) != renderFingerprint(aAgain) {
		t.Fatal("fingerprint is not stable for identical overrides: every apply would re-materialize")
	}
}
