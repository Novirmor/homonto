package tocli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/tostate"
)

// run executes the root command with args and returns combined output; when
// wantErr is true the execution must fail, otherwise it must succeed.
func run(t *testing.T, wantErr bool, args ...string) string {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if wantErr && err == nil {
		t.Fatalf("execute %v = nil, want error", args)
	}
	if !wantErr && err != nil {
		t.Fatalf("execute %v: %v\noutput: %s", args, err, out.String())
	}
	return out.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// setUpGatedWorkspace prepares a temp workspace that passes gate(): a
// homonto.toml declaring [frameworks.to] plus an applied to catalog
// directory.
func setUpGatedWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.to]\nsource=\"builtin:to\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "to"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGate_OrderedFailures(t *testing.T) {
	// Step 1: no homonto.toml at all.
	dir := t.TempDir()
	if err := toFramework.Gate(dir); err == nil || !strings.Contains(err.Error(), "homonto init") {
		t.Errorf("gate(no toml) = %v, want mention of homonto init", err)
	}

	// Step 2: homonto.toml without [frameworks.to].
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.onto]\nsource=\"builtin:onto\"\n")
	if err := toFramework.Gate(dir); err == nil || !strings.Contains(err.Error(), "[frameworks.to]") {
		t.Errorf("gate(no frameworks.to) = %v, want mention of [frameworks.to]", err)
	}

	// Step 3: declared but never applied.
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.to]\nsource=\"builtin:to\"\n")
	if err := toFramework.Gate(dir); err == nil || !strings.Contains(err.Error(), "homonto apply") {
		t.Errorf("gate(unapplied) = %v, want mention of homonto apply", err)
	}

	// All present: gate passes.
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "to"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := toFramework.Gate(dir); err != nil {
		t.Errorf("gate(all present) = %v, want nil", err)
	}
}

func TestMutatingCommandsRefuseWithoutGate(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--dir", dir},
		{"new", "x", "--dir", dir},
		{"phase", "x", "--dir", dir},
		{"bypass", "x", "--to", "do", "--reason", "test", "--dir", dir},
		{"done", "x", "--verified", "--dir", dir},
		{"abandon", "x", "--dir", dir},
	} {
		run(t, true, args...)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs")); !os.IsNotExist(err) {
		t.Errorf("gated commands created docs/, stat err = %v", err)
	}
}

// TestPromoteAcceptsEitherFramework pins the promote bridge gate: promotion
// must work both while [frameworks.to] is declared (the documented order:
// promote, then swap the declaration) and after the declaration already moved
// to [frameworks.onto] (promoting a leftover `to` change). With neither
// framework applied it is refused like every other mutating command.
func TestPromoteAcceptsEitherFramework(t *testing.T) {
	// to declared and applied: canonical order.
	dir := setUpGatedWorkspace(t)
	run(t, false, "init", "--dir", dir)
	run(t, false, "new", "grower", "--dir", dir)
	out := run(t, false, "promote", "grower", "--yes", "--dir", dir)
	if !strings.Contains(out, "promoted") {
		t.Errorf("promote output = %q, want mention of promoted", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "grower", "onto-state.yaml")); err != nil {
		t.Errorf("onto state after to-applied promote: %v", err)
	}

	// onto declared and applied: leftover-change order (the declaration moved
	// before the promotion).
	dir2 := setUpGatedWorkspace(t)
	run(t, false, "init", "--dir", dir2)
	run(t, false, "new", "grower", "--dir", dir2)
	writeFile(t, filepath.Join(dir2, "homonto.toml"), "[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir2, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatal(err)
	}
	out = run(t, false, "promote", "grower", "--yes", "--dir", dir2)
	if !strings.Contains(out, "promoted") {
		t.Errorf("promote (onto-applied) output = %q, want mention of promoted", out)
	}
	if _, err := os.Stat(filepath.Join(dir2, "docs", "changes", "grower", "onto-state.yaml")); err != nil {
		t.Errorf("onto state after onto-applied promote: %v", err)
	}

	// Neither framework applied: refused, nothing written.
	dir3 := t.TempDir()
	run(t, true, "promote", "grower", "--yes", "--dir", dir3)
	if _, err := os.Stat(filepath.Join(dir3, "docs")); !os.IsNotExist(err) {
		t.Errorf("ungated promote created docs/, stat err = %v", err)
	}
}

