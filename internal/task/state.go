// Package task is the explicit Task workflow engine: plan -> do -> done,
// with all four subagent roles, binary-run checks, a bounded repair loop,
// and one archived record.
//
// # Explicit, not generic
//
// The steps are an enumerated list and the transitions are a switch. There
// is deliberately no workflow definition language, no table of rules
// loaded from configuration, no plugin points. A workflow you can express
// in data is a workflow whose invalid states you cannot see; this one's
// legal moves are readable in one file, and everything else is a refused
// transition with a message naming what was attempted.
//
// # Steps
//
// Plan: parallel explorers survey the confirmed repositories, the host
// drafts the goal and checklist from their reports, a skeptic attacks the
// assumptions, and consequential open questions are answered. There is no
// plan-approval gate.
//
// Do: unchecked work is partitioned into parallel implementer assignments
// in separate isolation areas, and a dedicated integration implementer
// combines their output per member.
//
// Done: Homonto runs the configured checks itself, then a reviewer and a
// skeptic assess the integrated result. Critical and high findings block
// until fixed or explicitly accepted. Repair returns to checks; three
// consecutive failed repairs stop and ask a human. Finalizing writes the
// evidence and archives the record.
//
// # Recorded step versus real step
//
// Reconcile is what makes the recorded step trustworthy. Every step rests
// on a baseline of fingerprints; when one moves, the workflow returns to
// the earliest affected step rather than trusting where it thinks it is.
package task

import (
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// Step is one explicit position in the Task workflow.
type Step string

const (
	// StepPlanExplore: parallel explorers survey the confirmed members.
	StepPlanExplore Step = "plan_explore"
	// StepPlanDraft: the host writes the goal and checklist from the
	// explorer reports, under an edit grant.
	StepPlanDraft Step = "plan_draft"
	// StepPlanChallenge: a skeptic attacks the assumptions, the missing
	// cases, and the scope.
	StepPlanChallenge Step = "plan_challenge"
	// StepPlanResolve: consequential open questions are answered by the
	// human before any implementation starts.
	StepPlanResolve Step = "plan_resolve"
	// StepDoImplement: parallel implementers work in separate isolation
	// areas, each with its own declared scope.
	StepDoImplement Step = "do_implement"
	// StepDoIntegrate: one integration implementer per member combines the
	// parallel output into a single branch or staged directory.
	StepDoIntegrate Step = "do_integrate"
	// StepDoneChecks: Homonto runs the configured verification commands
	// itself against the integrated result.
	StepDoneChecks Step = "done_checks"
	// StepDoneReview: reviewer and skeptic assess the integrated result in
	// parallel.
	StepDoneReview Step = "done_review"
	// StepDoRepair: a repair round addresses failed checks or blocking
	// findings, then returns to checks.
	StepDoRepair Step = "do_repair"
	// StepDoneFinalize: evidence is appended, every checklist item is
	// checked, and the record is written.
	StepDoneFinalize Step = "done_finalize"
	// StepArchived: the task document has been moved into the archive.
	StepArchived Step = "archived"
	// StepAbandoned: the work was given up on. Its isolation areas and
	// evidence remain for external handling; nothing is merged.
	StepAbandoned Step = "abandoned"
)

// steps is the canonical order, used for "earliest affected step"
// comparisons during reconciliation. The two terminals sit at the end and
// are never a reconciliation target.
var steps = []Step{
	StepPlanExplore, StepPlanDraft, StepPlanChallenge, StepPlanResolve,
	StepDoImplement, StepDoIntegrate, StepDoneChecks, StepDoneReview,
	StepDoRepair, StepDoneFinalize, StepArchived, StepAbandoned,
}

// index returns the step's position in canonical order, or -1.
func (s Step) index() int {
	for i, step := range steps {
		if step == s {
			return i
		}
	}
	return -1
}

// Known reports whether s is one of the enumerated steps.
func (s Step) Known() bool { return s.index() >= 0 }

// Terminal reports whether the workflow has stopped at s.
func (s Step) Terminal() bool { return s == StepArchived || s == StepAbandoned }

// Phase returns the workflow phase a step belongs to — the phase the
// artifact ownership table is consulted with. The repair step belongs to
// Do: a repair is implementation work, whatever discovered the need for it.
func (s Step) Phase() (artifact.Phase, error) {
	switch s {
	case StepPlanExplore, StepPlanDraft, StepPlanChallenge, StepPlanResolve:
		return artifact.PhasePlan, nil
	case StepDoImplement, StepDoIntegrate, StepDoRepair:
		return artifact.PhaseDo, nil
	case StepDoneChecks, StepDoneReview, StepDoneFinalize:
		return artifact.PhaseDone, nil
	case StepArchived, StepAbandoned:
		return "", fmt.Errorf("task: step %s is terminal and has no phase", s)
	}
	return "", fmt.Errorf("task: step %q is not a known step", s)
}

// earliest returns whichever of two steps comes first in canonical order.
func earliest(a, b Step) Step {
	if b.index() < a.index() {
		return b
	}
	return a
}

// Baseline is the set of fingerprints the current step rests on. Every one
// of them is an input the workflow's evidence depends on; when one moves,
// Reconcile returns the workflow to the earliest step that input affects.
type Baseline struct {
	// Document is the digest of the task document's host-authored regions
	// — the goal and the checklist. Evidence regions are excluded on
	// purpose: Homonto's own appends must not invalidate the plan.
	Document fingerprint.Digest `json:"document"`
	// Membership is the workspace's confirmed repository list.
	Membership fingerprint.Digest `json:"membership"`
	// PathClass is the test/generated/vendored classification.
	PathClass fingerprint.Digest `json:"path_class"`
	// CheckConfig is the verification configuration.
	CheckConfig fingerprint.Digest `json:"check_config"`
	// Sources are the integrated source fingerprints the checks and the
	// final reviews were taken against.
	Sources []fingerprint.Digest `json:"sources"`
}

// State is one Task's position and the baseline it rests on.
type State struct {
	WorkID     identity.WorkID `json:"work_id"`
	Name       string          `json:"name"`
	Step       Step            `json:"step"`
	Generation int64           `json:"generation"`
	Baseline   Baseline        `json:"baseline"`
	UpdatedAt  time.Time       `json:"updated_at"`
}

// Validate checks a state before it is persisted.
func (s State) Validate() error {
	if err := identity.ValidateUUID(string(s.WorkID)); err != nil {
		return fmt.Errorf("task: work_id: %w", err)
	}
	if !s.Step.Known() {
		return fmt.Errorf("task: step %q is not a known step", s.Step)
	}
	if s.Generation < 1 {
		return fmt.Errorf("task: generation %d must be at least 1", s.Generation)
	}
	return nil
}
