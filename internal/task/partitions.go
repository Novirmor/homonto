package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/noviopenworks/homonto/internal/artifact"
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
			INSERT INTO task_partitions (action_id, work_id, step, partition)
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
			`SELECT partition FROM task_partitions WHERE action_id = ?`, string(actionID))
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

// issuePartitions creates one implementer assignment per partition and
// records the split.
func (e *Engine) issuePartitions(ctx context.Context, st State, step Step, partitions []Partition, reason func(Partition) string) error {
	for _, p := range partitions {
		spec, err := e.implementer(st, step, p, reason(p))
		if err != nil {
			return err
		}
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

// completedPartitions returns the partitions of the implementation round
// that has just finished — the material the integration units combine.
func (e *Engine) completedPartitions(ctx context.Context, st State) ([]Partition, error) {
	return e.partitionsForStep(ctx, st, StepDoImplement)
}