func TestLifecycle_PlanDoDoneArchives(t *testing.T) {
	dir := setUpGatedWorkspace(t)

	run(t, false, "init", "--dir", dir)
	run(t, false, "new", "my-change", "--dir", dir)

	// Scaffolded: state at plan + empty plan.md.
	st, err := tostate.Load(statePath(dir, "my-change"))
	if err != nil || st.Phase != tostate.PhasePlan || st.Change != "my-change" {
		t.Fatalf("state after new = %+v, err %v", st, err)
	}
	if _, err := os.Stat(planPath(dir, "my-change")); err != nil {
		t.Fatalf("plan.md missing: %v", err)
	}

	// done from plan refuses; phase advances; done without --verified refuses.
	run(t, true, "done", "my-change", "--verified", "--dir", dir)
	run(t, false, "phase", "my-change", "--dir", dir)
	out := run(t, true, "done", "my-change", "--dir", dir)
	_ = out

	// phase from do refuses (done is the only exit).
	run(t, true, "phase", "my-change", "--dir", dir)

	run(t, false, "done", "my-change", "--verified", "--evidence", "go test ./... passed", "--dir", dir)

	// Archived under a date prefix, terminal, out of the active listing, with
	// the asserted evidence recorded verbatim.
	archived := filepath.Join(findArchived(dir, "my-change"), tostate.FileName)
	st, err = tostate.Load(archived)
	if err != nil || st.Phase != tostate.PhaseDone || !st.Verified || st.Finished == "" {
		t.Fatalf("archived state = %+v, err %v", st, err)
	}
	if st.Evidence != "go test ./... passed" {
		t.Errorf("evidence = %q, want the asserted text verbatim", st.Evidence)
	}
	if _, err := os.Stat(changeDir(dir, "my-change")); !os.IsNotExist(err) {
		t.Errorf("active dir still exists after archive, stat err = %v", err)
	}
	if out := run(t, false, "status", "--dir", dir); !strings.Contains(out, "no active changes") {
		t.Errorf("status after archive = %q, want no active changes", out)
	}

	// Terminal is terminal for the archived change, but its NAME is free
	// again: date-prefixed archives let a recurring chore name be reused.
	run(t, true, "phase", "my-change", "--dir", dir)
	run(t, false, "new", "my-change", "--dir", dir)
	if st, err := tostate.Load(statePath(dir, "my-change")); err != nil || st.Phase != tostate.PhasePlan {
		t.Fatalf("reused name state = %+v, err %v", st, err)
	}
}

func TestCustomWorkflowRootLifecycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[workflow]\nroot=\"workflow/records\"\n[frameworks.to]\nsource=\"builtin:to\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "to"), 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, false, "init", "--dir", dir)
	run(t, false, "new", "custom", "--dir", dir)
	run(t, false, "phase", "custom", "--dir", dir)
	run(t, false, "done", "custom", "--verified", "--dir", dir)
	if got := findArchived(dir, "custom"); !strings.HasPrefix(got, filepath.Join(dir, "workflow", "records", "tasks", "archive")) {
		t.Fatalf("archive = %q, want custom workflow root", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "tasks")); !os.IsNotExist(err) {
		t.Fatalf("default tasks root was created, stat err = %v", err)
	}
}

func TestAbandonIsTerminalAndArchives(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "dead-end", "--dir", dir)
	run(t, false, "abandon", "dead-end", "--dir", dir)

	st, err := tostate.Load(filepath.Join(findArchived(dir, "dead-end"), tostate.FileName))
	if err != nil || st.Phase != tostate.PhaseAbandoned || st.Verified {
		t.Fatalf("abandoned state = %+v, err %v", st, err)
	}
	run(t, true, "abandon", "dead-end", "--dir", dir)
}

// TestCrashConvergence_DoneCompletesInterruptedArchive simulates a crash
// between the terminal state write and the archive rename: the change sits
// done-but-active. Re-running `to done --verified` must complete the move
// instead of refusing (the wedge that made a change permanently stuck).
func TestCrashConvergence_DoneCompletesInterruptedArchive(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "crashed", "--dir", dir)
	// Simulate the post-crash file state through the state package (the same
	// bytes runDone writes before the rename that never happened).
	if err := tostate.Save(statePath(dir, "crashed"), tostate.State{
		Change: "crashed", Phase: tostate.PhaseDone, Verified: true, Finished: "2026-07-01",
	}); err != nil {
		t.Fatal(err)
	}

	out := run(t, false, "done", "crashed", "--verified", "--dir", dir)
	if !strings.Contains(out, "completed the archive") {
		t.Errorf("output = %q, want the completed-archive message", out)
	}
	// Archived under its recorded finish date, not today's.
	if _, err := os.Stat(filepath.Join(archiveDir(dir), "2026-07-01-crashed", tostate.FileName)); err != nil {
		t.Errorf("archive not completed at the recorded finish date: %v", err)
	}
	if _, err := os.Stat(changeDir(dir, "crashed")); !os.IsNotExist(err) {
		t.Errorf("active dir still present after convergence, stat err = %v", err)
	}

	// Same for abandon.
	run(t, false, "new", "crashed-b", "--dir", dir)
	if err := tostate.Save(statePath(dir, "crashed-b"), tostate.State{
		Change: "crashed-b", Phase: tostate.PhaseAbandoned, Finished: "2026-07-02",
	}); err != nil {
		t.Fatal(err)
	}
	run(t, false, "abandon", "crashed-b", "--dir", dir)
	if _, err := os.Stat(filepath.Join(archiveDir(dir), "2026-07-02-crashed-b", tostate.FileName)); err != nil {
		t.Errorf("abandon convergence failed: %v", err)
	}
}

