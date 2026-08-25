package change

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

// saveUnit records how one assignment's work was split. The mapping cannot
// be recovered later: once Homonto checks an item off, the "unchecked
// items" that produced the unit no longer exist.
func (e *Engine) saveUnit(ctx context.Context, workID identity.WorkID, step Step, actionID identity.ActionID, u Unit) error {
	encoded, err := json.Marshal(u)
	if err != nil {
		return fmt.Errorf("change: encode unit %q: %w", u.Label, err)
	}
	return e.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_units (action_id, work_id, step, partition)
			VALUES (?, ?, ?, ?)`,
			string(actionID), string(workID), string(step), string(encoded)); err != nil {
			return fmt.Errorf("change: record unit %q: %w", u.Label, err)
		}
		return nil
	})
}

// unitOf returns the unit one assignment was issued for.
func (e *Engine) unitOf(ctx context.Context, actionID identity.ActionID) (Unit, bool, error) {
	var (
		encoded string
		found   bool
	)
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
		return Unit{}, false, fmt.Errorf("change: read unit of %s: %w", actionID, err)
	}
	if !found {
		return Unit{}, false, nil
	}
	var u Unit
	if err := json.Unmarshal([]byte(encoded), &u); err != nil {
		return Unit{}, false, fmt.Errorf("change: decode unit of %s: %w", actionID, err)
	}
	return u, true, nil
}

// issueUnits creates one implementer assignment per unit. The action id is
// minted first, the isolation area is created against it, and only then is
// the assignment persisted — so an assignment never exists without the
// area it was issued for, and a crash leaves at most an unused area.
func (e *Engine) issueUnits(ctx context.Context, st State, step Step, units []Unit, reason func(Unit) string) error {
	for _, unit := range units {
		actionID, err := identity.NewActionID()
		if err != nil {
			return err
		}
		u := unit
		if u.Root == "" {
			if u, err = e.env.Isolate(ctx, st.WorkID, actionID, unit); err != nil {
				return err
			}
		}
		spec, err := e.implementer(st, step, u, reason(u))
		if err != nil {
			return err
		}
		spec.ActionID = actionID
		act, err := e.assignments.Create(ctx, spec)
		if err != nil {
			return err
		}
		if err := e.saveUnit(ctx, st.WorkID, step, act.ID, u); err != nil {
			return err
		}
	}
	return nil
}

// completedResults returns the finished units of the MOST RECENT
// implementation round — the first one, or the newest repair — because
// that is the material integration must combine. It deliberately ignores
// the current generation: a repair round closes its own generation when it
// finishes, so the material to integrate always sits one behind.
func (e *Engine) completedResults(ctx context.Context, st State, steps ...Step) ([]Result, error) {
	actions, err := e.assignments.Actions(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, s := range steps {
		wanted[string(s)] = true
	}
	newest := int64(-1)
	for _, act := range actions {
		if act.Role != protocol.RoleImplementer || act.State != assignment.StateSubmitted {
			continue
		}
		if !wanted[act.Step] {
			continue
		}
		if act.Generation > newest {
			newest = act.Generation
		}
	}
	if newest < 0 {
		return nil, nil
	}
	var out []Result
	for _, act := range actions {
		if act.Generation != newest || act.Role != protocol.RoleImplementer ||
			act.State != assignment.StateSubmitted || !wanted[act.Step] {
			continue
		}
		result, ok, err := e.resultOf(ctx, act)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, result)
		}
	}
	return out, nil
}

// resultOf builds one finished unit's Result from its recorded unit and
// its report.
func (e *Engine) resultOf(ctx context.Context, act assignment.Action) (Result, bool, error) {
	u, found, err := e.unitOf(ctx, act.ID)
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
	return Result{ActionID: act.ID, Unit: u, Material: impl.Material}, true, nil
}

// openItems returns the change's checklist items that are not yet checked
// off.
func (e *Engine) openItems(ctx context.Context, st State) ([]artifact.Item, error) {
	items, err := e.checklist(ctx, st)
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

// checklist reads the change's tasks.md items.
func (e *Engine) checklist(ctx context.Context, st State) ([]artifact.Item, error) {
	path, err := st.DocumentPath(artifact.KindTasks)
	if err != nil {
		return nil, err
	}
	return e.artifacts.Checklist(ctx, artifact.Ref{
		WorkID: st.WorkID, Kind: artifact.KindTasks, Path: path,
	})
}

// checkOffUnit checks the items an accepted assignment addressed. Only
// Homonto checks items, and only for assignments it accepted — which is
// exactly here, after the final diff gate has passed.
func (e *Engine) checkOffUnit(ctx context.Context, st State, actionID identity.ActionID) error {
	u, found, err := e.unitOf(ctx, actionID)
	if err != nil {
		return err
	}
	if !found || len(u.Items) == 0 {
		return nil
	}
	path, err := st.DocumentPath(artifact.KindTasks)
	if err != nil {
		return err
	}
	_, err = e.artifacts.CheckOff(ctx, artifact.Ref{
		WorkID: st.WorkID, Kind: artifact.KindTasks, Path: path,
	}, artifact.PhaseBuild, u.Items)
	return err
}

// actionsForStep returns the actions issued for one step at the current
// generation.
func (e *Engine) actionsForStep(ctx context.Context, st State, step Step) ([]assignment.Action, error) {
	all, err := e.assignments.Actions(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	var out []assignment.Action
	for _, act := range all {
		if act.Step == string(step) && act.Generation == st.Generation &&
			act.State != assignment.StateInvalidated {
			out = append(out, act)
		}
	}
	return out, nil
}

// allAnswered reports whether every action of a set has been answered, and
// whether the set is non-empty.
func allAnswered(actions []assignment.Action) (answered bool, any bool) {
	if len(actions) == 0 {
		return false, false
	}
	for _, act := range actions {
		if act.State != assignment.StateSubmitted {
			return false, true
		}
	}
	return true, true
}
