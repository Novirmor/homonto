package ontocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

func TestMergeDeltasCommand_MergesAndMarksMerged(t *testing.T) {
	root := prepWorkspace(t)
	// a change in the close phase with a delta spec
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{Change: "c", Workflow: "full", Phase: "close", CloseConfirmed: "2026-07-22 confirmed"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "docs", "specs", "cap.md"),
		"# Cap\n\n## Requirements\n\n### Requirement: A\n\nThe system SHALL a.\n\n#### Scenario: s\n\n- **WHEN** x\n- **THEN** y\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"),
		"## ADDED Requirements\n\n### Requirement: B\n\nThe system SHALL b.\n\n#### Scenario: s\n\n- **WHEN** x\n- **THEN** y\n")

	if out, err := runOnto(t, "merge-deltas", "c", "--dir", root); err != nil {
		t.Fatalf("merge-deltas: %v\n%s", err, out)
	}
	living, _ := os.ReadFile(filepath.Join(root, "docs", "specs", "cap.md"))
	if !strings.Contains(string(living), "### Requirement: A") || !strings.Contains(string(living), "### Requirement: B") {
		t.Errorf("merged spec missing a requirement:\n%s", living)
	}
	if strings.Contains(string(living), "## ADDED") {
		t.Errorf("delta heading leaked:\n%s", living)
	}
	st, _ := ontostate.LoadChange(changeDir)
	if !st.Close.Merged {
		t.Error("merge-deltas did not set close.merged")
	}
	// Idempotent: a second run is a no-op (would otherwise re-ADD B and error).
	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err != nil {
		t.Fatalf("second merge-deltas must be a no-op: %v", err)
	}
}

func TestMergeDeltasCommand_InvalidDeltaWritesNothing(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{Change: "c", Workflow: "full", Phase: "close", CloseConfirmed: "2026-07-22 confirmed"})
	writeFile(t, filepath.Join(root, "docs", "specs", "cap.md"), "# Cap\n\n## Requirements\n\n### Requirement: A\n\nSHALL a.\n")
	// MODIFIED targets a requirement that does not exist → error, nothing written.
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"),
		"## MODIFIED Requirements\n\n### Requirement: Ghost\n\nSHALL x.\n")

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil {
		t.Fatal("merge-deltas must error on a MODIFIED of an absent requirement")
	}
	living, _ := os.ReadFile(filepath.Join(root, "docs", "specs", "cap.md"))
	if strings.Contains(string(living), "Ghost") {
		t.Errorf("a failed merge must write nothing:\n%s", living)
	}
	st, _ := ontostate.LoadChange(changeDir)
	if st.Close.Merged {
		t.Error("a failed merge must not set close.merged")
	}
}

// merge-deltas must refuse to mutate living specs before the change reaches the
// close phase. An open/build/verify change whose deltas are not yet accepted
// must not touch the living specs. See F7.
func TestMergeDeltasCommand_RefusesOutsideClosePhase(t *testing.T) {
	for _, phase := range []string{"open", "design", "build", "verify"} {
		t.Run(phase, func(t *testing.T) {
			root := prepWorkspace(t)
			changeDir := filepath.Join(root, "docs", "changes", "c")
			ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{Change: "c", Workflow: "full", Phase: phase})
			writeFile(t, filepath.Join(changeDir, "specs", "cap.md"),
				"## ADDED Requirements\n\n### Requirement: B\n\nSHALL b.\n")

			if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil {
				t.Fatalf("merge-deltas must refuse at phase %q", phase)
			}
		})
	}
}

// TestMergeDeltasCommand_RefusesWithoutCloseConfirmed: the final-confirmation
// gate's token is required before any global mutation; the refusal names the
// setter and the living spec is untouched.
func TestMergeDeltasCommand_RefusesWithoutCloseConfirmed(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{Change: "c", Workflow: "full", Phase: "close"})
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"),
		"## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")

	_, err := runOnto(t, "merge-deltas", "c", "--dir", root)
	if err == nil {
		t.Fatal("merge-deltas must refuse without close_confirmed")
	}
	if !strings.Contains(err.Error(), "close-confirmed") {
		t.Errorf("error %q must name the close-confirmed setter", err.Error())
	}
	if _, statErr := os.Stat(filepath.Join(root, "docs", "specs", "cap.md")); statErr == nil {
		t.Error("refusal must happen before any living-spec write")
	}
}
