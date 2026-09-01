package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeRepoSkill writes a local: skill under the content dir.
func writeRepoSkill(t *testing.T, contentDir, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(contentDir, "skills", name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(contentDir, "skills", name, "SKILL.md"),
		[]byte("---\nname: "+name+"\ndescription: test skill\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newGitWorktree creates a directory with a .git entry (the load-validation
// worktree check tests presence, not git metadata).
func newGitWorktree(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestMultiRepoProjectionScopesPerRepository is the stage-2 end-to-end
// contract (ADR 0024): repo-tagged project resources land in the declared
// repository's own files, untagged ones stay in the config repo, an
// UNDECLARED sibling is never touched, each repository records its own state
// partition, and re-planning is idempotent.
func TestMultiRepoProjectionScopesPerRepository(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	cfgRepo := filepath.Join(base, "cfg")
	svc := newGitWorktree(t, filepath.Join(base, "svc"))
	undeclared := newGitWorktree(t, filepath.Join(base, "other"))
	content := filepath.Join(cfgRepo, "homonto")
	for _, d := range []string{cfgRepo, content} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRepoSkill(t, content, "cfg-skill")
	writeRepoSkill(t, content, "svc-skill")

	cfgPath := filepath.Join(cfgRepo, "homonto.toml")
	cfg := `
[repos]
svc = "../svc"

[mcps.global-probe]
command = ["probe", "serve"]
targets = ["opencode"]

[mcps.cfg-probe]
command = ["cfg", "serve"]
scope = "project"
targets = ["opencode"]

[mcps.svc-probe]
command = ["svc", "serve"]
scope = "project"
repo = "svc"
targets = ["opencode"]

[skills.cfg-skill]
source = "local:cfg-skill"
scope = "project"

[skills.svc-skill]
source = "local:svc-skill"
scope = "project"
repo = "svc"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	build := func() *Engine {
		e, err := Build(context.Background(), cfgPath, home, content)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := build()
	if len(e.RepoTargets) != 1 || e.RepoTargets[0].Name != "svc" || e.RepoTargets[0].Dir != svc {
		t.Fatalf("RepoTargets = %+v, want one pair for svc at %s", e.RepoTargets, svc)
	}

	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	var sawMain, sawRepo bool
	for _, cs := range sets {
		if cs.Tool == "opencode" {
			sawMain = true
		}
		if cs.Tool == "opencode@svc" {
			sawRepo = true
		}
	}
	if !sawMain || !sawRepo {
		t.Fatalf("plan tools = %+v, want both opencode and opencode@svc changesets", sets)
	}

	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The declared repo received exactly its tagged resources.
	svcLink := filepath.Join(svc, ".opencode", "skills", "svc-skill")
	if fi, err := os.Lstat(svcLink); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("svc skill link missing at %s", svcLink)
	}
	svcJSON, err := os.ReadFile(filepath.Join(svc, "opencode.jsonc"))
	if err != nil || !strings.Contains(string(svcJSON), "svc-probe") {
		t.Errorf("svc project config missing svc-probe: %v %s", err, svcJSON)
	}
	if strings.Contains(string(svcJSON), "cfg-probe") || strings.Contains(string(svcJSON), "global-probe") {
		t.Errorf("svc project config leaked another repo's server: %s", svcJSON)
	}
	// The config repo received its own, and only its own.
	cfgJSON, err := os.ReadFile(filepath.Join(cfgRepo, "opencode.jsonc"))
	if err != nil || !strings.Contains(string(cfgJSON), "cfg-probe") {
		t.Errorf("config repo project config missing cfg-probe: %v %s", err, cfgJSON)
	}
	if strings.Contains(string(cfgJSON), "svc-probe") {
		t.Errorf("config repo project config leaked svc's server: %s", cfgJSON)
	}
	cfgLink := filepath.Join(cfgRepo, ".opencode", "skills", "cfg-skill")
	if _, err := os.Lstat(cfgLink); err != nil {
		t.Errorf("config repo skill link missing at %s", cfgLink)
	}
	if _, err := os.Lstat(filepath.Join(cfgRepo, ".opencode", "skills", "svc-skill")); err == nil {
		t.Error("config repo must not link the svc-tagged skill")
	}
	// The undeclared sibling was never touched.
	if _, err := os.Stat(filepath.Join(undeclared, ".opencode")); err == nil {
		t.Error("undeclared repo must not gain .opencode/")
	}
	if _, err := os.Stat(filepath.Join(undeclared, "opencode.jsonc")); err == nil {
		t.Error("undeclared repo must not gain opencode.jsonc")
	}
	// Per-repo state partition exists alongside the main state.
	if _, err := os.Stat(filepath.Join(cfgRepo, ".homonto", "state.svc.json")); err != nil {
		t.Errorf("svc state partition missing: %v", err)
	}

	// Idempotent: a fresh plan over both adapters is empty.
	e2 := build()
	sets2, err := e2.Plan()
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range sets2 {
		for _, c := range cs.Changes {
			if c.Action != "noop" {
				t.Errorf("replan not idempotent: %s %s %s", cs.Tool, c.Action, c.Key)
			}
		}
	}

	// Drift is attributed per repo: hand-edit the svc project config and the
	// finding must name opencode@svc.
	svcJSONPath := filepath.Join(svc, "opencode.jsonc")
	if err := os.WriteFile(svcJSONPath, []byte(`{"mcp":{"svc-probe":{"command":["edited"]}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e3 := build()
	drift, _, err := e3.Status()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(drift, "\n")
	if !strings.Contains(joined, "opencode@svc") {
		t.Errorf("drift must attribute to opencode@svc:\n%s", joined)
	}
}

// TestMultiRepoPruneStaysScoped verifies de-declaring a repo-tagged resource
// prunes it from ITS repository only, via the repo's own state partition.
func TestMultiRepoPruneStaysScoped(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	cfgRepo := filepath.Join(base, "cfg")
	svc := newGitWorktree(t, filepath.Join(base, "svc"))
	content := filepath.Join(cfgRepo, "homonto")
	for _, d := range []string{cfgRepo, content} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeRepoSkill(t, content, "svc-skill")
	writeRepoSkill(t, content, "cfg-skill")

	cfgPath := filepath.Join(cfgRepo, "homonto.toml")
	writeCfg := func(withSvc bool) {
		t.Helper()
		svcBlock := ""
		if withSvc {
			svcBlock = "\n[skills.svc-skill]\nsource = \"local:svc-skill\"\nscope = \"project\"\nrepo = \"svc\"\n"
		}
		out := "[repos]\nsvc = \"../svc\"\n\n[skills.cfg-skill]\nsource = \"local:cfg-skill\"\nscope = \"project\"\n" + svcBlock
		if err := os.WriteFile(cfgPath, []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg(true)

	e, err := Build(context.Background(), cfgPath, home, content)
	if err != nil {
		t.Fatal(err)
	}
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(svc, ".opencode", "skills", "svc-skill")); err != nil {
		t.Fatalf("setup: svc link missing: %v", err)
	}

	// De-declare the svc skill: prune removes it in svc, leaves the config
	// repo's own skill link alone.
	writeCfg(false)
	e2, err := Build(context.Background(), cfgPath, home, content)
	if err != nil {
		t.Fatal(err)
	}
	sets2, err := e2.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.Apply(context.Background(), sets2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(svc, ".opencode", "skills", "svc-skill")); !os.IsNotExist(err) {
		t.Errorf("svc link must be pruned after de-declaration, stat err = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(cfgRepo, ".opencode", "skills", "cfg-skill")); err != nil {
		t.Errorf("config repo skill must survive the svc prune: %v", err)
	}
}
