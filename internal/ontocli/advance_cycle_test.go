package ontocli

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// TestAdvanceCommand_EnteringBuildRefusesDependencyCycle verifies that a change
// participating in a depends-on cycle cannot advance design→build, and the
// phase is left unchanged.
func TestAdvanceCommand_EnteringBuildRefusesDependencyCycle(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "a", "design")
	seedChange(t, dir, "b", "design")
	mutateState(t, dir, "a", func(s *ontostate.State) { s.ApproachConfirmed = "2026-07-22 approach" })
	for _, args := range [][]string{
		{"set", "isolation", "a", "branch", "--dir", dir},
		{"set", "isolation", "b", "branch", "--dir", dir},
		{"set", "deps", "a", "--dep", "b", "--dir", dir},
		{"set", "deps", "b", "--dep", "a", "--dir", dir},
	} {
		if _, err := runOnto(t, args...); err != nil {
			t.Fatalf("setup %v: %v", args, err)
		}
	}
	commitAll(t, dir, "seed cycle")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "a", "--dir", dir})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want error on dependency cycle")
	}
	if !contains(err.Error(), "cycle") {
		t.Errorf("error = %q, want it to mention %q", err.Error(), "cycle")
	}
	st, lErr := ontostate.Load(filepath.Join(dir, "docs", "changes", "a", "onto-state.yaml"))
	if lErr != nil {
		t.Fatal(lErr)
	}
	if st.Phase != "design" {
		t.Errorf("st.Phase = %q, want unchanged %q", st.Phase, "design")
	}
}

// TestAdvanceCommand_EnteringBuildAllowedWithAcyclicDeps verifies a change with
// a non-cyclic dependency still advances into build.
func TestAdvanceCommand_EnteringBuildAllowedWithAcyclicDeps(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "a", "design")
	seedChange(t, dir, "b", "design")
	mutateState(t, dir, "a", func(s *ontostate.State) { s.ApproachConfirmed = "2026-07-22 approach" })
	for _, args := range [][]string{
		{"set", "isolation", "a", "branch", "--dir", dir},
		{"set", "deps", "a", "--dep", "b", "--dir", dir}, // a -> b, b has no dep back: acyclic
	} {
		if _, err := runOnto(t, args...); err != nil {
			t.Fatalf("setup %v: %v", args, err)
		}
	}
	commitAll(t, dir, "seed acyclic")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "a", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (%s)", err, out.String())
	}
	st, lErr := ontostate.Load(filepath.Join(dir, "docs", "changes", "a", "onto-state.yaml"))
	if lErr != nil {
		t.Fatal(lErr)
	}
	if st.Phase != "build" {
		t.Errorf("st.Phase = %q, want %q", st.Phase, "build")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
