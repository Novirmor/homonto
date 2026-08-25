package change

import (
	"errors"
	"fmt"
)

// ErrInvalidTransition reports a move a Change workflow does not have.
var ErrInvalidTransition = errors.New("change: invalid transition")

// Event is something that happened, offered to a Change state machine.
type Event string

// Full path events.
const (
	// EventExplorersDone: every Open explorer answered.
	EventExplorersDone Event = "explorers_done"
	// EventChallengeDone: the Open skeptic answered.
	EventChallengeDone Event = "challenge_done"
	// EventProposalDrafted: the host's proposal edit was accepted.
	EventProposalDrafted Event = "proposal_drafted"
	// EventScopeApproved: the human approved the scope.
	EventScopeApproved Event = "scope_approved"
	// EventScopeRejected: the human sent the proposal back.
	EventScopeRejected Event = "scope_rejected"
	// EventDesignDrafted: the host's design and tasks edits were accepted.
	EventDesignDrafted Event = "design_drafted"
	// EventDesignChallenged: the Design skeptic answered.
	EventDesignChallenged Event = "design_challenged"
	// EventDesignApproved: the human approved the design.
	EventDesignApproved Event = "design_approved"
	// EventDesignRejected: the human sent the design back.
	EventDesignRejected Event = "design_rejected"
	// EventPlanDrafted: the host's plan edit was accepted.
	EventPlanDrafted Event = "plan_drafted"
	// EventImplementersDone: every parallel implementer answered.
	EventImplementersDone Event = "implementers_done"
	// EventIntegrated: the integration implementers answered.
	EventIntegrated Event = "integrated"
	// EventChecksPassed: Homonto ran the configured checks and all passed.
	EventChecksPassed Event = "checks_passed"
	// EventChecksFailed: at least one check did not pass.
	EventChecksFailed Event = "checks_failed"
	// EventReviewClean: reviewer and skeptic answered with no blocking
	// finding open.
	EventReviewClean Event = "review_clean"
	// EventReviewBlocked: a critical or high finding is open.
	EventReviewBlocked Event = "review_blocked"
	// EventVerificationRecorded: verification.md was generated.
	EventVerificationRecorded Event = "verification_recorded"
	// EventRepairDone: a repair round produced new material.
	EventRepairDone Event = "repair_done"
	// EventRepairLimitReached: the third consecutive repair failed.
	EventRepairLimitReached Event = "repair_limit_reached"
	// EventRepairContinued: the human chose to keep repairing.
	EventRepairContinued Event = "repair_continued"
	// EventADRsWritten: every required ADR exists and is fresh.
	EventADRsWritten Event = "adrs_written"
	// EventDecisionDiscovered: Close found a durable decision the design
	// never recorded. It returns the change to Design rather than writing
	// an ADR for a decision nobody designed.
	EventDecisionDiscovered Event = "decision_discovered"
	// EventFinalized: record.md was written and the change archived.
	EventFinalized Event = "finalized"
	// EventAbandon: the human gave the work up.
	EventAbandon Event = "abandon"
)

// Preset events. They are separate names on purpose: a preset is not a
// Full change with steps skipped, and sharing an event vocabulary is the
// first step toward sharing a code path that should not be shared.
const (
	// EventPresetDrafted: the host's fix.md or tweak.md and tasks.md
	// edits were accepted.
	EventPresetDrafted Event = "preset_drafted"
	// EventReproductionRecorded: a failing test or reproducible command
	// exists — or a human accepted the recorded reason there is none.
	EventReproductionRecorded Event = "reproduction_recorded"
	// EventScopeClear: the preset scope assessment fired nothing.
	EventScopeClear Event = "scope_clear"
	// EventScopeContinued: the human chose to continue the preset with the
	// broader scope recorded.
	EventScopeContinued Event = "scope_continued"
	// EventPresetImplemented: every parallel implementer answered.
	EventPresetImplemented Event = "preset_implemented"
	// EventPresetIntegrated: the integration implementers answered.
	EventPresetIntegrated Event = "preset_integrated"
)

