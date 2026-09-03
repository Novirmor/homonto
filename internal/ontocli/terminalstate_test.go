package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

// Abandoned is the UNSUCCESSFUL terminal state. Only `advance` used to enforce
// it: `close` archived an abandoned change as a success (a contradictory
// archived+abandoned terminal that falsely resolved other changes'
// dependencies), `set` let its evidence tokens be forged after the fact, and
// `merge-deltas` merged its never-accepted deltas into the living specs.
func TestAbandonedIsTerminalEverywhere(t *testing.T) {
	t.Run("close refuses an abandoned change", func(t *testing.T) {
		dir := prepWorkspace(t)
		seedCloseState(t, dir, ontostate.State{
			Change: "demo", Workflow: "full", Phase: "close", Created: "2026-07-10", CloseConfirmed: "2026-07-22 confirmed",
			Verify: ontostate.Verify{Result: "pass"}, Close: ontostate.Close{Merged: true},
			Guides: "updated", Abandoned: true,
		})
		commitAll(t, dir, "seed abandoned change at close")

		if _, err := run(t, "close", "demo", "--dir", dir); err == nil || !strings.Contains(err.Error(), "abandoned") {
			t.Fatalf("close of an abandoned change must fail naming abandonment, got: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "demo")); err != nil {
			t.Errorf("abandoned change must remain in place, not archive: %v", err)
		}
	})

	t.Run("set refuses an abandoned change", func(t *testing.T) {
		dir := prepWorkspace(t)
		seedCloseState(t, dir, ontostate.State{
			Change: "demo", Workflow: "full", Phase: "verify", Created: "2026-07-10",
			Abandoned: true,
		})
		if _, err := run(t, "set", "verify-result", "demo", "pass", "--dir", dir); err == nil || !strings.Contains(err.Error(), "abandoned") {
			t.Fatalf("set on an abandoned change must fail naming abandonment, got: %v", err)
		}
	})

	t.Run("set refuses an archived change", func(t *testing.T) {
		dir := prepWorkspace(t)
		seedCloseState(t, dir, ontostate.State{
			Change: "demo", Workflow: "full", Phase: "close", Created: "2026-07-10", CloseConfirmed: "2026-07-22 confirmed",
			Archived: true,
		})
		if _, err := run(t, "set", "verify-result", "demo", "pass", "--dir", dir); err == nil || !strings.Contains(err.Error(), "archived") {
			t.Fatalf("set on an archived change must fail naming archival, got: %v", err)
		}
	})

	t.Run("merge-deltas refuses an abandoned change", func(t *testing.T) {
		dir := prepWorkspace(t)
		seedCloseState(t, dir, ontostate.State{
			Change: "demo", Workflow: "full", Phase: "close", Created: "2026-07-10", CloseConfirmed: "2026-07-22 confirmed",
			Abandoned: true,
		})
		delta := filepath.Join(dir, "docs", "changes", "demo", "specs", "ghostcap.md")
		writeFile(t, delta, "## ADDED Requirements\n\n### Requirement: Ghost\nSHALL not exist.\n")
		if _, err := run(t, "merge-deltas", "demo", "--dir", dir); err == nil || !strings.Contains(err.Error(), "abandoned") {
			t.Fatalf("merge-deltas on an abandoned change must fail naming abandonment, got: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "docs", "specs", "ghostcap.md")); !os.IsNotExist(err) {
			t.Errorf("abandoned change's delta must never reach the living specs (stat err = %v)", err)
		}
	})
}

func TestCommandsRejectStateDirectoryNameMismatch(t *testing.T) {
	commands := [][]string{
		{"advance", "demo"},
		{"gate", "demo"},
		{"merge-deltas", "demo"},
		{"close", "demo"},
		{"set", "directive", "demo", "text"},
	}
	for _, args := range commands {
		t.Run(strings.Join(args[:1], "-"), func(t *testing.T) {
			dir := prepWorkspace(t)
			seedCloseState(t, dir, ontostate.State{
				Change: "demo", Workflow: "full", Phase: "close", Created: "2026-07-10",
				Verify: ontostate.Verify{Result: "pass"}, Close: ontostate.Close{Merged: true},
				Guides: "updated", Integration: "merge", CloseConfirmed: "reviewed",
			})
			mutateState(t, dir, "demo", func(st *ontostate.State) { st.Change = "other" })
			fullArgs := append(append([]string(nil), args...), "--dir", dir)
			if _, err := run(t, fullArgs...); err == nil || !strings.Contains(err.Error(), "does not match directory") {
				t.Fatalf("%v must reject mismatched state identity, got %v", args, err)
			}
		})
	}
}

func TestAdvanceRejectsArchivedActiveState(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "build", Created: "2026-07-10", Archived: true,
	})
	if _, err := run(t, "advance", "demo", "--dir", dir); err == nil || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("advance must reject archived active state, got %v", err)
	}
}

