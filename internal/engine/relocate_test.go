package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
)

// relocateCfg declares one project-scoped local skill and one user-scoped
// local skill, exercising both relocation domains.
const relocateCfg = `
[skills.projskill]
source = "local:projskill"
scope = "project"

[skills.userskill]
source = "local:userskill"
scope = "user"
`

func relocateSetup(t *testing.T) (home, repo string, e *Engine) {
	t.Helper()
	home = t.TempDir()
	repo = t.TempDir()
	os.MkdirAll(filepath.Join(repo, "homonto", "skills", "projskill"), 0o755)
	os.WriteFile(filepath.Join(repo, "homonto", "skills", "projskill", "SKILL.md"), []byte("# proj\n"), 0o644)
	os.MkdirAll(filepath.Join(repo, "homonto", "skills", "userskill"), 0o755)
	os.WriteFile(filepath.Join(repo, "homonto", "skills", "userskill", "SKILL.md"), []byte("# user\n"), 0o644)
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(relocateCfg), 0o644)

	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "homonto"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return home, repo, e
}

// TestRepoMoveConverges (L6): renaming the whole repository and re-applying
// converges for the project link — it carries a relative target, so it
// resolves at the new location without manual deletion — and the user link
// stays absolute (its $HOME destination did not move).
func TestRepoMoveConverges(t *testing.T) {
	home, repo, _ := relocateSetup(t)

	moved := filepath.Join(filepath.Dir(repo), "moved-repo")
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}

	// Re-apply at the new location: fresh engine over the moved tree.
	e2, err := Build(context.Background(), filepath.Join(moved, "homonto.toml"), home, filepath.Join(moved, "homonto"))
	if err != nil {
		t.Fatal(err)
	}
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	sets, err := e2.Plan()
	if err != nil {
		t.Fatalf("plan after move: %v", err)
	}
	if err := e2.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply after move: %v", err)
	}

	projLink := filepath.Join(moved, ".opencode", "skills", "projskill")
	if resolved, err := filepath.EvalSymlinks(projLink); err != nil || !strings.HasSuffix(resolved, filepath.Join("homonto", "skills", "projskill")) {
		t.Fatalf("project link does not resolve after the move: %q (%v)", resolved, err)
	}

	userLink := filepath.Join(home, ".config", "opencode", "skills", "userskill")
	tgt, err := os.Readlink(userLink)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(tgt) {
		t.Fatalf("user-scope link must stay absolute, got %q", tgt)
	}

	// Second apply is a no-op.
	sets2, _ := e2.Plan()
	for _, cs := range sets2 {
		for _, c := range cs.Changes {
			if c.Action != "noop" && c.Action != "adopt" {
				t.Fatalf("second apply after move not clean: %s %s", c.Action, c.Key)
			}
		}
	}
}

// TestLegacyAbsoluteLinkRepair (L4): a link recorded with the pre-0026
// absolute spelling survives a wholesale repo move as a dangling link whose
// target EXACTLY matches the record. plan must authorize a repair (not a
// conflict), apply must relink it, and a foreign link that does NOT match the
// record must still be refused.
func TestLegacyAbsoluteLinkRepair(t *testing.T) {
	home, repo, e := relocateSetup(t)

	moved := filepath.Join(filepath.Dir(repo), "moved-repo")
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}

	// Simulate the legacy state: rewrite the project entry's recorded target
	// (and the on-disk link) to the OLD absolute path.
	stFile := filepath.Join(moved, ".homonto")
	projLink := filepath.Join(moved, ".opencode", "skills", "projskill")
	if err := os.Symlink(filepath.Join(repo, "homonto", "skills", "projskill"), projLink+".tmp"); err != nil {
		t.Fatal(err)
	}
	os.Remove(projLink)
	if err := os.Rename(projLink+".tmp", projLink); err != nil {
		t.Fatal(err)
	}
	oldDesired := filepath.Join(repo, ".opencode", "skills", "projskill") + " -> " + filepath.Join(repo, "homonto", "skills", "projskill")
	st, err := state.Load(stFile)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := st.Get("opencode", "skill.projskill"); !ok {
		t.Fatal("skill.projskill not recorded")
	} else {
		st.Set("opencode", "skill.projskill", oldDesired, e.Applied)
	}
	if err := st.Save(stFile); err != nil {
		t.Fatal(err)
	}
	_ = e // engine from setup is bound to the old path; the move below rebuilds

	e2, err := Build(context.Background(), filepath.Join(moved, "homonto.toml"), home, filepath.Join(moved, "homonto"))
	if err != nil {
		t.Fatal(err)
	}
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	sets, err := e2.Plan()
	if err != nil {
		t.Fatalf("plan with stale legacy link: %v (repair must be authorized)", err)
	}
	foundRepair := false
	for _, cs := range sets {
		for _, c := range cs.Changes {
			if c.Key == "skill.projskill" && c.Action == "update" {
				foundRepair = true
			}
		}
	}
	if !foundRepair {
		t.Fatalf("plan did not plan a repair for the stale legacy link:\n%v", sets)
	}
	if err := e2.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply repair: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(projLink); err != nil || !strings.HasSuffix(resolved, filepath.Join("homonto", "skills", "projskill")) {
		t.Fatalf("repaired link does not resolve: %q (%v)", resolved, err)
	}
}

