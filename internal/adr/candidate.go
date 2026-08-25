// Package adr decides when a Change owes an architecture decision record,
// where that record goes, and whether what was written is one.
//
// # Decisions trigger ADRs, candidates do not
//
// An ADR is owed when a change ESTABLISHES a durable decision a future
// maintainer could reasonably question. Identifying a candidate during
// Design is not that: a candidate is a question someone might have to
// answer, and a question nobody answered leaves nothing to record. So a
// requirement is a candidate PLUS the recorded decision that settled it.
//
// The inverse matters more. A decision recorded against no candidate is a
// decision Close discovered that Design never anticipated — and writing an
// ADR for it there would document an accident. The change goes back to
// Design instead.
//
// # Presets are not exempt
//
// A preset that discovers an architectural, API, schema, or cross-module
// decision normally upgrades. When a human chooses to continue the preset
// instead, the decision-triggered ADR requirement still applies: choosing
// less ceremony for the work does not choose less record for the decision.
package adr

import (
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// Dir is where a control repository keeps its decision records.
const Dir = "docs/homonto/adr"

// Candidate is an ADR candidate identified during Design: something the
// change may have to decide, and which a future maintainer could
// reasonably question.
type Candidate struct {
	// ID is the stable handle the design and the decisions both use.
	ID string `json:"id"`
	// Title is the ADR's imperative title ("Adopt X", "Stop doing Y").
	Title string `json:"title"`
	// Question is what a future maintainer would ask. It is the test for
	// whether an ADR is owed at all, so it is required.
	Question string `json:"question"`
	// Design pins the design content the candidate was identified from. A
	// candidate whose design has moved describes a design that no longer
	// exists.
	Design fingerprint.Digest `json:"design"`
}

// Validate checks a candidate.
func (c Candidate) Validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return fmt.Errorf("adr: candidate id must not be blank")
	}
	if strings.TrimSpace(c.Title) == "" {
		return fmt.Errorf("adr: candidate %q: title must not be blank", c.ID)
	}
	if strings.TrimSpace(c.Question) == "" {
		return fmt.Errorf(
			"adr: candidate %q: question must not be blank; "+
				"a candidate nobody could question is not an ADR candidate", c.ID)
	}
	return nil
}

// Record is one durable decision the change made.
type Record struct {
	// CandidateIDs are the candidates this decision settles. Approving a
	// design settles every candidate that design identified — that is what
	// approving it means — so a decision usually names several. An empty
	// list is a durable decision nothing identified, which is the case
	// that sends a Full change back to Design.
	CandidateIDs []string `json:"candidate_ids,omitempty"`
	// ActionID is the decision action that carried it.
	ActionID identity.ActionID `json:"action_id"`
	// Kind is the decision gate's kind.
	Kind decision.Kind `json:"kind"`
	// Choice and Rationale are what the human answered.
	Choice    string `json:"choice"`
	Rationale string `json:"rationale,omitempty"`
	// Durable reports whether this decision establishes something a future
	// maintainer could question. A scope approval usually does not; a
	// preset continued past an architectural tripwire does.
	Durable bool `json:"durable"`
}

// DurableKinds are the decision gates that establish something durable by
// their nature. A preset tripwire continued rather than upgraded is the
// clearest case: the human decided the architecture question in passing,
// and that is exactly the decision a future maintainer asks about.
func DurableKinds() []decision.Kind {
	return []decision.Kind{
		decision.KindPresetTripwire,
		decision.KindApproveDesign,
		decision.KindReproductionException,
	}
}

// IsDurableKind reports whether a decision gate is durable by nature.
func IsDurableKind(k decision.Kind) bool {
	for _, d := range DurableKinds() {
		if d == k {
			return true
		}
	}
	return false
}
