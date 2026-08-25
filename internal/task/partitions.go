package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
)

// savePartition records how one assignment's work was split. The mapping
// cannot be recovered later: once Homonto checks an item off, the
// "unchecked items" that produced the partition no longer exist.
func (e *Engine) savePartition(ctx context.Context, workID identity.WorkID, step Step, actionID identity.ActionID, p Partition) error {
	encoded, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("task: encode partition %q: %w", p.Label, err)
	}
	return e.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_units (action_id, work_id, step, partition)
			VALUES (?, ?, ?, ?)`,
			string(actionID), string(workID), string(step), string(encoded)); err != nil {
			return fmt.Errorf("task: record partition %q: %w", p.Label, err)
		}
		return nil
	})
}

// partitionOf returns the partition one assignment was issued for.
func (e *Engine) partitionOf(ctx context.Context, actionID identity.ActionID) (Partition, bool, error) {
	var encoded string
	found := false
	err := e.db.View(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT partition FROM work_units WHERE action_id = ?`, string(actionID))
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			if err := rows.Scan(&encoded); err != nil {
				return err
			}
			found = true
		}
		return rows.Err()
	})
	if err != nil {
		return Partition{}, false, fmt.Errorf("task: read partition of %s: %w", actionID, err)
	}
	if !found {
		return Partition{}, false, nil
	}
	var p Partition
	if err := json.Unmarshal([]byte(encoded), &p); err != nil {
		return Partition{}, false, fmt.Errorf("task: decode partition of %s: %w", actionID, err)
	}
	return p, true, nil
}

// partitionsForStep returns the partitions issued for one step at the
// current generation, in issue order.
func (e *Engine) partitionsForStep(ctx context.Context, st State, step Step) ([]Partition, error) {
	actions, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return nil, err
	}
	var out []Partition
	for _, act := range actions {
		if act.Kind != protocol.KindAssignment {
			continue
		}
		p, found, err := e.partitionOf(ctx, act.ID)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, p)
		}
	}
	return out, nil
}

// issuePartitions creates one implementer assignment per unit. The action
// id is minted first, the isolation area is created against it, and only
// then is the assignment persisted — so an assignment never exists without
// the area it was issued for, and a crash leaves at most an unused area.
func (e *Engine) issuePartitions(ctx context.Context, st State, step Step, units []Partition, reason func(Partition) string) error {
	for _, unit := range units {
		actionID, err := identity.NewActionID()
		if err != nil {
			return err
		}
		p := unit
		if p.Root == "" {
			if p, err = e.env.Isolate(ctx, st.WorkID, actionID, unit); err != nil {
				return err
			}
		}
		spec, err := e.implementer(st, step, p, reason(p))
		if err != nil {
			return err
		}
		spec.ActionID = actionID
		act, err := e.assignments.Create(ctx, spec)
		if err != nil {
			return err
		}
		if err := e.savePartition(ctx, st.WorkID, step, act.ID, p); err != nil {
			return err
		}
	}
	return nil
}

// openItems returns the checklist items that are not yet checked off.
func (e *Engine) openItems(ctx context.Context, st State) ([]artifact.Item, error) {
	items, err := e.artifacts.Checklist(ctx, st.ref())
	if err != nil {
		return nil, err
	}
	var open []artifact.Item
	for _, it := range items {
		if !it.Done {
			open = append(open, it)
		}
	}
	return open, nil
}

// checkOffPartition checks the items an accepted assignment addressed.
// Only Homonto checks items, and only for assignments it accepted — which
// is exactly what this is: the accept path, after the final diff gate.
func (e *Engine) checkOffPartition(ctx context.Context, st State, actionID identity.ActionID) error {
	p, found, err := e.partitionOf(ctx, actionID)
	if err != nil {
		return err
	}
	if !found || len(p.Items) == 0 {
		return nil
	}
	_, err = e.artifacts.CheckOff(ctx, st.ref(), artifact.PhaseDo, p.Items)
	return err
}

// completedResults returns the material integration must combine: for
// each work unit, the newest finished implementer assignment.
//
// "Newest per unit" rather than "newest generation" is the whole point. A
// repair round is issued for the same units as the round it repairs, and
// it carries the SAME generation — the generation only closes once the
// repair finishes. So selecting by generation returns the failed original
// alongside its repair, and integration then tries to apply both: the
// second cherry-pick lands on a tree that already has the first, and git
// stops with "the previous cherry-pick is now empty". The failed attempt
// was what reached the integration branch, and the record said the checks
// passed.
//
// A repair therefore SUPERSEDES the attempt it repairs. Between two
// assignments for one unit the later one wins, and a repair beats an
// implement outright — a repair is by definition issued after the attempt
// it replaces.
func (e *Engine) completedResults(ctx context.Context, st State) ([]Result, error) {
	actions, err := e.assignments.Actions(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	// Insertion order is preserved so integration applies materials in the
	// order they were produced rather than in map order.
	var order []string
	newest := map[string]assignment.Action{}
	units := map[string]Partition{}
	for _, act := range actions {
		if act.Kind != protocol.KindAssignment || act.Role != protocol.RoleImplementer {
			continue
		}
		if act.Step != string(StepDoImplement) && act.Step != string(StepDoRepair) {
			continue
		}
		if act.State != assignment.StateSubmitted {
			continue
		}
		p, found, err := e.partitionOf(ctx, act.ID)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		key := string(p.Member.ID) + "\x00" + p.Label
		prior, seen := newest[key]
		if !seen {
			order = append(order, key)
		}
		if !seen || supersedes(act, prior) {
			newest[key] = act
			units[key] = p
		}
	}
	var out []Result
	for _, key := range order {
		result, ok, err := e.resultOf(ctx, newest[key])
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, result)
		}
	}
	return out, nil
}

// supersedes reports whether a replaces b as one unit's finished work.
func supersedes(a, b assignment.Action) bool {
	if a.Generation != b.Generation {
		return a.Generation > b.Generation
	}
	if repairing(a) != repairing(b) {
		return repairing(a)
	}
	return a.CreatedAt.After(b.CreatedAt)
}

// repairing reports whether an assignment is a repair rather than a first
// attempt.
func repairing(act assignment.Action) bool { return act.Step == string(StepDoRepair) }

// resultOf builds one finished unit's Result from its recorded partition
// and its report.
func (e *Engine) resultOf(ctx context.Context, act assignment.Action) (Result, bool, error) {
	p, found, err := e.partitionOf(ctx, act.ID)
	if err != nil || !found {
		return Result{}, false, err
	}
	sub, found, err := e.assignments.Report(ctx, act.ID)
	if err != nil || !found {
		return Result{}, false, err
	}
	wire := act.Spec
	wire.FreshnessToken = e.assignments.Token(act.ID)
	report, err := protocol.DecodeReport(wire, sub.Report)
	if err != nil {
		return Result{}, false, err
	}
	impl, ok := report.(*protocol.ImplementerReport)
	if !ok {
		return Result{}, false, nil
	}
	return Result{ActionID: act.ID, Partition: p, Material: impl.Material}, true, nil
}
