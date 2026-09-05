package convert

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/tostate"
)

func testOps() opid.Supplier {
	return opid.Fixed(time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC))
}

func seedToChange(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, "docs", "tasks", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "changes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := tostate.Save(filepath.Join(dir, "to-state.yaml"), tostate.State{
		Change: name, Phase: tostate.PhaseDo, Created: "2026-09-05",
	}); err != nil {
		t.Fatal(err)
	}
	plan := "# plan\n- [ ] #1 work\n  - Files: `x.go`\n  - Change: work\n  - Verify: `go test ./...`\nFinal Verify: `go test ./...`\n"
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte(plan), 0o644); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRunResumesAfterSourceMovedToConversionStaging(t *testing.T) {
	root := t.TempDir()
	seedToChange(t, root, "grower")
	spec := specs[Promote]
	wfRoot := filepath.Join(root, "docs")
	src := filepath.Join(wfRoot, "tasks", "grower")
	tgt := filepath.Join(wfRoot, "changes", "grower")
	if err := preconditions(spec, root, wfRoot, src, tgt, "grower", "grower"); err != nil {
		t.Fatal(err)
	}
	stg, m, resumed, err := findOrStage(spec, root, wfRoot, src, "grower", "grower", testOps())
	if err != nil || resumed {
		t.Fatalf("stage = %q, resumed=%v, err=%v", stg, resumed, err)
	}
	if err := buildWork(spec, filepath.Join(stg, "work"), src, m); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source must be in staging, stat err=%v", err)
	}
	if _, err := Run(Promote, root, "grower", "grower", testOps()); err != nil {
		t.Fatalf("resume after source move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tgt, "onto-state.yaml")); err != nil {
		t.Fatalf("resumed target missing: %v", err)
	}
}

