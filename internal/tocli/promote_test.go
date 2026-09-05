package tocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// seedPromotable creates a `to` change in the do phase with plan/evidence.

// runErrString executes the root command and returns the error text; the
// command must fail (SilenceErrors suppresses the text, so it is recovered
// from the returned error directly).
func runErrString(t *testing.T, args ...string) string {
	t.Helper()
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("execute %v = nil, want error; output: %s", args, out.String())
	}
	return err.Error()
}

func seedPromotable(t *testing.T, dir, name string) {
	t.Helper()
	run(t, false, "new", name, "--dir", dir)
	run(t, false, "phase", name, "--dir", dir)
	os.WriteFile(planPath(dir, name), []byte("# plan\n- [ ] #1 the work\n  - Files: `x.go`\n  - Change: the work\n  - Verify: `go test ./...`\nFinal Verify: `go test ./...`\n"), 0o644)
}

// TestPromoteCreatesOntoWorkspace (P1/P3/P5): promotion moves the source
// into the neutral control plane's snapshots, creates a fresh proposal-only
// full change at phase open with a lineage receipt, and keeps `to status`
// clean.
func TestPromoteCreatesOntoWorkspace(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")

	run(t, false, "promote", "grower", "--dir", dir, "--yes")

	// Source moved; nothing active remains under docs/tasks.
	if _, err := os.Stat(changeDir(dir, "grower")); err == nil {
		t.Fatal("source workspace still under docs/tasks")
	}

	ontoDir := filepath.Join(dir, "docs", "changes", "grower")
	for _, f := range []string{"onto-state.yaml", "proposal.md"} {
		if _, err := os.Stat(filepath.Join(ontoDir, f)); err != nil {
			t.Fatalf("promoted change missing %s: %v", f, err)
		}
	}
	snapPlan := filepath.Join(ontoDir, ".workflow", "snapshots")
	found := findSnapshotFile(t, snapPlan, "plan.md")
	if !strings.Contains(readTestFile(t, found), "Final Verify:") {
		t.Fatalf("snapshotted plan must keep the original bytes")
	}
	// State says full/open — promotion claims no design or verification.
	st := loadOntoState(t, filepath.Join(ontoDir, "onto-state.yaml"))
	if st.Workflow != "full" || st.Phase != "open" {
		t.Fatalf("promoted state must be full/open, got %+v", st)
	}
	// The lineage receipt names the conversion.
	lin := readTestFile(t, filepath.Join(ontoDir, ".workflow", "lineage.json"))
	if !strings.Contains(lin, "\"currentWorkflow\": \"onto\"") {
		t.Fatalf("lineage must name onto as the current workflow: %s", lin)
	}
	events := listDir(t, filepath.Join(ontoDir, ".workflow", "events"))
	if len(events) != 1 {
		t.Fatalf("exactly one conversion event expected, got %v", events)
	}
	// No tasks.md/design.md/verification.md at the root: proposal-only.
	for _, f := range []string{"tasks.md", "design.md", "verification.md"} {
		if _, err := os.Stat(filepath.Join(ontoDir, f)); err == nil {
			t.Fatalf("promoted change must not claim %s", f)
		}
	}
	// `to status` is empty.
	if out := run(t, false, "status", "--dir", dir); !strings.Contains(out, "no active changes") {
		t.Fatalf("to status after promote: %q", out)
	}
}

// TestPromoteRequiresYesAndRefusesCollisions (P1/P2): without --yes nothing
// happens and nothing is staged; an existing target change is refused; the
// source name must be an active to change. Failed preconditions leave no
// staging behind — a later good run must not wedge.
func TestPromoteRequiresYesAndRefusesCollisions(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")

	if msg := runErrString(t, "promote", "grower", "--dir", dir); !strings.Contains(msg, "--yes") {
		t.Fatalf("promote without --yes must fail: %q", msg)
	}
	if _, err := os.Stat(changeDir(dir, "grower")); err != nil {
		t.Fatal("refused promote must not move the source")
	}
	assertNoStaging(t, dir, ".to-promote")

	// Target collision.
	if err := os.MkdirAll(filepath.Join(dir, "docs", "changes", "grower"), 0o755); err != nil {
		t.Fatal(err)
	}
	if msg := runErrString(t, "promote", "grower", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing to overwrite") {
		t.Fatalf("existing target must be refused: %q", msg)
	}
	os.RemoveAll(filepath.Join(dir, "docs", "changes", "grower"))
	assertNoStaging(t, dir, ".to-promote")

	// Unknown source.
	if msg := runErrString(t, "promote", "ghost", "--dir", dir, "--yes"); !strings.Contains(msg, "no such") {
		t.Fatalf("unknown source must fail: %q", msg)
	}
	assertNoStaging(t, dir, ".to-promote")

	// After every refusal a clean run still succeeds.
	run(t, false, "promote", "grower", "--dir", dir, "--yes")
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "grower", "proposal.md")); err != nil {
		t.Fatalf("clean run after refusals failed: %v", err)
	}
}

