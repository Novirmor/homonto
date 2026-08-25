package task

import (
	"context"
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

// stepPlanExplore issues one explorer per confirmed member — the whole
// workspace is surveyed, not just the control repository — and advances
// when every one has reported.
func (e *Engine) stepPlanExplore(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepPlanExplore)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		members, err := e.env.Members(ctx)
		if err != nil {
			return st, false, err
		}
		if len(members) == 0 {
			return st, false, fmt.Errorf("task: the workspace has no confirmed members to explore")
		}
		for _, m := range members {
			spec, err := e.readOnly(st, StepPlanExplore, protocol.RoleExplorer, m,
				fmt.Sprintf("survey %s for the task", m.Path),
				"Read "+m.Path+" and report the facts, constraints, affected surfaces, and tests "+
					"that bear on this task. Do not write anything.")
			if err != nil {
				return st, false, err
			}
			if _, err := e.assignments.Create(ctx, spec); err != nil {
				return st, false, err
			}
		}
		return st, true, nil
	}
	if done, _ := allAnswered(issued); !done {
		return st, false, nil
	}
	next, err := Advance(st.Step, EventExplorersDone)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// stepPlanDraft hands the host an edit grant on the goal and checklist and
// advances when the edit is accepted. The baseline is re-taken at that
// point: the host's own draft is what the plan now rests on, and it must
// not immediately invalidate the step that produced it.
func (e *Engine) stepPlanDraft(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepPlanDraft)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		grant, err := e.artifacts.GrantEdit(ctx, artifact.GrantRequest{
			Ref:   st.ref(),
			Phase: artifact.PhasePlan,
			Regions: []artifact.Region{
				artifact.RegionTaskGoal, artifact.RegionTaskChecklist,
			},
		})
		if err != nil {
			return st, false, err
		}
		spec, err := e.editSpec(st, StepPlanDraft, control, grant,
			"incorporate the explorer reports into the task document",
			"Write the task's outcome and its checklist from the explorer reports. "+
				"Edit only the goal and checklist regions of "+grant.Ref.Path+".")
		if err != nil {
			return st, false, err
		}
		if _, err := e.assignments.Create(ctx, spec); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if done, _ := allAnswered(issued); !done {
		return st, false, nil
	}
	baseline, err := e.currentBaseline(ctx, st.WorkID, st.ref(), st.Baseline.Sources)
	if err != nil {
		return st, false, err
	}
	st.Baseline = baseline
	next, err := Advance(st.Step, EventDraftAccepted)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// stepPlanChallenge issues the skeptic that attacks the drafted plan
// before any implementation starts.
func (e *Engine) stepPlanChallenge(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepPlanChallenge)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		spec, err := e.readOnly(st, StepPlanChallenge, protocol.RoleSkeptic, control,
			"challenge the drafted plan",
			"Attack the assumptions, the missing cases, and the scope of the task in "+
				st.documentPath()+". Raise every consequential question. Do not write anything.")
		if err != nil {
			return st, false, err
		}
		if _, err := e.assignments.Create(ctx, spec); err != nil {
			return st, false, err
		}
		return st, true, nil
	}
	if done, _ := allAnswered(issued); !done {
		return st, false, nil
	}
	next, err := Advance(st.Step, EventChallengeDone)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// stepPlanResolve turns every consequential question the Plan reports
// raised into a human decision gate. A subagent cannot answer its own
// question, and unanswered questions block implementation even though
// there is no plan-approval gate.
func (e *Engine) stepPlanResolve(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepPlanResolve)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		questions, err := e.planQuestions(ctx, st)
		if err != nil {
			return st, false, err
		}
		if len(questions) == 0 {
			next, err := Advance(st.Step, EventQuestionsResolved)
			if err != nil {
				return st, false, err
			}
			st, err = e.moveTo(ctx, st, next)
			return st, err == nil, err
		}
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		for _, q := range questions {
			spec, err := e.decisionSpec(st, StepPlanResolve, control,
				questionSchema(q.ID, q.Prompt),
				"answer a question raised during planning", q.Prompt)
			if err != nil {
				return st, false, err
			}
			if _, err := e.assignments.Create(ctx, spec); err != nil {
				return st, false, err
			}
		}
		return st, true, nil
	}
	if done, _ := allAnswered(issued); !done {
		return st, false, nil
	}
	next, err := Advance(st.Step, EventQuestionsResolved)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// question is one consequential question raised by a Plan report.