// A crash between merge-deltas' per-file atomic writes leaves each living spec
// either untouched or fully merged with close.merged still false. Re-running
// used to fail `ADDED %q already exists` forever — the only escape was
// hand-asserting close.merged. The re-run must instead recognize the
// fully-applied spec (deltamerge.Applied), skip it, apply the rest, and set
// the flag — while a genuinely conflicting delta still fails.
func TestMergeDeltasConvergesAfterPartialCommit(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "close", Created: "2026-07-10", CloseConfirmed: "2026-07-22 confirmed",
		Verify: ontostate.Verify{Result: "pass"},
	})
	changeSpecs := filepath.Join(dir, "docs", "changes", "demo", "specs")
	writeFile(t, filepath.Join(changeSpecs, "alpha.md"), "## ADDED Requirements\n\n### Requirement: Alpha\nSHALL alpha.\n")
	writeFile(t, filepath.Join(changeSpecs, "beta.md"), "## ADDED Requirements\n\n### Requirement: Beta\nSHALL beta.\n")

	// Create the exact receipt, then simulate a crash before beta's post-image
	// landed and before the completion bit was saved.
	if _, err := run(t, "merge-deltas", "demo", "--dir", dir); err != nil {
		t.Fatalf("initial merge: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "docs", "specs", "beta.md")); err != nil {
		t.Fatal(err)
	}
	mutateState(t, dir, "demo", func(st *ontostate.State) { st.Close.Merged = false })

	out, err := run(t, "merge-deltas", "demo", "--dir", dir)
	if err != nil {
		t.Fatalf("re-run after partial commit must converge, got: %v\n%s", err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "docs", "specs", "beta.md")); !strings.Contains(string(b), "Requirement: Beta") {
		t.Errorf("the unapplied delta must be applied on the re-run:\n%s", b)
	}
	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "demo", "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Close.Merged {
		t.Error("close.merged must be set once the re-run converges")
	}

	// Contrast: a delta whose name exists with DIFFERENT content is a genuine
	// conflict, not an applied post-state — it must still fail loudly.
	dir2 := prepWorkspace(t)
	seedCloseState(t, dir2, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "close", Created: "2026-07-10", CloseConfirmed: "2026-07-22 confirmed",
		Verify: ontostate.Verify{Result: "pass"},
	})
	writeFile(t, filepath.Join(dir2, "docs", "changes", "demo", "specs", "alpha.md"),
		"## ADDED Requirements\n\n### Requirement: Alpha\nSHALL alpha.\n")
	writeFile(t, filepath.Join(dir2, "docs", "specs", "alpha.md"),
		"# alpha\n\n## Requirements\n\n### Requirement: Alpha\nSHALL something else entirely.\n")
	if _, err := run(t, "merge-deltas", "demo", "--dir", dir2); err == nil {
		t.Fatal("a conflicting ADDED (same name, different content) must still fail")
	}
}

// scale used to fall back to `git diff HEAD` (worktree vs HEAD) when no base
// ref was recorded — but the workflow commits per task, so at verify time the
// tree is clean and a large committed change measured {0,0} → "light",
// silently selecting the weakest verification gate on the DEFAULT path.
func TestScaleRequiresBaseRef(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "verify", Created: "2026-07-10",
	})
	_, err := run(t, "scale", "demo", "--dir", dir)
	if err == nil || !strings.Contains(err.Error(), "base ref") {
		t.Fatalf("scale without a recorded base ref must error naming base-ref, got: %v", err)
	}
}

