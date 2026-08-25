// Recording of host and human answers: reports, decisions, edits, questions.
package task

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

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
