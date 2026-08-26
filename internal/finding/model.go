// Package finding owns the issues reviewers and skeptics raise and what
// happens to them. It is the store-side twin of protocol.Finding: the wire
// form is what a host submits, this is what Homonto persists and gates on.
//
// # The gate
//
// Critical and high findings BLOCK. A blocking finding leaves exactly
// three ways forward: fix it, withdraw it (the reporter was wrong and said
// so), or have a human explicitly accept it as a documented deviation with
// a rationale and a decision behind it. Nothing else clears a blocker —
// in particular, silence and the passage of time do not, and neither does
// a later report that simply stops mentioning it.
//
// Lower-severity findings are recorded and reported but never block.
package finding

import (
	"errors"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// Severity grades a finding. The spellings match protocol.Severity so the
// persisted and wire forms never disagree.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// known reports whether s is one of the four severities.
func (s Severity) known() bool {
	switch s {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
		return true
	}
	return false
}

// Blocking reports whether a severity stops the workflow. Critical and
// high do; medium and low are recorded and reported but never gate.
//
// An unrecognized severity blocks. A finding whose grade Homonto does not
// understand is not a finding Homonto may wave through.
func Blocking(severity Severity) bool {
	switch severity {
	case SeverityMedium, SeverityLow:
		return false
	}
	return true
}

// State is where a finding stands.
type State string

const (
	// StateOpen: raised and unresolved. A blocking open finding gates.
	StateOpen State = "open"
	// StateFixed: the implementer changed the code and the evidence was
	// re-taken against the new fingerprints.
	StateFixed State = "fixed"
	// StateAccepted: a human explicitly accepted it as a documented
	// deviation, with a rationale and a decision behind it.
	StateAccepted State = "accepted"
	// StateWithdrawn: the reporter itself retracted it.
	StateWithdrawn State = "withdrawn"
)

// known reports whether s is a persisted state.
func (s State) known() bool {
	switch s {
	case StateOpen, StateFixed, StateAccepted, StateWithdrawn:
		return true
	}
	return false
}

// Resolved reports whether the state clears the finding from the gate.
func (s State) Resolved() bool { return s != StateOpen }

// Typed errors. Callers branch with errors.Is.
var (
	// ErrUnknownFinding: no finding with that id in that work.
	ErrUnknownFinding = errors.New("finding: no such finding")
	// ErrRationaleRequired: accepting a blocking finding without a
	// rationale — a deviation nobody explained is not a documented one.
	ErrRationaleRequired = errors.New("finding: accepting a blocking finding requires a rationale")
	// ErrDecisionRequired: accepting a blocking finding without the human
	// decision that authorized it.
	ErrDecisionRequired = errors.New("finding: accepting a blocking finding requires a human decision")
	// ErrAlreadyResolved: the finding was already resolved, and a second
	// resolution would overwrite the first one's record.
	ErrAlreadyResolved = errors.New("finding: finding is already resolved")
	// ErrInvalidResolution: the resolution is not one of the three ways
	// out, or contradicts itself.
	ErrInvalidResolution = errors.New("finding: invalid resolution")
)

// Finding is one persisted issue: who raised it, how bad they said it is,
// what backs it, what to do about it, and where it stands now.
type Finding struct {
	ID             string
	WorkID         identity.WorkID
	ActionID       identity.ActionID
	ExternalID     string
	Role           protocol.Role
	Severity       Severity
	Summary        string
	Evidence       []string
	Recommendation string
	State          State
	Rationale      string
	DecisionID     identity.ActionID
}

// Blocking reports whether this finding currently gates the workflow.
func (f Finding) Blocking() bool { return Blocking(f.Severity) && !f.State.Resolved() }

// Validate checks a finding before it is persisted.
func (f Finding) Validate() error {
	if err := identity.ValidateUUID(string(f.WorkID)); err != nil {
		return fmt.Errorf("finding: work_id: %w", err)
	}
	if strings.TrimSpace(f.ExternalID) == "" {
		return fmt.Errorf("finding: external id must not be blank")
	}
	if !f.Severity.known() {
		return fmt.Errorf("finding: severity %q is not one of critical, high, medium, low", f.Severity)
	}
	switch f.Role {
	case protocol.RoleReviewer, protocol.RoleSkeptic:
	default:
		return fmt.Errorf("finding: role %q must be reviewer or skeptic; only they raise findings", f.Role)
	}
	if strings.TrimSpace(f.Summary) == "" {
		return fmt.Errorf("finding: summary must not be blank")
	}
	if strings.TrimSpace(f.Recommendation) == "" {
		return fmt.Errorf("finding: recommendation must not be blank")
	}
	for i, e := range f.Evidence {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("finding: evidence[%d] must not be blank", i)
		}
	}
	if f.State != "" && !f.State.known() {
		return fmt.Errorf("finding: state %q is not known", f.State)
	}
	if f.ActionID != "" {
		if err := identity.ValidateUUID(string(f.ActionID)); err != nil {
			return fmt.Errorf("finding: action_id: %w", err)
		}
	}
	return nil
}

// FromReport converts the findings of a role report into persistable
// findings attributed to the assignment that produced them.
func FromReport(workID identity.WorkID, actionID identity.ActionID, role protocol.Role, findings []protocol.Finding) ([]Finding, error) {
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		converted := Finding{
			WorkID:         workID,
			ActionID:       actionID,
			ExternalID:     f.ID,
			Role:           role,
			Severity:       Severity(f.Severity),
			Summary:        f.Summary,
			Evidence:       append([]string(nil), f.Evidence...),
			Recommendation: f.Recommendation,
			State:          StateOpen,
		}
		if err := converted.Validate(); err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}
