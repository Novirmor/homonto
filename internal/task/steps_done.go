// Done-phase workflow steps: checks, review, finalize, and the evidence record.
package task

import (
	"context"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// stepDoneChecks runs the configured verification commands itself. Homonto
// executes them rather than accepting an agent's claim that they passed,
// and the evidence is recorded before the verdict is acted on.
func (e *Engine) stepDoneChecks(ctx context.Context, st State) (State, bool, error) {
	set, err := e.env.RunChecks(ctx, st.WorkID)
	if err != nil {
		return st, false, err
	}
	if err := e.evidence.Record(ctx, st.WorkID, set); err != nil {
		return st, false, err
	}
	event := EventChecksPassed
	if !set.Passed() {
		event = EventChecksFailed
	}
	next, err := Advance(st.Step, event)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// stepDoneReview dispatches the reviewer and the skeptic in parallel over
// the integrated result, then gates on their findings.
func (e *Engine) stepDoneReview(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepDoneReview)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		for _, role := range []protocol.Role{protocol.RoleReviewer, protocol.RoleSkeptic} {
			spec, err := e.readOnly(st, StepDoneReview, role, control,
				"assess the integrated result",
				"Assess the integrated result against the goal and the checklist in "+
					st.documentPath()+". Report findings with severities. Do not write anything.")
			if err != nil {
				return st, false, err
			}
			if _, err := e.assignments.Create(ctx, spec); err != nil {
				return st, false, err
			}
		}
		return st, true, nil
	}
	if done, _ := allAnswered(issued); !done {
		return st, false, nil
	}
	blocked, err := e.blockingFindings(ctx, st.WorkID)
	if err != nil {
		return st, false, err
	}
	event := EventReviewClean
	if blocked {
		event = EventReviewBlocked
	}
	next, err := Advance(st.Step, event)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// stepDoneFinalize appends the evidence, checks every item off, and
// archives the record. Only Homonto writes here: the checkoffs and the
// evidence are binary-owned, and the archive move is a binary operation.
func (e *Engine) stepDoneFinalize(ctx context.Context, st State) (State, bool, error) {
	ref := st.ref()
	// Items are NOT checked off here. The ownership table gives Homonto
	// the evidence region in Done and the checklist only in Do, and that
	// is the right split: an item is checked when the assignment that did
	// it was accepted, never because the task reached the end. An item
	// still open at this point is an item nothing accepted work for, and
	// the record should say so rather than tidy it away.
	block, err := e.evidenceBlock(ctx, st)
	if err != nil {
		return st, false, err
	}
	if _, err := e.artifacts.AppendEvidence(ctx, ref, artifact.PhaseDone, block); err != nil {
		return st, false, err
	}
	if _, err := e.archive.ArchiveWork(ctx, st.WorkID, e.now()); err != nil {
		return st, false, err
	}
	if err := e.findings.ResetRepairs(ctx, st.WorkID); err != nil {
		return st, false, err
	}
	next, err := Advance(st.Step, EventFinalized)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// evidenceBlock renders what the record must carry: a short outcome, the
// exact commands and their outcomes, the integration material, and every
// accepted deviation.
func (e *Engine) evidenceBlock(ctx context.Context, st State) ([]byte, error) {
	var b strings.Builder
	b.WriteString("## Outcome\n\nCompleted.\n\n## Verification\n\n")
	set, err := e.evidence.Latest(ctx, st.WorkID)
	if err != nil {
		b.WriteString("No verification evidence was recorded.\n")
	} else {
		for _, r := range set.Results {
			fmt.Fprintf(&b, "- `%s` in %q: %s (exit %d, %s)\n",
				strings.Join(r.Spec.Command, " "), displayDir(r.Spec.WorkingDir),
				r.Outcome, r.ExitCode, r.Duration)
		}
	}
	b.WriteString("\n## Integration\n\n")
	if len(st.Baseline.Sources) == 0 {
		b.WriteString("No integrated source fingerprints were recorded.\n")
	} else {
		for _, d := range st.Baseline.Sources {
			fmt.Fprintf(&b, "- %s\n", d)
		}
	}
	devs, err := e.findings.Deviations(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	b.WriteString("\n## Accepted deviations\n\n")
	if len(devs) == 0 {
		b.WriteString("None.\n")
	} else {
		for _, d := range devs {
			fmt.Fprintf(&b, "- %s %s: %s — accepted because %s (decision %s)\n",
				d.Severity, d.ExternalID, d.Summary, d.Rationale, d.DecisionID)
		}
	}
	return []byte(b.String()), nil
}

// displayDir renders a check's working directory for the record.
func displayDir(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}