func TestRunRestartsPreMoveConversionStaging(t *testing.T) {
	root := t.TempDir()
	seedToChange(t, root, "grower")
	stg := filepath.Join(root, "docs", ".to-promote", "op-000001")
	if err := os.MkdirAll(stg, 0o700); err != nil {
		t.Fatal(err)
	}
	data := `{"schemaVersion":1,"kind":"convert","direction":"promote","source":"grower","target":"grower","operationId":"op-000001"}`
	if err := os.WriteFile(filepath.Join(stg, manifestFile), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stg, "work", controlDir, snapshotsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Promote, root, "grower", "grower", testOps()); err != nil {
		t.Fatalf("restart pre-move conversion: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "changes", "grower")); err != nil {
		t.Fatalf("target missing after restart: %v", err)
	}
}

func TestRunRejectsTamperedReceiptSnapshot(t *testing.T) {
	root := t.TempDir()
	seedToChange(t, root, "grower")
	if _, err := Run(Promote, root, "grower", "grower", testOps()); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(root, "docs", "changes", "grower", controlDir, snapshotsDir, "op-000001", workflowTo, "plan.md")
	if err := os.WriteFile(snap, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Promote, root, "grower", "grower", testOps()); err == nil {
		t.Fatal("retry with tampered snapshot succeeded")
	}
}

func TestRunRestoresRenamedInverseAndItsRetry(t *testing.T) {
	root := t.TempDir()
	wantPlan := seedToChange(t, root, "old")
	ops := testOps()
	if _, err := Run(Promote, root, "old", "new", ops); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Demote, root, "new", "old", ops); err != nil {
		t.Fatalf("renamed inverse restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "old", "plan.md"))
	if err != nil || string(got) != wantPlan {
		t.Fatalf("restored plan = %q, err=%v", got, err)
	}
	if _, err := Run(Demote, root, "new", "old", ops); err != nil {
		t.Fatalf("restore retry: %v", err)
	}
}

func TestRunResumesInterruptedRestore(t *testing.T) {
	root := t.TempDir()
	seedToChange(t, root, "old")
	ops := testOps()
	if _, err := Run(Promote, root, "old", "new", ops); err != nil {
		t.Fatal(err)
	}
	occupied := filepath.Join(root, "docs", "tasks", "old")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Demote, root, "new", "old", ops); err == nil {
		t.Fatal("restore into occupied target succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "changes", "new")); err != nil {
		t.Fatalf("occupied restore target moved the active source: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(root, "docs", ".onto-demote")); err == nil && len(entries) != 0 {
		t.Fatalf("occupied restore target left staging: %v", entries)
	}
	if err := os.RemoveAll(occupied); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Demote, root, "new", "old", ops); err != nil {
		t.Fatalf("resume interrupted restore: %v", err)
	}
}

func stageRestore(t *testing.T, root string) (string, string) {
	t.Helper()
	change := filepath.Join(root, "docs", "changes", "new")
	lin, ok, err := loadLineage(change)
	if err != nil || !ok {
		t.Fatalf("load lineage: %v, %v", ok, err)
	}
	e, ok, err := latestConversionEvent(change)
	if err != nil || !ok {
		t.Fatalf("load conversion event: %v, %v", ok, err)
	}
	stg := filepath.Join(root, "docs", ".onto-demote", "restore-0001")
	if err := os.MkdirAll(stg, 0o700); err != nil {
		t.Fatal(err)
	}
	m := manifest{
		SchemaVersion:     schemaVersion,
		Kind:              "restore",
		Direction:         Demote,
		Source:            "new",
		Target:            "old",
		OperationID:       "restore-0001",
		SourceOperationID: e.OperationID,
		SourceDigest:      e.From.Digest,
		Lineage:           lin,
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stg, manifestFile), data, 0o644); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(stg, "work")
	if err := os.Rename(change, work); err != nil {
		t.Fatal(err)
	}
	return stg, filepath.Join(work, controlDir, snapshotsDir, e.OperationID, workflowTo)
}

func TestRunCompletesPartiallyMovedRestore(t *testing.T) {
	root := t.TempDir()
	want := seedToChange(t, root, "old")
	ops := testOps()
	if _, err := Run(Promote, root, "old", "new", ops); err != nil {
		t.Fatal(err)
	}
	stg, snap := stageRestore(t, root)
	restored := filepath.Join(stg, "restored")
	if err := os.MkdirAll(restored, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(snap, "plan.md"), filepath.Join(restored, "plan.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Demote, root, "new", "old", ops); err != nil {
		t.Fatalf("complete partial restore: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "docs", "tasks", "old", "plan.md"))
	if err != nil || string(got) != want {
		t.Fatalf("partial restore plan = %q, err=%v", got, err)
	}
}

func TestRunRejectsTamperedRestoreStaging(t *testing.T) {
	root := t.TempDir()
	seedToChange(t, root, "old")
	ops := testOps()
	if _, err := Run(Promote, root, "old", "new", ops); err != nil {
		t.Fatal(err)
	}
	_, snap := stageRestore(t, root)
	if err := os.WriteFile(filepath.Join(snap, "plan.md"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(Demote, root, "new", "old", ops); err == nil {
		t.Fatal("tampered restore staging succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "tasks", "old")); !os.IsNotExist(err) {
		t.Fatalf("tampered restore installed a target, stat err=%v", err)
	}
}

func TestRunRefusesSymlinkedControlPlane(t *testing.T) {
	root := t.TempDir()
	seedToChange(t, root, "grower")
	if _, err := Run(Promote, root, "grower", "grower", testOps()); err != nil {
		t.Fatal(err)
	}
	change := filepath.Join(root, "docs", "changes", "grower")
	if err := os.WriteFile(filepath.Join(change, "proposal.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.json")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lineage := filepath.Join(change, controlDir, lineageFile)
	if err := os.Remove(lineage); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lineage); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := Run(Demote, root, "grower", "grower", testOps())
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlinked control plane error = %v", err)
	}
	got, err := os.ReadFile(outside)
	if err != nil || string(got) != "outside\n" {
		t.Fatalf("outside target changed to %q, err=%v", got, err)
	}
}
