package decision

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
)

func validToken(t *testing.T) identity.Token {
	t.Helper()
	tok, err := identity.NewToken()
	if err != nil {
		t.Fatalf("identity.NewToken: %v", err)
	}
	return tok
}

func validActionID(t *testing.T) identity.ActionID {
	t.Helper()
	id, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("identity.NewActionID: %v", err)
	}
	return id
}

// schemaForKind builds a minimal valid schema for each gate kind.
func schemaForKind(k Kind) Schema {
	switch k {
	case KindAcceptFinding:
		return Schema{Kind: k, Prompt: "accept?", FindingID: "f1",
			Choices: []Choice{{Value: "accept", Label: "Accept"}, {Value: "reject", Label: "Reject"}}}
	case KindAnswerQuestion:
		return Schema{Kind: k, Prompt: "what?", QuestionID: "q1",
			Choices: []Choice{{Value: "answer", Label: "Answer"}, {Value: "decline", Label: "Decline"}}}
	default:
		return Schema{Kind: k, Prompt: "proceed?",
			Choices: []Choice{{Value: "yes", Label: "Yes"}, {Value: "no", Label: "No"}}}
	}
}

// TestValidateSchemaCoversAllEightKinds proves every gate kind validates as a
// schema (no kind falls through the switch), and that the field rules of the
// finding and question gates hold.
func TestValidateSchemaCoversAllEightKinds(t *testing.T) {
	kinds := []Kind{
		KindConfirmClassification,
		KindApproveScope,
		KindApproveDesign,
		KindReproductionException,
		KindPresetTripwire,
		KindAcceptFinding,
		KindRepairLimit,
		KindAnswerQuestion,
	}
	for _, k := range kinds {
		t.Run(string(k), func(t *testing.T) {
			if err := ValidateSchema(schemaForKind(k)); err != nil {
				t.Fatalf("ValidateSchema(%q) = %v, want nil", k, err)
			}
		})
	}
}

func TestValidateSchemaRejectsUnknownKind(t *testing.T) {
	err := ValidateSchema(schemaForKind(KindApproveScope))
	_ = err
	s := schemaForKind(KindApproveScope)
	s.Kind = Kind("not_a_gate")
	if err := ValidateSchema(s); err == nil {
		t.Fatal("ValidateSchema with unknown kind = nil, want error")
	}
}

func TestValidateSchemaRejectsFindingAndQuestionFieldMismatches(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Schema)
	}{
		{"accept_finding without finding_id", func(s *Schema) { s.Kind = KindAcceptFinding; s.FindingID = "" }},
		{"accept_finding with question_id", func(s *Schema) { s.FindingID = ""; s.QuestionID = "q1" }},
		{"answer_question without question_id", func(s *Schema) { s.Kind = KindAnswerQuestion; s.QuestionID = "" }},
		{"answer_question with finding_id", func(s *Schema) { s.FindingID = "f1" }},
		{"other kind with finding_id", func(s *Schema) { s.Kind = KindApproveScope; s.FindingID = "f1" }},
		{"blank prompt", func(s *Schema) { s.Prompt = "  " }},
		{"no choices", func(s *Schema) { s.Choices = nil }},
		{"blank choice value", func(s *Schema) { s.Choices[0].Value = " " }},
		{"blank choice label", func(s *Schema) { s.Choices[0].Label = "" }},
		{"duplicate choice value", func(s *Schema) { s.Choices[1].Value = s.Choices[0].Value }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := schemaForKind(KindApproveScope)
			tt.mutate(&s)
			if err := ValidateSchema(s); err == nil {
				t.Fatalf("%s: ValidateSchema = nil, want error", tt.name)
			}
		})
	}
}

// TestValidateAllEightKinds covers the submission rules for every gate kind:
// valid choice accepted, unknown choice rejected, empty choice rejected
// (silence is not approval), rationale enforced exactly where required.
func TestValidateAllEightKinds(t *testing.T) {
	kinds := []Kind{
		KindConfirmClassification,
		KindApproveScope,
		KindApproveDesign,
		KindReproductionException,
		KindPresetTripwire,
		KindAcceptFinding,
		KindRepairLimit,
		KindAnswerQuestion,
	}
	for _, k := range kinds {
		t.Run(string(k), func(t *testing.T) {
			s := schemaForKind(k)
			sub := Submission{
				ActionID:       validActionID(t),
				FreshnessToken: validToken(t),
				Choice:         s.Choices[0].Value,
			}
			if k == KindAnswerQuestion {
				sub.Answer = "42"
			}
			if err := Validate(s, sub); err != nil {
				t.Fatalf("Validate(valid submission) = %v, want nil", err)
			}
		})
	}
}

