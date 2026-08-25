// Package change is the Change workflow: the Full path and the Fix and
// Tweak presets, with the classification preflight that decides between
// them.
//
// # Preflight is local
//
// Starting a Change does NOT start a Change. It starts a local,
// uncommitted classification candidate: read-only explorers and a skeptic
// inspect the request and the project, Homonto suggests fix, tweak, or
// full and explains the evidence, and a human confirms. No portable state
// and no Change document exists until that confirmation — so abandoning a
// preflight leaves the repository exactly as it was, and a suggestion is
// never mistaken for a decision.
//
// # Presets are not a smaller Full
//
// Fix and Tweak are their own state machines with their own documents, not
// Full with steps skipped. A preset that discovers it is really a Full
// change PAUSES and asks; it never widens itself, and Homonto never
// upgrades on the human's behalf.
package change

import (
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
)

// Path is which Change workflow a work follows.
type Path string

const (
	// PathFull: proposal, design, tasks, plan, verification, record.
	PathFull Path = "full"
	// PathFix: an existing-behavior defect. fix.md plus tasks.
	PathFix Path = "fix"
	// PathTweak: a bounded behavior, configuration, documentation, or
	// prompt change. tweak.md plus tasks.
	PathTweak Path = "tweak"
)

// Known reports whether p is one of the three paths.
func (p Path) Known() bool {
	switch p {
	case PathFull, PathFix, PathTweak:
		return true
	}
	return false
}

// Preset reports whether the path is one of the two presets.
func (p Path) Preset() bool { return p == PathFix || p == PathTweak }

// InputKind returns the document kind that carries the path's confirmed
// intent: a proposal for Full, fix.md or tweak.md for a preset.
func (p Path) InputKind() (artifact.Kind, error) {
	switch p {
	case PathFull:
		return artifact.KindProposal, nil
	case PathFix:
		return artifact.KindFix, nil
	case PathTweak:
		return artifact.KindTweak, nil
	}
	return "", fmt.Errorf("change: %q is not a known change path", p)
}

// PreflightStep is where a classification candidate is.
type PreflightStep string

const (
	// PreflightAssess: read-only explorers and a skeptic are inspecting
	// the request and the project.
	PreflightAssess PreflightStep = "preflight_assess"
	// PreflightConfirm: Homonto has a suggestion and is waiting for a
	// human to confirm the path.
	PreflightConfirm PreflightStep = "preflight_confirm"
	// PreflightConfirmed: the human confirmed; the Change exists.
	PreflightConfirmed PreflightStep = "preflight_confirmed"
	// PreflightAbandoned: the candidate was dropped. Nothing was created,
	// so nothing is left behind.
	PreflightAbandoned PreflightStep = "preflight_abandoned"
)

// Terminal reports whether the preflight has finished.
func (s PreflightStep) Terminal() bool {
	return s == PreflightConfirmed || s == PreflightAbandoned
}

// Known reports whether s is a recognized preflight step.
func (s PreflightStep) Known() bool {
	switch s {
	case PreflightAssess, PreflightConfirm, PreflightConfirmed, PreflightAbandoned:
		return true
	}
	return false
}

// Suggestion is what Homonto concluded during preflight, and why.
type Suggestion struct {
	// Path is the suggested workflow. It is a suggestion: only a human
	// confirmation makes it the Change's path.
	Path Path `json:"path"`
	// Signals are the preset scope signals that fired during assessment.
	// A suggestion of Full is usually a signal list; a suggestion of a
	// preset is usually an empty one.
	Signals []pathclass.Signal `json:"signals,omitempty"`
	// Evidence explains the suggestion in sentences a human can weigh.
	Evidence []string `json:"evidence,omitempty"`
}

