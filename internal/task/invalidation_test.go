package task

import (
	"testing"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

func digest(s string) fingerprint.Digest { return fingerprint.Bytes("test", []byte(s)) }

func baseline() Baseline {
	return Baseline{
		Document:    digest("doc"),
		Membership:  digest("members"),
		PathClass:   digest("paths"),
		CheckConfig: digest("checks"),
		Sources:     []fingerprint.Digest{digest("src")},
	}
}

func causes(invs []Invalidation) map[Cause]Invalidation {
	out := make(map[Cause]Invalidation, len(invs))
	for _, inv := range invs {
		out[inv.Cause] = inv
	}
	return out
}

func TestCompareDetectsEachCause(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Baseline)
		cause    Cause
		returnTo Step
	}{
		{"goal or checklist", func(b *Baseline) { b.Document = digest("edited") }, CauseDocument, StepPlanDraft},
		{"membership", func(b *Baseline) { b.Membership = digest("more members") }, CauseMembership, StepPlanExplore},
		{"path classes", func(b *Baseline) { b.PathClass = digest("new globs") }, CausePathClass, StepPlanExplore},
		{"check configuration", func(b *Baseline) { b.CheckConfig = digest("new commands") }, CauseCheckConfig, StepDoneChecks},
		{"integrated sources", func(b *Baseline) { b.Sources = []fingerprint.Digest{digest("rebased")} }, CauseSource, StepDoneChecks},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := baseline()
			tt.mutate(&now)
			invs := Compare(StepDoneFinalize, baseline(), now)
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
			if Target(StepDoneFinalize, invs) != tt.returnTo {
				t.Fatalf("Target = %s, want %s", Target(StepDoneFinalize, invs), tt.returnTo)
			}
		})
	}
}

func TestCompareIgnoresDriftAheadOfTheCurrentStep(t *testing.T) {
	// A source fingerprint moving while the task is still planning
	// invalidates nothing: there is no check, report, or completion result
	// yet for it to invalidate.
	now := baseline()
	now.Sources = []fingerprint.Digest{digest("moved")}
	if invs := Compare(StepPlanExplore, baseline(), now); len(invs) != 0 {
		t.Fatalf("Compare = %+v, want nothing to invalidate", invs)
	}
	// The same drift at review time does invalidate.
	if invs := Compare(StepDoneReview, baseline(), now); len(invs) != 1 {
		t.Fatalf("Compare = %+v, want the source invalidation", invs)
	}
}

func TestCompareReturnsToTheEarliestAffectedStep(t *testing.T) {
	now := baseline()
	now.Document = digest("edited")
	now.CheckConfig = digest("new commands")
	now.Sources = []fingerprint.Digest{digest("rebased")}
	invs := Compare(StepDoneFinalize, baseline(), now)
	if len(invs) != 3 {
		t.Fatalf("Compare = %+v, want three causes", invs)
	}
	if got := Target(StepDoneFinalize, invs); got != StepPlanDraft {
		t.Fatalf("Target = %s, want the earliest affected step plan_draft", got)
	}
}

func TestCompareIsQuietWhenNothingMoved(t *testing.T) {
	if invs := Compare(StepDoneReview, baseline(), baseline()); len(invs) != 0 {
		t.Fatalf("Compare of an unchanged world = %+v, want nothing", invs)
	}
	if got := Target(StepDoneReview, nil); got != StepDoneReview {
		t.Fatalf("Target with no invalidations = %s, want the current step", got)
	}
}

// TestUnrecordedSourcesAreNotDrift proves a task that has integrated
// nothing is not sent backwards merely because the world has sources.
func TestUnrecordedSourcesAreNotDrift(t *testing.T) {
	was := baseline()
	was.Sources = nil
	now := baseline()
	if invs := Compare(StepDoneReview, was, now); len(invs) != 0 {
		t.Fatalf("Compare = %+v, want nothing: the baseline recorded no sources", invs)
	}
}

// TestSourceDriftDoesNotInvalidateThePlan pins the spec's exception: the
// goal did not change just because the code did.
func TestSourceDriftDoesNotInvalidateThePlan(t *testing.T) {
	now := baseline()
	now.Sources = []fingerprint.Digest{digest("rebased")}
	invs := Compare(StepDoneReview, baseline(), now)
	for _, inv := range invs {
		if inv.ReturnTo.index() < StepDoneChecks.index() {
			t.Fatalf("a source change returned the workflow to %s", inv.ReturnTo)
		}
	}
}

func TestCompareCountsSourceListLengthChanges(t *testing.T) {
	now := baseline()
	now.Sources = append(now.Sources, digest("another member"))
	invs := Compare(StepDoneReview, baseline(), now)
	if len(invs) != 1 || invs[0].Cause != CauseSource {
		t.Fatalf("Compare = %+v, want one source invalidation", invs)
	}
}
