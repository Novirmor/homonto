package ontocli

import (
	"bytes"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

func newChange(t *testing.T, dir, name string) ontostate.State {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"new", name, "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("new %s: %v (%s)", name, err, out.String())
	}
	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", name, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return st
}

func TestNewCommand_AssignsStableUniqueID(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	a := newChange(t, dir, "alpha")
	b := newChange(t, dir, "beta")

	hex8 := regexp.MustCompile(`^[0-9a-f]{8}$`)
	if !hex8.MatchString(a.ID) {
		t.Errorf("alpha id = %q, want 8 hex chars", a.ID)
	}
	if !hex8.MatchString(b.ID) {
		t.Errorf("beta id = %q, want 8 hex chars", b.ID)
	}
	if a.ID == b.ID {
		t.Errorf("two changes share id %q, want distinct", a.ID)
	}
}

func TestID_ImmutableAcrossSetAndReload(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	a := newChange(t, dir, "alpha")
	id := a.ID

	// Mutate an unrelated field via `onto set` and reload — id must be
	// preserved. base-ref resolves a real commit, so give the fixture a repo.
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "init")
	head, err := gitOutput(t, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"set", "base-ref", "alpha", head, "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set: %v (%s)", err, out.String())
	}
	reloaded, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ID != id {
		t.Errorf("id changed after set: %q -> %q", id, reloaded.ID)
	}
}
