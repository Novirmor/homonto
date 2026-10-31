package tocli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/tostate"
)

// runErr drives the root command expecting failure and returns the error
// message (cobra is silenced, so the diagnostic lives in err, not stdout).
func runErr(t *testing.T, args ...string) string {
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

// TestPhase_FromDoRefuses verifies the only forward transition plan→do, and
// that `phase` from do refuses (done/abandon are the only exits).
func TestPhase_FromDoRefuses(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "p", "--dir", dir)
	run(t, false, "phase", "p", "--dir", dir)
	out := runErr(t, "phase", "p", "--dir", dir)
	for _, want := range []string{"already at do", "to done", "to abandon"} {
		if !strings.Contains(out, want) {
			t.Errorf("phase-from-do error %q missing %q", out, want)
		}
	}
}

// TestPhase_TerminalRefuses verifies a terminal change refuses phase. Uses
// the wedge pattern (terminal-but-active) so loadChange finds the state
// rather than reporting it archived.
func TestPhase_TerminalRefuses(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "gone", "--dir", dir)
	run(t, false, "abandon", "gone", "--dir", dir)
	// Re-create the active directory with a terminal state (wedge pattern).
	if err := tostate.Save(statePath(dir, "gone"), tostate.State{
		Change: "gone", Phase: tostate.PhaseAbandoned, Finished: "2030-01-01",
	}); err != nil {
		t.Fatal(err)
	}
	out := runErr(t, "phase", "gone", "--dir", dir)
	if !strings.Contains(out, "terminal") {
		t.Errorf("phase-terminal error %q missing 'terminal'", out)
	}
}

// TestDone_FromPlanRefuses verifies done rejects a change still at plan, with
// the actionable hint pointing at `to phase`.
func TestDone_FromPlanRefuses(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "early", "--dir", dir)
	out := runErr(t, "done", "early", "--verified", "--dir", dir)
	for _, want := range []string{"still at plan", "to phase"} {
		if !strings.Contains(out, want) {
			t.Errorf("done-from-plan error %q missing %q", out, want)
		}
	}
}

// TestDone_WithoutVerifiedRefuses verifies --verified is mandatory, even from
// a valid do-phase change.
func TestDone_WithoutVerifiedRefuses(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "v", "--dir", dir)
	run(t, false, "phase", "v", "--dir", dir)
	out := runErr(t, "done", "v", "--dir", dir)
	if !strings.Contains(out, "--verified is required") {
		t.Errorf("done-without-verified error %q missing --verified-required hint", out)
	}
}

// TestAbandon_FromDoneRefuses verifies abandon cannot unwind a finished
// change; the diagnostic must point at `to done --verified`.
func TestAbandon_FromDoneRefuses(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "finished", "--dir", dir)
	run(t, false, "phase", "finished", "--dir", dir)
	// Drop a done-but-active state (without going through the archive) so
	// abandon sees PhaseDone in the active tree.
	if err := tostate.Save(statePath(dir, "finished"), tostate.State{
		Change: "finished", Phase: tostate.PhaseDone, Verified: true, Finished: "2030-01-01",
	}); err != nil {
		t.Fatal(err)
	}
	out := runErr(t, "abandon", "finished", "--dir", dir)
	for _, want := range []string{"done", "to done", "--verified"} {
		if !strings.Contains(out, want) {
			t.Errorf("abandon-from-done error %q missing %q", out, want)
		}
	}
}

// TestAbandon_FromAnyNonTerminal is the table the audit asked for: abandon is
// the universal exit and works from every non-terminal phase.
func TestAbandon_FromAnyNonTerminal(t *testing.T) {
	for _, phase := range []string{tostate.PhasePlan, tostate.PhaseDo} {
		t.Run(phase, func(t *testing.T) {
			dir := setUpGatedWorkspace(t)
			run(t, false, "new", "x", "--dir", dir)
			if phase == tostate.PhaseDo {
				run(t, false, "phase", "x", "--dir", dir)
			}
			run(t, false, "abandon", "x", "--dir", dir)

			archived := findArchived(dir, "x")
			if archived == "" {
				t.Fatalf("no archive created from phase %s", phase)
			}
			st, err := tostate.Load(filepath.Join(archived, tostate.FileName))
			if err != nil {
				t.Fatalf("loading archived state: %v", err)
			}
			if st.Phase != tostate.PhaseAbandoned {
				t.Errorf("abandon-from-%s state = %s, want abandoned", phase, st.Phase)
			}
		})
	}
}

// TestLifecycle_JSONShapeForTerminalCommands verifies the JSON envelope of the
// terminal-move commands carries the expected fields (the consumers that
// parse this output rely on the exact shape).
func TestLifecycle_JSONShapeForTerminalCommands(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "j", "--dir", dir)
	run(t, false, "phase", "j", "--dir", dir)

	doneJSON := run(t, false, "done", "j", "--verified", "--json", "--dir", dir)
	for _, want := range []string{`"change":`, `"archived":`, `"phase": "done"`, `"verified": true`} {
		if !strings.Contains(doneJSON, want) {
			t.Errorf("done JSON %q missing %s", doneJSON, want)
		}
	}

	run(t, false, "new", "k", "--dir", dir)
	abandonJSON := run(t, false, "abandon", "k", "--json", "--dir", dir)
	for _, want := range []string{`"change":`, `"archived":`, `"phase": "abandoned"`} {
		if !strings.Contains(abandonJSON, want) {
			t.Errorf("abandon JSON %q missing %s", abandonJSON, want)
		}
	}
}

// TestPhase_JSONShapeVerifiesFromAndTo verifies the phase JSON carries both
// the from and to phase (the audit's "shape" ask).
func TestPhase_JSONShapeVerifiesFromAndTo(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "p", "--dir", dir)
	out := run(t, false, "phase", "p", "--json", "--dir", dir)
	for _, want := range []string{`"from": "plan"`, `"to": "do"`} {
		if !strings.Contains(out, want) {
			t.Errorf("phase JSON %q missing %s", out, want)
		}
	}
}

// TestDone_EvidenceRecordedVerbatimUnchangedBySpecialChars verifies the
// evidence string is stored verbatim, including characters a YAML emitter
// might otherwise reflow.
func TestDone_EvidenceRecordedVerbatimUnchangedBySpecialChars(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "e", "--dir", dir)
	run(t, false, "phase", "e", "--dir", dir)
	const evidence = "`go test ./...` — passed: 42/42 (no skips)"
	run(t, false, "done", "e", "--verified", "--evidence", evidence, "--dir", dir)

	st, err := tostate.Load(filepath.Join(findArchived(dir, "e"), tostate.FileName))
	if err != nil {
		t.Fatalf("loading archived state: %v", err)
	}
	if st.Evidence != evidence {
		t.Errorf("evidence = %q, want %q verbatim", st.Evidence, evidence)
	}
}
