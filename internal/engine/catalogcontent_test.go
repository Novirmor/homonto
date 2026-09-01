package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localFrameworkTOML declares a local single-framework root whose one skill the
// tests edit between applies.
const localFrameworkTOML = `
[frameworks.myfw]
source = "local:./myfw"
scope = "project"
targets = ["opencode"]
`

func seedLocalFramework(t *testing.T, repo, skillBody string) {
	t.Helper()
	root := filepath.Join(repo, "myfw")
	if err := os.MkdirAll(filepath.Join(root, "skills", "greet"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "name = \"myfw\"\nversion = \"0.0.1\"\ndescription = \"t\"\n[dependencies]\nframeworks = []\n[skills]\ngreet = \"skills/greet\"\n"
	if err := os.WriteFile(filepath.Join(root, "framework.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "greet", "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The materialize gate used to key on the BASE catalog's version.txt plus file
// existence, so editing a local: framework's content (or repinning a remote:
// framework's digest — the same overlay path) was met with "No changes.
// Everything up to date." forever: the projected catalog kept serving the old
// bytes. Repinning is how a patched resource ships, which made the staleness
// security-relevant. The gate now digests the source content of every declared
// resource.
func TestApplyRematerializesWhenLocalFrameworkContentChanges(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	seedLocalFramework(t, repo, "---\nname: greet\ndescription: v1\n---\nVERSION ONE\n")
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(localFrameworkTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	materialized := filepath.Join(e.CatalogRoot, "greet", "SKILL.md")
	if b, _ := os.ReadFile(materialized); !strings.Contains(string(b), "VERSION ONE") {
		t.Fatalf("first apply did not materialize the skill:\n%s", b)
	}

	// Edit the framework's content. Nothing else changes: same version.txt,
	// same file set, same model routes.
	seedLocalFramework(t, repo, "---\nname: greet\ndescription: v2\n---\nVERSION TWO\n")
	e2 := buildEngine(t, home, repo)
	if err := e2.Apply(context.Background(), mustPlan(t, e2)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if b, _ := os.ReadFile(materialized); !strings.Contains(string(b), "VERSION TWO") {
		t.Fatalf("content edit did not re-materialize — the tool keeps serving stale bytes:\n%s", b)
	}
}

// The Materialize* calls only ever WRITE declared names, so a renamed or
// de-declared resource left its old files in the catalog roots forever. Litter
// with teeth: the adapters prefer a <name>.<tool>.md variant when one exists,
// so an old render could win over a future same-named verbatim agent.
func TestApplyGCsUndeclaredCatalogEntries(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeConfig(t, repo, "first/model-a") // declares builtin:onto-reviewer for opencode

	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	// Plant pre-rename litter of every kind (the command root may not exist yet
	// — this config declares no commands, which is itself the GC case). The
	// .claude.md variant is litter the removed adapter left behind — exactly
	// the old-world file the GC must still prune.
	stale := []string{
		filepath.Join(e.SubagentCatalogRoot, "code-reviewer.md"),
		filepath.Join(e.SubagentCatalogRoot, "code-reviewer.claude.md"),
		filepath.Join(e.CommandCatalogRoot, "old-command.md"),
	}
	for _, p := range stale {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(e.CatalogRoot, "old-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Change a model route so the gate re-materializes (and therefore GCs).
	writeConfig(t, repo, "second/model-b")
	e2 := buildEngine(t, home, repo)
	if err := e2.Apply(context.Background(), mustPlan(t, e2)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	for _, p := range append(stale, filepath.Join(e.CatalogRoot, "old-skill")) {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("undeclared catalog entry survived GC: %s", p)
		}
	}
	// The declared agent's files must survive.
	if _, err := os.Stat(filepath.Join(e.SubagentCatalogRoot, "onto-reviewer.opencode.md")); err != nil {
		t.Errorf("declared agent's rendered variant must survive GC: %v", err)
	}
}
