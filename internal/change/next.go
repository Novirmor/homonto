package change

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/finding"
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

// runStep issues the current step's work if it has none, or completes the
// step if its work is answered.
func (e *Engine) runStep(ctx context.Context, st State) (State, bool, error) {
	step := Step(st.Step)
	switch step {
	// --- Full: Open ---
	case StepOpenExplore:
		return e.stepExplore(ctx, st, step, EventExplorersDone,
			"establish the current behavior, constraints, and affected repositories")
	case StepOpenChallenge:
		return e.stepChallenge(ctx, st, step, EventChallengeDone,
			"attack the hidden assumptions in the request before anything is proposed")
	case StepOpenDraft:
		return e.stepDraft(ctx, st, step, EventProposalDrafted,
			[]artifact.Kind{artifact.KindProposal},
			"write the proposal from the explorer and skeptic reports")
	case StepOpenApprove:
		return e.stepApprove(ctx, st, step, decision.KindApproveScope, "scope",
			artifact.KindProposal, EventScopeApproved, EventScopeRejected)

	// --- Full: Design ---
	case StepDesignDraft:
		return e.stepDraft(ctx, st, step, EventDesignDrafted,
			[]artifact.Kind{artifact.KindDesign, artifact.KindTasks},
			"compare the viable approaches, write the design, the task list, the "+
				"acceptance criteria, and the ADR candidates")
	case StepDesignChallenge:
		return e.stepChallenge(ctx, st, step, EventDesignChallenged,
			"attack the design: the approach not taken, the cases not covered, the risks not named")
	case StepDesignApprove:
		return e.stepApprove(ctx, st, step, decision.KindApproveDesign, "design",
			artifact.KindDesign, EventDesignApproved, EventDesignRejected)

	// --- Full: Build ---
	case StepBuildPlan:
		return e.stepDraft(ctx, st, step, EventPlanDrafted,
			[]artifact.Kind{artifact.KindPlan},
			"write the detailed implementation plan from the approved design")
	case StepBuildImplement:
		return e.stepImplement(ctx, st, step, EventImplementersDone)
	case StepBuildIntegrate:
		return e.stepIntegrate(ctx, st, step, EventIntegrated,
			StepBuildImplement, StepBuildRepair)

	// --- Full: Verify ---
	case StepVerifyChecks:
		return e.stepChecks(ctx, st, step)
	case StepVerifyReview:
		return e.stepReview(ctx, st, step)
	case StepVerifyRecord:
		return e.stepVerificationRecord(ctx, st, step)
	case StepBuildRepair:
		return e.stepRepair(ctx, st, step)

	// --- Full: Close ---
	case StepCloseADR:
		return e.stepCloseADR(ctx, st, step)
	case StepCloseFinalize:
		return e.stepFinalize(ctx, st, step)

	// --- Presets ---
	case StepPresetOpenDraft:
		return e.stepPresetDraft(ctx, st, step)
	case StepPresetReproduce:
		return e.stepReproduce(ctx, st, step)
	case StepPresetScope:
		return e.stepPresetScope(ctx, st, step)
	case StepPresetImplement:
		return e.stepImplement(ctx, st, step, EventPresetImplemented)
	case StepPresetIntegrate:
		return e.stepIntegrate(ctx, st, step, EventPresetIntegrated,
			StepPresetImplement, StepPresetRepair)
	case StepPresetChecks:
		return e.stepChecks(ctx, st, step)
	case StepPresetReview:
		return e.stepReview(ctx, st, step)
	case StepPresetRepair:
		return e.stepRepair(ctx, st, step)
	case StepPresetFinalize:
		return e.stepFinalize(ctx, st, step)
	}
	return st, false, fmt.Errorf("change: step %q has no handler", st.Step)
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

// stepExplore issues one explorer per confirmed member and advances when
// every one has reported. The whole workspace is surveyed, not just the
// control repository.
func (e *Engine) stepExplore(ctx context.Context, st State, step Step, done Event, what string) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		members, err := e.env.Members(ctx)
		if err != nil {
			return st, false, err
		}
		if len(members) == 0 {
			return st, false, fmt.Errorf("change: the workspace has no confirmed members to explore")
		}
		for _, m := range members {
			spec, err := e.readOnly(st, step, protocol.RoleExplorer, m,
				fmt.Sprintf("survey %s: %s", m.Path, what),
				"Read "+m.Path+" and "+what+" for the change described in "+st.inputPath()+
					". Do not write anything.")
			if err != nil {
				return st, false, err
			}
			if _, err := e.assignments.Create(ctx, spec); err != nil {
				return st, false, err
			}
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	return e.advance(ctx, st, step, done)
}

// stepChallenge issues the skeptic that attacks the current state of the
// change before it is committed to.
func (e *Engine) stepChallenge(ctx context.Context, st State, step Step, done Event, what string) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		spec, err := e.readOnly(st, step, protocol.RoleSkeptic, control, what,
			what+", for the change described in "+st.inputPath()+". "+
				"Report every finding with a severity. Do not write anything.")
		if err != nil {
			return st, false, err
		}
		if _, err := e.assignments.Create(ctx, spec); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	return e.advance(ctx, st, step, done)
}

// stepDraft hands the host an edit grant on each document the step
// produces, and advances when every one is accepted. The baseline is
// re-taken at that point: what the host just wrote is what the change now
// rests on, and it must not immediately invalidate the step that produced
// it.
func (e *Engine) stepDraft(ctx context.Context, st State, step Step, done Event, kinds []artifact.Kind, what string) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		for _, kind := range kinds {
			if err := e.issueDraft(ctx, st, step, control, kind, what); err != nil {
				return st, false, err
			}
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	if err := e.rebaselineDocuments(ctx, &st); err != nil {
		return st, false, err
	}
	return e.advance(ctx, st, step, done)
}

