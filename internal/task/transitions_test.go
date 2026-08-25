package task

import (
	"errors"
	"testing"

	"github.com/noviopenworks/homonto/internal/artifact"
)

func TestAdvanceCoversEveryValidTransition(t *testing.T) {
	tests := []struct {
		from  Step
		event Event
		want  Step
	}{
		{StepPlanExplore, EventExplorersDone, StepPlanDraft},
		{StepPlanDraft, EventDraftAccepted, StepPlanChallenge},
		{StepPlanChallenge, EventChallengeDone, StepPlanResolve},
		{StepPlanResolve, EventQuestionsResolved, StepDoImplement},
		{StepDoImplement, EventImplementersDone, StepDoIntegrate},
		{StepDoIntegrate, EventIntegrated, StepDoneChecks},
		{StepDoneChecks, EventChecksPassed, StepDoneReview},
		{StepDoneChecks, EventChecksFailed, StepDoRepair},
		{StepDoneReview, EventReviewClean, StepDoneFinalize},
		{StepDoneReview, EventReviewBlocked, StepDoRepair},
		{StepDoRepair, EventRepairDone, StepDoIntegrate},
		{StepDoRepair, EventRepairContinued, StepDoRepair},
		{StepDoRepair, EventRepairLimitReached, StepDoRepair},
		{StepDoneFinalize, EventFinalized, StepArchived},
	}
	for _, tt := range tests {
		got, err := Advance(tt.from, tt.event)
		if err != nil {
			t.Errorf("Advance(%s, %s): %v", tt.from, tt.event, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Advance(%s, %s) = %s, want %s", tt.from, tt.event, got, tt.want)
		}
	}
}

func TestAbandonIsAlwaysAvailableUntilTerminal(t *testing.T) {
	for _, step := range steps {
		got, err := Advance(step, EventAbandon)
		if step.Terminal() {
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("Advance(%s, abandon) error = %v, want ErrInvalidTransition", step, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Advance(%s, abandon): %v", step, err)
			continue
		}
		if got != StepAbandoned {
			t.Errorf("Advance(%s, abandon) = %s, want abandoned", step, got)
		}
	}
}

func TestAdvanceRefusesEverythingElse(t *testing.T) {
	valid := map[Step]map[Event]bool{
		StepPlanExplore:   {EventExplorersDone: true},
		StepPlanDraft:     {EventDraftAccepted: true},
		StepPlanChallenge: {EventChallengeDone: true},
		StepPlanResolve:   {EventQuestionsResolved: true},
		StepDoImplement:   {EventImplementersDone: true},
		StepDoIntegrate:   {EventIntegrated: true},
		StepDoneChecks:    {EventChecksPassed: true, EventChecksFailed: true},
		StepDoneReview:    {EventReviewClean: true, EventReviewBlocked: true},
		StepDoRepair: {
			EventRepairDone: true, EventRepairContinued: true, EventRepairLimitReached: true,
		},
		StepDoneFinalize: {EventFinalized: true},
	}
	events := []Event{
		EventExplorersDone, EventDraftAccepted, EventChallengeDone, EventQuestionsResolved,
		EventImplementersDone, EventIntegrated, EventChecksPassed, EventChecksFailed,
		EventReviewClean, EventReviewBlocked, EventRepairDone, EventRepairContinued,
		EventRepairLimitReached, EventFinalized,
	}
	for _, step := range steps {
		for _, event := range events {
			_, err := Advance(step, event)
			if valid[step][event] {
				if err != nil {
					t.Errorf("Advance(%s, %s) rejected a valid move: %v", step, event, err)
				}
				continue
			}
			if !errors.Is(err, ErrInvalidTransition) {
				t.Errorf("Advance(%s, %s) error = %v, want ErrInvalidTransition", step, event, err)
			}
		}
	}
}

// TestSkippingAStepIsRefused is the property the whole switch exists for:
// an explorer round cannot become an implementation round because someone
// offered the wrong event.
func TestSkippingAStepIsRefused(t *testing.T) {
	for _, event := range []Event{EventIntegrated, EventChecksPassed, EventFinalized} {
		if _, err := Advance(StepPlanExplore, event); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("Advance(plan_explore, %s) error = %v, want ErrInvalidTransition", event, err)
		}
	}
}

func TestAdvanceRefusesUnknownSteps(t *testing.T) {
	if _, err := Advance(Step("plan_something"), EventExplorersDone); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Advance(unknown step) error = %v, want ErrInvalidTransition", err)
	}
	if _, err := Advance(StepPlanExplore, Event("whatever")); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("Advance(unknown event) error = %v, want ErrInvalidTransition", err)
	}
}

func TestStepPhases(t *testing.T) {
	tests := map[Step]artifact.Phase{
		StepPlanExplore:   artifact.PhasePlan,
		StepPlanDraft:     artifact.PhasePlan,
		StepPlanChallenge: artifact.PhasePlan,
		StepPlanResolve:   artifact.PhasePlan,
		StepDoImplement:   artifact.PhaseDo,
		StepDoIntegrate:   artifact.PhaseDo,
		StepDoRepair:      artifact.PhaseDo,
		StepDoneChecks:    artifact.PhaseDone,
		StepDoneReview:    artifact.PhaseDone,
		StepDoneFinalize:  artifact.PhaseDone,
	}
	for step, want := range tests {
		got, err := step.Phase()
		if err != nil {
			t.Errorf("%s.Phase(): %v", step, err)
			continue
		}
		if got != want {
			t.Errorf("%s.Phase() = %s, want %s", step, got, want)
		}
	}
	for _, step := range []Step{StepArchived, StepAbandoned} {
		if _, err := step.Phase(); err == nil {
			t.Errorf("%s.Phase() = nil error, want a refusal for a terminal step", step)
		}
	}
	if _, err := Step("nonsense").Phase(); err == nil {
		t.Error("Phase() of an unknown step = nil error, want refusal")
	}
}

// TestEveryStepIsReachable guards against a step that exists in the
// enumeration but that no transition can ever reach.
func TestEveryStepIsReachable(t *testing.T) {
	reached := map[Step]bool{StepPlanExplore: true}
	events := []Event{
		EventExplorersDone, EventDraftAccepted, EventChallengeDone, EventQuestionsResolved,
		EventImplementersDone, EventIntegrated, EventChecksPassed, EventChecksFailed,
		EventReviewClean, EventReviewBlocked, EventRepairDone, EventRepairContinued,
		EventRepairLimitReached, EventFinalized, EventAbandon,
	}
	for changed := true; changed; {
		changed = false
		for _, step := range steps {
			if !reached[step] {
				continue
			}
			for _, event := range events {
				next, err := Advance(step, event)
				if err != nil || reached[next] {
					continue
				}
				reached[next] = true
				changed = true
			}
		}
	}
	for _, step := range steps {
		if !reached[step] {
			t.Errorf("step %s is unreachable", step)
		}
	}
}

func FuzzAdvance(f *testing.F) {
	f.Add("plan_explore", "explorers_done")
	f.Add("done_repair", "")
	f.Add("", "abandon")
	f.Fuzz(func(t *testing.T, step, event string) {
		next, err := Advance(Step(step), Event(event))
		if err != nil {
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("untyped transition error: %v", err)
			}
			return
		}
		// Anything the machine accepts must land on a known step, and a
		// terminal step must never be a source.
		if !next.Known() {
			t.Fatalf("Advance(%q, %q) = %q, which is not a known step", step, event, next)
		}
		if Step(step).Terminal() {
			t.Fatalf("Advance accepted a move out of the terminal step %q", step)
		}
	})
}