func TestLockBlocksConcurrentMutation(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	unlock, err := lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	run(t, true, "new", "blocked", "--dir", dir)
	unlock()
	run(t, false, "new", "blocked", "--dir", dir)
}

func TestDoctor(t *testing.T) {
	dir := setUpGatedWorkspace(t)

	// Healthy: empty workspace (docs/tasks may not even exist).
	if out := run(t, false, "doctor", "--dir", dir); !strings.Contains(out, "healthy") {
		t.Errorf("doctor = %q, want healthy", out)
	}

	// A wedged terminal-but-active change is a finding naming the fix.
	run(t, false, "new", "wedged", "--dir", dir)
	if err := tostate.Save(statePath(dir, "wedged"), tostate.State{
		Change: "wedged", Phase: tostate.PhaseDone, Verified: true, Finished: "2026-07-01",
	}); err != nil {
		t.Fatal(err)
	}
	out := run(t, true, "doctor", "--dir", dir)
	if !strings.Contains(out, "interrupted archive") || !strings.Contains(out, "to done wedged --verified") {
		t.Errorf("doctor = %q, want the wedge finding with its fix command", out)
	}

	// --quiet: exit-code only, no output at all.
	quiet := run(t, true, "doctor", "--quiet", "--dir", dir)
	if strings.TrimSpace(quiet) != "" {
		t.Errorf("doctor --quiet printed %q, want nothing", quiet)
	}

	// Converge the wedge, then check the do-phase plan contract.
	run(t, false, "done", "wedged", "--verified", "--dir", dir)
	run(t, false, "new", "no-boxes", "--dir", dir)
	writeFile(t, planPath(dir, "no-boxes"), "just prose, no tasks\n")
	run(t, false, "phase", "no-boxes", "--dir", dir)
	out = run(t, true, "doctor", "--dir", dir)
	if !strings.Contains(out, "no `- [ ]` task checkboxes") {
		t.Errorf("doctor = %q, want the checkbox-contract finding", out)
	}
	writeFile(t, planPath(dir, "no-boxes"), "# plan\n- [ ] a task\n  - Files: `internal/example.go`\nFinal Verify:\n")
	out = run(t, true, "doctor", "--dir", dir)
	for _, want := range []string{"`Change:`", "`Verify:`", "non-empty `Final Verify:`"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor = %q, want missing contract field %q", out, want)
		}
	}
	writeFile(t, planPath(dir, "no-boxes"), "# plan\n- [ ] a task\n  - Files: `internal/example.go`\n  - Change: preserve the contract\n  - Verify: `go test ./internal/tocli` — passes\nFinal Verify: `go test ./...` — passes\n")
	if out := run(t, false, "doctor", "--dir", dir); !strings.Contains(out, "healthy") {
		t.Errorf("doctor after fix = %q, want healthy", out)
	}
}

func TestHandoffExcerptKeepsUncheckedTail(t *testing.T) {
	dir := t.TempDir()
	if err := tostate.Save(statePath(dir, "long"), tostate.State{
		Change: "long", Phase: tostate.PhaseDo, Created: "2026-07-18",
	}); err != nil {
		t.Fatal(err)
	}
	var plan strings.Builder
	plan.WriteString("# goal\n")
	for i := 0; i < 70; i++ {
		plan.WriteString("- [x] finished step\n")
	}
	plan.WriteString("- [ ] the remaining task at the bottom\n")
	plan.WriteString("  - Files: `internal/example.go`\n")
	plan.WriteString("  - Change: preserve the complete task contract\n")
	plan.WriteString("  - Verify: `go test ./internal/tocli` — passes\n")
	plan.WriteString("Final Verify: `go test ./...` — passes\n")
	plan.WriteString("## Notes\nKeep this recovery decision.\n")
	writeFile(t, planPath(dir, "long"), plan.String())

	out := run(t, false, "handoff", "long", "--dir", dir)
	if !strings.Contains(out, "the remaining task at the bottom") {
		t.Errorf("handoff dropped the unchecked tail task:\n%s", out)
	}
	for _, want := range []string{"Files: `internal/example.go`", "Change: preserve", "Verify: `go test ./internal/tocli`"} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff dropped task contract line %q:\n%s", want, out)
		}
	}
	for _, want := range []string{"Final Verify: `go test ./...`", "## Notes", "Keep this recovery decision."} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff dropped recovery context %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("handoff must note the truncation:\n%s", out)
	}
}

