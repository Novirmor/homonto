package change

import (
	"errors"
	"testing"

	"github.com/noviopenworks/homonto/internal/artifact"
)

func TestAdvanceFullCoversEveryValidTransition(t *testing.T) {
	tests := []struct {
		from  Step
		event Event
		want  Step
	}{
		{StepOpenExplore, EventExplorersDone, StepOpenChallenge},
		{StepOpenChallenge, EventChallengeDone, StepOpenDraft},
		{StepOpenDraft, EventProposalDrafted, StepOpenApprove},
		{StepOpenApprove, EventScopeApproved, StepDesignDraft},
		{StepOpenApprove, EventScopeRejected, StepOpenDraft},
		{StepDesignDraft, EventDesignDrafted, StepDesignChallenge},
		{StepDesignChallenge, EventDesignChallenged, StepDesignApprove},
		{StepDesignApprove, EventDesignApproved, StepBuildPlan},
		{StepDesignApprove, EventDesignRejected, StepDesignDraft},
		{StepBuildPlan, EventPlanDrafted, StepBuildImplement},
		{StepBuildImplement, EventImplementersDone, StepBuildIntegrate},
		{StepBuildIntegrate, EventIntegrated, StepVerifyChecks},
		{StepVerifyChecks, EventChecksPassed, StepVerifyReview},
		{StepVerifyChecks, EventChecksFailed, StepBuildRepair},
		{StepVerifyReview, EventReviewClean, StepVerifyRecord},
		{StepVerifyReview, EventReviewBlocked, StepBuildRepair},
		{StepVerifyRecord, EventVerificationRecorded, StepCloseADR},
		{StepBuildRepair, EventRepairDone, StepBuildIntegrate},
		{StepBuildRepair, EventRepairContinued, StepBuildIntegrate},
		{StepBuildRepair, EventRepairLimitReached, StepBuildRepair},
		{StepCloseADR, EventADRsWritten, StepCloseFinalize},
		{StepCloseADR, EventDecisionDiscovered, StepDesignDraft},
		{StepCloseFinalize, EventFinalized, StepArchived},
	}
	for _, tt := range tests {
		got, err := AdvanceFull(tt.from, tt.event)
		if err != nil {
			t.Errorf("AdvanceFull(%s, %s): %v", tt.from, tt.event, err)
			continue
		}
		if got != tt.want {
			t.Errorf("AdvanceFull(%s, %s) = %s, want %s", tt.from, tt.event, got, tt.want)
		}
	}
}

func TestAdvancePresetCoversEveryValidTransition(t *testing.T) {
	tests := []struct {
		path  Path
		from  Step
		event Event
		want  Step
	}{
		// Fix proves the defect before implementing it.
		{PathFix, StepPresetOpenDraft, EventPresetDrafted, StepPresetReproduce},
		{PathFix, StepPresetReproduce, EventReproductionRecorded, StepPresetScope},
		// Tweak has no defect to reproduce.
		{PathTweak, StepPresetOpenDraft, EventPresetDrafted, StepPresetScope},
		{PathTweak, StepPresetScope, EventScopeClear, StepPresetImplement},
		{PathFix, StepPresetScope, EventScopeContinued, StepPresetImplement},
		{PathTweak, StepPresetImplement, EventPresetImplemented, StepPresetIntegrate},
		{PathTweak, StepPresetIntegrate, EventPresetIntegrated, StepPresetChecks},
		{PathTweak, StepPresetChecks, EventChecksPassed, StepPresetReview},
		{PathTweak, StepPresetChecks, EventChecksFailed, StepPresetRepair},
		{PathTweak, StepPresetReview, EventReviewClean, StepPresetFinalize},
		{PathTweak, StepPresetReview, EventReviewBlocked, StepPresetRepair},
		{PathTweak, StepPresetRepair, EventRepairDone, StepPresetIntegrate},
		{PathTweak, StepPresetRepair, EventRepairLimitReached, StepPresetRepair},
		{PathTweak, StepPresetFinalize, EventFinalized, StepArchived},
	}
	for _, tt := range tests {
		got, err := AdvancePreset(tt.path, tt.from, tt.event)
		if err != nil {
			t.Errorf("AdvancePreset(%s, %s, %s): %v", tt.path, tt.from, tt.event, err)
			continue
		}
		if got != tt.want {
			t.Errorf("AdvancePreset(%s, %s, %s) = %s, want %s", tt.path, tt.from, tt.event, got, tt.want)
		}
	}
}

// TestTweakNeverReproduces proves the one place the two presets differ:
// Tweak has no defect, so asking it for a reproduction would be asking for
// evidence of nothing.
func TestTweakNeverReproduces(t *testing.T) {
	if _, err := AdvancePreset(PathTweak, StepPresetReproduce, EventReproductionRecorded); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("a tweak reproduced: %v", err)
	}
	got, err := AdvancePreset(PathTweak, StepPresetOpenDraft, EventPresetDrafted)
	if err != nil {
		t.Fatalf("AdvancePreset: %v", err)
	}
	if got == StepPresetReproduce {
		t.Fatal("a tweak was routed through reproduction")
	}
}

