package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestApply_LocalFrameworkSkillMaterialized is the acceptance gate for local
// framework resolution: a [engines.X] source="local:<path>" installs its
// skill through the same path as a builtin framework.
func TestApply_LocalFrameworkSkillMaterialized(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	// A local framework root at <repo>/myfw (framework.toml name=myfw + a skill).
	fw := filepath.Join(repo, "myfw")
	if err := os.MkdirAll(filepath.Join(fw, "skills", "myskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must(filepath.Join(fw, "framework.toml"), "name = \"myfw\"\nversion = \"0.1.0\"\n[skills]\nmyskill = \"skills/myskill\"\n")
	must(filepath.Join(fw, "skills", "myskill", "SKILL.md"), "local skill body")
	must(filepath.Join(repo, "homonto.toml"), "[engines.myfw]\nsource = \"local:./myfw\"\nscope = \"user\"\n")

	e := buildEngine(t, home, repo)
	sets, err := e.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The local framework's skill materialized into the catalog root, same as a
	// builtin framework's skill.
	got := filepath.Join(repo, ".homonto", "catalog", "skills", "myskill", "SKILL.md")
	b, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("local framework skill not materialized: %v", err)
	}
	if string(b) != "local skill body" {
		t.Errorf("materialized content = %q, want %q", b, "local skill body")
	}
}
