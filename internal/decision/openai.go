package decision

import (
	"encoding/json"
	"fmt"
)

// OpenAIDecisions is the native OpenAI Decisions API: POST /v1/decisions
// with `input` and a `questions` array of predicate, choice and score
// questions; answers come back as an array matched by name.
type OpenAIDecisions struct{}

// Name implements Protocol.
func (OpenAIDecisions) Name() string { return "openai-decisions" }

// DefaultEndpoint implements Protocol.
func (OpenAIDecisions) DefaultEndpoint() string { return "https://api.openai.com/v1/decisions" }

// Supports implements Protocol.
func (o OpenAIDecisions) Supports(req Request) error { return requireLevels(o.Name(), req) }

type openAIChoice struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

type openAILevel struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

type openAIQuestion struct {
	Type         string         `json:"type"`
	Name         string         `json:"name"`
	Instructions string         `json:"instructions"`
	Choices      []openAIChoice `json:"choices,omitempty"`
	Levels       []openAILevel  `json:"levels,omitempty"`
}

type openAIRequest struct {
	Model     string           `json:"model"`
	Input     string           `json:"input"`
	Questions []openAIQuestion `json:"questions"`
}

// Encode implements Protocol.
func (OpenAIDecisions) Encode(model string, req Request) ([]byte, error) {
	q := openAIQuestion{Name: questionName, Instructions: req.Question}
	switch req.Kind {
	case YesNo:
		q.Type = "predicate"
	case Choice:
		q.Type = "choice"
		for _, o := range req.Options {
			q.Choices = append(q.Choices, openAIChoice{Value: o, Description: o})
		}
	case Score:
		q.Type = "score"
		for _, l := range req.Levels {
			q.Levels = append(q.Levels, openAILevel{Label: l, Description: l})
		}
	}
	input := contextText(req.Context)
	if input == "" {
		input = req.Question
	}
	return json.Marshal(openAIRequest{Model: model, Input: input, Questions: []openAIQuestion{q}})
}

type openAIAnswer struct {
	Type        string   `json:"type"`
	Name        *string  `json:"name"`
	Probability *float64 `json:"probability"`
	Choice      string   `json:"choice"`
	Score       *float64 `json:"score"`
	Confidence  *float64 `json:"confidence"`
}

type openAIResponse struct {
	Answers []openAIAnswer `json:"answers"`
	Model   string         `json:"model"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Decode implements Protocol.
func (OpenAIDecisions) Decode(body []byte, req Request) (Answer, error) {
	var resp openAIResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Answer{}, fmt.Errorf("decode openai-decisions response: %w", err)
	}
	ans := Answer{Model: resp.Model, Usage: Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens}}
	var found *openAIAnswer
	for i := range resp.Answers {
		a := &resp.Answers[i]
		// A refusal may come back unnamed; with one question it is ours.
		if a.Name == nil || *a.Name == questionName {
			found = a
			break
		}
	}
	if found == nil {
		return Answer{}, fmt.Errorf("openai-decisions response has no answer named %q", questionName)
	}
	res := Result{Kind: req.Kind, Confidence: found.Confidence}
	switch {
	case found.Type == "refusal":
		ans.Refused = true
		return ans, nil
	case req.Kind == YesNo && found.Type == "predicate":
		res.Probability = found.Probability
	case req.Kind == Choice && found.Type == "choice":
		res.Choice = found.Choice
	case req.Kind == Score && found.Type == "score":
		if found.Score == nil {
			return Answer{}, fmt.Errorf("openai-decisions score answer has no score")
		}
		v, err := levelToScale(*found.Score, req)
		if err != nil {
			return Answer{}, fmt.Errorf("openai-decisions: %w", err)
		}
		res.Score = &v
	default:
		return Answer{}, fmt.Errorf("openai-decisions answered a %q for a %s question", found.Type, req.Kind)
	}
	ans.Result = res
	return ans, nil
}