// issueDraft creates one document if it does not exist yet and opens an
// edit grant on it.
func (e *Engine) issueDraft(ctx context.Context, st State, step Step, control Member, kind artifact.Kind, what string) error {
	path, err := st.DocumentPath(kind)
	if err != nil {
		return err
	}
	ref := artifact.Ref{WorkID: st.WorkID, Kind: kind, Path: path}
	if _, err := e.artifacts.Read(ctx, ref); errors.Is(err, artifact.ErrArtifactMissing) {
		if _, err := e.artifacts.Create(ctx, path, artifact.Metadata{
			Schema: artifact.MetadataSchema, WorkID: st.WorkID, Name: st.Name, Kind: kind,
		}); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	phase, err := Phase(st.Path, step)
	if err != nil {
		return err
	}
	grant, err := e.artifacts.GrantEdit(ctx, artifact.GrantRequest{
		Ref: ref, Phase: phase, Regions: []artifact.Region{artifact.RegionWholeDocument},
	})
	if err != nil {
		return err
	}
	spec, err := e.editSpec(st, step, control, grant, "write "+string(kind),
		"Write "+path+": "+what+".")
	if err != nil {
		return err
	}
	_, err = e.assignments.Create(ctx, spec)
	return err
}

// stepApprove puts a document to a human and routes on the answer.
func (e *Engine) stepApprove(ctx context.Context, st State, step Step, kind decision.Kind, what string, doc artifact.Kind, approved, rejected Event) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		path, err := st.DocumentPath(doc)
		if err != nil {
			return st, false, err
		}
		spec, err := e.decisionSpec(st, step, control,
			approvalSchema(kind, what, path), "approve the "+what)
		if err != nil {
			return st, false, err
		}
		if _, err := e.assignments.Create(ctx, spec); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	sub, found, err := e.assignments.Decision(ctx, issued[0].ID)
	if err != nil {
		return st, false, err
	}
	if !found {
		return st, false, fmt.Errorf("change: decision %s is answered but carries no choice", issued[0].ID)
	}
	if sub.Choice == "approve" {
		return e.advance(ctx, st, step, approved)
	}
	// A rejection starts a fresh generation: the document is going to be
	// rewritten, and the approval gate must be askable again.
	st.Generation++
	return e.advance(ctx, st, step, rejected)
}

// stepImplement partitions the unchecked work into parallel implementer
// assignments, each in its own isolation area with its own declared scope.
func (e *Engine) stepImplement(ctx context.Context, st State, step Step, done Event) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		open, err := e.openItems(ctx, st)
		if err != nil {
			return st, false, err
		}
		units, err := e.env.Partition(ctx, st.WorkID, open)
		if err != nil {
			return st, false, err
		}
		if len(units) == 0 {
			return st, false, fmt.Errorf(
				"change: %s has no unchecked work to implement; the task list is empty or complete", st.Name)
		}
		if err := e.issueUnits(ctx, st, step, units,
			func(u Unit) string { return "implement " + u.Label }); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	return e.advance(ctx, st, step, done)
}

// stepIntegrate issues one integration implementer per affected member and
// re-baselines the integrated source fingerprints when they are done.
// Reviewer and skeptic only ever see the integrated result.
func (e *Engine) stepIntegrate(ctx context.Context, st State, step Step, done Event, from ...Step) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		results, err := e.completedResults(ctx, st, from...)
		if err != nil {
			return st, false, err
		}
		units, err := e.env.Integrations(ctx, st.WorkID, results)
		if err != nil {
			return st, false, err
		}
		if len(units) == 0 {
			return st, false, fmt.Errorf("change: %s produced no integration unit", st.Name)
		}
		if err := e.issueUnits(ctx, st, step, units,
			func(u Unit) string { return "integrate " + u.Label }); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	if err := e.rebaselineSources(ctx, &st); err != nil {
		return st, false, err
	}
	return e.advance(ctx, st, step, done)
}

// stepChecks runs the configured verification commands itself. Homonto
// executes them rather than accepting an agent's claim that they passed,
// and the evidence is recorded before the verdict is acted on.
func (e *Engine) stepChecks(ctx context.Context, st State, step Step) (State, bool, error) {
	set, err := e.env.RunChecks(ctx, st.WorkID)
	if err != nil {
		return st, false, err
	}
	if err := e.evidence.Record(ctx, st.WorkID, set); err != nil {
		return st, false, err
	}
	digest, err := set.Digest()
	if err != nil {
		return st, false, err
	}
	st.Baseline.Verification = digest
	event := EventChecksPassed
	if !set.Passed() {
		event = EventChecksFailed
	}
	return e.advance(ctx, st, step, event)
}

// stepReview dispatches the reviewer and the skeptic in parallel over the
// integrated result, then gates on their findings.
func (e *Engine) stepReview(ctx context.Context, st State, step Step) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, step)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		for _, role := range []protocol.Role{protocol.RoleReviewer, protocol.RoleSkeptic} {
			spec, err := e.readOnly(st, step, role, control,
				"assess the integrated result",
				"Assess the integrated result against "+st.inputPath()+
					" and its acceptance criteria. Report findings with severities. "+
					"Do not write anything.")
			if err != nil {
				return st, false, err
			}
			if _, err := e.assignments.Create(ctx, spec); err != nil {
				return st, false, err
			}
		}
		return st, true, nil
	}
	if answered, _ := allAnswered(issued); !answered {
		return st, false, nil
	}
	blockers, err := e.findings.Blockers(ctx, st.WorkID)
	if err != nil {
		return st, false, err
	}
	event := EventReviewClean
	if len(blockers) > 0 {
		event = EventReviewBlocked
	}
	return e.advance(ctx, st, step, event)
}

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