// TestFixCannotSkipReproduction is the other half: a defect is not fixed
// until it has been shown to exist.
func TestFixCannotSkipReproduction(t *testing.T) {
	got, err := AdvancePreset(PathFix, StepPresetOpenDraft, EventPresetDrafted)
	if err != nil {
		t.Fatalf("AdvancePreset: %v", err)
	}
	if got != StepPresetReproduce {
		t.Fatalf("a fix went to %s without reproducing the defect", got)
	}
	if _, err := AdvancePreset(PathFix, StepPresetOpenDraft, EventScopeClear); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("a fix skipped straight to the scope assessment: %v", err)
	}
}

func TestAbandonIsAlwaysAvailableUntilTerminal(t *testing.T) {
	for _, path := range []Path{PathFull, PathFix, PathTweak} {
		for _, step := range Steps(path) {
			got, err := Advance(path, step, EventAbandon)
			if terminalStep(path, string(step)) {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Errorf("%s/%s accepted abandon after finishing: %v", path, step, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("Advance(%s, %s, abandon): %v", path, step, err)
				continue
			}
			if got != StepAbandoned {
				t.Errorf("Advance(%s, %s, abandon) = %s", path, step, got)
			}
		}
	}
}

// TestPathsDoNotShareAVocabulary proves a preset step is not reachable
// through the Full machine and vice versa, so the two can never quietly
// merge.
func TestPathsDoNotShareAVocabulary(t *testing.T) {
	for _, step := range presetSteps {
		if step == StepArchived || step == StepAbandoned {
			continue
		}
		if _, err := AdvanceFull(step, EventPresetDrafted); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("the full machine accepted the preset step %s", step)
		}
	}
	for _, step := range fullSteps {
		if step == StepArchived || step == StepAbandoned {
			continue
		}
		if _, err := AdvancePreset(PathTweak, step, EventProposalDrafted); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("the preset machine accepted the full step %s", step)
		}
	}
	if _, err := AdvancePreset(PathFull, StepPresetOpenDraft, EventPresetDrafted); !errors.Is(err, ErrInvalidTransition) {
		t.Error("AdvancePreset accepted the full path")
	}
}

func TestAdvanceRefusesEverythingElse(t *testing.T) {
	events := []Event{
		EventExplorersDone, EventChallengeDone, EventProposalDrafted, EventScopeApproved,
		EventScopeRejected, EventDesignDrafted, EventDesignChallenged, EventDesignApproved,
		EventDesignRejected, EventPlanDrafted, EventImplementersDone, EventIntegrated,
		EventChecksPassed, EventChecksFailed, EventReviewClean, EventReviewBlocked,
		EventVerificationRecorded, EventRepairDone, EventRepairLimitReached,
		EventRepairContinued, EventADRsWritten, EventDecisionDiscovered, EventFinalized,
		EventPresetDrafted, EventReproductionRecorded, EventScopeClear, EventScopeContinued,
		EventPresetImplemented, EventPresetIntegrated,
	}
	// Every accepted move must land on a step of the same path; every
	// refusal must be typed.
	for _, path := range []Path{PathFull, PathFix, PathTweak} {
		for _, step := range Steps(path) {
			for _, event := range events {
				next, err := Advance(path, step, event)
				if err != nil {
					if !errors.Is(err, ErrInvalidTransition) {
						t.Errorf("Advance(%s, %s, %s) returned an untyped error: %v", path, step, event, err)
					}
					continue
				}
				if index(path, next) < 0 {
					t.Errorf("Advance(%s, %s, %s) = %s, which is not a step of that path",
						path, step, event, next)
				}
			}
		}
	}
	if _, err := Advance(PathFull, Step("open_something"), EventExplorersDone); !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("an unknown step was accepted: %v", err)
	}
}

// TestEveryStepIsReachable guards against a step that exists in a
// vocabulary but that no transition can ever reach.
func TestEveryStepIsReachable(t *testing.T) {
	events := []Event{
		EventExplorersDone, EventChallengeDone, EventProposalDrafted, EventScopeApproved,
		EventScopeRejected, EventDesignDrafted, EventDesignChallenged, EventDesignApproved,
		EventDesignRejected, EventPlanDrafted, EventImplementersDone, EventIntegrated,
		EventChecksPassed, EventChecksFailed, EventReviewClean, EventReviewBlocked,
		EventVerificationRecorded, EventRepairDone, EventRepairLimitReached,
		EventRepairContinued, EventADRsWritten, EventDecisionDiscovered, EventFinalized,
		EventPresetDrafted, EventReproductionRecorded, EventScopeClear, EventScopeContinued,
		EventPresetImplemented, EventPresetIntegrated, EventAbandon,
	}
	for _, path := range []Path{PathFull, PathFix, PathTweak} {
		reached := map[Step]bool{Step(firstStep(path)): true}
		for changed := true; changed; {
			changed = false
			for _, step := range Steps(path) {
				if !reached[step] {
					continue
				}
				for _, event := range events {
					next, err := Advance(path, step, event)
					if err != nil || reached[next] {
						continue
					}
					reached[next] = true
					changed = true
				}
			}
		}
		for _, step := range Steps(path) {
			// Tweak legitimately never reproduces.
			if path == PathTweak && step == StepPresetReproduce {
				if reached[step] {
					t.Errorf("tweak reached the reproduction step")
				}
				continue
			}
			if !reached[step] {
				t.Errorf("%s step %s is unreachable", path, step)
			}
		}
	}
}

