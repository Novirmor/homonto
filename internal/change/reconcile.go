package change

import (
	"context"
	"errors"
	"fmt"

	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/verify"
)

// Reconcile compares the baseline the recorded step rests on against the
// world as it is now, and returns the change to the earliest step any
// moved input affects.
//
// This is what makes the recorded step worth acting on. Homonto never
// trusts "we are at close" on its own: it trusts "we are at close AND the
// proposal, the design, the tasks, the plan, the membership, the path
// classes, the check configuration, the integrated sources, and the
// verification are all still what that position was reached on". When one
// has moved, the position is fiction and reconciliation replaces it with
// the truth.
//
// Everything a return invalidates is invalidated for real: the pending and
// issued actions of the superseded generation are marked invalid so no
// in-flight host can answer them, the generation is bumped so a late
// answer to an old action is refused, and evidence the return reaches back
// past is deleted rather than left readable.
func (e *Engine) Reconcile(ctx context.Context, id identity.WorkID) (State, []Invalidation, error) {
	st, err := e.State(ctx, id)
	if err != nil {
		return State{}, nil, err
	}
	if terminalStep(st.Path, st.Step) {
		return st, nil, nil
	}
	now, err := e.currentBaseline(ctx, st)
	if err != nil {
		return State{}, nil, err
	}

	current := Step(st.Step)
	invalidations := Compare(st.Path, current, st.Baseline, now)
	target := Target(st.Path, current, invalidations)
	if target == current {
		return st, nil, nil
	}

	if err := e.invalidateOpenActions(ctx, st); err != nil {
		return State{}, nil, err
	}
	if index(st.Path, target) <= index(st.Path, checksStep(st.Path)) {
		// The return reaches back past the checks, so the recorded
		// evidence describes a world that no longer exists. Delete it
		// rather than leave it readable: stale evidence that can still be
		// read is evidence someone eventually trusts.
		if err := e.evidence.Clear(ctx, st.WorkID); err != nil && !errors.Is(err, verify.ErrNoEvidence) {
			return State{}, nil, err
		}
		if err := e.findings.ResetRepairs(ctx, st.WorkID); err != nil {
			return State{}, nil, err
		}
		now.Verification = ""
	}
	// A return that reaches back past integration has no integrated result
	// to speak of any more.
	if index(st.Path, target) <= index(st.Path, integrateStep(st.Path)) {
		now.Sources = nil
	}
	// The immutable work baseline is never re-captured. Reconciliation is
	// exactly the moment a preset might otherwise escape its own scope
	// warning, so the one thing that must not move here is the ruler.
	now.Work = st.Baseline.Work
	st.Baseline = now
	st.Generation++
	st.Step = string(target)
	st.UpdatedAt = e.now().UTC()
	if err := e.saveState(ctx, st); err != nil {
		return State{}, nil, err
	}
	return st, invalidations, nil
}

// currentBaseline reads today's fingerprints for a change, keeping the
// immutable work baseline it was confirmed with.
func (e *Engine) currentBaseline(ctx context.Context, st State) (Baseline, error) {
	baseline, err := e.env.Fingerprints(ctx)
	if err != nil {
		return Baseline{}, err
	}
	docs, err := e.documentDigests(ctx, st)
	if err != nil {
		return Baseline{}, err
	}
	baseline.Documents = docs
	baseline.Work = st.Baseline.Work
	if len(st.Baseline.Sources) > 0 {
		sources, err := e.env.SourceFingerprints(ctx, st.WorkID)
		if err != nil {
			return Baseline{}, err
		}
		baseline.Sources = sortedDigests(sources)
	}
	if st.Baseline.Verification != "" {
		set, err := e.evidence.Latest(ctx, st.WorkID)
		if err == nil {
			digest, err := set.Digest()
			if err != nil {
				return Baseline{}, err
			}
			baseline.Verification = digest
		}
	}
	return baseline, nil
}

// checksStep is the path's verification-checks step.
func checksStep(p Path) Step {
	if p == PathFull {
		return StepVerifyChecks
	}
	return StepPresetChecks
}

// integrateStep is the path's integration step.
func integrateStep(p Path) Step {
	if p == PathFull {
		return StepBuildIntegrate
	}
	return StepPresetIntegrate
}

// invalidateOpenActions marks every unanswered action of a change invalid,
// so a host still holding one cannot answer it after the ground moved.
func (e *Engine) invalidateOpenActions(ctx context.Context, st State) error {
	actions, err := e.assignments.Actions(ctx, st.WorkID)
	if err != nil {
		return err
	}
	var ids []identity.ActionID
	for _, act := range actions {
		switch act.State {
		case assignment.StatePending, assignment.StateIssued:
			ids = append(ids, act.ID)
		}
	}
	if err := e.assignments.Invalidate(ctx, ids...); err != nil {
		return fmt.Errorf("change: invalidate open actions of %s: %w", st.WorkID, err)
	}
	return nil
}
