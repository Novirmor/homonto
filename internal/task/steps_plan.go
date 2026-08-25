// Plan-phase workflow steps: explore, draft, challenge, and resolve questions.
package task

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/protocol"
)

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
