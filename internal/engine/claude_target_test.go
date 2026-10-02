package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/plan"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/tidwall/gjson"
)

// A dual-target MCP projects into both tools' files from one config: the
// opencode entry in opencode.jsonc, the claude entry in ~/.claude.json.
func TestClaudeTargetDualProjection(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	doc := `
[mcps.demo]
command = ["srv", "--flag"]
targets = ["opencode", "claude"]
`
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	build := func() *Engine {
		e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
		return e
	}
	e := build()
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]bool{}
	for _, cs := range sets {
		tools[cs.Tool] = true
	}
	if !tools["opencode"] || !tools["claude"] {
		t.Fatalf("a dual-target MCP must plan for both tools, got %v", tools)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	oc, err := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.jsonc"))
	if err != nil || gjson.GetBytes(oc, "mcp.demo.command.0").String() != "srv" {
		t.Fatalf("opencode projection missing:\n%s\n%v", oc, err)
	}
	cj, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil || gjson.GetBytes(cj, "mcpServers.demo.command").String() != "srv" ||
		gjson.GetBytes(cj, "mcpServers.demo.args.0").String() != "--flag" {
		t.Fatalf("claude projection missing:\n%s\n%v", cj, err)
	}

	// Idempotent across a fresh build.
	e2 := build()
	sets2, err := e2.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.HasChanges(sets2) {
		t.Fatalf("second apply not idempotent: %s", plan.Render(sets2))
	}
}

// A claude-only MCP config must not write any OpenCode destination: the
// opencode adapter has no footprint (nothing declared for it, nothing
// recorded), so it neither plans, nor observes, nor writes.
func TestClaudeOnlyLeavesOpenCodeUnwritten(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	doc := `
[mcps.demo]
command = ["srv"]
targets = ["claude"]
`
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil {
		t.Fatalf("claude projection missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.jsonc")); !os.IsNotExist(err) {
		t.Fatal("a claude-only MCP config must not create opencode.jsonc")
	}
}

// Adapter selection for an opt-in tool (ADR 0066): with no claude target and
// no claude records in state, the claude adapter is not built at all — no
// claude changeset, no claude files. But claude records present at Build time
// DO activate the adapter even without a target: dropping the last claude
// declaration (or inheriting pre-v0.13.0 records) must reconcile — prune the
// de-declared records and retire the stale structured ones — never orphan
// them, because only the adapter can plan their removal.
func TestClaudeAdapterSelectionFollowsState(t *testing.T) {
	newCase := func(t *testing.T) (home, repo string, build func() *Engine) {
		t.Helper()
		home = t.TempDir()
		repo = t.TempDir()
		doc := `
[mcps.demo]
command = ["srv"]
`
		if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		build = func() *Engine {
			e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
			if err != nil {
				t.Fatal(err)
			}
			e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
			return e
		}
		return home, repo, build
	}

	t.Run("no target, no records: dormant", func(t *testing.T) {
		home, _, build := newCase(t)
		e := build()
		for _, a := range e.Adapters {
			if a.Name() == "claude" {
				t.Fatal("no claude adapter may be built without a target or records")
			}
		}
		sets, err := e.Plan()
		if err != nil {
			t.Fatal(err)
		}
		for _, cs := range sets {
			if cs.Tool == "claude" {
				t.Fatal("no claude changeset may exist")
			}
		}
		if err := e.Apply(context.Background(), sets); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
			t.Fatal("no claude file may be written")
		}
	})

	t.Run("no target, stale records: reconciled, not orphaned", func(t *testing.T) {
		home, repo, build := newCase(t)
		// Seed state BEFORE Build so the engine sees the records: a
		// de-declared mcp record plus a stale structured one, exactly what a
		// target removal or a pre-v0.13.0 upgrade leaves behind.
		st, err := state.Load(filepath.Join(repo, ".homonto"))
		if err != nil {
			t.Fatal(err)
		}
		st.Set("claude", "mcp.old", `{"type":"stdio","command":"old","args":[],"env":{}}`, "h1")
		st.Set("claude", "setting.model", `"opus"`, "h2")
		if err := st.Save(filepath.Join(repo, ".homonto")); err != nil {
			t.Fatal(err)
		}

		e := build()
		found := false
		for _, a := range e.Adapters {
			if a.Name() == "claude" {
				found = true
			}
		}
		if !found {
			t.Fatal("claude records in state must activate the claude adapter for reconciliation")
		}
		sets, err := e.Plan()
		if err != nil {
			t.Fatal(err)
		}
		var claudeDeletes int
		for _, cs := range sets {
			if cs.Tool != "claude" {
				continue
			}
			for _, c := range cs.Changes {
				if c.Action == "delete" && (c.Key == "mcp.old" || c.Key == "setting.model") {
					claudeDeletes++
				}
			}
		}
		if claudeDeletes != 2 {
			t.Fatalf("both stale records must be planned for removal, saw %d in %+v", claudeDeletes, sets)
		}
		if err := e.Apply(context.Background(), sets); err != nil {
			t.Fatal(err)
		}
		if _, ok := e.State.Get("claude", "mcp.old"); ok {
			t.Fatal("de-declared mcp record must be gone after apply")
		}
		if _, ok := e.State.Get("claude", "setting.model"); ok {
			t.Fatal("stale structured record must be retired after apply")
		}
		if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
			t.Fatal("reconciling absent values must not create a claude file")
		}
	})
}

