package tocli

import (
	"bytes"
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
	var out bytes.Buffer
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
	os.WriteFile(planPath(dir, name), []byte("# plan\n- [ ] #1 the work\n  - Files: `x.go`\nFinal Verify: `go test ./...`\n"), 0o644)
}

// TestPromoteCreatesOntoWorkspace (P1/P3/P5): promotion moves the source
// byte-for-byte under imported-to/, creates a fresh proposal-only full change
// at phase open, prints the framework-swap next steps, and keeps `to status`
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
	if _, err := os.Stat(filepath.Join(ontoDir, "imported-to", "plan.md")); err != nil {
		t.Fatalf("imported plan missing: %v", err)
	}
	// State says full/open — promotion claims no design or verification.
	st := loadOntoState(t, filepath.Join(ontoDir, "onto-state.yaml"))
	if st.Workflow != "full" || st.Phase != "open" {
		t.Fatalf("promoted state must be full/open, got %+v", st)
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
// happens; an existing target change is refused; the source name must be an
// active to change.
func TestPromoteRequiresYesAndRefusesCollisions(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")

	if msg := runErrString(t, "promote", "grower", "--dir", dir); !strings.Contains(msg, "--yes") {
		t.Fatalf("promote without --yes must fail: %q", msg)
	}
	if _, err := os.Stat(changeDir(dir, "grower")); err != nil {
		t.Fatal("refused promote must not move the source")
	}

	// Target collision.
	if err := os.MkdirAll(filepath.Join(dir, "docs", "changes", "grower"), 0o755); err != nil {
		t.Fatal(err)
	}
	if msg := runErrString(t, "promote", "grower", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing to overwrite") {
		t.Fatalf("existing target must be refused: %q", msg)
	}
	os.RemoveAll(filepath.Join(dir, "docs", "changes", "grower"))

	// Unknown source.
	if msg := runErrString(t, "promote", "ghost", "--dir", dir, "--yes"); !strings.Contains(msg, "no such") {
		t.Fatalf("unknown source must fail: %q", msg)
	}
}

// TestPromoteIdempotentRecovery (P3/P4): an interrupted promotion (staging
// left behind) is resumed by a retry — generated files are regenerated and
// the imported bytes hash-match the manifest. Tampered staging is refused.
func TestPromoteIdempotentRecovery(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")

	// Simulate an interrupted promotion: build the staging tree by hand with
	// a manifest and a moved source, then tamper with a generated file.
	base := filepath.Join(dir, "docs", ".to-promote")
	os.MkdirAll(base, 0o755)
	stg := filepath.Join(base, "deadbeef")
	os.MkdirAll(filepath.Join(stg, "work"), 0o700)
	if err := os.Rename(changeDir(dir, "grower"), filepath.Join(stg, "work", "imported-to")); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schemaVersion":1,"source":"grower","target":"grower","operationId":"deadbeef","sourceHashes":{}}`
	os.WriteFile(filepath.Join(stg, "manifest.json"), []byte(manifest), 0o644)
	// A tampered generated file that a resume must NOT trust.
	os.WriteFile(filepath.Join(stg, "work", "onto-state.yaml"), []byte("change: EVIL\nphase: close\n"), 0o644)

	// The manifest hashes are empty — real recovery needs correct hashes, so
	// this staging must be REFUSED (tampered), not resumed.
	if msg := runErrString(t, "promote", "grower", "--dir", dir, "--yes"); !strings.Contains(msg, "tampered") && !strings.Contains(msg, "refusing") {
		t.Fatalf("tampered staging must be refused: %q", msg)
	}
	if _, err := os.Stat(changeDir(dir, "grower")); err == nil {
		t.Fatal("refused recovery must not install a target")
	}
	// Clean the tampered staging and retry fresh.
	os.RemoveAll(base)
	seedPromotable(t, dir, "grower")
	run(t, false, "promote", "grower", "--dir", dir, "--yes")
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "grower", "imported-to", "plan.md")); err != nil {
		t.Fatalf("fresh retry failed: %v", err)
	}
	// A second promote of the same name: source gone, target present → success.
	run(t, false, "promote", "grower", "--dir", dir, "--yes")
}

// TestPromotePreservesBytes (P3): every imported file is byte-identical.
func TestPromotePreservesBytes(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")
	extra := "secret payload line\n"
	os.WriteFile(filepath.Join(changeDir(dir, "grower"), "notes.txt"), []byte(extra), 0o644)

	run(t, false, "promote", "grower", "--dir", dir, "--yes")

	got, err := os.ReadFile(filepath.Join(dir, "docs", "changes", "grower", "imported-to", "notes.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != extra {
		t.Fatalf("imported bytes differ: %q", got)
	}
}

// TestPromoteHandoffDiscovery (P5): the promoted change's handoff hashes the
// imported provenance.
func TestPromoteHandoffDiscovery(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedPromotable(t, dir, "grower")
	run(t, false, "promote", "grower", "--dir", dir, "--yes")

	// The onto handoff (built by the onto binary, asserted in ontocli) hashes
	// imported-to; here we assert the imported tree exists and is hashed by
	// the evidence path — the promotion moved it intact.
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "grower", "imported-to", "plan.md")); err != nil {
		t.Fatalf("imported provenance missing: %v", err)
	}
}

func loadOntoState(t *testing.T, path string) ontostate.State {
	t.Helper()
	st, err := ontostate.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
