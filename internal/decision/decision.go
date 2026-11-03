// Package decision is the plugin-facing typed decision capability
// (ADR 0098, ADR 0101): a package asks a question of a decision model and
// gets a typed, validated answer. Providers speak a registered Protocol
// (System One-compatible endpoints and native OpenAI Decisions ship); a
// Service routes each decision point to a configured provider, applies the
// confidence threshold and classifies failures (ADR 0102).
package decision

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
)

// Kind is the type of judgment requested.
type Kind string

const (
	// Choice picks exactly one of Options.
	Choice Kind = "choice"
	// YesNo returns the probability that the answer is yes.
	YesNo Kind = "yes-no"
	// Score rates on [Scale.Min, Scale.Max].
	Score Kind = "score"
)

// Scale bounds a score.
type Scale struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

// Request is one decision call. Point names the decision point the calling
// package declares, "<package>.<point>" (for example "onto.triage-severity"),
// so routing policies and audit can address it.
type Request struct {
	Point    string   `json:"point"`
	Kind     Kind     `json:"kind"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"` // choice only
	Scale    *Scale   `json:"scale,omitempty"`   // score only
	// Levels names the score's ordered levels, lowest first (score only).
	// Both shipped protocols score on named levels and require them; the
	// answer's level position is mapped linearly onto Scale.
	Levels  []string          `json:"levels,omitempty"`
	Context map[string]string `json:"context,omitempty"` // evidence the provider may see
}

// Result is a typed answer. Exactly the field for the request's kind is set.
type Result struct {
	Kind        Kind     `json:"kind"`
	Choice      string   `json:"choice,omitempty"`
	Probability *float64 `json:"probability,omitempty"` // yes-no: P(yes) in [0, 1]
	Score       *float64 `json:"score,omitempty"`
	// Confidence is the provider's own confidence in [0, 1], when it has one.
	Confidence *float64 `json:"confidence,omitempty"`
	// Provider names who answered, for audit.
	Provider string `json:"provider"`
}

// Decider answers decision requests. Implementations validate their own
// result with Result.Validate before returning it.
type Decider interface {
	Decide(ctx context.Context, req Request) (Result, error)
}

// ErrNoProvider is returned when the config names no decision provider for
// a point.
var ErrNoProvider = errors.New("decision: no decision provider is configured; declare [decisions.providers.<name>] and [decisions].default (docs/contracts/decisions.md)")

var point = regexp.MustCompile(`^[a-z][a-z0-9-]*\.[a-z][a-z0-9-]*$`)

// Validate checks a request is well formed for its kind.
func (r Request) Validate() error {
	if !point.MatchString(r.Point) {
		return fmt.Errorf("decision: point %q must be <package>.<point> (lowercase letters, digits, '-')", r.Point)
	}
	if r.Question == "" {
		return fmt.Errorf("decision %s: question is required", r.Point)
	}
	switch r.Kind {
	case Choice:
		if len(r.Options) < 2 {
			return fmt.Errorf("decision %s: a choice needs at least two options", r.Point)
		}
		seen := map[string]bool{}
		for _, o := range r.Options {
			if o == "" || seen[o] {
				return fmt.Errorf("decision %s: options must be non-empty and distinct", r.Point)
			}
			seen[o] = true
		}
		if r.Scale != nil || len(r.Levels) > 0 {
			return fmt.Errorf("decision %s: a choice takes no scale or levels", r.Point)
		}
	case YesNo:
		if len(r.Options) > 0 || r.Scale != nil || len(r.Levels) > 0 {
			return fmt.Errorf("decision %s: a yes-no question takes no options, scale or levels", r.Point)
		}
	case Score:
		if r.Scale == nil || !(r.Scale.Min < r.Scale.Max) || isBad(r.Scale.Min) || isBad(r.Scale.Max) {
			return fmt.Errorf("decision %s: a score needs a finite scale with min < max", r.Point)
		}
		if len(r.Options) > 0 {
			return fmt.Errorf("decision %s: a score takes no options", r.Point)
		}
		if len(r.Levels) > 0 {
			if len(r.Levels) < 2 || len(r.Levels) > 10 {
				return fmt.Errorf("decision %s: a score takes 2 to 10 levels", r.Point)
			}
			seen := map[string]bool{}
			for _, l := range r.Levels {
				if l == "" || seen[l] {
					return fmt.Errorf("decision %s: levels must be non-empty and distinct", r.Point)
				}
				seen[l] = true
			}
		}
	default:
		return fmt.Errorf("decision %s: unknown kind %q (choice, yes-no, score)", r.Point, r.Kind)
	}
	return nil
}

// Validate checks res answers req: same kind, exactly its field, in range.
func (res Result) Validate(req Request) error {
	if res.Kind != req.Kind {
		return fmt.Errorf("decision %s: answered %q, asked %q", req.Point, res.Kind, req.Kind)
	}
	if res.Provider == "" {
		return fmt.Errorf("decision %s: result names no provider", req.Point)
	}
	if res.Confidence != nil && !unit(*res.Confidence) {
		return fmt.Errorf("decision %s: confidence %v is outside [0, 1]", req.Point, *res.Confidence)
	}
	switch req.Kind {
	case Choice:
		if res.Probability != nil || res.Score != nil || !slices.Contains(req.Options, res.Choice) {
			return fmt.Errorf("decision %s: a choice result must name one of the options and nothing else", req.Point)
		}
	case YesNo:
		if res.Choice != "" || res.Score != nil || res.Probability == nil || !unit(*res.Probability) {
			return fmt.Errorf("decision %s: a yes-no result carries only a probability in [0, 1]", req.Point)
		}
	case Score:
		if res.Choice != "" || res.Probability != nil || res.Score == nil || isBad(*res.Score) || *res.Score < req.Scale.Min || *res.Score > req.Scale.Max {
			return fmt.Errorf("decision %s: a score result carries only a score within the scale", req.Point)
		}
	}
	return nil
}

func unit(v float64) bool  { return !isBad(v) && v >= 0 && v <= 1 }
func isBad(v float64) bool { return math.IsNaN(v) || math.IsInf(v, 0) }
