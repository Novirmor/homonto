package decision

import (
	"errors"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
)

// ValidateSchema checks the schema structurally: gate spelling, prompt,
// non-empty unique choices, and the finding_id/question_id presence rules
// (finding_id exactly on accept_finding, question_id exactly on
// answer_question).
func ValidateSchema(s Schema) error {
	switch s.Kind {
	case
		KindConfirmClassification,
		KindApproveScope,
		KindApproveDesign,
		KindReproductionException,
		KindPresetTripwire,
		KindAcceptFinding,
		KindRepairLimit,
		KindAnswerQuestion:
	default:
		return fmt.Errorf("decision: kind %q is not a known decision gate", s.Kind)
	}
	if strings.TrimSpace(s.Prompt) == "" {
		return fmt.Errorf("decision: prompt must not be blank")
	}
	if len(s.Choices) == 0 {
		return fmt.Errorf("decision: choices must not be empty")
	}
	seen := make(map[string]bool, len(s.Choices))
	for i, c := range s.Choices {
		if strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("decision: choices[%d].value must not be blank", i)
		}
		if strings.TrimSpace(c.Label) == "" {
			return fmt.Errorf("decision: choices[%d].label must not be blank", i)
		}
		if seen[c.Value] {
			return fmt.Errorf("decision: choices[%d].value %q is a duplicate", i, c.Value)
		}
		seen[c.Value] = true
	}
	if s.Kind == KindAcceptFinding {
		if s.FindingID == "" {
			return fmt.Errorf("decision: finding_id is required for %q", KindAcceptFinding)
		}
	} else if s.FindingID != "" {
		return fmt.Errorf("decision: finding_id is only valid for %q decisions", KindAcceptFinding)
	}
	if s.Kind == KindAnswerQuestion {
		if s.QuestionID == "" {
			return fmt.Errorf("decision: question_id is required for %q", KindAnswerQuestion)
		}
	} else if s.QuestionID != "" {
		return fmt.Errorf("decision: question_id is only valid for %q decisions", KindAnswerQuestion)
	}
	return nil
}

// Validate checks a submission against the schema it answers: identifier
// and token formats, choice membership (silence is never a choice), the
// rationale requirement of the chosen option, and the answer requirement
// of question gates. Freshness against the stored action's token and the
// action's existence are the assignment store's job, not this package's.
func Validate(s Schema, sub Submission) error {
	if err := identity.ValidateUUID(string(sub.ActionID)); err != nil {
		return fmt.Errorf("decision: action_id: %w", err)
	}
	if err := identity.ValidateToken(string(sub.FreshnessToken)); err != nil {
		return fmt.Errorf("decision: freshness_token: %w", err)
	}
	if sub.Choice == "" {
		return fmt.Errorf("%w", ErrEmptyChoice)
	}
	var chosen *Choice
	for i := range s.Choices {
		if s.Choices[i].Value == sub.Choice {
			chosen = &s.Choices[i]
			break
		}
	}
	if chosen == nil {
		return fmt.Errorf("decision: choice %q: %w", sub.Choice, ErrUnknownChoice)
	}
	if chosen.RequiresRationale && strings.TrimSpace(sub.Rationale) == "" {
		return fmt.Errorf("decision: choice %q: %w", sub.Choice, ErrMissingRationale)
	}
	if s.Kind == KindAnswerQuestion {
		if strings.TrimSpace(sub.Answer) == "" {
			return fmt.Errorf("decision: %w", ErrMissingAnswer)
		}
	} else if strings.TrimSpace(sub.Answer) != "" {
		return fmt.Errorf("decision: %w", ErrUnwantedAnswer)
	}
	return nil
}

// IsTyped reports whether err is one of the package's typed validation
// errors — the contract the fuzz test enforces on every rejection.
func IsTyped(err error) bool {
	return errors.Is(err, ErrEmptyChoice) ||
		errors.Is(err, ErrUnknownChoice) ||
		errors.Is(err, ErrMissingRationale) ||
		errors.Is(err, ErrMissingAnswer) ||
		errors.Is(err, ErrUnwantedAnswer)
}
