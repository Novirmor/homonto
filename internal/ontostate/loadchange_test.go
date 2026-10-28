package ontostate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// A lone pre-binary state.yaml is malformed, never silently missing.
func TestLoadChange_LegacySkillStateRejected(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "state.yaml", "change: c\nworkflow: full\nphase: build\n")
	if _, class, err := Classify(dir); class != "malformed" || err == nil || !strings.Contains(err.Error(), "no longer supported") {
		t.Fatalf("Classify = %q, %v; want malformed no-longer-supported", class, err)
	}
}

func TestClassify_MissingState(t *testing.T) {
	dir := t.TempDir() // directory exists, no state file
	_, class, err := Classify(dir)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	if class != "missing-state" {
		t.Errorf("class = %q, want missing-state", class)
	}
}

func TestClassify_ValidAndMalformed(t *testing.T) {
	valid := t.TempDir()
	writeFixture(t, valid, "onto-state.yaml", "schema_version: 3\nchange: c\nphase: build\n")
	if st, class, err := Classify(valid); err != nil || class != "valid" || st.Phase != "build" {
		t.Errorf("valid case: class=%q phase=%q err=%v", class, st.Phase, err)
	}

	bad := t.TempDir()
	writeFixture(t, bad, "onto-state.yaml", "schema_version: 3\nchange: c\nphase: bogus\n")
	if _, class, err := Classify(bad); class != "malformed" || err == nil {
		t.Errorf("malformed case: class=%q err=%v, want malformed + error", class, err)
	}
}
