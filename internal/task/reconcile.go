package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/verify"
)

// Reconcile compares the baseline the recorded step rests on against the
// world as it is now, and returns the workflow to the earliest step any
// moved input affects.
//
// This is what makes the recorded step worth acting on. Homonto never
// trusts "we are at done_review" on its own: it trusts "we are at
// done_review AND the goal, the membership, the path classes, the check
// configuration, and the integrated sources are all still what that
// position was reached on". When one of them has moved, the position is
// fiction and reconciliation replaces it with the truth.
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
	if st.Step.Terminal() {
		return st, nil, nil
	}
	now, err := e.currentBaseline(ctx, st.WorkID, st.ref(), nil)
	if err != nil {
		return State{}, nil, err
	}
	// Sources are compared only against a baseline that recorded them.
	if len(st.Baseline.Sources) > 0 {
		sources, err := e.env.SourceFingerprints(ctx, st.WorkID)
		if err != nil {
			return State{}, nil, err
		}
		now.Sources = sortedDigests(sources)
	}

	invalidations := Compare(st.Step, st.Baseline, now)
	target := Target(st.Step, invalidations)
	if target == st.Step {
		return st, nil, nil
	}

	if err := e.invalidateOpenActions(ctx, st); err != nil {
		return State{}, nil, err
	}
	if target.index() <= StepDoneChecks.index() {
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
	}
	st.Baseline = now
	if target.index() > StepDoIntegrate.index() {
		// A return that stays past integration keeps the sources it was
		// re-baselined against; one that reaches further back has no
		// integrated result to speak of yet.
		st.Baseline.Sources = now.Sources
	} else {
		st.Baseline.Sources = nil
	}
	st.Generation++
	st.Step = target
	st.UpdatedAt = e.now().UTC()
	if err := e.saveState(ctx, st); err != nil {
		return State{}, nil, err
	}
	return st, invalidations, nil
}

// invalidateOpenActions marks every unanswered action of a task invalid,
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
		return fmt.Errorf("task: invalidate open actions of %s: %w", st.WorkID, err)
	}
	return nil
}
