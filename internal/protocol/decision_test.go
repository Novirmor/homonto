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