type question struct {
	ID     string
	Prompt string
}

// planQuestions collects the consequential questions from every answered
// Plan report, in report order, deduplicated by id.
func (e *Engine) planQuestions(ctx context.Context, st State) ([]question, error) {
	var out []question
	seen := map[string]bool{}
	for _, step := range []Step{StepPlanExplore, StepPlanChallenge} {
		actions, err := e.actionsForStep(ctx, st, step)
		if err != nil {
			return nil, err
		}
		for _, act := range actions {
			sub, found, err := e.assignments.Report(ctx, act.ID)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			wire := act.Spec
			wire.FreshnessToken = e.assignments.Token(act.ID)
			report, err := protocol.DecodeReport(wire, sub.Report)
			if err != nil {
				return nil, err
			}
			for _, q := range questionsOf(report) {
				if seen[q.ID] {
					continue
				}
				seen[q.ID] = true
				out = append(out, q)
			}
		}
	}
	return out, nil
}

// questionsOf extracts the blocking questions of any role report.
func questionsOf(report protocol.Report) []question {
	var raw []protocol.Question
	switch r := report.(type) {
	case *protocol.ExplorerReport:
		raw = r.Questions
	case *protocol.ImplementerReport:
		raw = r.Questions
	case *protocol.ReviewerReport:
		raw = r.Questions
	case *protocol.SkepticReport:
		raw = r.Questions
	}
	// Every question in a report is consequential by construction: the
	// schema requires each one to state what it changes. There is no
	// "minor question" tier to filter out, and inventing one here would
	// let Homonto decide on the human's behalf which questions matter.
	var out []question
	for _, q := range raw {
		out = append(out, question{
			ID:     q.ID,
			Prompt: q.Text + "\n\nConsequence: " + q.Consequence,
		})
	}
	return out
}

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

