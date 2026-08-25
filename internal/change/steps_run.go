// Workflow step execution: the dispatcher that routes a step to its handler.
package change

import (
	"context"
	"errors"
	"fmt"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/protocol"
)

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
	case StepPresetExplore:
		return e.stepExplore(ctx, st, step, EventExplorersDone,
			"establish the current behavior and the affected surfaces")
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
	case StepPresetRecord:
		return e.stepVerificationRecord(ctx, st, step)
	case StepPresetADR:
		return e.stepCloseADR(ctx, st, step)
	case StepPresetRepair:
		return e.stepRepair(ctx, st, step)
	case StepPresetFinalize:
		return e.stepFinalize(ctx, st, step)
	}
	return st, false, fmt.Errorf("change: step %q has no handler", st.Step)
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
