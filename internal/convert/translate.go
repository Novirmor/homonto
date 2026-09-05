package convert

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// generatedFiles names the files the engine (re)generates for a direction;
// a resume deletes them before regenerating, and never trusts staged copies.
func generatedFiles(spec directionSpec) []string {
	if spec.name == Promote {
		return []string{"onto-state.yaml", "proposal.md"}
	}
	return []string{"to-state.yaml", "plan.md"}
}

// buildProposal writes the promoted change's fresh proposal: promotion claims
// no design or verification happened, so the change starts at open with a
// proposal seeded from the imported plan.
func buildProposal(m manifest, srcPhase, plan string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Proposal: %s (promoted from `to`)\n\n", m.Target)
	fmt.Fprintf(&b, "Promoted from the `to` change snapshotted under\n")
	fmt.Fprintf(&b, "`.workflow/snapshots/%s/to/`.\n", m.OperationID)
	fmt.Fprintf(&b, "The promotion does not claim design or verification happened —\n")
	fmt.Fprintf(&b, "this change starts at phase open with a fresh proposal.\n\n")
	fmt.Fprintf(&b, "- **Imported phase**: %s\n", srcPhase)
	fmt.Fprintf(&b, "- **Lineage**: %s\n", m.Lineage.LineageID)
	if head := firstMeaningful(plan); head != "" {
		fmt.Fprintf(&b, "- **Plan excerpt (from the imported plan.md)**: %s\n", head)
	}
	b.WriteString("\n## Why promoted\n\n<fill in: what grew beyond `to`'s shape — design questions, evidence\nobligations, a second reader>\n")
	return b.String()
}

// buildToPlan writes the demoted change's plan. At phase do the plan carries
// the onto change's tasks over in `to`'s contract shape (checkboxes with
// Files/Change/Verify and a Final Verify line, doctor-clean); at phase plan
// it is an honest stub that points at the snapshot for the carry-over.
func buildToPlan(m manifest, srcPhase, snapDir string) string {
	if m.TargetIdent.Phase == "do" {
		if plan, ok := translateTasks(m, snapDir); ok {
			return plan
		}
		// The pre-mint decided do based on the same bytes; a mismatch here
		// means tampered staging, which authentication already refuses. Fall
		// through to the stub rather than emitting an invalid do plan.
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan: %s (demoted from onto)\n\n", m.Target)
	fmt.Fprintf(&b, "Demoted from the onto change snapshotted under\n")
	fmt.Fprintf(&b, "`.workflow/snapshots/%s/onto/`. The demotion does not claim\n", m.OperationID)
	fmt.Fprintf(&b, "verification happened — this change restarts at phase plan under `to`'s\n")
	fmt.Fprintf(&b, "no-gates workflow. Carry the remaining work over from the snapshot.\n\n")
	fmt.Fprintf(&b, "- **Imported phase**: %s\n", srcPhase)
	fmt.Fprintf(&b, "- **Lineage**: %s\n", m.Lineage.LineageID)
	b.WriteString("\n## Tasks\n\n<fill in: carry over the remaining work from the snapshot's tasks.md>\n")
	return b.String()
}

// ontoTask matches one line of an onto tasks.md: completion, dotted id,
// title, and optional numeric trace marker.
var ontoTask = regexp.MustCompile(`^[-*] \[( |x)\] (\d+)\.(\d+) (.+?)(?:\s+\[trace #(\d+)\])?$`)

// planFilesLine matches the "- Files: …" sub-line of an onto plan.md task
// block, used to carry real file lists into the translated plan.
var planFilesLine = regexp.MustCompile(`(?m)^\s*[-*]\s+Files:\s*(\S.*)$`)

// planFinalVerify matches a non-empty "Final Verify:" line in plan.md.
var planFinalVerify = regexp.MustCompile(`(?m)^\s*Final Verify:\s*(\S.*)$`)

// translatedTask is one carried-over task.
type translatedTask struct {
	num    int
	title  string
	done   bool
	files  string
	verify string
}

// translateTasks converts an onto change's tasks.md (plus matching plan.md
// detail) into a doctor-clean `to` plan. ok=false when no task parses — the
// caller then demotes to phase plan instead.
func translateTasks(m manifest, snapDir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(snapDir, "tasks.md"))
	if err != nil {
		return "", false
	}
	plan := ""
	if b, err := os.ReadFile(filepath.Join(snapDir, "plan.md")); err == nil {
		plan = string(b)
	}
	finalVerify := "carry over from `.workflow/snapshots/" + m.OperationID + "/onto/plan.md`"
	if mm := planFinalVerify.FindStringSubmatch(plan); mm != nil {
		finalVerify = mm[1]
	}

	seen := map[int]bool{}
	tasks := []translatedTask{}
	for _, line := range strings.Split(string(data), "\n") {
		mm := ontoTask.FindStringSubmatch(line)
		if mm == nil {
			continue
		}
		num := 0
		if mm[5] != "" {
			fmt.Sscanf(mm[5], "%d", &num)
		}
		if num <= 0 || seen[num] {
			num = len(tasks) + 1
			for seen[num] {
				num++
			}
		}
		seen[num] = true
		t := translatedTask{
			num:    num,
			title:  strings.TrimSpace(mm[4]),
			done:   mm[1] == "x",
			files:  "see `.workflow/snapshots/" + m.OperationID + "/onto/plan.md`",
			verify: "see the imported plan",
		}
		if f := planFilesFor(plan, mm[2]+"."+mm[3]); f != "" {
			t.files = f
		}
		tasks = append(tasks, t)
	}
	if len(tasks) == 0 {
		return "", false
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Plan: %s (demoted from onto)\n\n", m.Target)
	fmt.Fprintf(&b, "Demoted from the onto change snapshotted under\n")
	fmt.Fprintf(&b, "`.workflow/snapshots/%s/onto/`; tasks carried over. The demotion does\n", m.OperationID)
	fmt.Fprintf(&b, "not claim verification happened — `to` has no gates.\n\n")
	for _, t := range tasks {
		box := " "
		if t.done {
			box = "x"
		}
		fmt.Fprintf(&b, "- [%s] #%d %s\n", box, t.num, t.title)
		fmt.Fprintf(&b, "  - Files: %s\n", t.files)
		fmt.Fprintf(&b, "  - Change: %s\n", t.title)
		fmt.Fprintf(&b, "  - Verify: %s\n", t.verify)
	}
	fmt.Fprintf(&b, "\nFinal Verify: %s\n", finalVerify)
	return b.String(), true
}

// planFilesFor extracts the "- Files: …" value from the plan.md block of one
// dotted task id ("## Task 1.1"), if present.
func planFilesFor(plan, id string) string {
	idx := strings.Index(plan, "## Task "+id)
	if idx < 0 {
		return ""
	}
	rest := plan[idx:]
	if next := strings.Index(rest[7:], "\n## Task "); next >= 0 {
		rest = rest[:next+7]
	}
	if mm := planFilesLine.FindStringSubmatch(rest); mm != nil {
		return mm[1]
	}
	return ""
}

// planTranslatable reports whether the source workspace's tasks.md carries
// at least one parseable task — the precondition for demoting into phase do.
func planTranslatable(srcDir string) bool {
	data, err := os.ReadFile(filepath.Join(srcDir, "tasks.md"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if ontoTask.MatchString(line) {
			return true
		}
	}
	return false
}

func firstMeaningful(plan string) string {
	for _, ln := range strings.Split(plan, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") {
			continue
		}
		if len(t) > 160 {
			t = t[:160] + "…"
		}
		return t
	}
	return ""
}
