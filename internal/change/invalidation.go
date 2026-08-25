package change

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// Cause names which input moved under a recorded step.
type Cause string

const (
	// CauseProposal: proposal.md changed. It invalidates the scope
	// approval and everything from Design onward — the approval was of a
	// document that no longer says what it said.
	CauseProposal Cause = "proposal"
	// CauseDesign: design.md changed. It invalidates the design approval,
	// tasks, plan, Build, Verify, and Close.
	CauseDesign Cause = "design"
	// CauseTasks: tasks.md changed. It invalidates the design approval and
	// returns to Design, because tasks are a design output and a change to
	// them is a change to the design that produced them.
	CauseTasks Cause = "tasks"
	// CausePlan: plan.md changed. It invalidates the affected Build
	// assignments and every later piece of evidence, but not the design
	// the plan implements.
	CausePlan Cause = "plan"
	// CausePresetInput: fix.md, tweak.md, or a preset tasks.md changed. It
	// invalidates the path confirmation, reruns the preset scope
	// assessment, and invalidates Build through Close.
	CausePresetInput Cause = "preset_input"
	// CauseMembership: the confirmed repository list changed. Explorers
	// and the skeptic must assess the complete workspace again.
	CauseMembership Cause = "membership"
	// CausePathClass: the test/generated/vendored classification changed,
	// so the scopes assignments were issued against are not the current
	// ones and the preset scope count was measured with the wrong ruler.
	CausePathClass Cause = "path_class"
	// CauseCheckConfig: the verification configuration changed, so the
	// recorded checks are not the configured checks.
	CauseCheckConfig Cause = "check_config"
	// CauseSource: an integrated source fingerprint changed. It
	// invalidates checks, the reviewer and skeptic reports, verification,
	// and completion — but NOT the approved requirements or design: the
	// proposal did not change just because the code did.
	CauseSource Cause = "source"
	// CauseVerification: the recorded verification evidence or a finding
	// resolution changed. It invalidates Close and nothing before it.
	CauseVerification Cause = "verification"
)

// Invalidation is one input that moved, what it invalidates, and where the
// change must return to because of it.
type Invalidation struct {
	Cause    Cause    `json:"cause"`
	Detail   string   `json:"detail"`
	ReturnTo Step     `json:"return_to"`
	Evidence []string `json:"evidence"`
}

// fullReturns is the Full path's invalidation graph, spelled out.
var fullReturns = map[Cause]struct {
	step     Step
	evidence []string
}{
	CauseProposal:     {StepOpenDraft, []string{"scope approval", "design", "tasks", "plan", "build", "verify", "close"}},
	CauseDesign:       {StepDesignDraft, []string{"design approval", "tasks", "plan", "build", "verify", "close"}},
	CauseTasks:        {StepDesignDraft, []string{"design approval", "plan", "build", "verify", "close"}},
	CausePlan:         {StepBuildPlan, []string{"build assignments", "checks", "reports", "verification", "close"}},
	CauseMembership:   {StepOpenExplore, []string{"assignments", "checks", "reports", "approvals", "completion"}},
	CausePathClass:    {StepOpenExplore, []string{"scopes", "assignments", "checks", "reports", "completion"}},
	CauseCheckConfig:  {StepVerifyChecks, []string{"checks", "final reports", "verification", "completion"}},
	CauseSource:       {StepVerifyChecks, []string{"checks", "reviewer report", "skeptic report", "verification", "completion"}},
	CauseVerification: {StepCloseADR, []string{"close"}},
}

// presetReturns is the Fix and Tweak invalidation graph.
var presetReturns = map[Cause]struct {
	step     Step
	evidence []string
}{
	CausePresetInput:  {StepPresetOpenDraft, []string{"path confirmation", "preset scope", "build", "verify", "close"}},
	CauseMembership:   {StepPresetExplore, []string{"assignments", "checks", "reports", "completion"}},
	CausePathClass:    {StepPresetScope, []string{"preset scope", "scopes", "assignments", "checks", "reports", "completion"}},
	CauseCheckConfig:  {StepPresetChecks, []string{"checks", "final reports", "verification", "completion"}},
	CauseSource:       {StepPresetChecks, []string{"checks", "reviewer report", "skeptic report", "verification", "completion"}},
	CauseVerification: {StepPresetADR, []string{"close"}},
}

