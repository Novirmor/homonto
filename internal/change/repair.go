// The repair loop: bounded rounds, their reason, and the human limit decision.
package change

import (
	"context"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/finding"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// rebaselineSources records the integrated source fingerprints the
// evidence from here on is taken against.
func (e *Engine) rebaselineSources(ctx context.Context, st *State) error {
	sources, err := e.env.SourceFingerprints(ctx, st.WorkID)
	if err != nil {
		return err
	}
	st.Baseline.Sources = sortedDigests(sources)
	return nil
}

// rebaselineDocuments records the documents the change now rests on.
func (e *Engine) rebaselineDocuments(ctx context.Context, st *State) error {
	docs, err := e.documentDigests(ctx, *st)
	if err != nil {
		return err
	}
	st.Baseline.Documents = docs
	return nil
}

// stepRepair runs one bounded repair round. Entering repair again after a
// previous round means that round failed, and three failed rounds hand the
// choice to a human rather than letting Homonto try a fourth time.
func (e *Engine) stepRepair(ctx context.Context, st State, step Step) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	gate, repairs := splitRepairActions(issued)

	if gate != nil && gate.State == assignment.StateSubmitted {
		return e.applyRepairDecision(ctx, st, step, *gate)
	}
	if gate != nil {
		return st, false, nil // waiting on the human
	}
	if len(repairs) > 0 {
		if answered, _ := allAnswered(repairs); !answered {
			return st, false, nil
		}
		if err := e.rebaselineSources(ctx, &st); err != nil {
			return st, false, err
		}
		// Each repair round is its own generation: it changed the
		// integrated sources, so what comes next rests on a different
		// world — and without closing it, this round's answered actions
		// would read as the next round's and the engine would shuttle
		// between repair and integration forever.
		st.Generation++
		return e.advance(ctx, st, step, EventRepairDone)
	}

	previous, err := e.previousRepairRounds(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if previous > 0 {
		rounds, limit, err := e.findings.FailRepair(ctx, st.WorkID)
		if err != nil {
			return st, false, err
		}
		if limit {
			return e.issueRepairLimitDecision(ctx, st, step, rounds)
		}
	}
	return e.issueRepairRound(ctx, st, step)
}

// splitRepairActions separates the repair-limit decision from the repair
// assignments of the current generation.
func splitRepairActions(actions []assignment.Action) (*assignment.Action, []assignment.Action) {
	var (
		gate    *assignment.Action
		repairs []assignment.Action
	)
	for i := range actions {
		if actions[i].Kind == protocol.KindDecision {
			gate = &actions[i]
			continue
		}
		repairs = append(repairs, actions[i])
	}
	return gate, repairs
}

// previousRepairRounds counts repair assignments issued for this change in
// any generation. A repair that was invalidated still happened.
func (e *Engine) previousRepairRounds(ctx context.Context, st State, step Step) (int, error) {
	all, err := e.assignments.Actions(ctx, st.WorkID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, act := range all {
		if act.Step == string(step) && act.Kind == protocol.KindAssignment {
			n++
		}
	}
	return n, nil
}

// issueRepairRound partitions the outstanding problems into repair
// assignments.
func (e *Engine) issueRepairRound(ctx context.Context, st State, step Step) (State, bool, error) {
	// A repair may need to touch anything the change covers, not only what
	// is still unchecked: the items an earlier round checked off are
	// exactly the ones a failing check is most likely to be about.
	items, err := e.checklist(ctx, st)
	if err != nil {
		return st, false, err
	}
	units, err := e.env.Partition(ctx, st.WorkID, items)
	if err != nil {
		return st, false, err
	}
	if len(units) == 0 {
		return st, false, fmt.Errorf(
			"change: %s needs repair but the environment offered no unit to repair in", st.Name)
	}
	reason, err := e.repairReason(ctx, st)
	if err != nil {
		return st, false, err
	}
	for i := range units {
		units[i].Prompt = reason + "\n\n" + units[i].Prompt
		// A repair round is not item completion: it addresses failed
		// checks and open findings, and accepting it must not check off
		// items nothing implemented.
		units[i].Items = nil
	}
	return st, true, e.issueUnits(ctx, st, step, units,
		func(u Unit) string { return "repair " + u.Label })
}

// repairReason states what the repair round must address: the failed
// checks and the open blocking findings, named.
func (e *Engine) repairReason(ctx context.Context, st State) (string, error) {
	var parts []string
	set, err := e.evidence.Latest(ctx, st.WorkID)
	if err == nil {
		for _, r := range set.Failures() {
			parts = append(parts, fmt.Sprintf("check %q %s (exit %d)", r.Spec.Name, r.Outcome, r.ExitCode))
		}
	}
	blockers, err := e.findings.Blockers(ctx, st.WorkID)
	if err != nil {
		return "", err
	}
	for _, f := range blockers {
		parts = append(parts, fmt.Sprintf("%s finding %s: %s", f.Severity, f.ExternalID, f.Summary))
	}
	if len(parts) == 0 {
		return "Repair the outstanding problems.", nil
	}
	return "Repair the following, and nothing else:\n- " + strings.Join(parts, "\n- "), nil
}

// issueRepairLimitDecision puts the choice to the human after three failed
// repair rounds.
func (e *Engine) issueRepairLimitDecision(ctx context.Context, st State, step Step, rounds int) (State, bool, error) {
	control, err := e.env.Control(ctx)
	if err != nil {
		return st, false, err
	}
	spec, err := e.decisionSpec(st, step, control, repairLimitSchema(rounds),
		"the repair limit was reached")
	if err != nil {
		return st, false, err
	}
	if _, err := e.assignments.Create(ctx, spec); err != nil {
		return st, false, err
	}
	return st, true, nil
}

// applyRepairDecision carries out the human's answer to the repair limit.
func (e *Engine) applyRepairDecision(ctx context.Context, st State, step Step, gate assignment.Action) (State, bool, error) {
	sub, found, err := e.assignments.Decision(ctx, gate.ID)
	if err != nil {
		return st, false, err
	}
	if !found {
		return st, false, fmt.Errorf("change: decision %s is answered but carries no choice", gate.ID)
	}
	switch sub.Choice {
	case "abandon":
		return e.advance(ctx, st, step, EventAbandon)
	case "accept":
		// Accepting resolves the open BLOCKING findings as documented
		// deviations. It does not make a failing check pass: the checks
		// run again, and if they still fail Homonto will say so again.
		if err := e.acceptBlockers(ctx, st, gate.ID, sub.Rationale); err != nil {
			return st, false, err
		}
		if err := e.findings.ResetRepairs(ctx, st.WorkID); err != nil {
			return st, false, err
		}
		st.Generation++
		return e.advance(ctx, st, step, EventRepairDone)
	case "continue":
		if err := e.findings.ResetRepairs(ctx, st.WorkID); err != nil {
			return st, false, err
		}
		st.Generation++
		return e.advance(ctx, st, step, EventRepairContinued)
	}
	return st, false, fmt.Errorf("change: decision %s carries unrecognized choice %q", gate.ID, sub.Choice)
}

// acceptBlockers records every open blocking finding as a documented
// deviation authorized by one decision.
func (e *Engine) acceptBlockers(ctx context.Context, st State, decisionID identity.ActionID, rationale string) error {
	if strings.TrimSpace(rationale) == "" {
		return fmt.Errorf("change: accepting findings needs a rationale: %w", finding.ErrRationaleRequired)
	}
	blockers, err := e.findings.Blockers(ctx, st.WorkID)
	if err != nil {
		return err
	}
	for _, f := range blockers {
		if err := e.findings.Resolve(ctx, finding.Resolution{
			WorkID: st.WorkID, ExternalID: f.ExternalID, Kind: finding.KindAccepted,
			Rationale: rationale, DecisionID: decisionID,
		}); err != nil {
			return err
		}
	}
	return nil
}
