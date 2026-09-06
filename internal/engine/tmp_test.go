package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tmpFrameworkTOML = `[frameworks.onto]
source = "builtin:onto"
scope = "project"

[tmp]
dir = ".tmp"

[subagents.homonto.opencode]
model = "test/m"
[subagents.onto-explorer.opencode]
model = "test/m"
[subagents.onto-reviewer.opencode]
model = "test/m"
[subagents.onto-implementer.opencode]
model = "test/m"
[subagents.onto-skeptic.opencode]
model = "test/m"
`

// Declaring [tmp] projects the whole surface: the generated reference into
// the dispatcher and the shared knowledge skill (never a phase skill), the
// scratch directory itself, and a gitignore entry keeping it invisible to the
// workflow dirt gates.
func TestTmpSurfaceProjected(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(tmpFrameworkTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	for _, skill := range []string{"onto", "homonto"} {
		ref := filepath.Join(e.CatalogRoot, skill, "references", "tmp.md")
		data, err := os.ReadFile(ref)
		if err != nil {
			t.Fatalf("%s must carry the tmp reference: %v", skill, err)
		}
		if !strings.Contains(string(data), "` .tmp`") && !strings.Contains(string(data), "`.tmp`") {
			t.Errorf("%s reference must name .tmp:\n%s", skill, data)
		}
	}
	if _, err := os.Stat(filepath.Join(e.CatalogRoot, "onto-build", "references", "tmp.md")); err == nil {
		t.Error("a phase skill must not carry the tmp reference")
	}

	if fi, err := os.Stat(filepath.Join(repo, ".tmp")); err != nil || !fi.IsDir() {
		t.Fatalf("the declared scratch dir must exist after apply: %v", err)
	}
	gi, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatalf("apply must create/augment .gitignore: %v", err)
	}
	if !strings.Contains(string(gi), "/.tmp/") {
		t.Errorf(".gitignore must anchor the scratch dir:\n%s", gi)
	}

	// The surface is not part of the materialize fingerprint: a deleted
	// directory is restored by the next apply without any config change.
	if err := os.RemoveAll(filepath.Join(repo, ".tmp")); err != nil {
		t.Fatal(err)
	}
	// The CLI short-circuits a no-change apply before Engine.Apply runs, so
	// an incomplete surface must force the apply path by itself.
	if !e.CatalogNeedsMaterialize() {
		t.Fatal("a deleted scratch dir must make CatalogNeedsMaterialize true")
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(repo, ".tmp")); err != nil || !fi.IsDir() {
		t.Fatalf("a deleted scratch dir must be recreated by apply: %v", err)
	}
	if e.CatalogNeedsMaterialize() {
		t.Fatal("a complete surface must not force materialization")
	}
}

// Removing [tmp] withdraws the generated references (the wholesale skill
// rebuild drops them) but never deletes the scratch directory or its
// gitignore entry — cleanup is a human decision (ADR 0048).
func TestTmpDisableKeepsDirAndIgnoresButWithdrawsReferences(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(tmpFrameworkTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(repo, ".tmp", "draft.md")
	if err := os.WriteFile(scratch, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	disabled := strings.Replace(tmpFrameworkTOML, "[tmp]\ndir = \".tmp\"\n\n", "", 1)
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(disabled), 0o644); err != nil {
		t.Fatal(err)
	}
	e2 := buildEngine(t, home, repo)
	if err := e2.Apply(context.Background(), mustPlan(t, e2)); err != nil {
		t.Fatalf("apply after disable: %v", err)
	}
	for _, skill := range []string{"onto", "homonto"} {
		if _, err := os.Stat(filepath.Join(e2.CatalogRoot, skill, "references", "tmp.md")); err == nil {
			t.Errorf("%s kept a stale tmp reference after disable", skill)
		}
	}
	if data, err := os.ReadFile(scratch); err != nil || string(data) != "keep me" {
		t.Fatalf("disable must never delete scratch content: %v", err)
	}
	gi, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil || !strings.Contains(string(gi), "/.tmp/") {
		t.Fatalf("the gitignore entry survives disable (removing it is the user's call): %v", err)
	}
}

// A scratch dir under .homonto/ is already covered by the scaffolded
// /.homonto/ ignore entry — apply must not add a redundant one.
func TestTmpUnderHomontoNeedsNoGitignoreEntry(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	doc := strings.Replace(tmpFrameworkTOML, `dir = ".tmp"`, `dir = ".homonto/tmp"`, 1)
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".homonto", "tmp")); err != nil {
		t.Fatalf("scratch dir under .homonto must exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("no .gitignore is needed under .homonto (err=%v)", err)
	}
}
