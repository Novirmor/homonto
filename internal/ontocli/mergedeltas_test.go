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
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "2026-07-22 confirmed"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
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
	ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "2026-07-22 confirmed"})
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
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

func TestMergeDeltasCommand_RejectsUnboundMergedMarker(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"},
		Close: ontostate.Close{Merged: true}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"),
		"## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil || !strings.Contains(err.Error(), "unbound") {
		t.Fatalf("merge-deltas must reject an unbound marker, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "specs", "cap.md")); !os.IsNotExist(err) {
		t.Fatal("unbound marker recovery must not write a living spec")
	}
}

func TestMergeDeltasCommand_AbsentRemovedTargetFailsWithoutReceipt(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(root, "docs", "specs", "cap.md"), "# Cap\n\n## Requirements\n\n### Requirement: A\n\nSHALL a.\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"), "## REMOVED Requirements\n\n### Requirement: Ghost\n\nobsolete\n")

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil || !strings.Contains(err.Error(), "REMOVED") {
		t.Fatalf("absent REMOVED target must fail, got %v", err)
	}
	if _, err := os.Stat(mergeReceiptPath(changeDir)); !os.IsNotExist(err) {
		t.Fatal("invalid delta must not write a receipt")
	}
}

func TestMergeDeltasCommand_UppercaseExtensionUsesCanonicalTarget(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.MD"), "## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err != nil {
		t.Fatalf("merge uppercase delta: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "specs", "cap.md")); err != nil {
		t.Fatalf("canonical target missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "specs", "cap.MD.md")); !os.IsNotExist(err) {
		t.Fatal("uppercase extension produced a doubled target")
	}
}

func TestMergeDeltasCommand_ReceiptRejectsChangedInputsAndOutputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{name: "deleted delta", mutate: func(t *testing.T, root, changeDir string) {
			if err := os.Remove(filepath.Join(changeDir, "specs", "cap.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "changed delta", mutate: func(t *testing.T, _, changeDir string) {
			writeFile(t, filepath.Join(changeDir, "specs", "cap.md"), "## ADDED Requirements\n\n### Requirement: B\n\nSHALL changed.\n")
		}},
		{name: "newer living spec", mutate: func(t *testing.T, root, _ string) {
			writeFile(t, filepath.Join(root, "docs", "specs", "cap.md"), "# cap\n\n## Requirements\n\n### Requirement: Newer\n\nSHALL survive.\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := prepWorkspace(t)
			changeDir := filepath.Join(root, "docs", "changes", "c")
			if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
				Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
			}); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
			writeFile(t, filepath.Join(changeDir, "specs", "cap.md"), "## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")
			if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err != nil {
				t.Fatalf("initial merge: %v", err)
			}
			tc.mutate(t, root, changeDir)
			before, _ := os.ReadFile(filepath.Join(root, "docs", "specs", "cap.md"))
			if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil {
				t.Fatal("stale receipt was accepted")
			}
			after, _ := os.ReadFile(filepath.Join(root, "docs", "specs", "cap.md"))
			if string(after) != string(before) {
				t.Fatal("stale receipt recovery overwrote the living spec")
			}
		})
	}
}

func TestMergeDeltasCommand_RejectsDuplicateCanonicalTarget(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"), "## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.MD"), "## ADDED Requirements\n\n### Requirement: B\n\nSHALL b.\n")

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil || !strings.Contains(err.Error(), "same living spec") {
		t.Fatalf("duplicate target must fail before writing, got %v", err)
	}
	if _, err := os.Stat(mergeReceiptPath(changeDir)); !os.IsNotExist(err) {
		t.Fatal("duplicate target must not write a receipt")
	}
}

func TestMergeDeltasCommand_UnreadableDeltaLocationFailsClosed(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(changeDir, "specs"), "not a directory")

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil || !strings.Contains(err.Error(), "listing delta specs") {
		t.Fatalf("unreadable delta location must fail closed, got %v", err)
	}
	st, _ := ontostate.LoadChange(changeDir)
	if st.Close.Merged {
		t.Fatal("failed delta inspection must not mark close.merged")
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
	ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}})
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
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

