package task

import (
	"errors"
	"fmt"
)

// ErrInvalidTransition reports a move the workflow does not have.
var ErrInvalidTransition = errors.New("task: invalid transition")

// Event is something that happened, offered to the state machine.
type Event string

const (
	// EventExplorersDone: every explorer assignment was answered.
	EventExplorersDone Event = "explorers_done"
	// EventDraftAccepted: the host's edit of the goal and checklist was
	// accepted under its grant.
	EventDraftAccepted Event = "draft_accepted"
	// EventChallengeDone: the skeptic answered.
	EventChallengeDone Event = "challenge_done"
	// EventQuestionsResolved: every consequential question raised in Plan
	// has a recorded human answer.
	EventQuestionsResolved Event = "questions_resolved"
	// EventImplementersDone: every parallel implementer answered.
	EventImplementersDone Event = "implementers_done"
	// EventIntegrated: the integration implementer answered for every
	// affected member.
	EventIntegrated Event = "integrated"
	// EventChecksPassed: Homonto ran the configured checks and all passed.
	EventChecksPassed Event = "checks_passed"
	// EventChecksFailed: at least one check did not pass.
	EventChecksFailed Event = "checks_failed"
	// EventReviewClean: reviewer and skeptic answered with no blocking
	// finding left open.
	EventReviewClean Event = "review_clean"
	// EventReviewBlocked: a critical or high finding is open.
	EventReviewBlocked Event = "review_blocked"
	// EventRepairDone: a repair round produced new integrated material.
	EventRepairDone Event = "repair_done"
	// EventRepairLimitReached: the third consecutive repair failed, so the
	// choice goes to a human.
	EventRepairLimitReached Event = "repair_limit_reached"
	// EventRepairContinued: the human chose to keep repairing after the
	// limit.
	EventRepairContinued Event = "repair_continued"
	// EventFinalized: evidence was appended and the record written.
	EventFinalized Event = "finalized"
	// EventAbandon: the human gave the work up.
	EventAbandon Event = "abandon"
)

// Advance is the whole state machine. It is a switch on purpose: the legal
// moves are the ones written here, and nothing outside this function can
// add one.
//
// Abandon is legal from every non-terminal step — a human may always stop
// — and nothing is legal from a terminal one.
func Advance(from Step, event Event) (Step, error) {
	if !from.Known() {
		return "", fmt.Errorf("task: step %q is not a known step: %w", from, ErrInvalidTransition)
	}
	if from.Terminal() {
		return "", fmt.Errorf("task: %s is terminal; %q is not possible: %w", from, event, ErrInvalidTransition)
	}
	if event == EventAbandon {
		return StepAbandoned, nil
	}

	switch from {
	case StepPlanExplore:
		if event == EventExplorersDone {
			return StepPlanDraft, nil
		}
	case StepPlanDraft:
		if event == EventDraftAccepted {
			return StepPlanChallenge, nil
		}
	case StepPlanChallenge:
		if event == EventChallengeDone {
			return StepPlanResolve, nil
		}
	case StepPlanResolve:
		if event == EventQuestionsResolved {
			return StepDoImplement, nil
		}
	case StepDoImplement:
		if event == EventImplementersDone {
			return StepDoIntegrate, nil
		}
	case StepDoIntegrate:
		if event == EventIntegrated {
			return StepDoneChecks, nil
		}
	case StepDoneChecks:
		switch event {
		case EventChecksPassed:
			return StepDoneReview, nil
		case EventChecksFailed:
			return StepDoRepair, nil
		}
	case StepDoneReview:
		switch event {
		case EventReviewClean:
			return StepDoneFinalize, nil
		case EventReviewBlocked:
			return StepDoRepair, nil
		}
	case StepDoRepair:
		switch event {
		case EventRepairDone:
			// A repair returns to INTEGRATION, not straight to the checks.
			// A repair round produces new material in fresh isolation
			// areas, and material that has not been integrated is material
			// the checks would never see; re-running them against the old
			// integrated result would grade the previous attempt again.
			return StepDoIntegrate, nil
		case EventRepairContinued:
			// The human chose to keep going. The workflow stays in repair
			// and issues a fresh round rather than re-running checks it
			// already knows the answer to.
			return StepDoRepair, nil
		case EventRepairLimitReached:
			// The limit does not move the workflow. It stays in repair and
			// waits for a human, because "keep going" and "give up" are
			// both still open and neither is Homonto's to pick.
			return StepDoRepair, nil
		}
	case StepDoneFinalize:
		if event == EventFinalized {
			return StepArchived, nil
		}
	}
	return "", fmt.Errorf("task: %s does not accept %q: %w", from, event, ErrInvalidTransition)
}
