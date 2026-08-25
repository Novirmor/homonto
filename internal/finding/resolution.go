package finding

import (
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
)

// Kind is one of the three ways a finding leaves the gate.
type Kind string

const (
	// KindFixed: the code changed. The engine is responsible for having
	// re-taken evidence against the new fingerprints before claiming it.
	KindFixed Kind = "fixed"
	// KindAccepted: a human accepted it as a documented deviation. Only
	// this kind carries a rationale and a decision, and for a blocking
	// finding both are mandatory.
	KindAccepted Kind = "accepted"
	// KindWithdrawn: the reporter retracted it.
	KindWithdrawn Kind = "withdrawn"
)

// state maps a resolution kind to the state it leaves behind.
func (k Kind) state() (State, bool) {
	switch k {
	case KindFixed:
		return StateFixed, true
	case KindAccepted:
		return StateAccepted, true
	case KindWithdrawn:
		return StateWithdrawn, true
	}
	return "", false
}

// Resolution is one way out for one finding. Accepting a blocking finding
// is the only path that needs a human, and it needs BOTH a rationale and
// the decision that authorized it: a deviation with no reason and no
// decider is indistinguishable from having ignored the finding.
type Resolution struct {
	WorkID     identity.WorkID
	ExternalID string
	Kind       Kind
	Rationale  string
	DecisionID identity.ActionID
}

// Validate checks the resolution in isolation. blocking says whether the
// finding it resolves gates the workflow; the human-decision requirement
// applies only to those.
func (r Resolution) Validate(blocking bool) error {
	if err := identity.ValidateUUID(string(r.WorkID)); err != nil {
		return fmt.Errorf("finding: resolution work_id: %w", err)
	}
	if strings.TrimSpace(r.ExternalID) == "" {
		return fmt.Errorf("finding: resolution names no finding: %w", ErrInvalidResolution)
	}
	if _, ok := r.Kind.state(); !ok {
		return fmt.Errorf("finding: resolution kind %q must be fixed, accepted, or withdrawn: %w",
			r.Kind, ErrInvalidResolution)
	}
	if r.Kind != KindAccepted {
		if strings.TrimSpace(r.Rationale) != "" || r.DecisionID != "" {
			return fmt.Errorf("finding: only an accepted finding carries a rationale and a decision: %w",
				ErrInvalidResolution)
		}
		return nil
	}
	if !blocking {
		// A non-blocking finding may be accepted without ceremony; a
		// rationale is welcome but not demanded.
		if r.DecisionID != "" {
			if err := identity.ValidateUUID(string(r.DecisionID)); err != nil {
				return fmt.Errorf("finding: resolution decision_id: %w", err)
			}
		}
		return nil
	}
	if strings.TrimSpace(r.Rationale) == "" {
		return fmt.Errorf("finding %q: %w", r.ExternalID, ErrRationaleRequired)
	}
	if r.DecisionID == "" {
		return fmt.Errorf("finding %q: %w", r.ExternalID, ErrDecisionRequired)
	}
	if err := identity.ValidateUUID(string(r.DecisionID)); err != nil {
		return fmt.Errorf("finding: resolution decision_id: %w", err)
	}
	return nil
}

// Deviation is one accepted blocking finding, the form the committed
// record carries: what was accepted, why, and who decided.
type Deviation struct {
	ExternalID string   `json:"finding_id"`
	Severity   Severity `json:"severity"`
	Summary    string   `json:"summary"`
	Rationale  string   `json:"rationale"`
	DecisionID string   `json:"decision_id"`
}

// Deviations returns the accepted blocking findings of a set, in the order
// given. They are what the record must include: an accepted blocker that
// the record does not name is an undocumented deviation.
func Deviations(findings []Finding) []Deviation {
	var out []Deviation
	for _, f := range findings {
		if f.State != StateAccepted || !Blocking(f.Severity) {
			continue
		}
		out = append(out, Deviation{
			ExternalID: f.ExternalID,
			Severity:   f.Severity,
			Summary:    f.Summary,
			Rationale:  f.Rationale,
			DecisionID: string(f.DecisionID),
		})
	}
	return out
}
