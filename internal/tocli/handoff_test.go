package tocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/tostate"
)

// TestNextStep_TerminalPhaseIsNone verifies the terminal-phase branch of
// nextStep (the previously-uncovered default case): a terminal change
// recommends "none".
func TestNextStep_TerminalPhaseIsNone(t *testing.T) {
	for _, phase := range []string{tostate.PhaseDone, tostate.PhaseAbandoned} {
		if got := nextStep("x", phase, ""); !strings.Contains(got, "terminal") || !strings.Contains(got, "none") {
			t.Errorf("nextStep(%q) = %q, want a 'none — terminal' diagnostic", phase, got)
		}
	}
}

// TestNextStep_PlanPointsAtPlanSkill verifies the plan branch recommends the
// plan skill (not the bookkeeping commands).
func TestNextStep_PlanPointsAtPlanSkill(t *testing.T) {
	got := nextStep("feat", tostate.PhasePlan, "")
	if !strings.Contains(got, "/to-plan") || !strings.Contains(got, "feat") {
		t.Errorf("nextStep(plan) = %q, want /to-plan and the change name", got)
	}
}

// TestNextStep_DoWithUncheckedTasksResumes verifies the do-phase-with-work
// branch points at /to-do (resume implementation).
func TestNextStep_DoWithUncheckedTasksResumes(t *testing.T) {
	plan := "# plan\n- [x] done\n- [ ] remaining\n"
	got := nextStep("feat", tostate.PhaseDo, plan)
	if !strings.Contains(got, "/to-do") {
		t.Errorf("nextStep(do, unchecked) = %q, want /to-do", got)
	}
}

// TestNextStep_DoWithAllCheckedFinishes verifies the do-phase-all-done branch
// points at /to-done (finish).
func TestNextStep_DoWithAllCheckedFinishes(t *testing.T) {
	plan := "# plan\n- [x] step one\n- [x] step two\n"
	got := nextStep("feat", tostate.PhaseDo, plan)
	if !strings.Contains(got, "/to-done") {
		t.Errorf("nextStep(do, all checked) = %q, want /to-done", got)
	}
}

// TestExcerptPlan_ShortPlanKeptWhole verifies a plan shorter than the
// excerpt threshold is carried verbatim (no truncation marker).
func TestExcerptPlan_ShortPlanKeptWhole(t *testing.T) {
	plan := "# goal\n- [ ] a task\n  - Files: `x.go`\nFinal Verify: `go test ./...`\n"
	got := excerptPlan(plan)
	if got != strings.TrimRight(plan, "\n") {
		t.Errorf("excerptPlan(short) = %q, want the plan unchanged", got)
	}
}

// TestHandoff_ArchivedChangeRefuses verifies handoff on an archived change
// surfaces the archive location (loadChange distinguishes archived from
// never-existed).
func TestHandoff_ArchivedChangeRefuses(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "done-deal", "--dir", dir)
	run(t, false, "phase", "done-deal", "--dir", dir)
	run(t, false, "done", "done-deal", "--verified", "--dir", dir)

	out := runErr(t, "handoff", "done-deal", "--dir", dir)
	if !strings.Contains(out, "archived") {
		t.Errorf("handoff-archived error %q missing 'archived'", out)
	}
}

// TestHandoff_IdentityLines verify the text-mode handoff carries the change,
// phase, and created identity lines in their documented shape.
func TestHandoff_IdentityLines(t *testing.T) {
	dir := t.TempDir()
	if err := tostate.Save(statePath(dir, "id"), tostate.State{
		Change: "id", Phase: tostate.PhaseDo, Created: "2030-04-04",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, planPath(dir, "id"), "# plan\n- [ ] one task\n  - Files: `x.go`\nFinal Verify: `go test ./...`\n")

	out := run(t, false, "handoff", "id", "--dir", dir)
	for _, want := range []string{"change: id", "phase: do", "created: 2030-04-04", "next:", "plan.md:"} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff text %q missing line %q", out, want)
		}
	}
}

// TestHandoff_JSONShape verifies the JSON pack carries change, state, plan,
// and next as top-level keys.
func TestHandoff_JSONShape(t *testing.T) {
	dir := t.TempDir()
	if err := tostate.Save(statePath(dir, "js"), tostate.State{
		Change: "js", Phase: tostate.PhaseDo, Created: "2030-04-04",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, planPath(dir, "js"), "# plan\n- [ ] one\nFinal Verify: `go test ./...`\n")

	out := run(t, false, "handoff", "js", "--json", "--dir", dir)
	for _, want := range []string{`"change":`, `"state":`, `"plan":`, `"next":`} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff JSON %q missing %s", out, want)
		}
	}
}

// TestHandoff_JSONEnvelope (H3): the legacy keys survive AND the versioned
// recovery-envelope fields ride alongside, so a machine consumer re-grounds
// without parsing prose.
func TestHandoff_JSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	if err := tostate.Save(statePath(dir, "env"), tostate.State{
		Change: "env", Phase: tostate.PhasePlan, Created: "2030-04-04", Repos: []string{"service"},
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, planPath(dir, "env"), "# plan\n- [ ] one\nFinal Verify: `go test ./...`\n")

	out := run(t, false, "handoff", "env", "--json", "--dir", dir)
	for _, want := range []string{
		`"schemaVersion": 1`, `"tool": "to"`, `"change": "env"`, `"phase": "plan"`,
		`"derivedPhase": "plan"`, `"repoAliases"`, `"artifacts"`, `"nextArgv"`,
		`"change": "env"`, `"state"`, `"plan"`, `"next"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff JSON envelope missing %q:\n%s", want, out)
		}
	}
}

// TestHandoff_WritePersistsMetadataOnly (H4/H5): --write persists a JSON
// envelope and markdown under the change workspace with unique operation-ID
// filenames, and a secret planted in plan prose never reaches either file.
func TestHandoff_WritePersistsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	if err := tostate.Save(statePath(dir, "w"), tostate.State{
		Change: "w", Phase: tostate.PhaseDo, Created: "2030-04-04",
	}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, planPath(dir, "w"), "# plan\nAPI_KEY=hunter2-super-secret\n- [ ] one\nFinal Verify: `go test ./...`\n")

	if out := run(t, false, "handoff", "w", "--dir", dir, "--write"); !strings.Contains(out, "wrote ") {
		t.Fatalf("handoff --write output: %q", out)
	}
	hd := filepath.Join(changeDir(dir, "w"), ".to", "handoff")
	mds, err := filepath.Glob(filepath.Join(hd, "*-context.md"))
	if err != nil || len(mds) != 1 {
		t.Fatalf("markdown pack missing: %v %v", mds, err)
	}
	jsons, err := filepath.Glob(filepath.Join(hd, "*-context.json"))
	if err != nil || len(jsons) != 1 {
		t.Fatalf("json pack missing: %v %v", jsons, err)
	}
	for _, f := range append(mds, jsons...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "hunter2") {
			t.Errorf("%s leaked plan prose/secret:\n%s", f, b)
		}
	}
}
