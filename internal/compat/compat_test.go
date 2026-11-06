package compat

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/engine"
	"github.com/Novirmor/homonto/internal/ontostate"
	"github.com/Novirmor/homonto/internal/state"
	"github.com/Novirmor/homonto/internal/tostate"
)

// fixture returns a frozen fixture directory. Tests read it in place or copy
// it, and never write into testdata.
func fixture(t *testing.T, version, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "compat", version, name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("fixture %s/%s: %v", version, name, err)
	}
	return dir
}

// copyTree copies a fixture into a temp dir so a loader that writes beside
// its input can never modify testdata.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestV1ConfigLoadsWithTheSameMeaning(t *testing.T) {
	dir := copyTree(t, fixture(t, "1.0", "config"))
	c, err := config.Load(filepath.Join(dir, "homonto.toml"))
	if err != nil {
		t.Fatalf("a 1.0 config no longer loads: %v", err)
	}
	if !c.TargetsHarness("claude") || !c.TargetsHarness("opencode") {
		t.Errorf("harnesses = %v", c.Harnesses)
	}
	for _, name := range []string{"onto", "to", "no"} {
		if c.Engines[name].Source != "builtin:"+name {
			t.Errorf("engine %s = %+v", name, c.Engines[name])
		}
	}
	if c.Engines["no"].Settings["isolation"] != "worktree" {
		t.Errorf("no settings = %v", c.Engines["no"].Settings)
	}
	if p := c.Routing.Default.For("claude"); p == nil || p.Model != "sonnet" {
		t.Errorf("routing.default.claude = %+v", p)
	}
	if p := c.Routing.Engines["no"].For("opencode"); p == nil || p.ModeOrDefault() != config.RouteAuto || p.Fallback != "anthropic/claude-sonnet-5-5" {
		t.Errorf("routing.engines.no.opencode = %+v", p)
	}
	route, err := c.DecisionRouteFor("triage.kind")
	if err != nil || route.ProviderName != "openai" || route.Provider.Protocol != "openai-decisions" {
		t.Errorf("decision route = %+v, %v", route, err)
	}
	if got := c.Subagents["onto-skeptic"].OpenCode; got.Model != "anthropic/claude-opus-5-5" || got.Variant != "high" || got.Steps == nil || *got.Steps != 160 {
		t.Errorf("onto-skeptic opencode = %+v", got)
	}
	if ok, tmp := c.ResolvedTmp(); !ok || tmp != ".tmp" {
		t.Errorf("tmp = %q %v", tmp, ok)
	}
}

func TestV1StateLoads(t *testing.T) {
	st, err := state.Load(filepath.Join(fixture(t, "1.0", "state"), ".homonto"))
	if err != nil {
		t.Fatalf("a 1.0 state.json no longer loads: %v", err)
	}
	if st.SchemaVersion != 3 {
		t.Errorf("schema = %d", st.SchemaVersion)
	}
	if e, ok := st.Get("claude", "projmcp.homonto"); !ok || e.Origin == nil || e.Origin.Kind != "direct" {
		t.Errorf("claude runtime entry = %+v %v", e, ok)
	}
	if e, ok := st.Get("opencode", "skill.onto"); !ok || e.Origin == nil || e.Origin.Framework != "onto" {
		t.Errorf("opencode skill entry = %+v %v", e, ok)
	}
	if len(st.Tombstones) != 1 || st.Tombstones[0].Key != "skill.grilling" {
		t.Errorf("tombstones = %+v", st.Tombstones)
	}
	if st.HomontoVersionRecorded() != "v1.0.0" || st.CatalogVersionRecorded() != "1.0.0" {
		t.Errorf("recorded versions = %q %q", st.HomontoVersionRecorded(), st.CatalogVersionRecorded())
	}
}

func TestV1RoutingRecordLoads(t *testing.T) {
	rec, ok, err := engine.ReadRoutingRecord(filepath.Join(fixture(t, "1.0", "state"), ".homonto"))
	if err != nil || !ok {
		t.Fatalf("a 1.0 routing.json no longer loads: %v %v", ok, err)
	}
	if rec.AppliedAt != "2026-11-06T00:00:00Z" || rec.Discovery != "not-run" || len(rec.Routes) != 2 || rec.Routes[0].Tool != "opencode" {
		t.Errorf("top-level opencode routing = %+v", rec.RouteReport)
	}
	by := rec.ByHarness()
	for harness, want := range map[string]string{"opencode": "p/review", "claude": "opus"} {
		var got string
		for _, r := range by[harness].Routes {
			if r.Agent == "no-reviewer" && r.Selection != nil {
				got = r.Selection.Model
			}
		}
		if got != want {
			t.Errorf("%s no-reviewer = %q, want %q", harness, got, want)
		}
	}
}

func TestV1OntoStateLoads(t *testing.T) {
	st, err := ontostate.LoadChange(filepath.Join(fixture(t, "1.0", "onto"), "docs", "changes", "compat-fixture"))
	if err != nil {
		t.Fatalf("a 1.0 onto-state.yaml no longer loads: %v", err)
	}
	if st.Change != "compat-fixture" || st.Workflow != "full" || st.Phase != "open" || st.ID != "5347f1c2" {
		t.Errorf("onto state = %+v", st)
	}
}

func TestV1ToStateLoads(t *testing.T) {
	st, err := tostate.Load(filepath.Join(fixture(t, "1.0", "to"), "docs", "tasks", "compat-task", "to-state.yaml"))
	if err != nil {
		t.Fatalf("a 1.0 to-state.yaml no longer loads: %v", err)
	}
	if st.Change != "compat-task" || st.Phase != "plan" || st.Terminal() {
		t.Errorf("to state = %+v", st)
	}
}

func TestV1PackageConforms(t *testing.T) {
	cl, err := catalog.NewWithLocal(map[string]fs.FS{"compat-engine": os.DirFS(fixture(t, "1.0", "package"))})
	if err != nil {
		t.Fatalf("a 1.0 package manifest no longer loads: %v", err)
	}
	r, err := cl.Conformance("compat-engine")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Passed() || r.Kind != catalog.KindEngine || r.Contract != "1.0" {
		t.Fatalf("1.0 package no longer conforms: %+v", r)
	}
	fw, _ := cl.Framework("compat-engine")
	if fw.Settings["review"].Default != "always" {
		t.Errorf("settings = %+v", fw.Settings)
	}
}
