package task

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// Cause names which input moved under a recorded step.
type Cause string

const (
	// CauseDocument: the task's goal or checklist changed. It invalidates
	// Plan's outputs and every later assignment, check, report, and
	// completion result — everything downstream was reasoning about a
	// different task.
	CauseDocument Cause = "document"
	// CauseMembership: the confirmed repository list changed. The
	// explorers and the skeptic must assess the complete workspace again.
	CauseMembership Cause = "membership"
	// CausePathClass: the test/generated/vendored classification changed,
	// so the scopes assignments were issued against are not the current
	// ones.
	CausePathClass Cause = "path_class"
	// CauseCheckConfig: the verification configuration changed, so the
	// recorded checks are not the configured checks.
	CauseCheckConfig Cause = "check_config"
	// CauseSource: an integrated source fingerprint changed. It
	// invalidates checks, the reviewer and skeptic reports, and the
	// completion result — but NOT the plan: the goal did not change just
	// because the code did.
	CauseSource Cause = "source"
)

// Invalidation is one input that moved, what it invalidates, and where the
// workflow must return to because of it.
type Invalidation struct {
	Cause  Cause  `json:"cause"`
	Detail string `json:"detail"`
	// ReturnTo is the earliest step this cause forces a return to.
	ReturnTo Step `json:"return_to"`
	// Evidence is what this cause invalidates, for the operator's benefit.
	Evidence []string `json:"evidence"`
}

// returnTo is the invalidation graph, spelled out. It is the same switch
// discipline as the transitions: the graph is readable here and nowhere
// else.
func returnTo(cause Cause) (Step, []string) {
	switch cause {
	case CauseDocument:
		return StepPlanDraft, []string{
			"plan outputs", "assignments", "checks", "reports", "completion",
		}
	case CauseMembership:
		return StepPlanExplore, []string{
			"assignments", "checks", "reports", "approvals", "completion",
		}
	case CausePathClass:
		return StepPlanExplore, []string{
			"scopes", "assignments", "checks", "reports", "completion",
		}
	case CauseCheckConfig:
		return StepDoneChecks, []string{
			"checks", "final reports", "verification", "completion",
		}
	case CauseSource:
		return StepDoneChecks, []string{
			"checks", "reviewer report", "skeptic report", "verification", "completion",
		}
	}
	return "", nil
}

// Compare returns the invalidations between the baseline a step rests on
// and the world as it is now, in canonical cause order so the same drift
// always reports identically.
//
// A cause whose return step is at or after the current step changes
// nothing: evidence that has not been produced yet cannot be invalid.
// Every other cause is reported, and the caller returns to the EARLIEST of
// them — the spec's "return to the earliest affected phase rather than
// trusting the recorded phase".
func Compare(current Step, was, now Baseline) []Invalidation {
	var out []Invalidation
	add := func(cause Cause, detail string) {
		step, evidence := returnTo(cause)
		if step == "" {
			return
		}
		// Nothing to invalidate if the workflow has not reached the step
		// this cause would send it back to.
		if step.index() >= current.index() {
			return
		}
		out = append(out, Invalidation{
			Cause: cause, Detail: detail, ReturnTo: step, Evidence: evidence,
		})
	}
	if was.Document != now.Document {
		add(CauseDocument, describe("the task's goal or checklist", was.Document, now.Document))
	}
	if was.Membership != now.Membership {
		add(CauseMembership, describe("the confirmed repository list", was.Membership, now.Membership))
	}
	if was.PathClass != now.PathClass {
		add(CausePathClass, describe("the path classification", was.PathClass, now.PathClass))
	}
	if was.CheckConfig != now.CheckConfig {
		add(CauseCheckConfig, describe("the verification configuration", was.CheckConfig, now.CheckConfig))
	}
	// Sources are only comparable once a baseline has recorded them: a
	// task that has not integrated anything yet has no integrated sources,
	// and finding some in the world is not drift.
	if len(was.Sources) > 0 {
		if detail, moved := sourcesMoved(was.Sources, now.Sources); moved {
			add(CauseSource, detail)
		}
	}
	return out
}

// Target returns the step the workflow must return to given a set of
// invalidations: the earliest of them, or the current step when there are
// none.
func Target(current Step, invalidations []Invalidation) Step {
	target := current
	for _, inv := range invalidations {
		target = earliest(target, inv.ReturnTo)
	}
	return target
}

// describe renders one moved fingerprint.
func describe(what string, was, now fingerprint.Digest) string {
	return fmt.Sprintf("%s moved from %s to %s", what, short(was), short(now))
}

// sourcesMoved compares two source-fingerprint lists.
func sourcesMoved(was, now []fingerprint.Digest) (string, bool) {
	if len(was) != len(now) {
		return fmt.Sprintf("the integrated sources changed from %d fingerprint(s) to %d",
			len(was), len(now)), true
	}
	for i := range was {
		if was[i] != now[i] {
			return describe("an integrated source fingerprint", was[i], now[i]), true
		}
	}
	return "", false
}

// short abbreviates a digest for a message. An empty digest is spelled out
// rather than shown as nothing, because "moved from  to abc" reads as a
// bug in the message rather than as a baseline that was never recorded.
func short(d fingerprint.Digest) string {
	if d == "" {
		return "(unrecorded)"
	}
	if len(d) <= 12 {
		return string(d)
	}
	return string(d[:12])
}
