// Do-phase workflow steps: implement partitions and integrate them.
package task

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/assignment"
)

// stepDoImplement partitions the unchecked work into parallel implementer
// assignments, each in its own isolation area with its own declared scope.
func (e *Engine) stepDoImplement(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepDoImplement)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		open, err := e.openItems(ctx, st)
		if err != nil {
			return st, false, err
		}
		partitions, err := e.env.Partition(ctx, st.WorkID, open)
		if err != nil {
			return st, false, err
		}
		if len(partitions) == 0 {
			return st, false, fmt.Errorf(
				"task: %s has no unchecked work to implement; add checklist items in the plan", st.WorkID)
		}
		if err := e.issuePartitions(ctx, st, StepDoImplement, partitions,
			func(p Partition) string { return "implement " + p.Label }); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if done, _ := allAnswered(issued); !done {
		return st, false, nil
	}
	next, err := Advance(st.Step, EventImplementersDone)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// stepDoIntegrate issues one integration implementer per affected member
// and re-baselines the integrated source fingerprints when they are done.
// Reviewer and skeptic only ever see the integrated result.
func (e *Engine) stepDoIntegrate(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepDoIntegrate)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		done, err := e.completedResults(ctx, st)
		if err != nil {
			return st, false, err
		}
		units, err := e.env.Integrations(ctx, st.WorkID, done)
		if err != nil {
			return st, false, err
		}
		if len(units) == 0 {
			return st, false, fmt.Errorf("task: %s produced no integration unit", st.WorkID)
		}
		if err := e.issuePartitions(ctx, st, StepDoIntegrate, units,
			func(p Partition) string { return "integrate " + p.Label }); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if done, _ := allAnswered(issued); !done {
		return st, false, nil
	}
	if err := e.rebaselineSources(ctx, &st); err != nil {
		return st, false, err
	}
	next, err := Advance(st.Step, EventIntegrated)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// stepDoRepair runs one bounded repair round. Entering repair again after
// a previous round means that round failed, and three failed rounds hand
// the choice to a human rather than letting Homonto try a fourth time.
func (e *Engine) stepDoRepair(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepDoRepair)
	if err != nil {
		return st, false, err
	}
	gate, repairs := assignment.SplitRepairActions(issued)

	// An answered limit decision is the human's instruction; act on it.
	if gate != nil && gate.State == assignment.StateSubmitted {
		return e.applyRepairDecision(ctx, st, *gate)
	}
	if gate != nil {
		return st, false, nil // waiting on the human
	}

	if len(repairs) > 0 {
		if done, _ := allAnswered(repairs); !done {
			return st, false, nil
		}
		// A round finished; re-take the evidence against what it produced
		// and close the generation. Each repair round is its own
		// generation: it changed the integrated sources, so what comes
		// next rests on a different world — and without closing it, the
		// round's answered actions would read as the NEXT round's, and the
		// engine would shuttle between repair and checks forever.
		if err := e.rebaselineSources(ctx, &st); err != nil {
			return st, false, err
		}
		st.Generation++
		next, err := Advance(st.Step, EventRepairDone)
		if err != nil {
			return st, false, err
		}
		st, err = e.moveTo(ctx, st, next)
		return st, err == nil, err
	}

	// No round issued yet at this generation. If a previous round exists,
	// it failed — count it, and stop if the limit is reached.
	previous, err := e.previousRepairRounds(ctx, st)
	if err != nil {
		return st, false, err
	}
	if previous > 0 {
		rounds, limit, err := e.findings.FailRepair(ctx, st.WorkID)
		if err != nil {
			return st, false, err
		}
		if limit {
			return e.issueRepairLimitDecision(ctx, st, rounds)
		}
	}
	return e.issueRepairRound(ctx, st)
}
