package adr

import (
	"fmt"
	"sort"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// Requirement is one ADR the change owes: the question, and the decision
// that made answering it durable.
type Requirement struct {
	Candidate Candidate `json:"candidate"`
	Decision  Record    `json:"decision"`
	// Reason explains why this ADR is owed, for the assignment prompt and
	// for an operator arguing with the requirement.
	Reason string `json:"reason"`
}

// Assessment is what the ADR assessment concluded.
type Assessment struct {
	// Required are the ADRs the change must write before it closes.
	Required []Requirement `json:"required,omitempty"`
	// Undesigned are durable decisions recorded against no candidate.
	// They return a Full change to Design rather than producing an ADR:
	// writing one here would document a decision nobody designed.
	Undesigned []Record `json:"undesigned,omitempty"`
	// Stale are candidates whose design has moved under them. They cannot
	// be written against, because they describe a design that no longer
	// exists.
	Stale []Candidate `json:"stale,omitempty"`
}

// Owed reports whether any ADR must be written.
func (a Assessment) Owed() bool { return len(a.Required) > 0 }

// Blocked reports whether the assessment prevents closing for a reason an
// ADR cannot fix — an undesigned decision or a stale candidate.
func (a Assessment) Blocked() bool { return len(a.Undesigned) > 0 || len(a.Stale) > 0 }

// Assess decides which ADRs a change owes.
//
// current is the digest of the design the change now has. A candidate
// whose recorded design digest does not match it is stale: it was
// identified against a design that has since been rewritten, and an ADR
// written from it would answer a question the change no longer poses.
//
// A candidate with no durable decision against it owes nothing. That is
// the common case and it is deliberate: Design identifying a candidate is
// Design noticing a question, and a question nobody had to answer is not a
// decision worth recording.
func Assess(candidates []Candidate, decisions []Record, current fingerprint.Digest) (Assessment, error) {
	byID := make(map[string]Candidate, len(candidates))
	for i, c := range candidates {
		if err := c.Validate(); err != nil {
			return Assessment{}, fmt.Errorf("adr: candidates[%d]: %w", i, err)
		}
		if _, dup := byID[c.ID]; dup {
			return Assessment{}, fmt.Errorf("adr: candidate id %q appears twice", c.ID)
		}
		byID[c.ID] = c
	}

	var out Assessment
	staleSeen := map[string]bool{}
	requiredSeen := map[string]bool{}
	for _, d := range decisions {
		if !d.Durable {
			continue
		}
		if len(d.CandidateIDs) == 0 {
			out.Undesigned = append(out.Undesigned, d)
			continue
		}
		for _, id := range d.CandidateIDs {
			c, known := byID[id]
			if !known {
				// A decision naming a candidate the design does not
				// contain is the same problem as naming none: the design
				// does not hold the question the decision answered.
				out.Undesigned = append(out.Undesigned, d)
				break
			}
			if current != "" && c.Design != "" && c.Design != current {
				if !staleSeen[c.ID] {
					staleSeen[c.ID] = true
					out.Stale = append(out.Stale, c)
				}
				continue
			}
			if requiredSeen[c.ID] {
				continue
			}
			requiredSeen[c.ID] = true
			out.Required = append(out.Required, Requirement{
				Candidate: c,
				Decision:  d,
				Reason: fmt.Sprintf(
					"the %s decision %q settled %q, which a future maintainer could reasonably question",
					d.Kind, d.Choice, c.Question),
			})
		}
	}

	sort.Slice(out.Required, func(i, j int) bool {
		return out.Required[i].Candidate.ID < out.Required[j].Candidate.ID
	})
	sort.Slice(out.Stale, func(i, j int) bool { return out.Stale[i].ID < out.Stale[j].ID })
	return out, nil
}
