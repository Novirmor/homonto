package change

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// maxAdvancesPerNext bounds how many steps one Next call may traverse. The
// workflows are finite and each pass either issues actions or advances, so
// the bound is only a backstop against a step that claims progress it did
// not make — better a loud refusal than a spin.
const maxAdvancesPerNext = 48

// Next returns the actions a host may execute now for a Change. It
// reconciles first, because the recorded step is only worth acting on once
// its baseline has been checked against the world.
func (e *Engine) Next(ctx context.Context, id identity.WorkID) (protocol.NextResponse, error) {
	st, _, err := e.Reconcile(ctx, id)
	if err != nil {
		return protocol.NextResponse{}, err
	}
	for i := 0; i < maxAdvancesPerNext; i++ {
		if terminalStep(st.Path, st.Step) {
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
				"change: %s is stuck at %s with nothing issued and nothing to advance", id, st.Step)
		}
		st = next
	}
	return protocol.NextResponse{}, fmt.Errorf(
		"change: %s traversed %d steps in one call without reaching a host action", id, maxAdvancesPerNext)
}

// moveTo persists a step change.
func (e *Engine) moveTo(ctx context.Context, st State, next Step) (State, error) {
	st.Step = string(next)
	st.UpdatedAt = e.now().UTC()
	if err := e.saveState(ctx, st); err != nil {
		return State{}, err
	}
	return st, nil
}

// advance fires an event and persists the resulting step.
func (e *Engine) advance(ctx context.Context, st State, step Step, event Event) (State, bool, error) {
	next, err := Advance(st.Path, step, event)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}
