package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/routing"
)

type fakeModels struct {
	ids   []string
	err   error
	calls int
}

func (f *fakeModels) Models(context.Context) ([]string, error) {
	f.calls++
	return f.ids, f.err
}

// useDiscoverer replaces model discovery for engines built during the test.
func useDiscoverer(t *testing.T, d routing.Discoverer) {
	t.Helper()
	prev := newRouteDiscoverer
	newRouteDiscoverer = func(string) routing.Discoverer { return d }
	t.Cleanup(func() { newRouteDiscoverer = prev })
}

const routedConfig = `
[engines.onto]
source = "builtin:onto"
scope = "project"

[engines.no]
source = "builtin:no"
scope = "project"

[subagents.mine]
source = "local:mine"
scope = "project"

[routing.default.opencode]
model = "p/default"

[routing.engines.onto.stages.verify.opencode]
model = "p/verify"
variant = "high"

[routing.engines.no.opencode]
mode = "auto"
providers = ["fast"]
prefer = "cost"
fallback = "p/default"

[routing.models.opencode."fast/a"]
cost = 2.0
[routing.models.opencode."fast/b"]
cost = 1.0

[subagents.onto-reviewer.opencode]
model = "p/pinned"
`

const mineAgent = "---\nname: mine\ndescription: mine\nmode: subagent\nhomonto:\n  read_only: true\n  bash: false\n  network: false\n  dialogs: false\n  spawn: []\n---\nbody\n"

func routedEngine(t *testing.T, discover *fakeModels) (*Engine, string) {
	t.Helper()
	home, repo := t.TempDir(), t.TempDir()
	for name, data := range map[string]string{"homonto.toml": routedConfig, "content/subagents/mine.md": mineAgent} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	useDiscoverer(t, discover)
	return buildEngine(t, home, repo), repo
}

func renderedAgent(t *testing.T, repo, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, ".homonto", "catalog", "subagents", name+".opencode.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRoutingPoliciesReachOpenCodeAgents is the projection proof for
// ADR 0099/0100: the routed model is stamped into each rendered OpenCode
// agent, the apply records it, and pins still win.
func TestRoutingPoliciesReachOpenCodeAgents(t *testing.T) {
	discover := &fakeModels{ids: []string{"fast/a", "fast/b", "p/default", "slow/c"}}
	e, repo := routedEngine(t, discover)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	for agent, want := range map[string]string{
		"homonto":        `model: "p/default"`,
		"onto-skeptic":   `model: "p/verify"`,
		"onto-reviewer":  `model: "p/pinned"`,
		"onto-explorer":  `model: "p/default"`,
		"no-implementer": `model: "fast/b"`,
		"no-reviewer":    `model: "fast/b"`,
	} {
		if got := renderedAgent(t, repo, agent); !strings.Contains(got, want) {
			t.Errorf("%s: want %s in\n%s", agent, want, got)
		}
	}
	if got := renderedAgent(t, repo, "onto-skeptic"); !strings.Contains(got, "variant: high") && !strings.Contains(got, `variant: "high"`) {
		t.Errorf("skeptic lacks its variant:\n%s", got)
	}
	external, err := os.ReadFile(filepath.Join(repo, ".homonto", "rendered", "subagents", "mine.opencode.md"))
	if err != nil || !strings.Contains(string(external), `model: "p/default"`) {
		t.Fatalf("external agent not routed: %v\n%s", err, external)
	}
	if discover.calls != 1 {
		t.Fatalf("discovery ran %d times for one plan+apply", discover.calls)
	}
	rec, ok, err := e.AppliedRoutes()
	if err != nil || !ok || rec.Discovery != "ok" || rec.Models != 4 {
		t.Fatalf("record = %+v %v %v", rec, ok, err)
	}
	byAgent := map[string]ResolvedRoute{}
	for _, r := range rec.Routes {
		byAgent[r.Agent] = r
	}
	if r := byAgent["no-reviewer"]; r.Selector != "routing.engines.no.opencode" || r.Selection.Model != "fast/b" || r.Selection.Mode != config.RouteAuto {
		t.Fatalf("no-reviewer record = %+v", r)
	}
	if r := byAgent["onto-reviewer"]; r.From != config.RouteFromPin || r.Selection != nil {
		t.Fatalf("pinned record = %+v", r)
	}
	// The explorer serves stages the verify policy does not cover: plan says so.
	if !strings.Contains(strings.Join(e.Warnings, "\n"), "routing: onto-explorer: places this agent serves disagree") {
		t.Fatalf("warnings = %v", e.Warnings)
	}
}

func TestRoutingFallbackIsRecordedAndWarned(t *testing.T) {
	e, _ := routedEngine(t, &fakeModels{err: errors.New("opencode not found")})
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	rec, _, err := e.AppliedRoutes()
	if err != nil || rec.Discovery != "failed" || !strings.Contains(rec.DiscoveryError, "opencode not found") {
		t.Fatalf("record = %+v %v", rec, err)
	}
	if !strings.Contains(strings.Join(e.Warnings, "\n"), "routing: no-implementer uses fallback p/default from routing.engines.no.opencode") {
		t.Fatalf("warnings = %v", e.Warnings)
	}
}

func TestRoutingRecordRemovedWithoutPolicies(t *testing.T) {
	e, repo := routedEngine(t, &fakeModels{ids: []string{"fast/b", "p/default"}})
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	pinned := "[engines.no]\nsource = \"builtin:no\"\nscope = \"project\"\n[subagents.no-implementer.opencode]\nmodel = \"m/x\"\n[subagents.no-reviewer.opencode]\nmodel = \"m/x\"\n"
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(pinned), 0o644); err != nil {
		t.Fatal(err)
	}
	e = buildEngine(t, t.TempDir(), repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := e.AppliedRoutes(); ok || err != nil {
		t.Fatalf("stale routing record kept: %v %v", ok, err)
	}
}

func TestExternalAutoRoutingDiscoversDuringPlanNotBuild(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	config := `[subagents.external]
source = "local:external"
scope = "project"

[routing.default.opencode]
mode = "auto"
fallback = "p/fallback"
`
	agent := `---
name: external
description: external
mode: subagent
homonto:
  read_only: true
  bash: false
  network: false
  dialogs: false
  spawn: []
---
body
`
	if err := os.MkdirAll(filepath.Join(repo, "content", "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "content", "subagents", "external.md"), []byte(agent), 0o644); err != nil {
		t.Fatal(err)
	}
	discover := &fakeModels{ids: []string{"p/available", "p/fallback"}}
	useDiscoverer(t, discover)
	e := buildEngine(t, home, repo)
	if discover.calls != 0 {
		t.Fatalf("Build ran discovery %d times", discover.calls)
	}
	if _, err := e.Plan(); err != nil {
		t.Fatal(err)
	}
	if discover.calls != 1 {
		t.Fatalf("Plan ran discovery %d times, want 1", discover.calls)
	}
}