// The dependency resolution glob `*-<dep>` matched any archive whose name
// merely ENDED with the dep ("auth" resolved by "2026-07-10-refactor-auth"),
// and metacharacters in a dep matched anything. Resolution must be an exact
// name match after the date prefix, and dep names must validate like change
// names when set.
func TestDepsResolvedExactMatch(t *testing.T) {
	root := t.TempDir()
	arch := filepath.Join(root, "docs", "changes", "archive")
	for _, d := range []string{"2026-07-10-refactor-auth", "2026-07-11-auth"} {
		if err := os.MkdirAll(filepath.Join(arch, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if unresolved := ontostate.DepsResolved(root, []string{"auth"}); len(unresolved) != 0 {
		t.Errorf("dep %q with an exact dated archive must resolve, got unresolved %v", "auth", unresolved)
	}
	if unresolved := ontostate.DepsResolved(root, []string{"refactor"}); len(unresolved) != 1 {
		t.Errorf("dep %q must NOT be resolved by suffix coincidence, got unresolved %v", "refactor", unresolved)
	}
	// Before the fix, "auth" was resolved by refactor-auth alone; prove the
	// suffix no longer suffices once the exact entry is gone.
	if err := os.RemoveAll(filepath.Join(arch, "2026-07-11-auth")); err != nil {
		t.Fatal(err)
	}
	if unresolved := ontostate.DepsResolved(root, []string{"auth"}); len(unresolved) != 1 {
		t.Errorf("dep %q must not be resolved by the -auth SUFFIX of another change, got unresolved %v", "auth", unresolved)
	}
	if unresolved := ontostate.DepsResolved(root, []string{"*"}); len(unresolved) != 1 {
		t.Errorf("a metacharacter dep must not self-resolve, got unresolved %v", unresolved)
	}
}

func TestSetDepsValidatesNames(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "open", Created: "2026-07-10",
	})
	if _, err := run(t, "set", "deps", "demo", "--dep", "ok-name", "--dep", "*", "--dir", dir); err == nil {
		t.Fatal("a glob-metacharacter dep name must be rejected")
	}
	if _, err := run(t, "set", "deps", "demo", "--dep", "ok-name", "--dir", dir); err != nil {
		t.Fatalf("a valid dep name must be accepted: %v", err)
	}
}

// The gate schema's guides option carries the template text "waived:<reason>";
// recording it verbatim used to count as a discharged guides obligation.
func TestGuidesRejectsPlaceholderWaiver(t *testing.T) {
	if ontostate.ValidGuides("waived:<reason>") {
		t.Error(`ValidGuides("waived:<reason>") = true; the schema placeholder must not count as a real waiver`)
	}
	if !ontostate.ValidGuides("waived: superseded by change X") {
		t.Error("a real waiver reason must stay valid")
	}
}

// The recovery setter historically accepted the gate schema's option value
// "yes". Preserve that shipped input even though ordinary close now runs
// merge-deltas directly.
func TestCloseMergedAcceptsSchemaValue(t *testing.T) {
	dir := prepWorkspace(t)
	seedCloseState(t, dir, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "close", Created: "2026-07-10", CloseConfirmed: "2026-07-22 confirmed",
		Verify: ontostate.Verify{Result: "pass"},
	})
	writeFile(t, filepath.Join(dir, "docs", "changes", "demo", "verification.md"), "Result: pass\n")
	if _, err := run(t, "set", "close-merged", "demo", "yes", "--dir", dir); err != nil {
		t.Fatalf(`set close-merged <name> yes must succeed (gate schema value), got: %v`, err)
	}
	if _, err := run(t, "set", "close-merged", "demo", "nope", "--dir", dir); err == nil {
		t.Fatal("an arbitrary value must still be rejected")
	}
}

// An abandoned change is a parked terminal state, not a health problem — its
// missing artifacts and verify rounds are why it was abandoned. Doctor counting
// them made a `doctor --quiet` Stop hook fail forever with no clearing action.
func TestDoctorSkipsAbandonedChanges(t *testing.T) {
	dir := prepWorkspace(t)
	// Doctor also checks the docs layout; complete it so the ONLY candidate
	// finding is the abandoned change itself.
	for _, d := range docsLayout {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seedCloseState(t, dir, ontostate.State{
		Change: "demo", Workflow: "full", Phase: "verify", Created: "2026-07-10",
		Abandoned: true, Observed: ontostate.Observed{VerifyRounds: 5},
	})
	// Remove an artifact so the change would fail the skeleton check if counted.
	os.Remove(filepath.Join(dir, "docs", "changes", "demo", "verification.md"))

	out, err := run(t, "doctor", "--dir", dir)
	if err != nil {
		t.Fatalf("doctor must be healthy with only an abandoned change present, got: %v\n%s", err, out)
	}
}
