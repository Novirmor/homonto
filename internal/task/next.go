package task

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// maxAdvancesPerNext bounds how many steps one Next call may traverse. The
// workflow is finite and each pass either issues actions or advances, so
// the bound is only a backstop against a step that claims progress it did
// not make — better a loud refusal than a spin.
const maxAdvancesPerNext = 32

// Next returns the actions a host may execute now. It reconciles first,
// because the recorded step is only worth acting on once its baseline has
// been checked against the world; then it drives the workflow forward
// until something is waiting on the host, and returns that.
func (e *Engine) Next(ctx context.Context, id identity.WorkID) (protocol.NextResponse, error) {
	st, _, err := e.Reconcile(ctx, id)
	if err != nil {
		return protocol.NextResponse{}, err
	}
	for i := 0; i < maxAdvancesPerNext; i++ {
		if st.Step.Terminal() {
			return assignment.CompleteResponse(), nil
		}
		group, ok, err := e.assignments.ReadyGroup(ctx, id)
		if err != nil {
			return protocol.NextResponse{}, err
		}
		if ok {
			return group.Response(), nil
		}
		next, progressed, err := e.runStep(ctx, st)
		if err != nil {
			return protocol.NextResponse{}, err
		}
		if !progressed {
			return protocol.NextResponse{}, fmt.Errorf(
				"task: %s is stuck at %s with nothing issued and nothing to advance", id, st.Step)
		}
		st = next
	}
	return protocol.NextResponse{}, fmt.Errorf(
		"task: %s traversed %d steps in one call without reaching a host action", id, maxAdvancesPerNext)
}

// runStep issues the current step's work if it has none, or completes the
// step if its work is answered. It reports whether it made progress; a
// step that makes none is a bug, and Next says so rather than spinning.
func (e *Engine) runStep(ctx context.Context, st State) (State, bool, error) {
	switch st.Step {
	case StepPlanExplore:
		return e.stepPlanExplore(ctx, st)
	case StepPlanDraft:
		return e.stepPlanDraft(ctx, st)
	case StepPlanChallenge:
		return e.stepPlanChallenge(ctx, st)
	case StepPlanResolve:
		return e.stepPlanResolve(ctx, st)
	case StepDoImplement:
		return e.stepDoImplement(ctx, st)
	case StepDoIntegrate:
		return e.stepDoIntegrate(ctx, st)
	case StepDoneChecks:
		return e.stepDoneChecks(ctx, st)
	case StepDoneReview:
		return e.stepDoneReview(ctx, st)
	case StepDoRepair:
		return e.stepDoRepair(ctx, st)
	case StepDoneFinalize:
		return e.stepDoneFinalize(ctx, st)
	}
	return st, false, fmt.Errorf("task: step %q has no handler", st.Step)
}