// AdvanceFull is the Full path's whole state machine. It is a switch on
// purpose: the legal moves are the ones written here, and nothing outside
// this function can add one.
//
// Abandon is legal from every non-terminal step — a human may always stop
// — and nothing is legal from a terminal one.
func AdvanceFull(from Step, event Event) (Step, error) {
	if index(PathFull, from) < 0 {
		return "", fmt.Errorf("change: %q is not a step of the full path: %w", from, ErrInvalidTransition)
	}
	if terminalStep(PathFull, string(from)) {
		return "", fmt.Errorf("change: %s is terminal; %q is not possible: %w", from, event, ErrInvalidTransition)
	}
	if event == EventAbandon {
		return StepAbandoned, nil
	}

	switch from {
	case StepOpenExplore:
		if event == EventExplorersDone {
			return StepOpenChallenge, nil
		}
	case StepOpenChallenge:
		if event == EventChallengeDone {
			return StepOpenDraft, nil
		}
	case StepOpenDraft:
		if event == EventProposalDrafted {
			return StepOpenApprove, nil
		}
	case StepOpenApprove:
		switch event {
		case EventScopeApproved:
			return StepDesignDraft, nil
		case EventScopeRejected:
			// Back to the draft, not back to exploring: the human read the
			// proposal and wants a different one, which is a writing
			// problem rather than a knowledge problem.
			return StepOpenDraft, nil
		}
	case StepDesignDraft:
		if event == EventDesignDrafted {
			return StepDesignChallenge, nil
		}
	case StepDesignChallenge:
		if event == EventDesignChallenged {
			return StepDesignApprove, nil
		}
	case StepDesignApprove:
		switch event {
		case EventDesignApproved:
			return StepBuildPlan, nil
		case EventDesignRejected:
			return StepDesignDraft, nil
		}
	case StepBuildPlan:
		if event == EventPlanDrafted {
			return StepBuildImplement, nil
		}
	case StepBuildImplement:
		if event == EventImplementersDone {
			return StepBuildIntegrate, nil
		}
	case StepBuildIntegrate:
		if event == EventIntegrated {
			return StepVerifyChecks, nil
		}
	case StepVerifyChecks:
		switch event {
		case EventChecksPassed:
			return StepVerifyReview, nil
		case EventChecksFailed:
			return StepBuildRepair, nil
		}
	case StepVerifyReview:
		switch event {
		case EventReviewClean:
			return StepVerifyRecord, nil
		case EventReviewBlocked:
			return StepBuildRepair, nil
		}
	case StepVerifyRecord:
		if event == EventVerificationRecorded {
			return StepCloseADR, nil
		}
	case StepBuildRepair:
		switch event {
		case EventRepairDone, EventRepairContinued:
			// A repair returns to INTEGRATION, not to the checks. A round
			// produces new material in fresh isolation areas, and material
			// that has not been integrated is material the checks would
			// never see.
			return StepBuildIntegrate, nil
		case EventRepairLimitReached:
			// The limit does not move the change. It waits for a human,
			// because "keep going" and "give up" are both still open and
			// neither is Homonto's to pick.
			return StepBuildRepair, nil
		}
	case StepCloseADR:
		switch event {
		case EventADRsWritten:
			return StepCloseFinalize, nil
		case EventDecisionDiscovered:
			// Close found a durable decision the design never recorded.
			// Writing an ADR for a decision nobody designed would document
			// an accident; the change goes back to Design instead.
			return StepDesignDraft, nil
		}
	case StepCloseFinalize:
		if event == EventFinalized {
			return StepArchived, nil
		}
	}
	return "", fmt.Errorf("change: full %s does not accept %q: %w", from, event, ErrInvalidTransition)
}

// AdvancePreset is the Fix and Tweak state machine. Fix and Tweak differ
// in one step — Fix must record a reproduction and Tweak must not — and
// that difference is the path argument rather than two near-identical
// switches.
func AdvancePreset(path Path, from Step, event Event) (Step, error) {
	if !path.Preset() {
		return "", fmt.Errorf("change: %q is not a preset path: %w", path, ErrInvalidTransition)
	}
	if index(path, from) < 0 {
		return "", fmt.Errorf("change: %q is not a step of the %s path: %w", from, path, ErrInvalidTransition)
	}
	if terminalStep(path, string(from)) {
		return "", fmt.Errorf("change: %s is terminal; %q is not possible: %w", from, event, ErrInvalidTransition)
	}
	if event == EventAbandon {
		return StepAbandoned, nil
	}

	switch from {
	case StepPresetOpenDraft:
		if event != EventPresetDrafted {
			break
		}
		// Fix must prove the defect before implementing it; Tweak has no
		// defect to reproduce and would be asked for evidence of nothing.
		if path == PathFix {
			return StepPresetReproduce, nil
		}
		return StepPresetScope, nil
	case StepPresetReproduce:
		if path != PathFix {
			break
		}
		if event == EventReproductionRecorded {
			return StepPresetScope, nil
		}
	case StepPresetScope:
		switch event {
		case EventScopeClear, EventScopeContinued:
			return StepPresetImplement, nil
		}
	case StepPresetImplement:
		if event == EventPresetImplemented {
			return StepPresetIntegrate, nil
		}
	case StepPresetIntegrate:
		if event == EventPresetIntegrated {
			return StepPresetChecks, nil
		}
	case StepPresetChecks:
		switch event {
		case EventChecksPassed:
			return StepPresetReview, nil
		case EventChecksFailed:
			return StepPresetRepair, nil
		}
	case StepPresetReview:
		switch event {
		case EventReviewClean:
			return StepPresetFinalize, nil
		case EventReviewBlocked:
			return StepPresetRepair, nil
		}
	case StepPresetRepair:
		switch event {
		case EventRepairDone, EventRepairContinued:
			return StepPresetIntegrate, nil
		case EventRepairLimitReached:
			return StepPresetRepair, nil
		}
	case StepPresetFinalize:
		if event == EventFinalized {
			return StepArchived, nil
		}
	}
	return "", fmt.Errorf("change: %s %s does not accept %q: %w", path, from, event, ErrInvalidTransition)
}

// Advance dispatches to the path's own state machine. There is no shared
// machine underneath: this is a two-way switch, not an abstraction.
func Advance(path Path, from Step, event Event) (Step, error) {
	if path == PathFull {
		return AdvanceFull(from, event)
	}
	return AdvancePreset(path, from, event)
}