// TestPromoteIdempotentRetryIsReceiptVerified: a retry after a completed
// promotion succeeds via the lineage receipt; a target that merely exists
// (no receipt) is a collision, never a fake success.
func TestPromoteIdempotentRetryIsReceiptVerified(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")
	run(t, false, "promote", "grower", "--dir", dir, "--yes")

	// Receipt-verified retry: same result, no error.
	run(t, false, "promote", "grower", "--dir", dir, "--yes")

	// An unrelated existing target with no receipt must not count as
	// completed for a different source — the unknown source is refused (the
	// source precondition runs first), and a REAL source aimed at an
	// occupied target is a collision refusal.
	os.MkdirAll(filepath.Join(dir, "docs", "changes", "other"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "changes", "other", "onto-state.yaml"), []byte("change: other\nphase: open\n"), 0o644)
	if msg := runErrString(t, "promote", "ghost", "--as", "other", "--dir", dir, "--yes"); !strings.Contains(msg, "no such") {
		t.Fatalf("unknown source must be refused, got: %q", msg)
	}
	seedPromotable(t, dir, "second")
	if msg := runErrString(t, "promote", "second", "--as", "other", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing to overwrite") {
		t.Fatalf("occupied target must be refused, got: %q", msg)
	}
}

// TestPromoteTamperedStagingRefused: staging whose bytes do not hash to the
// manifest is refused, never silently resumed.
func TestPromoteTamperedStagingRefused(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")

	// Run promote with staging simulated: build it by hand.
	base := filepath.Join(dir, "docs", ".to-promote")
	os.MkdirAll(base, 0o755)
	stg := filepath.Join(base, "deadbeef")
	os.MkdirAll(stg, 0o700)
	os.WriteFile(filepath.Join(stg, "manifest.json"), []byte(`{"schemaVersion":1,"kind":"convert","direction":"promote","source":"grower","target":"grower","operationId":"deadbeef","lineage":{"schemaVersion":1,"lineageId":"lin","created":"2026-09-04","currentWorkflow":"onto"},"sourcePhase":"do","targetIdentity":{"phase":"open","created":"2026-09-04"},"sourceDigest":"sha256:x","sourceHashes":{"to-state.yaml":"00"}}`), 0o644)
	os.MkdirAll(filepath.Join(stg, "work", ".workflow", "snapshots", "deadbeef", "to"), 0o755)

	if msg := runErrString(t, "promote", "grower", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing") && !strings.Contains(msg, "tampered") {
		t.Fatalf("tampered staging must be refused: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "grower")); err == nil {
		t.Fatal("refused recovery must not install a target")
	}
}

// TestPromotePreservesBytes: every snapshotted file is byte-identical.
func TestPromotePreservesBytes(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")
	extra := "secret payload line\n"
	os.WriteFile(filepath.Join(changeDir(dir, "grower"), "notes.txt"), []byte(extra), 0o644)

	run(t, false, "promote", "grower", "--dir", dir, "--yes")

	got := readTestFile(t, findSnapshotFile(t, filepath.Join(dir, "docs", "changes", "grower", ".workflow", "snapshots"), "notes.txt"))
	if got != extra {
		t.Fatalf("snapshotted bytes differ: %q", got)
	}
}

// TestPromoteStateIdentityMustMatch: a source whose state names another
// change is refused (defensive against copied workspaces).
func TestPromoteStateIdentityMustMatch(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")
	os.WriteFile(statePath(dir, "grower"), []byte("change: impostor\nphase: do\n"), 0o644)

	if msg := runErrString(t, "promote", "grower", "--dir", dir, "--yes"); !strings.Contains(msg, "impostor") {
		t.Fatalf("state/directory mismatch must be refused naming the state: %q", msg)
	}
	assertNoStaging(t, dir, ".to-promote")
}

func loadOntoState(t *testing.T, path string) ontostate.State {
	t.Helper()
	st, err := ontostate.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func assertNoStaging(t *testing.T, dir, name string) {
	t.Helper()
	if entries := listDir(t, filepath.Join(dir, "docs", name)); len(entries) != 0 {
		t.Fatalf("failed precondition must leave no %s staging, got %v", name, entries)
	}
}

// findSnapshotFile locates <name> anywhere under root (the snapshot lives at
// .workflow/snapshots/<op>/<workflow>/<name>).
func findSnapshotFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no %s under %s", name, root)
	}
	return found
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
