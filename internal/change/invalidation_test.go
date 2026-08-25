package change

import (
	"testing"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/fingerprint"
)

func digest(s string) fingerprint.Digest { return fingerprint.Bytes("test", []byte(s)) }

func fullBaseline() Baseline {
	return Baseline{
		Documents: map[artifact.Kind]fingerprint.Digest{
			artifact.KindProposal: digest("proposal"),
			artifact.KindDesign:   digest("design"),
			artifact.KindTasks:    digest("tasks"),
			artifact.KindPlan:     digest("plan"),
		},
		Verification: digest("verification"),
		Membership:   digest("members"),
		PathClass:    digest("paths"),
		CheckConfig:  digest("checks"),
		Sources:      []fingerprint.Digest{digest("src")},
	}
}

func presetBaseline() Baseline {
	return Baseline{
		Documents: map[artifact.Kind]fingerprint.Digest{
			artifact.KindFix:   digest("fix"),
			artifact.KindTasks: digest("tasks"),
		},
		Verification: digest("verification"),
		Membership:   digest("members"),
		PathClass:    digest("paths"),
		CheckConfig:  digest("checks"),
		Sources:      []fingerprint.Digest{digest("src")},
	}
}

func causes(invs []Invalidation) map[Cause]Invalidation {
	out := make(map[Cause]Invalidation, len(invs))
	for _, inv := range invs {
		out[inv.Cause] = inv
	}
	return out
}

// withDocument returns a baseline with one document's digest replaced.
func withDocument(b Baseline, kind artifact.Kind, d fingerprint.Digest) Baseline {
	docs := map[artifact.Kind]fingerprint.Digest{}
	for k, v := range b.Documents {
		docs[k] = v
	}
	if d == "" {
		delete(docs, kind)
	} else {
		docs[kind] = d
	}
	b.Documents = docs
	return b
}

func TestFullInvalidationGraph(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(Baseline) Baseline
		cause    Cause
		returnTo Step
	}{
		{"proposal", func(b Baseline) Baseline {
			return withDocument(b, artifact.KindProposal, digest("rewritten"))
		}, CauseProposal, StepOpenDraft},
		{"design", func(b Baseline) Baseline {
			return withDocument(b, artifact.KindDesign, digest("rewritten"))
		}, CauseDesign, StepDesignDraft},
		{"tasks", func(b Baseline) Baseline {
			return withDocument(b, artifact.KindTasks, digest("rewritten"))
		}, CauseTasks, StepDesignDraft},
		{"plan", func(b Baseline) Baseline {
			return withDocument(b, artifact.KindPlan, digest("rewritten"))
		}, CausePlan, StepBuildPlan},
		{"membership", func(b Baseline) Baseline {
			b.Membership = digest("more members")
			return b
		}, CauseMembership, StepOpenExplore},
		{"path classes", func(b Baseline) Baseline {
			b.PathClass = digest("new globs")
			return b
		}, CausePathClass, StepOpenExplore},
		{"check configuration", func(b Baseline) Baseline {
			b.CheckConfig = digest("new commands")
			return b
		}, CauseCheckConfig, StepVerifyChecks},
		{"integrated sources", func(b Baseline) Baseline {
			b.Sources = []fingerprint.Digest{digest("rebased")}
			return b
		}, CauseSource, StepVerifyChecks},
		{"verification", func(b Baseline) Baseline {
			b.Verification = digest("re-run")
			return b
		}, CauseVerification, StepCloseADR},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			invs := Compare(PathFull, StepCloseFinalize, fullBaseline(), tt.mutate(fullBaseline()))
			got, ok := causes(invs)[tt.cause]
			if !ok {
				t.Fatalf("Compare = %+v, want a %q cause", invs, tt.cause)
			}
			if got.ReturnTo != tt.returnTo {
				t.Fatalf("%q returns to %s, want %s", tt.cause, got.ReturnTo, tt.returnTo)
			}
			if got.Detail == "" || len(got.Evidence) == 0 {
				t.Fatalf("invalidation does not explain itself: %+v", got)
			}
			if Target(PathFull, StepCloseFinalize, invs) != tt.returnTo {
				t.Fatalf("Target = %s, want %s", Target(PathFull, StepCloseFinalize, invs), tt.returnTo)
			}
		})
	}
}

// TestSourceDriftDoesNotInvalidateApprovals pins the spec's exception: the
// proposal did not change just because the code did.
func TestSourceDriftDoesNotInvalidateApprovals(t *testing.T) {
	now := fullBaseline()
	now.Sources = []fingerprint.Digest{digest("rebased")}
	invs := Compare(PathFull, StepCloseFinalize, fullBaseline(), now)
	for _, inv := range invs {
		if index(PathFull, inv.ReturnTo) < index(PathFull, StepVerifyChecks) {
			t.Fatalf("a source change returned the change to %s", inv.ReturnTo)
		}
	}
}

