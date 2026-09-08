package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
			var model string
			if err := yaml.Unmarshal([]byte(m), &model); err != nil {
				t.Fatal(err)
			}
			return model
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
// for every expanded subagent. The shared `homonto` agent is OpenCode-primary
// (its homonto: block sets primary), rendered with mode: primary (ADR 0045).
const ontoFrameworkTOML = `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.homonto.opencode]
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

const toFrameworkTOML = `
[frameworks.to]
source = "builtin:to"
scope = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
`

// hFrameworkTOML installs the h companion alone: its catalog dependencies
// transitively expand onto's and to's agents plus the two h workers, all
// needing model blocks (active names are globally unique across frameworks —
// homonto appears once).
const hFrameworkTOML = `
[frameworks.h]
source = "builtin:h"
scope = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.h-spike.opencode]
model = "openai/gpt-5-mini"

[subagents.h-review.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.to-explorer.opencode]
model = "openai/gpt-5-mini"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.to-skeptic.opencode]
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
		return renderedModel(t, e, file)
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
// projection: `homonto` is OpenCode-primary, rendered with mode: primary and
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
		if !strings.Contains(line, `subagent "homonto"`) {
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

// TestHomontoPrimaryRendersViaToFramework: the shared homonto primary renders
// mode: primary with its full delegation topology — both frameworks'
// specialists — even when only the to framework is declared (ADR 0045).
func TestHomontoPrimaryRendersViaToFramework(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	doc := toFrameworkTOML
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	agent, err := os.ReadFile(filepath.Join(e.SubagentDir(), "homonto.opencode.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mode: primary", "steps: 1200", `"to-implementer": allow`, `"to-reviewer": allow`, `"onto-implementer": allow`} {
		if !strings.Contains(string(agent), want) {
			t.Errorf("homonto primary missing %q:\n%s", want, agent)
		}
	}
}

func TestFrameworkAgentsAllowDeclaredRepoDirectories(t *testing.T) {
	for _, tc := range []struct {
		name      string
		framework string
		agents    []string
		untrusted []string
	}{
		{"onto", ontoFrameworkTOML, []string{"homonto", "onto-implementer"}, []string{"onto-explorer", "onto-reviewer", "onto-skeptic"}},
		{"to", toFrameworkTOML, []string{"homonto", "to-implementer"}, []string{"to-explorer", "to-reviewer", "to-skeptic"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			repo := t.TempDir()
			sibling := filepath.Join(t.TempDir(), "service-a")
			if err := os.MkdirAll(filepath.Join(sibling, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			doc := tc.framework + "\n[repos]\nservice-a = " + fmt.Sprintf("%q", sibling) + "\n"
			if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
				t.Fatal(err)
			}
			e := buildEngine(t, home, repo)
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}

			want := fmt.Sprintf("    %q: allow", filepath.ToSlash(filepath.Join(sibling, "**")))
			for _, agentName := range tc.agents {
				data, err := os.ReadFile(filepath.Join(e.SubagentDir(), agentName+".opencode.md"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), "external_directory:") || !strings.Contains(string(data), want) {
					t.Errorf("%s missing declared-repo access %q:\n%s", agentName, want, data)
				}
			}
			for _, agentName := range tc.untrusted {
				data, err := os.ReadFile(filepath.Join(e.SubagentDir(), agentName+".opencode.md"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "external_directory:") {
					t.Errorf("%s must not receive external access:\n%s", agentName, data)
				}
			}
		})
	}
}

func TestFrameworkAgentsDenyExternalDirectoriesWithoutDeclaredRepos(t *testing.T) {
	for _, tc := range []struct {
		name      string
		framework string
		trusted   []string
		untrusted []string
	}{
		{"onto", ontoFrameworkTOML, []string{"homonto", "onto-implementer"}, []string{"onto-explorer", "onto-reviewer", "onto-skeptic"}},
		{"to", toFrameworkTOML, []string{"homonto", "to-implementer"}, []string{"to-explorer", "to-reviewer", "to-skeptic"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			repo := t.TempDir()
			if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(tc.framework), 0o644); err != nil {
				t.Fatal(err)
			}
			e := buildEngine(t, home, repo)
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			for _, agentName := range tc.trusted {
				data, err := os.ReadFile(filepath.Join(e.SubagentDir(), agentName+".opencode.md"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), "  external_directory:\n    \"*\": deny") {
					t.Errorf("%s must deny undeclared external directories:\n%s", agentName, data)
				}
			}
			for _, agentName := range tc.untrusted {
				data, err := os.ReadFile(filepath.Join(e.SubagentDir(), agentName+".opencode.md"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "external_directory:") {
					t.Errorf("%s must not gain an external-directory rule:\n%s", agentName, data)
				}
			}
		})
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
	a := mustSubagentRenderContext(t, buildEngine(t, home, repo))
	// Built independently from the same config: the fingerprint must not depend
	// on map iteration order, or every apply would needlessly re-materialize.
	aAgain := mustSubagentRenderContext(t, buildEngine(t, home, repo))
	writeConfig(t, repo, "second/model-b")
	b := mustSubagentRenderContext(t, buildEngine(t, home, repo))

	if renderFingerprint(a) == renderFingerprint(b) {
		t.Fatal("fingerprint collided across different overrides: an override change would not re-render")
	}
	if renderFingerprint(a) != renderFingerprint(aAgain) {
		t.Fatal("fingerprint is not stable for identical overrides: every apply would re-materialize")
	}
	baseFingerprint := renderFingerprint(a)
	opencode := a["opencode"]
	opencode.ExternalDirectoriesByAgent = map[string][]string{"onto": {"/work/service-a"}}
	a["opencode"] = opencode
	if baseFingerprint == renderFingerprint(a) {
		t.Fatal("fingerprint ignored declared repository paths; apply would leave agent permissions stale")
	}
	baseFingerprint = renderFingerprint(a)
	opencode.ShellProxy = "rtk"
	a["opencode"] = opencode
	if baseFingerprint == renderFingerprint(a) {
		t.Fatal("fingerprint ignored shell proxy; apply would leave command permissions stale")
	}
}

func TestApplyRerendersSubagentPatternsWhenShellProxyChanges(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	agents := []string{"homonto", "onto-implementer", "to-implementer", "h-review"}
	baseline := map[string]string{}
	fingerprints := map[string]string{}
	// Cover enabling, stable re-apply, disabling, and omitted == explicit none.
	for _, proxy := range []string{"", "none", "rtk", "rtk", "none", ""} {
		doc := hFrameworkTOML
		for _, agent := range agents[:3] {
			header := "[subagents." + agent + ".opencode]\n"
			doc = strings.Replace(doc, header, header+"bash_allow_add = [\"./scripts/task-check.sh\"]\n", 1)
		}
		if proxy != "" {
			doc += fmt.Sprintf("\n[tooling]\nshell_proxy = %q\n", proxy)
		}
		if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		e := buildEngine(t, home, repo)
		resolved := proxy
		if resolved == "" {
			resolved = "none"
		}
		renderCtx := mustSubagentRenderContext(t, e)
		if got := renderCtx["opencode"].ShellProxy; got != resolved {
			t.Fatalf("render shell proxy = %q, want %q", got, resolved)
		}
		fingerprint := renderFingerprint(renderCtx)
		if previous, ok := fingerprints[resolved]; ok && previous != fingerprint {
			t.Fatalf("render fingerprint unstable for %q", resolved)
		}
		fingerprints[resolved] = fingerprint
		if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
			t.Fatalf("apply with proxy %q: %v", proxy, err)
		}
		for _, agent := range agents {
			data, err := os.ReadFile(filepath.Join(e.SubagentDir(), agent+".opencode.md"))
			if err != nil {
				t.Fatal(err)
			}
			text := string(data)
			if baseline[agent] == "" {
				baseline[agent] = text
			}
			if resolved == "none" || agent == "h-review" {
				if text != baseline[agent] {
					t.Errorf("%s with proxy %q must match its unwrapped render", agent, proxy)
				}
				if strings.Contains(text, `"rtk `) {
					t.Errorf("%s with proxy %q gained wrapped rules", agent, proxy)
				}
				continue
			}
			for _, prefix := range []string{"", "rtk ", "rtk proxy "} {
				for _, pattern := range []string{"go test *", "npm test", "./scripts/task-check.sh"} {
					want := fmt.Sprintf("    %q: allow", prefix+pattern)
					if prefix == "rtk " && pattern == "./scripts/task-check.sh" {
						if strings.Contains(text, want) {
							t.Errorf("%s must only proxy unknown executable %s", agent, pattern)
						}
						continue
					}
					if !strings.Contains(text, want) {
						t.Errorf("%s missing %s", agent, want)
					}
				}
				denied := []string{"onto bypass*", "to bypass*"}
				if agent != "homonto" {
					denied = []string{"onto *", "to *", "gh *", "git push", "git push *", "git branch *"}
				}
				for _, pattern := range denied {
					want := fmt.Sprintf("    %q: deny", prefix+pattern)
					if !strings.Contains(text, want) {
						t.Errorf("%s missing %s", agent, want)
					}
				}
			}
			for _, want := range []string{`"*": ask`, `"*;*": ask`, `"*&&*": ask`} {
				if !strings.Contains(text, want) {
					t.Errorf("%s missing %s", agent, want)
				}
			}
			for _, forbidden := range []string{`"rtk *": allow`, `"rtk proxy *": allow`, `"rtk exec *": allow`, `"rtk unknown-command": allow`, `"rtk curl *": allow`} {
				if strings.Contains(text, forbidden) {
					t.Errorf("%s unexpectedly grants %s", agent, forbidden)
				}
			}
		}
	}
	if fingerprints["none"] == fingerprints["rtk"] {
		t.Fatal("tooling selection must change the subagent render fingerprint")
	}
}

// TestHFrameworkRendersCoordinatorAndReadonlyWorkers: declaring [frameworks.h]
// alone installs the shared homonto primary plus both frameworks' specialists
// and the two h workers. Declared repos grant external access to the primary
// and implementers only — the read-only workers must carry no
// external_directory rule (ADR 0039/0045), and the workers' rendered
// permission maps must deny both edits and bash.
func TestHFrameworkRendersCoordinatorAndReadonlyWorkers(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	sibling := filepath.Join(t.TempDir(), "service-a")
	if err := os.MkdirAll(filepath.Join(sibling, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := hFrameworkTOML + "\n[repos]\nservice-a = " + fmt.Sprintf("%q", sibling) + "\n"
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	primary, err := os.ReadFile(filepath.Join(e.SubagentDir(), "homonto.opencode.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"mode: primary", `"h-spike": allow`, `"h-review": allow`, "external_directory:", "webfetch: allow", "websearch: allow", `"go test *": allow`, `"npm run *": allow`, `"*;*": ask`, `"onto bypass*": deny`} {
		if !strings.Contains(string(primary), want) {
			t.Errorf("homonto primary (via h) missing %q:\n%s", want, primary)
		}
	}
	for _, deniedSetter := range []string{`"onto set verify-result*": deny`, `"onto set close-confirmed*": deny`, `"onto set proposal-approved*": deny`, `"onto set approach-confirmed*": deny`} {
		if strings.Contains(string(primary), deniedSetter) {
			t.Errorf("homonto must be able to record routine workflow evidence, but rendered %q:\n%s", deniedSetter, primary)
		}
	}
	for _, implementer := range []string{"onto-implementer", "to-implementer"} {
		data, err := os.ReadFile(filepath.Join(e.SubagentDir(), implementer+".opencode.md"))
		if err != nil {
			t.Fatal(err)
		}
		rendered := string(data)
		if !strings.Contains(rendered, "external_directory:") {
			t.Errorf("%s must inherit declared-repo access under h:\n%s", implementer, data)
		}
		for _, want := range []string{`"*": ask`, `"git diff *": allow`, `"go test *": allow`, `"npm run *": allow`, `"*;*": ask`, `"onto *": deny`, `"to *": deny`, `"gh *": deny`, `"git push": deny`, `"git push *": deny`, `"git branch *": deny`, "webfetch: allow", "websearch: allow"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("%s must retain the delegated execution boundary %q:\n%s", implementer, want, rendered)
			}
		}
	}
	for _, worker := range []string{"h-spike", "h-review", "onto-explorer", "onto-reviewer", "onto-skeptic", "to-explorer", "to-reviewer", "to-skeptic"} {
		data, err := os.ReadFile(filepath.Join(e.SubagentDir(), worker+".opencode.md"))
		if err != nil {
			t.Fatal(err)
		}
		rendered := string(data)
		if strings.Contains(rendered, "external_directory:") {
			t.Errorf("%s must not gain an external-directory rule:\n%s", worker, rendered)
		}
		for _, want := range []string{"edit: deny", "bash: deny", "webfetch: allow", "websearch: allow"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("%s missing %s in its permission map:\n%s", worker, want, rendered)
			}
		}
		if !strings.Contains(rendered, "task: deny") {
			t.Errorf("%s must not spawn:\n%s", worker, rendered)
		}
	}
}
