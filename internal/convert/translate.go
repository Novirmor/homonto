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
// Owner/Repo/Cwd/Files/Change/Verify and a Final Verify line); at phase plan
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

var planFieldLine = regexp.MustCompile(`^[ \t]*[-*][ \t]+(Owner|Repo|Cwd|Files|Do|Verify):[ \t]*(.*)$`)
var planTaskHeading = regexp.MustCompile(`^## Task (\d+\.\d+)(?:[ \t].*)?$`)

var planFinalVerify = regexp.MustCompile(`^Final Verify:[ \t]*(.*)$`)

// translatedTask is one carried-over task.
type translatedTask struct {
	num    int
	title  string
	done   bool
	owner  string
	repo   string
	cwd    string
	files  string
	change string
	verify string
}

// translateTasks converts an onto change's tasks.md (plus matching plan.md
// detail) into a doctor-clean `to` plan. Incomplete contracts return ok=false,
// so the caller demotes to phase plan rather than inventing execution detail.
func translateTasks(m manifest, snapDir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(snapDir, "tasks.md"))
	if err != nil {
		return "", false
	}
	plan := ""
	if b, err := os.ReadFile(filepath.Join(snapDir, "plan.md")); err == nil {
		plan = string(b)
	}
	finalVerify := ""
	lines := strings.Split(plan, "\n")
	for i, line := range lines {
		if mm := planFinalVerify.FindStringSubmatch(line); mm != nil {
			if finalVerify != "" {
				return "", false
			}
			finalVerify, _ = planFieldValue(lines, i, mm[1])
			if !concreteContract(finalVerify) {
				return "", false
			}
		}
	}

	seen := map[int]bool{}
	seenIDs := map[string]bool{}
	tasks := []translatedTask{}
	for _, line := range strings.Split(string(data), "\n") {
		mm := ontoTask.FindStringSubmatch(line)
		if mm == nil {
			if strings.HasPrefix(strings.TrimSpace(line), "- [") || strings.HasPrefix(strings.TrimSpace(line), "* [") {
				return "", false
			}
			continue
		}
		id := mm[2] + "." + mm[3]
		if seenIDs[id] {
			return "", false
		}
		seenIDs[id] = true
		fields := planContractFor(plan, id)
		for _, key := range []string{"Owner", "Repo", "Cwd", "Files", "Do", "Verify"} {
			if !concreteContract(fields[key]) {
				return "", false
			}
		}
		// Execution scope comes from the source contract, never a guessed repo
		// or a title-derived owner. Paths need not exist in the staging tree.
		for _, key := range []string{"Owner", "Repo", "Cwd"} {
			if strings.ContainsAny(fields[key], "\r\n") {
				return "", false
			}
		}
		if !filepath.IsAbs(strings.Trim(fields["Cwd"], "`")) {
			return "", false
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
			done:   mm[1] == "x" && !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(mm[4])), "DEFERRED TO CLOSE:"),
			owner:  fields["Owner"],
			repo:   fields["Repo"],
			cwd:    fields["Cwd"],
			files:  fields["Files"],
			change: fields["Do"],
			verify: fields["Verify"],
		}
		tasks = append(tasks, t)
	}
	if len(tasks) == 0 {
		return "", false
	}
	if finalVerify == "" {
		// Carry the final task's recorded check when no aggregate is explicit.
		finalVerify = tasks[len(tasks)-1].verify
	}
	if !concreteContract(finalVerify) {
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
		for _, field := range [][2]string{{"Owner", t.owner}, {"Repo", t.repo}, {"Cwd", t.cwd}, {"Files", t.files}, {"Change", t.change}, {"Verify", t.verify}} {
			lines := strings.Split(field[1], "\n")
			fmt.Fprintf(&b, "  - %s: %s\n", field[0], lines[0])
			for _, line := range lines[1:] {
				if line != "" {
					b.WriteString("  " + line)
				}
				b.WriteByte('\n')
			}
		}
	}
	fmt.Fprintf(&b, "\nFinal Verify: %s\n", finalVerify)
	return b.String(), true
}

// planContractFor reads one exact dotted task heading, never an ID prefix.
func planContractFor(plan, id string) map[string]string {
	fields := map[string]string{}
	active, found := false, false
	lines := strings.Split(plan, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "## ") {
			m := planTaskHeading.FindStringSubmatch(line)
			active = m != nil && m[1] == id
			if active && found {
				return nil
			}
			found = found || active
			continue
		}
		if active {
			if m := planFieldLine.FindStringSubmatch(line); m != nil {
				if _, exists := fields[m[1]]; exists {
					return nil
				}
				var end int
				fields[m[1]], end = planFieldValue(lines, i, m[2])
				i = end - 1
			}
		}
	}
	return fields
}

// Preserve Markdown continuation text, including nested lists and code blocks.
// Values retain indentation relative to their source field; rendering adds only
// the destination task's outer indentation. Unindented lazy wraps are indented
// so they cannot escape the destination checkbox's contract.
func planFieldValue(lines []string, start int, value string) (string, int) {
	indent := len(lines[start]) - len(strings.TrimLeft(lines[start], " \t"))
	i := start + 1
	blank := 0
	for ; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			blank++
			continue
		}
		leading := len(line) - len(strings.TrimLeft(line, " \t"))
		if leading <= indent {
			if blank > 0 || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || planFinalVerify.MatchString(trimmed) {
				break
			}
			line = "  " + trimmed
		} else {
			line = line[indent:]
		}
		value += strings.Repeat("\n", blank)
		value += "\n" + line
		blank = 0
	}
	return strings.TrimRight(value, " \t\r\n"), i
}

func concreteContract(value string) bool {
	first, _, _ := strings.Cut(value, "\n")
	if strings.TrimSpace(first) == "" {
		return false
	}
	v := strings.ToLower(strings.TrimSpace(value))
	return v != "" && v != "todo" && v != "tbd" && v != "..." && !strings.Contains(v, "<") &&
		!strings.Contains(v, "fill in") && !strings.Contains(v, "see the imported plan") && !strings.Contains(v, ".workflow/snapshots/")
}

// planTranslatable uses the same contract translation as generation.
func planTranslatable(srcDir string) bool {
	_, ok := translateTasks(manifest{}, srcDir)
	return ok
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
