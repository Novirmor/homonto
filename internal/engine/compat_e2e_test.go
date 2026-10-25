package engine

import (
	"os"
	"path/filepath"
	"testing"
)

// localFwCompat writes a local framework root declaring [compat].homonto=compat.
func localFwCompat(t *testing.T, repo, compat string) {
	t.Helper()
	fw := filepath.Join(repo, "myfw")
	if err := os.MkdirAll(filepath.Join(fw, "skills", "myskill"), 0o755); err != nil {
		t.Fatal(err)
	}
	man := "name = \"myfw\"\nversion = \"0.1.0\"\n[compat]\nhomonto = \"" + compat + "\"\n[skills]\nmyskill = \"skills/myskill\"\n"
	os.WriteFile(filepath.Join(fw, "framework.toml"), []byte(man), 0o644)
	os.WriteFile(filepath.Join(fw, "skills", "myskill", "SKILL.md"), []byte("s"), 0o644)
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[engines.myfw]\nsource = \"local:./myfw\"\nscope = \"user\"\n"), 0o644)
}

func TestPlan_IncompatibleFrameworkFailsFailClosed(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	localFwCompat(t, repo, ">=99.0.0")
	e := buildEngine(t, home, repo)
	e.HomontoVersion = "0.1.0"
	if _, err := e.Plan(); err == nil {
		t.Fatal("an incompatible framework must fail Plan fail-closed")
	}
}

func TestPlan_CompatibleFrameworkLoads(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	localFwCompat(t, repo, ">=0.1.0")
	e := buildEngine(t, home, repo)
	e.HomontoVersion = "0.1.0-dev" // dev build satisfies >=0.1.0 (pre-release stripped)
	if _, err := e.Plan(); err != nil {
		t.Fatalf("a compatible framework should load: %v", err)
	}
}