func TestMergeDeltasCommand_RefusesWithoutPassingVerification(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"),
		"## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil || !strings.Contains(err.Error(), "passing verification") {
		t.Fatalf("merge-deltas must refuse unverified global mutation, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "specs", "cap.md")); !os.IsNotExist(err) {
		t.Fatal("unverified merge must not create a living spec")
	}
}

// TestMergeDeltasCommand_InvalidatedRoundDiscardsStaleReceipt: verification
// invalidated, deltas changed, pass re-recorded — the old receipt's manifest
// no longer matches and must be discarded (recomputed from current images)
// rather than dead-ending the change forever.
func TestMergeDeltasCommand_InvalidatedRoundDiscardsStaleReceipt(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"), "## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")
	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err != nil {
		t.Fatalf("initial merge: %v", err)
	}

	// Invalidate the round, change the delta set, re-verify.
	if _, err := runOnto(t, "set", "verify-result", "c", "fail", "--dir", root); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "specs", "other.md"), "## ADDED Requirements\n\n### Requirement: B\n\nSHALL b.\n")
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	if _, err := runOnto(t, "set", "verify-result", "c", "pass", "--dir", root); err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "set", "close-confirmed", "c", "re-confirmed", "--dir", root); err != nil {
		t.Fatal(err)
	}

	out, err := runOnto(t, "merge-deltas", "c", "--dir", root)
	if err != nil {
		t.Fatalf("re-merge after invalidated round must discard the stale receipt: %v\n%s", err, out)
	}
	living, _ := os.ReadFile(filepath.Join(root, "docs", "specs", "other.md"))
	if !strings.Contains(string(living), "### Requirement: B") {
		t.Fatalf("second capability not merged:\n%s", living)
	}
	st, _ := ontostate.LoadChange(changeDir)
	if !st.Close.Merged {
		t.Fatal("re-merge did not set close.merged")
	}
}

func TestMergeDeltasCommand_RefusesSymlinkedSidecarParent(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"), "## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(changeDir, ".onto")); err != nil {
		t.Fatal(err)
	}

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked sidecar parent was not refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "merge-receipt.json")); !os.IsNotExist(err) {
		t.Fatalf("receipt escaped through symlinked parent: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "specs", "cap.md")); !os.IsNotExist(err) {
		t.Fatalf("living spec written after sidecar refusal: %v", err)
	}
}

func TestMergeDeltasCommand_RefusesSymlinkedLivingSpec(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"), "## ADDED Requirements\n\n### Requirement: B\n\nSHALL b.\n")
	victim := filepath.Join(t.TempDir(), "victim.md")
	original := "# Cap\n\n## Requirements\n\n### Requirement: A\n\nSHALL a.\n"
	writeFile(t, victim, original)
	if err := os.MkdirAll(filepath.Join(root, "docs", "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(root, "docs", "specs", "cap.md")); err != nil {
		t.Fatal(err)
	}

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked living spec was not refused: %v", err)
	}
	if got, _ := os.ReadFile(victim); string(got) != original {
		t.Fatalf("outside target was modified:\n%s", got)
	}
	if _, err := os.Stat(mergeReceiptPath(changeDir)); !os.IsNotExist(err) {
		t.Fatalf("receipt written after living-spec refusal: %v", err)
	}
}

func TestMergeDeltasCommand_RefusesSymlinkedSpecsDirectory(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), ontostate.State{
		Change: "c", Workflow: "full", Phase: "close", Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(changeDir, "specs", "cap.md"), "## ADDED Requirements\n\n### Requirement: A\n\nSHALL a.\n")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "docs", "specs")); err != nil {
		t.Fatal(err)
	}

	if _, err := runOnto(t, "merge-deltas", "c", "--dir", root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlinked specs directory was not refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "cap.md")); !os.IsNotExist(err) {
		t.Fatalf("living spec escaped through symlinked directory: %v", err)
	}
}
