package assignment

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
)

// Group is one released parallel group: the actions a host may run now and
// the state they put the workflow in. A blocked group carries exactly one
// decision; a ready group carries one or more assignments.
type Group struct {
	ID      identity.ParallelGroupID
	State   protocol.NextState
	Actions []protocol.Action
}

// Response renders the group as the `homonto next` payload.
func (g Group) Response() protocol.NextResponse {
	return protocol.NextResponse{
		ProtocolVersion: protocol.CurrentVersion,
		State:           g.State,
		Actions:         append([]protocol.Action(nil), g.Actions...),
	}
}

// CompleteResponse is the `homonto next` payload for a work with nothing
// left to do: an explicitly empty action list, never an omitted key.
func CompleteResponse() protocol.NextResponse {
	return protocol.NextResponse{
		ProtocolVersion: protocol.CurrentVersion,
		State:           protocol.NextComplete,
		Actions:         []protocol.Action{},
	}
}

// ReadyGroup returns the actions a host may run now for one work, issuing
// them as a parallel group if none is outstanding.
//
// The group is MAXIMAL: every pending action whose dependencies are all
// answered is released together, so independent assignments run
// concurrently. A ready human decision is the exception — it is released
// alone in a blocked group, because a decision gates everything behind it
// and the protocol's blocked state carries exactly one action.
//
// Re-asking while a group is outstanding returns that same group: action
// ids are stable and freshness tokens are derived, so `homonto next` is
// idempotent for as long as the group is unanswered. ok is false when the
// work has no unanswered action left.
func (s *Store) ReadyGroup(ctx context.Context, workID identity.WorkID) (Group, bool, error) {
	var (
		group Group
		found bool
	)
	err := s.db.Update(ctx, func(tx *store.Tx) error {
		actions, err := loadActions(ctx, tx, workID)
		if err != nil {
			return err
		}
		// An outstanding group is returned as it stands: nothing is
		// re-issued while a host still owes an answer.
		if out := outstanding(actions); len(out) > 0 {
			group = s.materialize(out)
			found = true
			return nil
		}
		ready, pending := readyActions(actions)
		if len(ready) == 0 {
			if pending > 0 {
				return fmt.Errorf("assignment: work %s has %d pending action(s): %w",
					workID, pending, ErrUnsatisfiable)
			}
			return nil
		}
		// A decision or a host edit goes out ALONE even when assignments
		// are also ready: both are the human's own turn, and neither
		// should be raced against agent work happening underneath it.
		if solo, ok := firstSolo(ready); ok {
			ready = []Action{solo}
		}
		groupID, err := identity.NewParallelGroupID()
		if err != nil {
			return err
		}
		now := formatTime(s.now().UTC())
		for i := range ready {
			if _, err := tx.ExecContext(ctx, `
				UPDATE actions SET state = ?, group_id = ?, updated_at = ?
				 WHERE id = ? AND state = ?`,
				string(StateIssued), string(groupID), now, string(ready[i].ID), string(StatePending)); err != nil {
				return fmt.Errorf("assignment: issue action %s: %w", ready[i].ID, err)
			}
			ready[i].State = StateIssued
			ready[i].GroupID = groupID
		}
		group = s.materialize(ready)
		found = true
		return nil
	})
	if err != nil {
		return Group{}, false, err
	}
	return group, found, nil
}

// materialize renders issued actions as a wire group: the derived
// freshness token and the group id are filled in here, never stored.
func (s *Store) materialize(actions []Action) Group {
	group := Group{State: protocol.NextReady}
	if len(actions) > 0 {
		group.ID = actions[0].GroupID
	}
	for _, act := range actions {
		wire := act.Spec
		wire.ID = act.ID
		wire.FreshnessToken = s.Token(act.ID)
		wire.ParallelGroupID = string(act.GroupID)
		wire.Dependencies = act.Dependencies
		if act.Kind == protocol.KindDecision {
			group.State = protocol.NextBlocked
		}
		group.Actions = append(group.Actions, wire)
	}
	return group
}

// outstanding returns the issued, still-unanswered actions.
func outstanding(actions []Action) []Action {
	var out []Action
	for _, act := range actions {
		if act.State == StateIssued {
			out = append(out, act)
		}
	}
	return out
}

// readyActions returns the pending actions whose dependencies are all
// answered, plus the number of pending actions that are not ready. A
// dependency that was invalidated can never be answered, so anything
// waiting on it is not ready and never will be — ReadyGroup reports that
// as unsatisfiable rather than silently reporting the work complete.
func readyActions(actions []Action) (ready []Action, pending int) {
	state := make(map[identity.ActionID]State, len(actions))
	for _, act := range actions {
		state[act.ID] = act.State
	}
	for _, act := range actions {
		if act.State != StatePending {
			continue
		}
		pending++
		satisfied := true
		for _, dep := range act.Dependencies {
			if state[dep] != StateSubmitted {
				satisfied = false
				break
			}
		}
		if satisfied {
			ready = append(ready, act)
		}
	}
	pending -= len(ready)
	return ready, pending
}

// firstSolo returns the oldest ready action that must be released alone —
// a human decision or a host edit. Actions arrive in creation order, so
// the first one found is the oldest.
func firstSolo(ready []Action) (Action, bool) {
	for _, act := range ready {
		switch act.Kind {
		case protocol.KindDecision, protocol.KindEdit:
			return act, true
		}
	}
	return Action{}, false
}