func TestNewValidatesNames(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	for _, bad := range []string{"Bad", "a b", "../x", "archive", "a--b"} {
		run(t, true, "new", bad, "--dir", dir)
	}
}

func TestStatusIsConfigFreeAndJSON(t *testing.T) {
	// No homonto.toml anywhere: status still answers.
	dir := t.TempDir()
	if out := run(t, false, "status", "--dir", dir); !strings.Contains(out, "no active changes") {
		t.Errorf("status = %q, want no active changes", out)
	}

	// With changes: sorted entries, invalid state reported not fatal.
	gated := setUpGatedWorkspace(t)
	run(t, false, "new", "bbb", "--dir", gated)
	run(t, false, "new", "aaa", "--dir", gated)
	run(t, false, "phase", "bbb", "--dir", gated)
	writeFile(t, filepath.Join(tasksDir(gated), "broken", tostate.FileName), "phase: nonsense\n")

	out := run(t, false, "status", "--json", "--dir", gated)
	var entries []statusEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("status --json output %q: %v", out, err)
	}
	if len(entries) != 3 || entries[0].Change != "aaa" || entries[1].Change != "bbb" || entries[2].Change != "broken" {
		t.Fatalf("entries = %+v, want aaa,bbb,broken", entries)
	}
	if entries[0].Phase != tostate.PhasePlan || entries[1].Phase != tostate.PhaseDo {
		t.Errorf("phases = %q,%q, want plan,do", entries[0].Phase, entries[1].Phase)
	}
	if entries[2].Error == "" {
		t.Errorf("broken entry = %+v, want an error", entries[2])
	}
}

func TestHandoffIsConfigFree(t *testing.T) {
	// Handoff must work without homonto.toml: build the change layout by hand
	// (simulating a repo where the config was removed after the change began).
	dir := t.TempDir()
	if err := tostate.Save(statePath(dir, "resume-me"), tostate.State{
		Change: "resume-me", Phase: tostate.PhaseDo, Created: "2026-07-18",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, planPath(dir, "resume-me"), "# plan\n- [x] step one\n- [ ] step two\n")

	out := run(t, false, "handoff", "resume-me", "--dir", dir)
	for _, want := range []string{"phase: do", "step two", "/to-do"} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff output %q missing %q", out, want)
		}
	}

	var pack map[string]any
	jsonOut := run(t, false, "handoff", "resume-me", "--json", "--dir", dir)
	if err := json.Unmarshal([]byte(jsonOut), &pack); err != nil {
		t.Fatalf("handoff --json output %q: %v", jsonOut, err)
	}
	if pack["next"] == "" || pack["plan"] == "" {
		t.Errorf("handoff pack = %+v, want next and plan", pack)
	}

	writeFile(t, planPath(dir, "resume-me"), "# plan\n- [x] step one\n- [x] step two\nFinal Verify: `go test ./...` — passes\n")
	if out := run(t, false, "handoff", "resume-me", "--dir", dir); !strings.Contains(out, "/to-done") {
		t.Errorf("handoff with all tasks checked = %q, want /to-done", out)
	}
}

func TestHandoffReportsMissingPlan(t *testing.T) {
	dir := t.TempDir()
	if err := tostate.Save(statePath(dir, "missing-plan"), tostate.State{
		Change: "missing-plan", Phase: tostate.PhasePlan, Created: "2026-07-18",
	}); err != nil {
		t.Fatal(err)
	}
	run(t, true, "handoff", "missing-plan", "--dir", dir)
}

func TestJSONOutputsAreWellFormed(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	for _, args := range [][]string{
		{"init", "--json", "--dir", dir},
		{"new", "j", "--json", "--dir", dir},
		{"phase", "j", "--json", "--dir", dir},
		{"done", "j", "--verified", "--json", "--dir", dir},
	} {
		out := run(t, false, args...)
		var v any
		if err := json.Unmarshal([]byte(out), &v); err != nil {
			t.Errorf("%v output %q is not JSON: %v", args, out, err)
		}
	}
}
