package decision

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Outcome statuses (ADR 0102). answered and uncertain carry a result.
const (
	StatusAnswered  = "answered"
	StatusUncertain = "uncertain"
	StatusFailed    = "failed"
)

// Outcome is what a decision call returns to the plugin and what the record
// keeps. It never carries the question or its context: those are the
// plugin's evidence and may be sensitive.
type Outcome struct {
	At       string  `json:"at"`
	Point    string  `json:"point"`
	Kind     Kind    `json:"kind"`
	Status   string  `json:"status"`
	Result   *Result `json:"result,omitempty"`
	Failure  string  `json:"failure,omitempty"` // failure class when Status is failed
	Error    string  `json:"error,omitempty"`
	Provider string  `json:"provider,omitempty"`
	Protocol string  `json:"protocol,omitempty"`
	Model    string  `json:"model,omitempty"`
	// Threshold is the minimum confidence the point required, when set.
	Threshold *float64 `json:"threshold,omitempty"`
	LatencyMS int64    `json:"latencyMs"`
	Usage     *Usage   `json:"usage,omitempty"`
	Task      string   `json:"task,omitempty"`
}

// Route is the provider and threshold one point uses.
type Route struct {
	Provider      *HTTPProvider
	MinConfidence *float64
}

// Service routes each decision point to its provider. Homonto never
// substitutes an answer: on failure or uncertainty the plugin, which knows
// its safe default, decides what to do (ADR 0102).
type Service struct {
	// RouteFor returns the route for a point, or an error that names what is
	// missing (classified not-configured).
	RouteFor func(point string) (Route, error)
	Now      func() time.Time
}

// Decide asks and classifies.
func (s Service) Decide(ctx context.Context, req Request) Outcome {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	start := now()
	out := Outcome{At: start.UTC().Format(time.RFC3339), Point: req.Point, Kind: req.Kind}
	fail := func(class string, err error) Outcome {
		out.Status, out.Failure, out.Error = StatusFailed, class, err.Error()
		out.LatencyMS = now().Sub(start).Milliseconds()
		return out
	}
	if err := req.Validate(); err != nil {
		return fail(FailUnsupported, err)
	}
	route, err := s.RouteFor(req.Point)
	if err != nil {
		return fail(FailNotConfigured, err)
	}
	p := route.Provider
	out.Provider, out.Protocol, out.Model, out.Threshold = p.Name, p.Protocol.Name(), p.Model, route.MinConfidence
	res, err := p.Decide(ctx, req)
	if err != nil {
		class := ClassOf(err)
		if class == "" {
			class = FailUnsupported
		}
		return fail(class, err)
	}
	out.LatencyMS = now().Sub(start).Milliseconds()
	last := p.LastAnswer()
	if last.Model != "" {
		out.Model = last.Model
	}
	if last.Usage != (Usage{}) {
		u := last.Usage
		out.Usage = &u
	}
	out.Result = &res
	out.Status = StatusAnswered
	if route.MinConfidence != nil && Certainty(res) < *route.MinConfidence {
		out.Status = StatusUncertain
	}
	return out
}

// Certainty is the confidence a threshold is judged against: the
// provider's confidence when it gives one, else, for a yes-no answer, how
// far the probability leans (max(p, 1-p)). An answer with neither is
// treated as fully uncertain, so a threshold never passes by default.
func Certainty(res Result) float64 {
	if res.Confidence != nil {
		return *res.Confidence
	}
	if res.Kind == YesNo && res.Probability != nil {
		return max(*res.Probability, 1-*res.Probability)
	}
	return 0
}

// RecordFile is the decision log under the state directory.
const RecordFile = "decisions.jsonl"

// Append writes one outcome to the log in stateDir.
func Append(stateDir string, out Outcome) error {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(stateDir, RecordFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Recent returns up to n of the latest outcomes in the log, oldest first.
// A missing log is empty. Unreadable lines are skipped and counted.
func Recent(stateDir string, n int) ([]Outcome, int, error) {
	f, err := os.Open(filepath.Join(stateDir, RecordFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	var all []Outcome
	bad := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var o Outcome
		if err := json.Unmarshal(sc.Bytes(), &o); err != nil {
			bad++
			continue
		}
		all = append(all, o)
	}
	if err := sc.Err(); err != nil {
		return nil, bad, fmt.Errorf("%s: %w", RecordFile, err)
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all, bad, nil
}
