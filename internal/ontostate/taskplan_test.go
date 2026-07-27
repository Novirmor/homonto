package ontostate

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTaskPlan seeds a change directory with the given tasks.md / plan.md
// bodies. An empty plan body means "no plan.md at all" (the preset case).
func writeTaskPlan(t *testing.T, tasks, plan string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	tasksPath := filepath.Join(dir, "tasks.md")
	planPath := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(tasksPath, []byte(tasks), 0o644); err != nil {
		t.Fatal(err)
	}
	if plan != "" {
		if err := os.WriteFile(planPath, []byte(plan), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return tasksPath, planPath
}

const goodTasks = `# Tasks: demo

## 1. Foundation

- [x] 1.1 Add the parser
- [ ] 1.2 Wire it in

## 2. Validation

- [ ] 2.1 Prove it
`

const goodPlan = `# Plan: demo

## Task 1.1 — Add the parser

- Files: a.go

## Task 1.2 — Wire it in

- Files: b.go

## Task 2.1 — Prove it

- Files: c_test.go
`

func TestCheckTaskPlan_InCorrespondence(t *testing.T) {
	drift, err := CheckTaskPlan(writeTaskPlan(t, goodTasks, goodPlan))
	if err != nil {
		t.Fatalf("CheckTaskPlan: %v", err)
	}
	if !drift.Empty() {
		t.Errorf("drift = %+v, want empty", drift)
	}
}

func TestCheckTaskPlan_NoPlanIsNotDrift(t *testing.T) {
	// Presets (fix/tweak) run without a plan.md. Reporting that as drift would
	// make `doctor` permanently unhappy on every preset change.
	drift, err := CheckTaskPlan(writeTaskPlan(t, goodTasks, ""))
	if err != nil {
		t.Fatalf("CheckTaskPlan: %v", err)
	}
	if !drift.Empty() {
		t.Errorf("drift = %+v, want empty for a preset with no plan.md", drift)
	}
}

func TestCheckTaskPlan_TaskAppendedToOnlyOneFile(t *testing.T) {
	// The real failure: discovered work appended to tasks.md but not plan.md,
	// so a resuming session hits an item with no detail block.
	tasks := goodTasks + "- [ ] 2.2 (discovered 2026-07-27): handle the empty case\n"
	drift, err := CheckTaskPlan(writeTaskPlan(t, tasks, goodPlan))
	if err != nil {
		t.Fatalf("CheckTaskPlan: %v", err)
	}
	if got := drift.MissingFromPlan; len(got) != 1 || got[0] != "2.2" {
		t.Errorf("MissingFromPlan = %v, want [2.2]", got)
	}
	if len(drift.MissingFromTasks) != 0 {
		t.Errorf("MissingFromTasks = %v, want none", drift.MissingFromTasks)
	}
}

func TestCheckTaskPlan_PlanTaskWithNoItem(t *testing.T) {
	plan := goodPlan + "\n## Task 3.1 — Orphaned detail\n\n- Files: d.go\n"
	drift, err := CheckTaskPlan(writeTaskPlan(t, goodTasks, plan))
	if err != nil {
		t.Fatalf("CheckTaskPlan: %v", err)
	}
	if got := drift.MissingFromTasks; len(got) != 1 || got[0] != "3.1" {
		t.Errorf("MissingFromTasks = %v, want [3.1]", got)
	}
}

func TestCheckTaskPlan_CheckboxInPlanIsDrift(t *testing.T) {
	// ADR 0018: plan.md carries no completion state. A checkbox here is a
	// second, unread task list — exactly what the ADR removed.
	plan := goodPlan + "\n- [x] 1.1 done\n"
	drift, err := CheckTaskPlan(writeTaskPlan(t, goodTasks, plan))
	if err != nil {
		t.Fatalf("CheckTaskPlan: %v", err)
	}
	if drift.PlanCheckboxes != 1 {
		t.Errorf("PlanCheckboxes = %d, want 1", drift.PlanCheckboxes)
	}
	if drift.Empty() {
		t.Error("drift.Empty() = true, want false — a plan checkbox is a finding")
	}
}

func TestCheckTaskPlan_ReportsEveryDivergentNumber(t *testing.T) {
	tasks := "# Tasks\n\n- [ ] 1.1 a\n- [ ] 1.2 b\n- [ ] 1.3 c\n"
	plan := "# Plan\n\n## Task 1.2 — b\n\n## Task 4.1 — z\n"
	drift, err := CheckTaskPlan(writeTaskPlan(t, tasks, plan))
	if err != nil {
		t.Fatalf("CheckTaskPlan: %v", err)
	}
	// Sorted, so the report is stable across runs.
	want := []string{"1.1", "1.3"}
	if len(drift.MissingFromPlan) != len(want) {
		t.Fatalf("MissingFromPlan = %v, want %v", drift.MissingFromPlan, want)
	}
	for i, w := range want {
		if drift.MissingFromPlan[i] != w {
			t.Errorf("MissingFromPlan[%d] = %q, want %q", i, drift.MissingFromPlan[i], w)
		}
	}
	if got := drift.MissingFromTasks; len(got) != 1 || got[0] != "4.1" {
		t.Errorf("MissingFromTasks = %v, want [4.1]", got)
	}
}

func TestCheckTaskPlan_MissingTasksFileIsError(t *testing.T) {
	dir := t.TempDir()
	planPath := filepath.Join(dir, "plan.md")
	if err := os.WriteFile(planPath, []byte(goodPlan), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckTaskPlan(filepath.Join(dir, "tasks.md"), planPath); err == nil {
		t.Error("CheckTaskPlan with no tasks.md = nil error, want an error")
	}
}
