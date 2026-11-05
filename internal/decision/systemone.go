package decision

import (
	"encoding/json"
	"fmt"
)

// SystemOne is the Jev / System One-compatible protocol: POST to a
// configured endpoint (for example OpenRouter's /api/v1/systemone) with
// `model`, `state` and a map of named questions of type noul, choice or
// score, each with `instructions` and `criteria`; answers come back keyed
// by question name. The endpoint has no default: compatible services differ.
type SystemOne struct{}

// Name implements Protocol.
func (SystemOne) Name() string { return "systemone" }

// DefaultEndpoint implements Protocol.
func (SystemOne) DefaultEndpoint() string { return "" }

// Supports implements Protocol. Model-dependent extensions (box questions,
// image state) are not part of the shared API, so no request can reach a
// model that lacks them.
func (s SystemOne) Supports(req Request) error { return requireLevels(s.Name(), req) }

type systemOneQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria"`
}

type systemOneRequest struct {
	Model     string                       `json:"model"`
	State     any                          `json:"state"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

// Encode implements Protocol.
func (SystemOne) Encode(model string, req Request) ([]byte, error) {
	q := systemOneQuestion{Instructions: req.Question}
	switch req.Kind {
	case YesNo:
		q.Type = "noul"
		q.Criteria = map[string]string{"true": "The answer is yes.", "false": "The answer is no."}
	case Choice:
		q.Type = "choice"
		criteria := map[string]string{}
		for _, o := range req.Options {
			criteria[o] = o
		}
		q.Criteria = criteria
	case Score:
		q.Type = "score"
		q.Criteria = req.Levels
	}
	var state any = req.Context
	if len(req.Context) == 0 {
		state = req.Question
	}
	return json.Marshal(systemOneRequest{Model: model, State: state, Questions: map[string]systemOneQuestion{questionName: q}})
}

type systemOneAnswer struct {
	Noul       *float64 `json:"noul"`
	Choice     *string  `json:"choice"`
	Score      *float64 `json:"score"`
	Confidence *float64 `json:"confidence"`
}

type systemOneResponse struct {
	Model   string                     `json:"model"`
	Answers map[string]systemOneAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int      `json:"input_tokens"`
		OutputTokens int      `json:"output_tokens"`
		Cost         *float64 `json:"cost"`
	} `json:"usage"`
}

// Decode implements Protocol.
func (SystemOne) Decode(body []byte, req Request) (Answer, error) {
	var resp systemOneResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return Answer{}, fmt.Errorf("decode systemone response: %w", err)
	}
	a, ok := resp.Answers[questionName]
	if !ok {
		return Answer{}, fmt.Errorf("systemone response has no answer named %q", questionName)
	}
	ans := Answer{Model: resp.Model, Usage: Usage{InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens, CostUSD: resp.Usage.Cost}}
	res := Result{Kind: req.Kind, Confidence: a.Confidence}
	switch req.Kind {
	case YesNo:
		if a.Noul == nil {
			return Answer{}, fmt.Errorf("systemone noul answer has no noul probability")
		}
		res.Probability = a.Noul
		// noul carries no separate confidence.
		res.Confidence = nil
	case Choice:
		if a.Choice == nil {
			return Answer{}, fmt.Errorf("systemone choice answer has no choice")
		}
		res.Choice = *a.Choice
	case Score:
		if a.Score == nil {
			return Answer{}, fmt.Errorf("systemone score answer has no score")
		}
		v, err := levelToScale(*a.Score, req)
		if err != nil {
			return Answer{}, fmt.Errorf("systemone: %w", err)
		}
		res.Score = &v
	}
	ans.Result = res
	return ans, nil
}
