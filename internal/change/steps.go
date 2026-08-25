package change

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/artifact"
)

// Step is one explicit position in a Change workflow. The vocabularies are
// per-path and deliberately do not overlap: a Full change's design_draft
// and a preset's build_implement are different enough that sharing a name
// would invite sharing a code path, and the presets are not a smaller Full.
type Step string

// Full path steps: open -> design -> build -> verify -> close.
const (
	// StepOpenExplore: parallel explorers establish current behavior,
	// constraints, and affected repositories.
	StepOpenExplore Step = "open_explore"
	// StepOpenChallenge: a skeptic attacks the hidden assumptions.
	StepOpenChallenge Step = "open_challenge"
	// StepOpenDraft: the host writes proposal.md.
	StepOpenDraft Step = "open_draft"
	// StepOpenApprove: the human approves the scope before Design.
	StepOpenApprove Step = "open_approve"
	// StepDesignDraft: the host writes design.md and tasks.md, with
	// acceptance criteria and ADR candidates.
	StepDesignDraft Step = "design_draft"
	// StepDesignChallenge: a skeptic attacks the design before approval.
	StepDesignChallenge Step = "design_challenge"
	// StepDesignApprove: the human approves the design before Build.
	StepDesignApprove Step = "design_approve"
	// StepBuildPlan: the host writes plan.md.
	StepBuildPlan Step = "build_plan"
	// StepBuildImplement: parallel implementers in separate isolation
	// areas.
	StepBuildImplement Step = "build_implement"
	// StepBuildIntegrate: one integration implementer per member.
	StepBuildIntegrate Step = "build_integrate"
	// StepVerifyChecks: Homonto runs the configured checks itself.
	StepVerifyChecks Step = "verify_checks"
	// StepVerifyReview: reviewer and skeptic assess the integrated result.
	StepVerifyReview Step = "verify_review"
	// StepVerifyRecord: Homonto generates verification.md from the
	// evidence.
	StepVerifyRecord Step = "verify_record"
	// StepBuildRepair: a bounded repair round. Failure returns to Build,
	// which is why repair is spelled as a Build step.
	StepBuildRepair Step = "build_repair"
	// StepCloseADR: required ADRs are written by an implementer
	// assignment.
	StepCloseADR Step = "close_adr"
	// StepCloseFinalize: freshness is confirmed, record.md is written, and
	// the change directory is archived.
	StepCloseFinalize Step = "close_finalize"
)

// Preset steps: open -> build -> verify -> close. A preset has no Design
// and no separate plan; that is the whole saving.
const (
	// StepPresetOpenDraft: the host writes fix.md or tweak.md and
	// tasks.md.
	StepPresetOpenDraft Step = "preset_open_draft"
	// StepPresetReproduce: Fix only — a failing automated test or a
	// reproducible command, or a recorded reason it is not reasonably
	// automatable plus human approval.
	StepPresetReproduce Step = "preset_reproduce"
	// StepPresetScope: the preset scope assessment. It runs against the
	// immutable work baseline and pauses for a human when anything fires.
	StepPresetScope Step = "preset_scope"
	// StepPresetImplement: parallel implementers.
	StepPresetImplement Step = "preset_implement"
	// StepPresetIntegrate: one integration implementer per member.
	StepPresetIntegrate Step = "preset_integrate"
	// StepPresetChecks: Homonto runs the configured checks.
	StepPresetChecks Step = "preset_checks"
	// StepPresetReview: reviewer and skeptic assess the result.
	StepPresetReview Step = "preset_review"
	// StepPresetRepair: a bounded repair round.
	StepPresetRepair Step = "preset_repair"
	// StepPresetFinalize: record.md is written and the change is
	// archived.
	StepPresetFinalize Step = "preset_finalize"
)

// Terminal steps, shared by every path: a finished change is finished the
// same way whichever route it took.
const (
	// StepArchived: the change directory has been moved into the archive.
	StepArchived Step = "archived"
	// StepAbandoned: the change was given up on. Its isolation areas,
	// branches, and evidence remain for external handling.
	StepAbandoned Step = "abandoned"
)

// fullSteps is the Full path's vocabulary in canonical order.
var fullSteps = []Step{
	StepOpenExplore, StepOpenChallenge, StepOpenDraft, StepOpenApprove,
	StepDesignDraft, StepDesignChallenge, StepDesignApprove,
	StepBuildPlan, StepBuildImplement, StepBuildIntegrate,
	StepVerifyChecks, StepVerifyReview, StepVerifyRecord, StepBuildRepair,
	StepCloseADR, StepCloseFinalize, StepArchived, StepAbandoned,
}

// presetSteps is the Fix and Tweak vocabulary in canonical order.
var presetSteps = []Step{
	StepPresetOpenDraft, StepPresetReproduce, StepPresetScope,
	StepPresetImplement, StepPresetIntegrate,
	StepPresetChecks, StepPresetReview, StepPresetRepair,
	StepPresetFinalize, StepArchived, StepAbandoned,
}

// Steps returns a path's vocabulary in canonical order.
func Steps(p Path) []Step {
	if p == PathFull {
		return append([]Step(nil), fullSteps...)
	}
	return append([]Step(nil), presetSteps...)
}

// index returns a step's position in its path's canonical order, or -1.
func index(p Path, s Step) int {
	for i, step := range Steps(p) {
		if step == s {
			return i
		}
	}
	return -1
}

// firstStep is where a confirmed change starts. Full begins by exploring;
// a preset begins with the host writing its one input document, because a
// preset's whole claim is that it already knows what it is doing.
func firstStep(p Path) string {
	if p == PathFull {
		return string(StepOpenExplore)
	}
	return string(StepPresetOpenDraft)
}

// terminalStep reports whether a change at this step has finished.
func terminalStep(p Path, step string) bool {
	s := Step(step)
	return s == StepArchived || s == StepAbandoned
}

// KnownStep reports whether step belongs to the path's vocabulary.
func KnownStep(p Path, step Step) bool { return index(p, step) >= 0 }

// Phase returns the artifact-ownership phase a step belongs to. It is what
// the ownership table is consulted with, so a step in the wrong phase
// would hand a document to the wrong writer.
func Phase(p Path, s Step) (artifact.Phase, error) {
	switch s {
	case StepOpenExplore, StepOpenChallenge, StepOpenDraft, StepOpenApprove,
		StepPresetOpenDraft, StepPresetReproduce, StepPresetScope:
		return artifact.PhaseOpen, nil
	case StepDesignDraft, StepDesignChallenge, StepDesignApprove:
		return artifact.PhaseDesign, nil
	case StepBuildPlan, StepBuildImplement, StepBuildIntegrate, StepBuildRepair,
		StepPresetImplement, StepPresetIntegrate, StepPresetRepair:
		return artifact.PhaseBuild, nil
	case StepVerifyChecks, StepVerifyReview, StepVerifyRecord,
		StepPresetChecks, StepPresetReview:
		return artifact.PhaseVerify, nil
	case StepCloseADR, StepCloseFinalize, StepPresetFinalize:
		return artifact.PhaseClose, nil
	case StepArchived, StepAbandoned:
		return "", fmt.Errorf("change: step %s is terminal and has no phase", s)
	}
	return "", fmt.Errorf("change: step %q is not a known step of the %s path", s, p)
}