// TestVerificationDriftInvalidatesCloseOnly pins the other narrow rule.
func TestVerificationDriftInvalidatesCloseOnly(t *testing.T) {
	now := fullBaseline()
	now.Verification = digest("re-run")
	invs := Compare(PathFull, StepCloseFinalize, fullBaseline(), now)
	if len(invs) != 1 || invs[0].Cause != CauseVerification {
		t.Fatalf("Compare = %+v, want only the verification cause", invs)
	}
	if invs[0].ReturnTo != StepCloseADR {
		t.Fatalf("verification returned the change to %s, want close", invs[0].ReturnTo)
	}
}

// TestPresetInputInvalidatesThePathConfirmation proves editing fix.md,
// tweak.md, or a preset tasks.md reruns the whole preset assessment.
func TestPresetInputInvalidatesThePathConfirmation(t *testing.T) {
	for _, kind := range []artifact.Kind{artifact.KindFix, artifact.KindTasks} {
		now := withDocument(presetBaseline(), kind, digest("rewritten"))
		invs := Compare(PathFix, StepPresetFinalize, presetBaseline(), now)
		got, ok := causes(invs)[CausePresetInput]
		if !ok {
			t.Fatalf("%s: Compare = %+v, want a preset-input cause", kind, invs)
		}
		if got.ReturnTo != StepPresetOpenDraft {
			t.Fatalf("%s returned to %s, want the preset draft", kind, got.ReturnTo)
		}
	}
}

// TestPresetTasksAreNotADesignOutput proves the same document invalidates
// very different amounts depending on the path it belongs to.
func TestPresetTasksAreNotADesignOutput(t *testing.T) {
	fullNow := withDocument(fullBaseline(), artifact.KindTasks, digest("rewritten"))
	fullInvs := Compare(PathFull, StepCloseFinalize, fullBaseline(), fullNow)
	if causes(fullInvs)[CauseTasks].ReturnTo != StepDesignDraft {
		t.Fatalf("a full tasks.md returned to %s, want the design draft",
			causes(fullInvs)[CauseTasks].ReturnTo)
	}
	presetNow := withDocument(presetBaseline(), artifact.KindTasks, digest("rewritten"))
	presetInvs := Compare(PathTweak, StepPresetFinalize, presetBaseline(), presetNow)
	if causes(presetInvs)[CauseTasks].ReturnTo != "" {
		t.Fatal("a preset tasks.md fired the full path's tasks cause")
	}
	if causes(presetInvs)[CausePresetInput].ReturnTo != StepPresetOpenDraft {
		t.Fatalf("a preset tasks.md did not fire the preset-input cause: %+v", presetInvs)
	}
}

func TestCompareIgnoresDriftAheadOfTheCurrentStep(t *testing.T) {
	now := fullBaseline()
	now.Sources = []fingerprint.Digest{digest("moved")}
	if invs := Compare(PathFull, StepOpenExplore, fullBaseline(), now); len(invs) != 0 {
		t.Fatalf("Compare = %+v, want nothing to invalidate before anything was built", invs)
	}
	if invs := Compare(PathFull, StepVerifyReview, fullBaseline(), now); len(invs) != 1 {
		t.Fatalf("Compare = %+v, want the source invalidation at review time", invs)
	}
}

func TestCompareReturnsToTheEarliestAffectedStep(t *testing.T) {
	now := withDocument(fullBaseline(), artifact.KindProposal, digest("rewritten"))
	now.CheckConfig = digest("new commands")
	now.Sources = []fingerprint.Digest{digest("rebased")}
	invs := Compare(PathFull, StepCloseFinalize, fullBaseline(), now)
	if len(invs) != 3 {
		t.Fatalf("Compare = %+v, want three causes", invs)
	}
	if got := Target(PathFull, StepCloseFinalize, invs); got != StepOpenDraft {
		t.Fatalf("Target = %s, want the earliest affected step", got)
	}
}

// TestACreatedDocumentMovesTheBaseline proves absence is a value: a design
// that did not exist and now does changes what everything after it rests
// on.
func TestACreatedDocumentMovesTheBaseline(t *testing.T) {
	was := withDocument(fullBaseline(), artifact.KindDesign, "")
	invs := Compare(PathFull, StepCloseFinalize, was, fullBaseline())
	got, ok := causes(invs)[CauseDesign]
	if !ok {
		t.Fatalf("Compare = %+v, want a design cause", invs)
	}
	if got.Detail == "" {
		t.Fatal("the created document was not explained")
	}
}

func TestCompareIsQuietWhenNothingMoved(t *testing.T) {
	if invs := Compare(PathFull, StepVerifyReview, fullBaseline(), fullBaseline()); len(invs) != 0 {
		t.Fatalf("Compare of an unchanged world = %+v, want nothing", invs)
	}
	if got := Target(PathFull, StepVerifyReview, nil); got != StepVerifyReview {
		t.Fatalf("Target with no invalidations = %s, want the current step", got)
	}
}

func TestUnrecordedSourcesAndVerificationAreNotDrift(t *testing.T) {
	was := fullBaseline()
	was.Sources = nil
	was.Verification = ""
	if invs := Compare(PathFull, StepCloseFinalize, was, fullBaseline()); len(invs) != 0 {
		t.Fatalf("Compare = %+v, want nothing: neither was ever recorded", invs)
	}
}