// PreflightState is one local classification candidate. It is deliberately
// NOT a Change: it has a work id so its assignments can be addressed, but
// no name, no documents, and no portable record until confirmation.
type PreflightState struct {
	WorkID identity.WorkID `json:"work_id"`
	// Name is the work name the human proposed. It is validated at start
	// (an unusable name should fail before any work is done) but nothing
	// on disk carries it yet.
	Name string `json:"name"`
	// Request is the human's description of what they want.
	Request    string        `json:"request"`
	Step       PreflightStep `json:"step"`
	Generation int64         `json:"generation"`
	Suggestion Suggestion    `json:"suggestion"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// Validate checks a preflight state before it is persisted.
func (s PreflightState) Validate() error {
	if err := identity.ValidateUUID(string(s.WorkID)); err != nil {
		return fmt.Errorf("change: preflight work_id: %w", err)
	}
	if !s.Step.Known() {
		return fmt.Errorf("change: preflight step %q is not known", s.Step)
	}
	if s.Generation < 1 {
		return fmt.Errorf("change: preflight generation %d must be at least 1", s.Generation)
	}
	if s.Suggestion.Path != "" && !s.Suggestion.Path.Known() {
		return fmt.Errorf("change: suggested path %q is not known", s.Suggestion.Path)
	}
	return nil
}

// Baseline is the fingerprint set a Change's step rests on.
//
// The documents are digested PER KIND rather than folded together,
// because the spec's invalidation graph is per document: editing
// proposal.md sends the change back further than editing plan.md does, and
// a combined digest could only ever say "something moved".
type Baseline struct {
	// Documents maps each host-authored document kind to its digest. An
	// absent document has no entry, and a document coming into existence
	// therefore moves the baseline — which is correct: a design that did
	// not exist and now does is a change to what everything after it
	// rests on.
	Documents map[artifact.Kind]fingerprint.Digest `json:"documents,omitempty"`
	// Verification is the digest of the recorded evidence set. It is
	// separate because verification and finding-resolution changes
	// invalidate Close and nothing before it.
	Verification fingerprint.Digest `json:"verification,omitempty"`
	// Membership, PathClass, and CheckConfig mirror the Task baseline.
	Membership  fingerprint.Digest `json:"membership"`
	PathClass   fingerprint.Digest `json:"path_class"`
	CheckConfig fingerprint.Digest `json:"check_config"`
	// Sources are the integrated source fingerprints.
	Sources []fingerprint.Digest `json:"sources"`
	// Work is the IMMUTABLE baseline the preset scope count is measured
	// from: the integrated workspace state at the moment the human
	// confirmed the path. Continuation, repair, and later path
	// reconfirmation never move it — that is the whole point of capturing
	// it once, and the reason a preset cannot escape its own scope
	// warning by re-baselining.
	Work []fingerprint.Digest `json:"work"`
}

// State is one Change: its path, where it is in that path's workflow, and
// the baseline the position rests on.
type State struct {
	WorkID identity.WorkID `json:"work_id"`
	Name   string          `json:"name"`
	Path   Path            `json:"path"`
	// Step is the current step, spelled in the path's own vocabulary.
	Step string `json:"step"`
	// UpgradedFrom records the preset a Full change was upgraded from,
	// empty for a change that started as Full. It is what makes an
	// upgraded change's preset input readable as history rather than as
	// live input.
	UpgradedFrom Path      `json:"upgraded_from,omitempty"`
	Generation   int64     `json:"generation"`
	Baseline     Baseline  `json:"baseline"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Validate checks a change state before it is persisted.
func (s State) Validate() error {
	if err := identity.ValidateUUID(string(s.WorkID)); err != nil {
		return fmt.Errorf("change: work_id: %w", err)
	}
	if !s.Path.Known() {
		return fmt.Errorf("change: path %q is not known", s.Path)
	}
	if s.Step == "" {
		return fmt.Errorf("change: step must not be empty")
	}
	if s.Generation < 1 {
		return fmt.Errorf("change: generation %d must be at least 1", s.Generation)
	}
	if s.UpgradedFrom != "" && !s.UpgradedFrom.Preset() {
		return fmt.Errorf("change: upgraded_from %q must be a preset", s.UpgradedFrom)
	}
	if s.UpgradedFrom != "" && s.Path != PathFull {
		return fmt.Errorf("change: only a Full change can have been upgraded, not %q", s.Path)
	}
	return nil
}

// Dir returns the change's document directory.
func (s State) Dir() (string, error) { return artifact.ChangeDir(s.Name) }

// DocumentPath returns the path of one of the change's documents.
func (s State) DocumentPath(k artifact.Kind) (string, error) {
	return artifact.Path(s.Name, k)
}

// Document returns the recorded digest of one document kind.
func (b Baseline) Document(k artifact.Kind) fingerprint.Digest { return b.Documents[k] }