// returnTo resolves a cause against a path's graph.
func returnTo(path Path, cause Cause) (Step, []string, bool) {
	table := presetReturns
	if path == PathFull {
		table = fullReturns
	}
	row, ok := table[cause]
	return row.step, row.evidence, ok
}

// documentCause maps a document kind to the cause its change fires, given
// the path. A preset's tasks.md is a preset input; a Full change's is a
// design output, and they invalidate very different amounts.
func documentCause(path Path, kind artifact.Kind) (Cause, bool) {
	if path.Preset() {
		switch kind {
		case artifact.KindFix, artifact.KindTweak, artifact.KindTasks:
			return CausePresetInput, true
		}
		return "", false
	}
	switch kind {
	case artifact.KindProposal:
		return CauseProposal, true
	case artifact.KindDesign:
		return CauseDesign, true
	case artifact.KindTasks:
		return CauseTasks, true
	case artifact.KindPlan:
		return CausePlan, true
	case artifact.KindPresetTasks:
		// A frozen preset tasks.md is a read-only input to an upgraded
		// change. It should never move; if it does, that is the same as
		// the preset input moving.
		return CausePresetInput, true
	}
	return "", false
}

// Compare returns the invalidations between the baseline a step rests on
// and the world as it is now, in canonical cause order so the same drift
// always reports identically.
//
// A cause whose return step is at or after the current step changes
// nothing: evidence that has not been produced yet cannot be invalid.
func Compare(path Path, current Step, was, now Baseline) []Invalidation {
	var out []Invalidation
	add := func(cause Cause, detail string) {
		step, evidence, ok := returnTo(path, cause)
		if !ok {
			return
		}
		if index(path, step) >= index(path, current) {
			return
		}
		out = append(out, Invalidation{
			Cause: cause, Detail: detail, ReturnTo: step, Evidence: evidence,
		})
	}
	seen := map[Cause]bool{}
	for _, kind := range hostDocumentKinds {
		before, after := was.Document(kind), now.Document(kind)
		if before == after {
			continue
		}
		cause, ok := documentCause(path, kind)
		if !ok || seen[cause] {
			continue
		}
		seen[cause] = true
		add(cause, describeDocument(kind, before, after))
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
	// Sources and verification are only comparable once recorded: a change
	// that has integrated nothing has no integrated sources, and finding
	// some in the world is not drift.
	if len(was.Sources) > 0 {
		if detail, moved := sourcesMoved(was.Sources, now.Sources); moved {
			add(CauseSource, detail)
		}
	}
	if was.Verification != "" && was.Verification != now.Verification {
		add(CauseVerification, describe("the recorded verification evidence",
			was.Verification, now.Verification))
	}
	return out
}

// Target returns the step a change must return to given a set of
// invalidations: the earliest of them, or the current step when there are
// none.
func Target(path Path, current Step, invalidations []Invalidation) Step {
	target := current
	for _, inv := range invalidations {
		if index(path, inv.ReturnTo) < index(path, target) {
			target = inv.ReturnTo
		}
	}
	return target
}

// describeDocument renders one moved document.
func describeDocument(kind artifact.Kind, was, now fingerprint.Digest) string {
	switch {
	case was == "":
		return fmt.Sprintf("%s was created (%s)", kind, short(now))
	case now == "":
		return fmt.Sprintf("%s was removed (was %s)", kind, short(was))
	}
	return fmt.Sprintf("%s moved from %s to %s", kind, short(was), short(now))
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
		return "(absent)"
	}
	if len(d) <= 12 {
		return string(d)
	}
	return string(d[:12])
}
