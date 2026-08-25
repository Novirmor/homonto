// Package decision defines the persisted human-decision contract: the
// schema of a decision gate an action presents, and the submission a human
// answers it with. The wire form hosts see is protocol.DecisionSchema and
// protocol.ValidateDecision; this package is the store-side twin the
// assignment layer persists inside action specs and the engines validate
// against. Conversion between the two spellings is engine work.
//
// The validation contract here is deliberately narrow and unforgiving:
// a submission must name one of the offered choices (an empty choice is
// never approval — silence must fail closed), a rationale is required
// exactly when the chosen option demands one, and question gates require
// an answer. Schema validation covers the eight gate kinds, the choice
// list, and the finding/question identifier rules.
package decision

import (
	"errors"

	"github.com/noviopenworks/homonto/internal/identity"
)

// Kind enumerates the human decision gates the workflows define. The
// spellings are identical to protocol.DecisionKind so the persisted and
// wire forms never disagree about a gate name.
type Kind string

const (
	// KindConfirmClassification: the human confirms a preflight
	// classification (full/fix/tweak) before a change opens.
	KindConfirmClassification Kind = "confirm_classification"
	// KindApproveScope: the human approves a change's proposed scope.
	KindApproveScope Kind = "approve_scope"
	// KindApproveDesign: the human approves a full change's design.
	KindApproveDesign Kind = "approve_design"
	// KindReproductionException: the human accepts a missing failing
	// reproduction for a fix, with rationale.
	KindReproductionException Kind = "reproduction_exception"
	// KindPresetTripwire: the human chooses continue or upgrade when a
	// preset (fix/tweak) trips a semantic or file tripwire.
	KindPresetTripwire Kind = "preset_tripwire"
	// KindAcceptFinding: the human accepts (or rejects) one finding,
	// identified by FindingID.
	KindAcceptFinding Kind = "accept_finding"
	// KindRepairLimit: the human decides what happens after the repair
	// limit is exhausted.
	KindRepairLimit Kind = "repair_limit"
	// KindAnswerQuestion: the human answers an open question an agent
	// raised, identified by QuestionID.
	KindAnswerQuestion Kind = "answer_question"
)

// Choice is one selectable answer of a decision gate. A choice that
// requires a rationale makes an empty rationale submission invalid.
type Choice struct {
	Value             string `json:"value"`
	Label             string `json:"label"`
	RequiresRationale bool   `json:"requires_rationale"`
}

// Schema presents one human decision: the gate kind, the question, the
// offered choices, and — for the finding and question gates — the
// identifier the decision is about.
type Schema struct {
	Kind       Kind     `json:"kind"`
	Prompt     string   `json:"prompt"`
	Choices    []Choice `json:"choices"`
	FindingID  string   `json:"finding_id,omitempty"`
	QuestionID string   `json:"question_id,omitempty"`
}

// Submission is a human's answer to one decision action: which action it
// answers, the freshness token proving the answer was minted for that
// action, the chosen option, an optional rationale, and the optional free
// answer of question gates.
type Submission struct {
	ActionID       identity.ActionID `json:"action_id"`
	FreshnessToken identity.Token    `json:"freshness_token"`
	Choice         string            `json:"choice"`
	Rationale      string            `json:"rationale,omitempty"`
	Answer         string            `json:"answer,omitempty"`
}

// Typed validation errors. Callers branch with errors.Is; messages always
// name the offending value.
var (
	// ErrEmptyChoice: the submission named no choice — silence is not
	// approval, and it must fail closed.
	ErrEmptyChoice = errors.New("decision: choice must not be empty; silence is not approval")
	// ErrUnknownChoice: the submission named a choice the schema does not
	// offer.
	ErrUnknownChoice = errors.New("decision: choice is not offered by the schema")
	// ErrMissingRationale: the chosen option requires a rationale.
	ErrMissingRationale = errors.New("decision: choice requires a rationale")
	// ErrUnwantedRationale: the chosen option forbids nothing, but a
	// rationale on a non-required choice is harmless; this sentinel is
	// reserved for the answer-rule violations below.
	ErrUnwantedRationale = errors.New("decision: rationale not allowed for this choice")
	// ErrMissingAnswer: an answer_question gate requires an answer.
	ErrMissingAnswer = errors.New("decision: answer_question requires an answer")
	// ErrUnwantedAnswer: only answer_question gates carry an answer.
	ErrUnwantedAnswer = errors.New("decision: answer is only valid for answer_question gates")
)
