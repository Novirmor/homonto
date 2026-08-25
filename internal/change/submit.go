// Recording of host and human answers: reports, decisions, edits.
package change

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

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
	if terminalStep(st.Path, st.Step) {
		return State{}, fmt.Errorf("change: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if act.Generation != st.Generation {
		return State{}, fmt.Errorf("change: action %s belongs to generation %d, the change is at %d: %w",
			act.ID, act.Generation, st.Generation, ErrStaleAction)
	}
	wire := act.Spec
	wire.FreshnessToken = e.assignments.Token(act.ID)
	if !wire.WriteScope.ReadOnly {
		unit, _, err := e.unitOf(ctx, act.ID)
		if err != nil {
			return State{}, err
		}
		diff, err := e.env.ResultDiff(ctx, wire, unit)
		if err != nil {
			return State{}, err
		}
		if err := e.guard.ValidateAssignmentResult(ctx, wire, diff); err != nil {
			return State{}, fmt.Errorf("%w: %w", ErrResultRejected, err)
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
	if act.Role == protocol.RoleImplementer &&
		(act.Step == string(StepBuildImplement) || act.Step == string(StepPresetImplement)) {
		if err := e.checkOffUnit(ctx, st, act.ID); err != nil {
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
	// Findings raised during Open and Design are preset scope SIGNALS, not
	// defects in a result that does not exist yet. Recording them as
	// blocking findings would gate a change on an observation about its
	// own scope.
	if act.Step != string(StepVerifyReview) && act.Step != string(StepPresetReview) {
		return nil
	}
	wire := act.Spec
	wire.FreshnessToken = e.assignments.Token(act.ID)
	report, err := protocol.DecodeReport(wire, in.Report)
	if err != nil {
		return err
	}
	findings := findingsOf(report)
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
	// A classification confirmation answers a candidate, not a change.
	if act.Step == string(PreflightConfirm) {
		if _, err := e.assignments.Decide(ctx, in); err != nil {
			return State{}, err
		}
		pre, err := e.Preflight(ctx, act.WorkID)
		if err != nil {
			return State{}, err
		}
		return e.ConfirmPreflight(ctx, ConfirmInput{
			WorkID: pre.WorkID, Path: Path(in.Choice),
			Rationale: in.Rationale, DecisionID: act.ID,
		})
	}
	st, err := e.State(ctx, act.WorkID)
	if err != nil {
		return State{}, err
	}
	if terminalStep(st.Path, st.Step) {
		return State{}, fmt.Errorf("change: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if _, err := e.assignments.Decide(ctx, in); err != nil {
		return State{}, err
	}
	return st, nil
}

// AcceptEdit accepts the host's document edit and marks the edit action
// answered. The host presents only the grant token it was issued: Homonto
// looks up what that grant actually opened rather than believing a
// structure the host hands back.
func (e *Engine) AcceptEdit(ctx context.Context, actionID identity.ActionID, grantToken identity.Token) (State, error) {
	act, err := e.assignments.Action(ctx, actionID)
	if err != nil {
		return State{}, err
	}
	st, err := e.State(ctx, act.WorkID)
	if err != nil {
		return State{}, err
	}
	if terminalStep(st.Path, st.Step) {
		return State{}, fmt.Errorf("change: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if act.Spec.Edit == nil {
		return State{}, fmt.Errorf("change: action %s is not an edit action", actionID)
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
