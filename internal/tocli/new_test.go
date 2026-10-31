package tocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/tostate"
)

// TestNew_ScaffoldsExpectedFiles verifies `to new` produces exactly the
// documented layout: a change directory containing to-state.yaml and an
// empty plan.md.
func TestNew_ScaffoldsExpectedFiles(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "shape", "--dir", dir)

	change := changeDir(dir, "shape")
	for _, sub := range []string{tostate.FileName, "plan.md"} {
		info, err := os.Stat(filepath.Join(change, sub))
		if err != nil {
			t.Errorf("new did not scaffold %s: %v", sub, err)
			continue
		}
		if sub == "plan.md" && info.Size() != 0 {
			t.Errorf("scaffolded plan.md size = %d, want 0 (empty)", info.Size())
		}
	}
	entries, err := os.ReadDir(change)
	if err != nil {
		t.Fatalf("reading change dir: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("change dir has %d entries, want exactly 2 (state + plan.md): %+v", len(entries), entries)
	}
}

// TestNew_StateIsPlanPhase verifies the scaffolded state is at phase=plan
// with no Finished and no Verified flags.
func TestNew_StateIsPlanPhase(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "fresh", "--dir", dir)
	st, err := tostate.Load(statePath(dir, "fresh"))
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if st.Phase != tostate.PhasePlan || st.Finished != "" || st.Verified {
		t.Errorf("scaffolded state = %+v, want phase=plan, no finished, not verified", st)
	}
	if st.Change != "fresh" {
		t.Errorf("scaffolded state change = %q, want 'fresh'", st.Change)
	}
}

// TestNew_RejectsExistingActiveChange verifies the "already exists" refusal
// and that the second invocation performs no destructive writes.
func TestNew_RejectsExistingActiveChange(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "dup", "--dir", dir)
	origState, err := os.ReadFile(statePath(dir, "dup"))
	if err != nil {
		t.Fatal(err)
	}
	out := runErr(t, "new", "dup", "--dir", dir)
	if !strings.Contains(out, "already exists") {
		t.Errorf("new(dup) error %q missing 'already exists'", out)
	}
	if now, _ := os.ReadFile(statePath(dir, "dup")); string(now) != string(origState) {
		t.Errorf("new(dup) mutated the existing state file")
	}
}

// TestNew_JSONShape verifies the new JSON carries change, phase, dir, and the
// list of created files.
func TestNew_JSONShape(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	out := run(t, false, "new", "j", "--json", "--dir", dir)
	for _, want := range []string{`"change": "j"`, `"phase": "plan"`, `"dir":`, `"files":`} {
		if !strings.Contains(out, want) {
			t.Errorf("new JSON %q missing %s", out, want)
		}
	}
}

// TestNew_NameValidationIsBeforeFileWrites verifies an invalid name fails
// before any scaffolding occurs (no half-created change directory).
func TestNew_NameValidationIsBeforeFileWrites(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	runErr(t, "new", "UPPER", "--dir", dir)
	if _, err := os.Stat(changeDir(dir, "UPPER")); !os.IsNotExist(err) {
		t.Errorf("new with an invalid name created the change dir, stat err = %v", err)
	}
}
