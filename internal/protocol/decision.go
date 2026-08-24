package protocol

import (
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
)

// DecisionKind enumerates the human decision gates the workflows define.
type DecisionKind string

const (
	DecisionConfirmClassification DecisionKind = "confirm_classification"
	DecisionApproveScope          DecisionKind = "approve_scope"
	DecisionApproveDesign         DecisionKind = "approve_design"
	DecisionReproductionException DecisionKind = "reproduction_exception"
	DecisionPresetTripwire        DecisionKind = "preset_tripwire"
	DecisionAcceptFinding         DecisionKind = "accept_finding"
	DecisionRepairLimit           DecisionKind = "repair_limit"
	DecisionAnswerQuestion        DecisionKind = "answer_question"
)

// Choice is one selectable answer of a decision schema. A choice that
// requires a rationale makes an empty rationale submission invalid.
type Choice struct {
	Value             string `json:"value"`
	Label             string `json:"label"`
	RequiresRationale bool   `json:"requires_rationale"`
}

// DecisionSchema presents one human decision: the gate kind, the question,
// the choices, and — for the finding and question gates — the identifier
// the decision is about.
type DecisionSchema struct {
	Kind       DecisionKind `json:"kind"`
	Prompt     string       `json:"prompt"`
	Choices    []Choice     `json:"choices"`
	FindingID  string       `json:"finding_id,omitempty"`
	QuestionID string       `json:"question_id,omitempty"`
}

// Validate checks the schema: gate spelling, prompt, non-empty unique
// choices, and the finding_id/question_id presence rules (finding_id
// exactly on accept_finding, question_id exactly on answer_question).
func (s DecisionSchema) Validate() error {
	switch s.Kind {
	case
		DecisionConfirmClassification,
		DecisionApproveScope,
		DecisionApproveDesign,
		DecisionReproductionException,
		DecisionPresetTripwire,
		DecisionAcceptFinding,
		DecisionRepairLimit,
		DecisionAnswerQuestion:
	default:
		return fmt.Errorf("kind %q is not a known decision gate", s.Kind)
	}
	if strings.TrimSpace(s.Prompt) == "" {
		return fmt.Errorf("prompt must not be blank")
	}
	if len(s.Choices) == 0 {
		return fmt.Errorf("choices must not be empty")
	}
	seen := make(map[string]bool, len(s.Choices))
	for i, c := range s.Choices {
		if strings.TrimSpace(c.Value) == "" {
			return fmt.Errorf("choices[%d].value must not be blank", i)
		}
		if strings.TrimSpace(c.Label) == "" {
			return fmt.Errorf("choices[%d].label must not be blank", i)
		}
		if seen[c.Value] {
			return fmt.Errorf("choices[%d].value %q is a duplicate", i, c.Value)
		}
		seen[c.Value] = true
	}
	if s.Kind == DecisionAcceptFinding {
		if s.FindingID == "" {
			return fmt.Errorf("finding_id is required for %q", DecisionAcceptFinding)
		}
	} else if s.FindingID != "" {
		return fmt.Errorf("finding_id is only valid for %q decisions", DecisionAcceptFinding)
	}
	if s.Kind == DecisionAnswerQuestion {
		if s.QuestionID == "" {
			return fmt.Errorf("question_id is required for %q", DecisionAnswerQuestion)
		}
	} else if s.QuestionID != "" {
		return fmt.Errorf("question_id is only valid for %q decisions", DecisionAnswerQuestion)
	}
	return nil
}

// DecisionSubmission is a human's answer to a decision action.
type DecisionSubmission struct {
	ProtocolVersion int               `json:"protocol_version"`
	ActionID        identity.ActionID `json:"action_id"`
	FreshnessToken  identity.Token    `json:"freshness_token"`
	Choice          string            `json:"choice"`
	Rationale       string            `json:"rationale,omitempty"`
	Answer          string            `json:"answer,omitempty"`
}

// ValidateDecision checks a submission against the schema it answers:
// protocol version, identifier and token formats, choice membership
// (silence is never a choice), the rationale requirement of the chosen
// option, and the answer requirement of question gates. Freshness against
// the stored assignment hash is not checked here; see the package doc.
func ValidateDecision(schema DecisionSchema, submission DecisionSubmission) error {
	if submission.ProtocolVersion != CurrentVersion {
		return fmt.Errorf("protocol: protocol_version %d, want exactly %d",
			submission.ProtocolVersion, CurrentVersion)
	}
	if err := identity.ValidateUUID(string(submission.ActionID)); err != nil {
		return fmt.Errorf("protocol: action_id: %w", err)
	}
	if err := identity.ValidateToken(string(submission.FreshnessToken)); err != nil {
		return fmt.Errorf("protocol: freshness_token: %w", err)
	}
	if submission.Choice == "" {
		return fmt.Errorf("protocol: choice must not be empty; silence is not approval")
	}
	var chosen *Choice
	for i := range schema.Choices {
		if schema.Choices[i].Value == submission.Choice {
			chosen = &schema.Choices[i]
			break
		}
	}
	if chosen == nil {
		return fmt.Errorf("protocol: choice %q is not one of the offered choices", submission.Choice)
	}
	if chosen.RequiresRationale && strings.TrimSpace(submission.Rationale) == "" {
		return fmt.Errorf("protocol: choice %q requires a rationale", submission.Choice)
	}
	if schema.Kind == DecisionAnswerQuestion {
		if strings.TrimSpace(submission.Answer) == "" {
			return fmt.Errorf("protocol: %q decisions require an answer", DecisionAnswerQuestion)
		}
	} else if strings.TrimSpace(submission.Answer) != "" {
		return fmt.Errorf("protocol: answer is only valid for %q decisions", DecisionAnswerQuestion)
	}
	return nil
}