// A claude-only config must not be blocked by foreign files in OpenCode's
// plugin directory: the bridge preflight and convergence only run for configs
// with an OpenCode integration surface (ADR 0066 isolation). A project that
// happens to carry its own .opencode/plugins/homonto-workflow.ts is none of
// the claude target's business.
func TestClaudeOnlyApplyIgnoresForeignOpenCodePlugin(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(`
[mcps.demo]
command = ["srv"]
targets = ["claude"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(repo, ".opencode", "plugins", "homonto-workflow.ts")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("// the user's own plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("a claude-only apply must not be blocked by a foreign OpenCode plugin: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); err != nil {
		t.Fatalf("claude projection missing: %v", err)
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != "// the user's own plugin\n" {
		t.Fatalf("the foreign plugin must be untouched, got %q (%v)", got, err)
	}
}

// A claude-only config must not read OpenCode's files at all: an unrelated,
// malformed opencode.jsonc produces no warning, no skipped adapter, and a
// claude-only changeset whose journal label names claude alone.
func TestClaudeOnlyNeverReadsOpenCodeFiles(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(`
[mcps.demo]
command = ["srv"]
targets = ["claude"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ocDir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ocDir, "opencode.jsonc"), []byte(`{"theme": }`), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Warnings) != 0 {
		t.Fatalf("a claude-only config must not warn about opencode's files: %v", e.Warnings)
	}
	for _, cs := range sets {
		if cs.Tool == "opencode" {
			t.Fatal("the idle opencode adapter must not even plan")
		}
	}
	if _, _, err := e.Status(); err != nil {
		t.Fatal(err)
	}
	if len(e.Warnings) != 0 {
		t.Fatalf("status must not observe the idle adapter either: %v", e.Warnings)
	}
	id, err := e.ApplySnapshot(context.Background(), sets)
	if err != nil {
		t.Fatal(err)
	}
	j, ok, err := snapshot.Load(e.StateDir, id)
	if err != nil || !ok {
		t.Fatalf("journal missing: %v %v", ok, err)
	}
	if j.Tool != "claude" {
		t.Fatalf("a claude-only snapshot must be labeled claude, got %q", j.Tool)
	}
}

// Relinquished bridge ownership still converges: a config that enabled the
// bridge, applied, then deleted the [integrations.opencode] table entirely
// (with no framework to keep the integration wanted) must have homonto's
// managed symlink removed on the next apply — without a foreign file at the
// destination ever blocking it, and without CatalogNeedsMaterialize wedging
// the apply path forever once cleanup is done.
func TestRelinquishedBridgeOwnershipConverges(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "homonto.toml")
	write := func(doc string) {
		if err := os.WriteFile(cfgPath, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	build := func() *Engine {
		e, err := Build(context.Background(), cfgPath, home, "homonto")
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
		return e
	}

	// Enable the bridge and apply: the managed symlink exists.
	write("[integrations.opencode]\nworkflow_bridge = true\n\n[mcps.demo]\ncommand=[\"srv\"]\ntargets=[\"opencode\"]\n")
	e := build()
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("bridge apply: %v", err)
	}
	dst := e.workflowBridgeDestination()
	if target, err := os.Readlink(dst); err != nil {
		t.Fatalf("managed bridge link missing after apply: %v", err)
	} else if target == "" {
		t.Fatal("empty link target")
	}

	// Relinquish: the whole table goes away; no frameworks declared.
	write("[mcps.demo]\ncommand=[\"srv\"]\ntargets=[\"opencode\"]\n")
	e2 := build()
	if !e2.bridgeOwnedOnDisk() {
		t.Fatal("owned-link detection must fire after relinquish")
	}
	if !e2.CatalogNeedsMaterialize() {
		t.Fatal("pending owned-link cleanup must force the apply path")
	}
	if err := e2.Apply(context.Background(), mustPlan(t, e2)); err != nil {
		t.Fatalf("relinquished-ownership apply must succeed: %v", err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("the managed bridge link must be removed, got %v", err)
	}

	// Converged: nothing wedges the apply path, and a foreign file at the
	// destination is none of this config's business.
	if e2.CatalogNeedsMaterialize() {
		t.Fatal("CatalogNeedsMaterialize must settle after cleanup")
	}
	if err := os.WriteFile(dst, []byte("// user's own plugin\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e3 := build()
	if e3.CatalogNeedsMaterialize() {
		t.Fatal("a foreign plugin must not wedge a config that claims no integration")
	}
	if err := e3.Apply(context.Background(), mustPlan(t, e3)); err != nil {
		t.Fatalf("a foreign plugin must not block a config that claims no integration: %v", err)
	}
}