func TestValidateEmptyChoiceIsSilenceNotApproval(t *testing.T) {
	s := schemaForKind(KindApproveScope)
	sub := Submission{
		ActionID:       validActionID(t),
		FreshnessToken: validToken(t),
	}
	if err := Validate(s, sub); err == nil {
		t.Fatal("Validate with empty choice = nil, want error (silence is not approval)")
	}
	if err := Validate(s, sub); !errors.Is(err, ErrEmptyChoice) {
		t.Fatalf("empty choice error must wrap %v, got %v", ErrEmptyChoice, err)
	}
}

func TestValidateChoiceMustBeOffered(t *testing.T) {
	s := schemaForKind(KindApproveScope)
	sub := Submission{
		ActionID:       validActionID(t),
		FreshnessToken: validToken(t),
		Choice:         "maybe",
	}
	if err := Validate(s, sub); err == nil {
		t.Fatal("Validate with unoffered choice = nil, want error")
	}
	if err := Validate(s, sub); !errors.Is(err, ErrUnknownChoice) {
		t.Fatalf("unoffered choice error must wrap %v, got %v", ErrUnknownChoice, err)
	}
}

func TestValidateRationaleRule(t *testing.T) {
	s := schemaForKind(KindApproveScope)
	s.Choices = []Choice{
		{Value: "no", Label: "No", RequiresRationale: true},
		{Value: "yes", Label: "Yes"},
	}
	base := Submission{ActionID: validActionID(t), FreshnessToken: validToken(t)}

	tests := []struct {
		name    string
		mutate  func(*Submission)
		wantErr bool
	}{
		{"rationale-required choice without rationale", func(s *Submission) { s.Choice = "no" }, true},
		{"rationale-required choice with rationale", func(s *Submission) { s.Choice = "no"; s.Rationale = "because" }, false},
		{"rationale-required choice with blank rationale", func(s *Submission) { s.Choice = "no"; s.Rationale = "   " }, true},
		{"non-required choice with rationale", func(s *Submission) { s.Choice = "yes"; s.Rationale = "volunteered" }, false},
		{"non-required choice without rationale", func(s *Submission) { s.Choice = "yes" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := base
			tt.mutate(&sub)
			err := Validate(s, sub)
			if tt.wantErr && err == nil {
				t.Fatalf("%s: Validate = nil, want error", tt.name)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("%s: Validate = %v, want nil", tt.name, err)
			}
		})
	}
}

func TestValidateAnswerRuleForQuestionGate(t *testing.T) {
	s := schemaForKind(KindAnswerQuestion)
	base := Submission{ActionID: validActionID(t), FreshnessToken: validToken(t), Choice: s.Choices[0].Value}

	if err := Validate(s, base); err == nil {
		t.Fatal("answer_question without answer = nil, want error")
	}
	sub := base
	sub.Answer = "the answer is 42"
	if err := Validate(s, sub); err != nil {
		t.Fatalf("answer_question with answer = %v, want nil", err)
	}

	other := schemaForKind(KindApproveScope)
	sub2 := base
	sub2.Answer = "stray"
	if err := Validate(other, sub2); err == nil {
		t.Fatal("non-question gate with answer = nil, want error")
	}
}

func TestValidateRejectsMalformedIdentifiers(t *testing.T) {
	s := schemaForKind(KindApproveScope)
	good := Submission{ActionID: validActionID(t), FreshnessToken: validToken(t), Choice: s.Choices[0].Value}

	badAction := good
	badAction.ActionID = "not-a-uuid"
	if err := Validate(s, badAction); err == nil {
		t.Fatal("malformed action id = nil, want error")
	}

	badToken := good
	badToken.FreshnessToken = "short"
	if err := Validate(s, badToken); err == nil {
		t.Fatal("malformed freshness token = nil, want error")
	}
}

// TestValidateDoesNotPanicOnNilSlices guards the fuzz contract: a schema with
// no choices must never panic.
func TestValidateDoesNotPanicOnNilSlices(t *testing.T) {
	s := Schema{Kind: KindApproveScope, Prompt: "p"}
	sub := Submission{ActionID: validActionID(t), FreshnessToken: validToken(t), Choice: "x"}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Validate panicked: %v", r)
		}
	}()
	_ = Validate(s, sub)
}

func TestSubmissionJSONRoundTrip(t *testing.T) {
	sub := Submission{
		ActionID:       validActionID(t),
		FreshnessToken: validToken(t),
		Choice:         "yes",
		Rationale:      "because",
	}
	b, err := json.Marshal(sub)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Submission
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != sub {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", back, sub)
	}
	if !strings.Contains(string(b), `"action_id"`) {
		t.Fatalf("JSON lacks action_id: %s", b)
	}
}