func TestStepPhases(t *testing.T) {
	tests := map[Step]artifact.Phase{
		StepOpenExplore:     artifact.PhaseOpen,
		StepOpenDraft:       artifact.PhaseOpen,
		StepOpenApprove:     artifact.PhaseOpen,
		StepDesignDraft:     artifact.PhaseDesign,
		StepDesignApprove:   artifact.PhaseDesign,
		StepBuildPlan:       artifact.PhaseBuild,
		StepBuildImplement:  artifact.PhaseBuild,
		StepBuildRepair:     artifact.PhaseBuild,
		StepVerifyChecks:    artifact.PhaseVerify,
		StepVerifyRecord:    artifact.PhaseVerify,
		StepCloseADR:        artifact.PhaseClose,
		StepCloseFinalize:   artifact.PhaseClose,
		StepPresetOpenDraft: artifact.PhaseOpen,
		StepPresetScope:     artifact.PhaseOpen,
		StepPresetImplement: artifact.PhaseBuild,
		StepPresetChecks:    artifact.PhaseVerify,
		StepPresetFinalize:  artifact.PhaseClose,
	}
	for step, want := range tests {
		got, err := Phase(PathFull, step)
		if err != nil {
			t.Errorf("Phase(%s): %v", step, err)
			continue
		}
		if got != want {
			t.Errorf("Phase(%s) = %s, want %s", step, got, want)
		}
	}
	for _, step := range []Step{StepArchived, StepAbandoned} {
		if _, err := Phase(PathFull, step); err == nil {
			t.Errorf("Phase(%s) = nil error, want a refusal for a terminal step", step)
		}
	}
}

// TestGeneratedDocumentsAreBinaryOwnedInTheirPhase is a cross-check
// between the step vocabulary and the artifact ownership table: every step
// must land in a phase where the document it is about is writable by the
// right party.
func TestGeneratedDocumentsAreBinaryOwnedInTheirPhase(t *testing.T) {
	cases := []struct {
		step  Step
		kind  artifact.Kind
		owner artifact.Owner
	}{
		{StepOpenDraft, artifact.KindProposal, artifact.OwnerHost},
		{StepDesignDraft, artifact.KindDesign, artifact.OwnerHost},
		{StepDesignDraft, artifact.KindTasks, artifact.OwnerHost},
		{StepBuildPlan, artifact.KindPlan, artifact.OwnerHost},
		{StepBuildImplement, artifact.KindTasks, artifact.OwnerBinary},
		{StepVerifyRecord, artifact.KindVerification, artifact.OwnerBinary},
		{StepCloseFinalize, artifact.KindRecord, artifact.OwnerBinary},
		{StepCloseADR, artifact.KindADR, artifact.OwnerImplementer},
		{StepPresetOpenDraft, artifact.KindFix, artifact.OwnerHost},
		{StepPresetOpenDraft, artifact.KindTweak, artifact.OwnerHost},
		{StepPresetOpenDraft, artifact.KindTasks, artifact.OwnerHost},
		{StepPresetImplement, artifact.KindTasks, artifact.OwnerBinary},
		{StepPresetChecks, artifact.KindVerification, artifact.OwnerBinary},
		{StepPresetFinalize, artifact.KindRecord, artifact.OwnerBinary},
	}
	for _, c := range cases {
		phase, err := Phase(PathFull, c.step)
		if err != nil {
			t.Errorf("Phase(%s): %v", c.step, err)
			continue
		}
		owner, _, ok := artifact.Ownership(c.kind, phase)
		if !ok {
			t.Errorf("%s is in phase %s, where nobody may write %s", c.step, phase, c.kind)
			continue
		}
		if owner != c.owner {
			t.Errorf("%s (%s): %s is owned by the %s, want the %s", c.step, phase, c.kind, owner, c.owner)
		}
	}
}

func FuzzAdvance(f *testing.F) {
	f.Add("full", "open_explore", "explorers_done")
	f.Add("tweak", "preset_scope", "scope_clear")
	f.Add("", "", "")
	f.Fuzz(func(t *testing.T, path, step, event string) {
		next, err := Advance(Path(path), Step(step), Event(event))
		if err != nil {
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("untyped transition error: %v", err)
			}
			return
		}
		p := Path(path)
		if index(p, next) < 0 {
			t.Fatalf("Advance(%q, %q, %q) = %q, not a step of that path", path, step, event, next)
		}
		if terminalStep(p, step) {
			t.Fatalf("Advance accepted a move out of the terminal step %q", step)
		}
	})
}
