package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/catalog"
)

const ontoProjectTOML = `
[frameworks.onto]
source = "builtin:onto"
scope = "project"
targets = ["opencode"]
` + ontoFrameworkModels

func writeToolingConfig(t *testing.T, repo, tooling string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(ontoProjectTOML+tooling), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dispatcherSidecar(e *Engine) string {
	return filepath.Join(e.CatalogRoot, "onto", filepath.FromSlash(catalog.ToolingReferencePath))
}

// Editing [tooling] leaves the catalog version and every resource byte
// untouched, so without the tooling fingerprint the gate would report "up to
// date" and serve the previous providers forever.
func TestApplyRerendersWhenToolingChanges(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeToolingConfig(t, repo, "\n[tooling]\ncode_intel = \"graphify\"\n")

	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	body, err := os.ReadFile(dispatcherSidecar(e))
	if err != nil {
		t.Fatalf("sidecar missing after first apply: %v", err)
	}
	if !strings.Contains(string(body), "graphify") {
		t.Fatalf("first apply did not render graphify:\n%s", body)
	}

	writeToolingConfig(t, repo, "\n[tooling]\ncode_intel = \"okf\"\n")
	e2 := buildEngine(t, home, repo)
	if err := e2.Apply(context.Background(), mustPlan(t, e2)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	body, err = os.ReadFile(dispatcherSidecar(e2))
	if err != nil {
		t.Fatalf("sidecar missing after second apply: %v", err)
	}
	if strings.Contains(string(body), "graphify") {
		t.Errorf("stale provider survived a [tooling] edit:\n%s", body)
	}
	if !strings.Contains(string(body), "okf") {
		t.Errorf("second apply did not render okf:\n%s", body)
	}
}

// The directory existing is not enough: a hand-deleted reference would
// otherwise stay missing behind an up-to-date fingerprint.
func TestApplyRestoresDeletedToolingSidecar(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeToolingConfig(t, repo, "\n[tooling]\nshell_proxy = \"rtk\"\n")

	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	sidecar := dispatcherSidecar(e)
	if err := os.Remove(sidecar); err != nil {
		t.Fatalf("remove sidecar: %v", err)
	}

	e2 := buildEngine(t, home, repo)
	if err := e2.Apply(context.Background(), mustPlan(t, e2)); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if _, err := os.Stat(sidecar); err != nil {
		t.Errorf("deleted tooling reference was not restored: %v", err)
	}
}

// A config with no [tooling] table still gets a reference — one that names no
// provider — so the dispatcher never points at a file that is not there.
func TestApplyWritesSidecarWhenToolingAbsent(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeToolingConfig(t, repo, "")

	e := buildEngine(t, home, repo)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	body, err := os.ReadFile(dispatcherSidecar(e))
	if err != nil {
		t.Fatalf("sidecar missing with no [tooling] table: %v", err)
	}
	for _, absent := range []string{"rtk", "graphify", "okf"} {
		if strings.Contains(string(body), absent) {
			t.Errorf("default reference must name no provider, found %q:\n%s", absent, body)
		}
	}
}
