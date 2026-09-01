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
// for every expanded subagent. onto's `onto` agent is OpenCode-primary (its
// homonto: block sets primary), rendered with mode: primary.
const ontoFrameworkTOML = `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"

[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"

[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
`

// A framework's subagents may not be re-declared explicitly (that collision is
// an error), so the per-agent [subagents.<name>.opencode] blocks above are
// tune-only entries (no source): they tune the framework's agent in place,
// declaring its model — required now that tiers are gone. Changing one agent's
// override must not affect any other agent.
func TestTuneOnlyEntryOverridesFrameworkAgentModel(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	// onto-skeptic gets a distinct model tune on top of its block, declared in
	// one tune-only entry (TOML rejects duplicate tables).
	doc := strings.Replace(ontoFrameworkTOML,
		"[subagents.onto-skeptic.opencode]\nmodel = \"anthropic/claude-opus-4-8\"\n",
		"[subagents.onto-skeptic.opencode]\nmodel = \"openai/o4-mini\"\n", 1)
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	modelOf := func(file string) string {
		data, err := os.ReadFile(filepath.Join(e.SubagentDir(), file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, ln := range strings.Split(string(data), "\n") {
			if m, ok := strings.CutPrefix(ln, "model: "); ok {
				return m
			}
		}
		return ""
	}
	if got := modelOf("onto-skeptic.opencode.md"); got != "openai/o4-mini" {
		t.Errorf("tuned agent model = %q, want openai/o4-mini (the override must apply)", got)
	}
	// onto-reviewer has its own override block; it must stay at its declared
	// model (no cross-contamination from onto-skeptic's tune).
	if got := modelOf("onto-reviewer.opencode.md"); got != "anthropic/claude-opus-4-8" {
		t.Errorf("onto-reviewer model = %q, want anthropic/claude-opus-4-8 (each agent has its own block)", got)
	}
}

// TestDoctorReportsPrimaryAgentHealthy guards the primary agent's doctor
// projection: `onto` is OpenCode-primary, rendered with mode: primary and
// projected like any other agent. Doctor must report its OpenCode link ok and
// must never raise a warn: finding for it. (The original form of this test
// guarded against a false positive on the primary agent's removed Claude
// variant; with Claude gone the surviving invariant is that the one real
// projection is reported healthy.)
func TestDoctorReportsPrimaryAgentHealthy(t *testing.T) {
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
	var sawOpenCode bool
	for _, line := range e2.Doctor() {
		if !strings.Contains(line, `subagent "onto"`) {
			continue
		}
		if strings.HasPrefix(line, "warn:") {
			t.Fatalf("doctor raised a finding for the primary agent: %q", line)
		}
		if strings.Contains(line, "opencode") {
			sawOpenCode = true
			if !strings.HasPrefix(line, "ok:") {
				t.Fatalf("primary agent's OpenCode projection not healthy: %q", line)
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