// TestForeignLinkAfterMoveStillRefused (L4 negative): a link at the
// destination pointing somewhere unrecorded is foreign — refused even when a
// repository just moved.
func TestForeignLinkAfterMoveStillRefused(t *testing.T) {
	home, repo, _ := relocateSetup(t)
	moved := filepath.Join(filepath.Dir(repo), "moved-repo")
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}
	projLink := filepath.Join(moved, ".opencode", "skills", "projskill")
	os.Remove(projLink)
	elsewhere := t.TempDir()
	if err := os.Symlink(filepath.Join(elsewhere, "not-ours"), projLink); err != nil {
		t.Fatal(err)
	}

	e2, err := Build(context.Background(), filepath.Join(moved, "homonto.toml"), home, filepath.Join(moved, "homonto"))
	if err != nil {
		t.Fatal(err)
	}
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	// The adapter's conflict surfaces as a skipped-adapter warning at engine
	// level (the pre-existing contract): the tool is never written over a
	// foreign link. Assert the warning, not a Plan error.
	if _, err := e2.Plan(); err != nil {
		t.Fatalf("plan: %v", err)
	}
	conflicted := false
	for _, w := range e2.Warnings {
		if strings.Contains(w, "conflict") {
			conflicted = true
		}
	}
	if !conflicted {
		t.Fatalf("foreign link must surface a conflict warning, got %v", e2.Warnings)
	}
}

// TestDeDeclareAfterMoveLeavesNoOrphan (L5): removing the declaration after
// a repository move prunes the link at its CURRENT location too, not only at
// the stale recorded one.
func TestDeDeclareAfterMoveLeavesNoOrphan(t *testing.T) {
	home, repo, _ := relocateSetup(t)
	moved := filepath.Join(filepath.Dir(repo), "moved-repo")
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}

	// Drop the project skill from the config; keep the user skill.
	without := ""
	for _, line := range splitLines(relocateCfg) {
		if line == "" {
			continue
		}
		without += line + "\n"
	}
	// Naive but sufficient: rewrite config without the projskill block.
	trimmed := ""
	skip := false
	for _, line := range splitLines(relocateCfg) {
		if line == "[skills.projskill]" {
			skip = true
			continue
		}
		if skip && line != "" && line[0] == '[' {
			skip = false
		}
		if !skip && line != "" {
			trimmed += line + "\n"
		}
	}
	os.WriteFile(filepath.Join(moved, "homonto.toml"), []byte(trimmed), 0o644)

	e2, err := Build(context.Background(), filepath.Join(moved, "homonto.toml"), home, filepath.Join(moved, "homonto"))
	if err != nil {
		t.Fatal(err)
	}
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	sets, err := e2.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := e2.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}

	projLink := filepath.Join(moved, ".opencode", "skills", "projskill")
	if _, err := os.Lstat(projLink); err == nil {
		t.Fatal("de-declared skill left an orphan link at the moved location")
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// mustReplaceJSONString swaps the "desired" value of the named key inside the
// state JSON. It uses the same escaping rules as encoding/json for plain
// paths (no control characters), which suffices for the test's temp paths.
