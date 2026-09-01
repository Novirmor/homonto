package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/secret"
)

// ontoFrameworkModels is the per-agent override blocks required by the onto
// framework's expanded subagents. Tests that install the onto framework must
// declare one of these per agent now that tiers are gone.
const ontoFrameworkModels = `
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

const ontoTOML = `
[frameworks.onto]
source = "builtin:onto"
scope = "user"
targets = ["opencode"]
` + ontoFrameworkModels

const commandTOML = `
[commands.example-command]
source = "builtin:example-command"
scope = "user"
targets = ["opencode"]
`

const subagentTOML = `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"
targets = ["opencode"]

[subagents.onto-reviewer.opencode]
model = "opus"
`

func buildEngine(t *testing.T, home, repo string) *Engine {
	t.Helper()
	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	return e
}

func buildEngineWithSubagent(t *testing.T) *Engine {
	t.Helper()
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(subagentTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	return buildEngine(t, home, repo)
}

func mustPlan(t *testing.T, e *Engine) []adapter.ChangeSet {
	t.Helper()
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	return sets
}

// projectOntoTOML installs a builtin framework at PROJECT scope so its skill
// links land under <projectRoot>/.opencode/skills — the layout where a catalog
// symlink target computed relative to the wrong base dangles.
const projectOntoTOML = `
[frameworks.onto]
source = "builtin:onto"
scope = "project"
targets = ["opencode"]
` + ontoFrameworkModels

// TestApplyRelativeConfigLinksResolve is the regression guard for the dangling
// catalog-symlink bug: with the default relative --config ("homonto.toml" from
// the repo cwd), filepath.Dir(configPath) is ".", so a stateDir built from it
// was relative and every catalog-skill link target was stored as
// ".homonto/catalog/skills/<name>" — which resolves against the *link's* dir
// (.opencode/skills/) and dangles, making the skill invisible to the tool. The
// link target must be absolute and the link must resolve to real content.
func TestApplyRelativeConfigLinksResolve(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(projectOntoTOML), 0o644)

	// cd into the repo and drive Build with RELATIVE paths, exactly as the CLI
	// does under the default `--config homonto.toml`. (Manual save/restore
	// rather than t.Chdir, which needs go1.24 while the module targets go1.23.)
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prevWD) })
	e, err := Build(context.Background(), "homonto.toml", home, "content")
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	link := filepath.Join(repo, ".opencode", "skills", "onto-open")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("expected a symlink at %s: %v", link, err)
	}
	if !filepath.IsAbs(target) {
		t.Fatalf("catalog link target must be absolute (dangles otherwise), got %q", target)
	}
	// os.Stat follows the link: it fails on a dangling target.
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("catalog skill link does not resolve to real content: %v", err)
	}
}

func TestApplyMaterializesBuiltinSkills(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(ontoTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// A known onto skill materialized under .homonto/catalog/skills/.
	got := filepath.Join(repo, ".homonto", "catalog", "skills", "onto-open", "SKILL.md")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("onto-open not materialized: %v", err)
	}
	// State recorded the catalog version.
	if e.State.CatalogVersionRecorded() == "" {
		t.Fatal("catalog version not recorded after materialization")
	}
	// The rest of the framework's skills materialized too, not just the first.
	if _, err := os.Stat(filepath.Join(repo, ".homonto", "catalog", "skills", "onto-no-slop")); err != nil {
		t.Fatalf("sibling framework skill onto-no-slop not materialized: %v", err)
	}
}

func TestApplyRematerializesWhenVersionStale(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(ontoTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}
	skillFile := filepath.Join(repo, ".homonto", "catalog", "skills", "onto-open", "SKILL.md")

	// Simulate a partial/stale cache: corrupt content + wipe the recorded version.
	os.WriteFile(skillFile, []byte("STALE"), 0o644)
	e.State.SetCatalogVersion("")
	if err := e.State.Save(e.StateDir); err != nil {
		t.Fatal(err)
	}

	e2 := buildEngine(t, home, repo)
	sets2, _ := e2.Plan()
	if err := e2.Apply(context.Background(), sets2); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if b, _ := os.ReadFile(skillFile); string(b) == "STALE" {
		t.Fatal("stale content not refreshed when recorded version was empty")
	}
}

func TestApplyRematerializesWhenSkillDirMissing(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(ontoTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}
	skillDir := filepath.Join(e.CatalogDir(), "onto-open")
	if _, err := os.Stat(skillDir); err != nil {
		t.Fatalf("onto-open not materialized after first apply: %v", err)
	}

	// Delete a materialized skill dir while leaving the recorded catalog
	// version unchanged (still matching the current catalog version).
	if err := os.RemoveAll(skillDir); err != nil {
		t.Fatal(err)
	}

	e2 := buildEngine(t, home, repo)
	sets2, _ := e2.Plan()
	if err := e2.Apply(context.Background(), sets2); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatalf("onto-open not restored after missing dir triggered re-materialization: %v", err)
	}
}

func TestApplySkipsRematerializeWhenVersionMatchesAndDirsIntact(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(ontoTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}
	skillDir := filepath.Join(e.CatalogDir(), "onto-open")
	sentinel := filepath.Join(skillDir, "SENTINEL")
	if err := os.WriteFile(sentinel, []byte("keep-me"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Recorded version still matches and all skill dirs are intact, so the
	// re-apply must skip materialization entirely (catalog.Materialize does
	// os.RemoveAll(dstDir) per skill, which would delete the sentinel).
	e2 := buildEngine(t, home, repo)
	sets2, _ := e2.Plan()
	if err := e2.Apply(context.Background(), sets2); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel removed: re-apply re-materialized despite matching version and intact dirs: %v", err)
	}
}

func TestApplyMaterializesBuiltinCommand(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(commandTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got := filepath.Join(repo, ".homonto", "catalog", "commands", "example-command.md")
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("example-command not materialized: %v", err)
	}
	if e.State.CatalogVersionRecorded() == "" {
		t.Fatal("catalog version not recorded after command materialization")
	}
}

func TestApplyRematerializesWhenCommandFileMissing(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(commandTOML), 0o644)

	e := buildEngine(t, home, repo)
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cmdFile := filepath.Join(e.CommandDir(), "example-command.md")
	if err := os.Remove(cmdFile); err != nil {
		t.Fatal(err)
	}

	e2 := buildEngine(t, home, repo)
	sets2, _ := e2.Plan()
	if err := e2.Apply(context.Background(), sets2); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	if _, err := os.Stat(cmdFile); err != nil {
		t.Fatalf("command not restored after missing file triggered re-materialization: %v", err)
	}
}

func TestApplyMaterializesBuiltinSubagent(t *testing.T) {
	e := buildEngineWithSubagent(t)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	p := filepath.Join(e.SubagentDir(), "onto-reviewer.md")
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("subagent not materialized: %v", err)
	}
}

func TestApplyRematerializesWhenSubagentFileMissing(t *testing.T) {
	e := buildEngineWithSubagent(t)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	p := filepath.Join(e.SubagentDir(), "onto-reviewer.md")
	if err := os.Remove(p); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("subagent not re-materialized when file missing: %v", err)
	}
}
