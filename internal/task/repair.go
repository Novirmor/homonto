// The repair loop: bounded rounds, their reason, and the human limit decision.
package task

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

// previousRepairRounds counts repair assignments issued for this task in
// any generation, including superseded ones. A repair that was
// invalidated still happened.
func (e *Engine) previousRepairRounds(ctx context.Context, st State) (int, error) {
	all, err := e.assignments.Actions(ctx, st.WorkID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, act := range all {
		if act.Step == string(StepDoRepair) && act.Kind == protocol.KindAssignment {
			n++
		}
	}
	return n, nil
}

// issueRepairRound partitions the outstanding problems into repair
// assignments.
func (e *Engine) issueRepairRound(ctx context.Context, st State) (State, bool, error) {
	// A repair may need to touch anything the task covers, not only what
	// is still unchecked: the items an earlier round checked off are
	// exactly the ones a failing check is most likely to be about. So the
	// whole checklist goes to the environment, which hands back fresh
	// isolation areas for this round.
	items, err := e.artifacts.Checklist(ctx, st.ref())
	if err != nil {
		return st, false, err
	}
	partitions, err := e.env.Partition(ctx, st.WorkID, items)
	if err != nil {
		return st, false, err
	}
	if len(partitions) == 0 {
		return st, false, fmt.Errorf(
			"task: %s needs repair but the environment offered no unit to repair in", st.WorkID)
	}
	reason, err := e.repairReason(ctx, st)
	if err != nil {
		return st, false, err
	}
	for i := range partitions {
		partitions[i].Prompt = reason + "\n\n" + partitions[i].Prompt
		// A repair round is not item completion: it addresses failed
		// checks and open findings, and accepting it must not check off
		// items nothing implemented.
		partitions[i].Items = nil
	}
	return st, true, e.issuePartitions(ctx, st, StepDoRepair, partitions,
		func(p Partition) string { return "repair " + p.Label })
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
func (e *Engine) issueRepairLimitDecision(ctx context.Context, st State, rounds int) (State, bool, error) {
	control, err := e.env.Control(ctx)
	if err != nil {
		return st, false, err
	}
	spec, err := e.decisionSpec(st, StepDoRepair, control, repairLimitSchema(rounds),
		"the repair limit was reached",
		fmt.Sprintf("%d consecutive repair rounds have failed.", rounds))
	if err != nil {
		return st, false, err
	}
	if _, err := e.assignments.Create(ctx, spec); err != nil {
		return st, false, err
	}
	return st, true, nil
}

// applyRepairDecision carries out the human's answer to the repair limit.
func (e *Engine) applyRepairDecision(ctx context.Context, st State, gate assignment.Action) (State, bool, error) {
	sub, found, err := e.decisionChoice(ctx, gate.ID)
	if err != nil {
		return st, false, err
	}
	if !found {
		return st, false, fmt.Errorf("task: decision %s is answered but carries no recorded choice", gate.ID)
	}
	switch sub.Choice {
	case "abandon":
		next, err := Advance(st.Step, EventAbandon)
		if err != nil {
			return st, false, err
		}
		st, err = e.moveTo(ctx, st, next)
		return st, err == nil, err
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
		next, err := Advance(st.Step, EventRepairDone)
		if err != nil {
			return st, false, err
		}
		st, err = e.moveTo(ctx, st, next)
		return st, err == nil, err
	case "continue":
		if err := e.findings.ResetRepairs(ctx, st.WorkID); err != nil {
			return st, false, err
		}
		next, err := Advance(st.Step, EventRepairContinued)
		if err != nil {
			return st, false, err
		}
		// Staying in repair with the counter reset issues a fresh round on
		// the next pass; the answered gate belongs to a spent generation,
		// so bump it to keep the new round separate from the old one.
		st.Generation++
		st, err = e.moveTo(ctx, st, next)
		return st, err == nil, err
	}
	return st, false, fmt.Errorf("task: decision %s carries unrecognized choice %q", gate.ID, sub.Choice)
}

// acceptBlockers records every open blocking finding as a documented
// deviation authorized by one decision.
func (e *Engine) acceptBlockers(ctx context.Context, st State, decisionID identity.ActionID, rationale string) error {
	blockers, err := e.findings.Blockers(ctx, st.WorkID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(rationale) == "" {
		return fmt.Errorf("task: accepting findings needs a rationale: %w", finding.ErrRationaleRequired)
	}
	for _, f := range blockers {
		if err := e.findings.Resolve(ctx, finding.Resolution{
			WorkID:     st.WorkID,
			ExternalID: f.ExternalID,
			Kind:       finding.KindAccepted,
			Rationale:  rationale,
			DecisionID: decisionID,
		}); err != nil {
			return err
		}
	}
	return nil
}