// stepDoneChecks runs the configured verification commands itself. Homonto
// executes them rather than accepting an agent's claim that they passed,
// and the evidence is recorded before the verdict is acted on.
func (e *Engine) stepDoneChecks(ctx context.Context, st State) (State, bool, error) {
	set, err := e.env.RunChecks(ctx, st.WorkID)
	if err != nil {
		return st, false, err
	}
	if err := e.evidence.Record(ctx, st.WorkID, set); err != nil {
		return st, false, err
	}
	event := EventChecksPassed
	if !set.Passed() {
		event = EventChecksFailed
	}
	next, err := Advance(st.Step, event)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// stepDoneReview dispatches the reviewer and the skeptic in parallel over
// the integrated result, then gates on their findings.
func (e *Engine) stepDoneReview(ctx context.Context, st State) (State, bool, error) {
	issued, err := e.actionsForStep(ctx, st, StepDoneReview)
	if err != nil {
		return st, false, err
	}
	if len(issued) == 0 {
		control, err := e.env.Control(ctx)
		if err != nil {
			return st, false, err
		}
		for _, role := range []protocol.Role{protocol.RoleReviewer, protocol.RoleSkeptic} {
			spec, err := e.readOnly(st, StepDoneReview, role, control,
				"assess the integrated result",
				"Assess the integrated result against the goal and the checklist in "+
					st.documentPath()+". Report findings with severities. Do not write anything.")
			if err != nil {
				return st, false, err
			}
			if _, err := e.assignments.Create(ctx, spec); err != nil {
				return st, false, err
			}
		}
		return st, true, nil
	}
	if done, _ := allAnswered(issued); !done {
		return st, false, nil
	}
	blocked, err := e.blockingFindings(ctx, st.WorkID)
	if err != nil {
		return st, false, err
	}
	event := EventReviewClean
	if blocked {
		event = EventReviewBlocked
	}
	next, err := Advance(st.Step, event)
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
	gate, repairs := splitRepairActions(issued)

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

// stepDoneFinalize appends the evidence, checks every item off, and
// archives the record. Only Homonto writes here: the checkoffs and the
// evidence are binary-owned, and the archive move is a binary operation.
func (e *Engine) stepDoneFinalize(ctx context.Context, st State) (State, bool, error) {
	ref := st.ref()
	// Items are NOT checked off here. The ownership table gives Homonto
	// the evidence region in Done and the checklist only in Do, and that
	// is the right split: an item is checked when the assignment that did
	// it was accepted, never because the task reached the end. An item
	// still open at this point is an item nothing accepted work for, and
	// the record should say so rather than tidy it away.
	block, err := e.evidenceBlock(ctx, st)
	if err != nil {
		return st, false, err
	}
	if _, err := e.artifacts.AppendEvidence(ctx, ref, artifact.PhaseDone, block); err != nil {
		return st, false, err
	}
	if _, err := e.archive.ArchiveWork(ctx, st.WorkID, e.now()); err != nil {
		return st, false, err
	}
	if err := e.findings.ResetRepairs(ctx, st.WorkID); err != nil {
		return st, false, err
	}
	next, err := Advance(st.Step, EventFinalized)
	if err != nil {
		return st, false, err
	}
	st, err = e.moveTo(ctx, st, next)
	return st, err == nil, err
}

// evidenceBlock renders what the record must carry: a short outcome, the
// exact commands and their outcomes, the integration material, and every
// accepted deviation.
func (e *Engine) evidenceBlock(ctx context.Context, st State) ([]byte, error) {
	var b strings.Builder
	b.WriteString("## Outcome\n\nCompleted.\n\n## Verification\n\n")
	set, err := e.evidence.Latest(ctx, st.WorkID)
	if err != nil {
		b.WriteString("No verification evidence was recorded.\n")
	} else {
		for _, r := range set.Results {
			fmt.Fprintf(&b, "- `%s` in %q: %s (exit %d, %s)\n",
				strings.Join(r.Spec.Command, " "), displayDir(r.Spec.WorkingDir),
				r.Outcome, r.ExitCode, r.Duration)
		}
	}
	b.WriteString("\n## Integration\n\n")
	if len(st.Baseline.Sources) == 0 {
		b.WriteString("No integrated source fingerprints were recorded.\n")
	} else {
		for _, d := range st.Baseline.Sources {
			fmt.Fprintf(&b, "- %s\n", d)
		}
	}
	devs, err := e.findings.Deviations(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	b.WriteString("\n## Accepted deviations\n\n")
	if len(devs) == 0 {
		b.WriteString("None.\n")
	} else {
		for _, d := range devs {
			fmt.Fprintf(&b, "- %s %s: %s — accepted because %s (decision %s)\n",
				d.Severity, d.ExternalID, d.Summary, d.Rationale, d.DecisionID)
		}
	}
	return []byte(b.String()), nil
}

// displayDir renders a check's working directory for the record.
func displayDir(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

// SubmitReport records a host's answer to an assignment. The result diff
// is validated FIRST for a writable assignment: a report backed by changes
// the assignment was not issued to make is refused rather than recorded,
// so nothing downstream ever reads it.
func (e *Engine) SubmitReport(ctx context.Context, in protocol.ReportSubmission) (State, error) {
	act, err := e.assignments.Action(ctx, in.ActionID)
	if err != nil {
		return State{}, err
	}
	st, err := e.State(ctx, act.WorkID)
	if err != nil {
		return State{}, err
	}
	if st.Step.Terminal() {
		return State{}, fmt.Errorf("task: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if act.Generation != st.Generation {
		return State{}, fmt.Errorf("task: action %s belongs to generation %d, the task is at %d: %w",
			act.ID, act.Generation, st.Generation, ErrStaleAction)
	}
	wire := act.Spec
	wire.FreshnessToken = e.assignments.Token(act.ID)
	if !wire.WriteScope.ReadOnly {
		unit, _, err := e.partitionOf(ctx, act.ID)
		if err != nil {
			return State{}, err
		}
		if err := e.validateResult(ctx, wire, unit); err != nil {
			return State{}, err
		}
	}
	if _, err := e.assignments.Submit(ctx, in); err != nil {
		return State{}, err
	}
	if err := e.recordFindings(ctx, st, act, in); err != nil {
		return State{}, err
	}
	// Only Homonto checks items off, and only for assignments it accepted
	// — which is exactly here, after the final diff gate has passed.
	if act.Step == string(StepDoImplement) && act.Role == protocol.RoleImplementer {
		if err := e.checkOffPartition(ctx, st, act.ID); err != nil {
			return State{}, err
		}
	}
	return st, nil
}

// recordFindings persists the findings of a reviewer or skeptic report.
func (e *Engine) recordFindings(ctx context.Context, st State, act assignment.Action, in protocol.ReportSubmission) error {
	switch act.Role {
	case protocol.RoleReviewer, protocol.RoleSkeptic:
	default:
		return nil
	}
	wire := act.Spec
	wire.FreshnessToken = e.assignments.Token(act.ID)
	report, err := protocol.DecodeReport(wire, in.Report)
	if err != nil {
		return err
	}
	var findings []protocol.Finding
	switch r := report.(type) {
	case *protocol.ReviewerReport:
		findings = r.Findings
	case *protocol.SkepticReport:
		findings = r.Findings
	}
	if len(findings) == 0 {
		return nil
	}
	return e.findings.RecordReport(ctx, st.WorkID, act.ID, act.Role, findings)
}

// Decide records a human's answer to a decision gate. Acting on the answer
// is the next step's job, not this one's: recording and acting are
// separate so a crash between them replays cleanly.
func (e *Engine) Decide(ctx context.Context, in decision.Submission) (State, error) {
	act, err := e.assignments.Action(ctx, in.ActionID)
	if err != nil {
		return State{}, err
	}
	st, err := e.State(ctx, act.WorkID)
	if err != nil {
		return State{}, err
	}
	if st.Step.Terminal() {
		return State{}, fmt.Errorf("task: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if _, err := e.assignments.Decide(ctx, in); err != nil {
		return State{}, err
	}
	return st, nil
}

// AcceptEdit accepts the host's document edit and marks the edit action
// answered. The host presents only the grant token it was issued: Homonto
// looks up what that grant actually opened rather than believing a
// structure the host hands back, and the artifact service then refuses any
// change outside the granted regions — leaving the action open, because
// nothing was accepted.
func (e *Engine) AcceptEdit(ctx context.Context, actionID identity.ActionID, grantToken identity.Token) (State, error) {
	act, err := e.assignments.Action(ctx, actionID)
	if err != nil {
		return State{}, err
	}
	st, err := e.State(ctx, act.WorkID)
	if err != nil {
		return State{}, err
	}
	if st.Step.Terminal() {
		return State{}, fmt.Errorf("task: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if act.Spec.Edit == nil {
		return State{}, fmt.Errorf("task: action %s is not an edit action", actionID)
	}
	grant, err := e.artifacts.Grant(ctx, act.Spec.Edit.GrantID, grantToken)
	if err != nil {
		return State{}, err
	}
	if _, err := e.artifacts.AcceptEdit(ctx, grant); err != nil {
		return State{}, err
	}
	if _, err := e.assignments.CompleteEdit(ctx, actionID, e.assignments.Token(actionID)); err != nil {
		return State{}, err
	}
	return st, nil
}
