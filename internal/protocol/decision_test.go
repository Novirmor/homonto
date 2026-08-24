package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func validSchema() DecisionSchema {
	return DecisionSchema{
		Kind:   DecisionApproveDesign,
		Prompt: "Approve the proposed retry design?",
		Choices: []Choice{
			{Value: "approve", Label: "Approve"},
			{Value: "reject", Label: "Reject", RequiresRationale: true},
		},
	}
}

func TestDecisionSchemaValidateAccepts(t *testing.T) {
	for _, s := range []DecisionSchema{
		validSchema(),
		{Kind: DecisionAnswerQuestion, Prompt: "p?", QuestionID: "Q-1",
			Choices: []Choice{{Value: "y", Label: "Yes"}}},
		{Kind: DecisionAcceptFinding, Prompt: "p?", FindingID: "F-2",
			Choices: []Choice{{Value: "accept", Label: "Accept", RequiresRationale: true}}},
	} {
		if err := s.Validate(); err != nil {
			t.Errorf("Validate: %v", err)
		}
	}
}

func TestDecisionSchemaValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DecisionSchema)
	}{
		{"unknown kind", func(s *DecisionSchema) { s.Kind = "decide" }},
		{"empty prompt", func(s *DecisionSchema) { s.Prompt = "" }},
		{"no choices", func(s *DecisionSchema) { s.Choices = nil }},
		{"choice without value", func(s *DecisionSchema) { s.Choices[0].Value = "" }},
		{"choice without label", func(s *DecisionSchema) { s.Choices[0].Label = "" }},
		{
			"duplicate choice values",
			func(s *DecisionSchema) { s.Choices[1].Value = "approve" },
		},
		{
			"accept_finding without finding id",
			func(s *DecisionSchema) { s.Kind = DecisionAcceptFinding },
		},
		{
			"answer_question without question id",
			func(s *DecisionSchema) { s.Kind = DecisionAnswerQuestion },
		},
		{
			"finding id on non-finding decision",
			func(s *DecisionSchema) { s.FindingID = "F-9" },
		},
		{
			"question id on non-question decision",
			func(s *DecisionSchema) { s.QuestionID = "Q-9" },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSchema()
			tt.mutate(&s)
			if err := s.Validate(); err == nil {
				t.Error("Validate accepted an invalid decision schema")
			}
		})
	}
}

func validDecisionSubmission() DecisionSubmission {
	return DecisionSubmission{
		ProtocolVersion: CurrentVersion,
		ActionID:        testAction1,
		FreshnessToken:  testToken,
		Choice:          "approve",
	}
}

func TestValidateDecisionSubmission(t *testing.T) {
	schema := validSchema()

	if err := ValidateDecision(schema, validDecisionSubmission()); err != nil {
		t.Fatalf("valid submission rejected: %v", err)
	}
	// Rationale is optional when the chosen option does not require it.
	withRationale := validDecisionSubmission()
	withRationale.Rationale = "looks sound"
	if err := ValidateDecision(schema, withRationale); err != nil {
		t.Errorf("voluntary rationale rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*DecisionSubmission)
	}{
		{"wrong protocol version", func(s *DecisionSubmission) { s.ProtocolVersion = 0 }},
		{"malformed action id", func(s *DecisionSubmission) { s.ActionID = "x" }},
		{"malformed token", func(s *DecisionSubmission) { s.FreshnessToken = "x" }},
		{"empty choice is silence", func(s *DecisionSubmission) { s.Choice = "" }},
		{"unknown choice", func(s *DecisionSubmission) { s.Choice = "abstain" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := validDecisionSubmission()
			tt.mutate(&sub)
			if err := ValidateDecision(schema, sub); err == nil {
				t.Error("ValidateDecision accepted an invalid submission")
			}
		})
	}

	// Rejecting requires a rationale.
	rejecting := validDecisionSubmission()
	rejecting.Choice = "reject"
	if err := ValidateDecision(schema, rejecting); err == nil {
		t.Error("reject without rationale accepted")
	}
	rejecting.Rationale = "conflicts with the plan"
	if err := ValidateDecision(schema, rejecting); err != nil {
		t.Errorf("reject with rationale rejected: %v", err)
	}
}

func TestValidateDecisionAnswerQuestion(t *testing.T) {
	schema := DecisionSchema{
		Kind:   DecisionAnswerQuestion,
		Prompt: "Which scope?",
		Choices: []Choice{
			{Value: "global", Label: "Global"},
			{Value: "client", Label: "Per client"},
		},
		QuestionID: "Q-1",
	}
	sub := validDecisionSubmission()
	sub.Choice = "global"
	if err := ValidateDecision(schema, sub); err == nil {
		t.Error("answer_question without answer accepted")
	}
	sub.Answer = "per client, keyed by endpoint"
	if err := ValidateDecision(schema, sub); err != nil {
		t.Errorf("answer_question with answer rejected: %v", err)
	}

	plain := validSchema()
	sub2 := validDecisionSubmission()
	sub2.Answer = "stray answer"
	if err := ValidateDecision(plain, sub2); err == nil {
		t.Error("answer on a non-question decision accepted")
	}
}

func TestDecodeDecisionSubmissionStrict(t *testing.T) {
	b, err := json.Marshal(validDecisionSubmission())
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeDecisionSubmission(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if got != validDecisionSubmission() {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	b = append(append([]byte{}, b...), []byte(" {}")...)
	if _, err := DecodeDecisionSubmission(bytes.NewReader(b)); !errors.Is(err, ErrTrailingData) {
		t.Errorf("error = %v, want ErrTrailingData", err)
	}
	b2 := bytes.Replace(b, []byte(`"choice"`), []byte(`"choiche"`), 1)
	b2 = b2[:len(b2)-3]
	if _, err := DecodeDecisionSubmission(bytes.NewReader(b2)); !errors.Is(err, ErrUnknownField) {
		t.Errorf("error = %v, want ErrUnknownField", err)
	}
}
