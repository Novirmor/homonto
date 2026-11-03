package decision

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func fixed() time.Time { return time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC) }

func serviceFor(t *testing.T, fixtureName string, threshold *float64) Service {
	t.Helper()
	s := serve(t, 200, fixture(t, fixtureName))
	p := provider(OpenAIDecisions{}, s.srv.URL, "gpt-6-luna")
	return Service{Now: fixed, RouteFor: func(string) (Route, error) { return Route{Provider: p, MinConfidence: threshold}, nil }}
}

func TestServiceOutcomes(t *testing.T) {
	t.Run("answered", func(t *testing.T) {
		out := serviceFor(t, "openai/choice.response.json", ptr(0.8)).Decide(context.Background(), choiceReq)
		if out.Status != StatusAnswered || out.Result.Choice != "bug" || out.Provider != "test-openai-decisions" || out.Protocol != "openai-decisions" || out.Model != "gpt-6-luna" || out.Usage.InputTokens != 74 {
			t.Fatalf("%+v", out)
		}
	})
	t.Run("uncertain below the threshold keeps the result", func(t *testing.T) {
		out := serviceFor(t, "openai/choice.response.json", ptr(0.9)).Decide(context.Background(), choiceReq)
		if out.Status != StatusUncertain || out.Result == nil || out.Result.Choice != "bug" || *out.Threshold != 0.9 {
			t.Fatalf("%+v", out)
		}
	})
	t.Run("yes-no certainty is how far the probability leans", func(t *testing.T) {
		if out := serviceFor(t, "openai/predicate.response.json", ptr(0.9)).Decide(context.Background(), yesNoReq); out.Status != StatusAnswered {
			t.Fatalf("0.93 vs 0.9: %+v", out)
		}
		if out := serviceFor(t, "openai/predicate.response.json", ptr(0.95)).Decide(context.Background(), yesNoReq); out.Status != StatusUncertain {
			t.Fatalf("0.93 vs 0.95: %+v", out)
		}
	})
	t.Run("refused", func(t *testing.T) {
		out := serviceFor(t, "openai/refusal.response.json", nil).Decide(context.Background(), yesNoReq)
		if out.Status != StatusFailed || out.Failure != FailRefused || out.Result != nil {
			t.Fatalf("%+v", out)
		}
	})
	t.Run("not configured", func(t *testing.T) {
		out := Service{RouteFor: func(string) (Route, error) { return Route{}, ErrNoProvider }}.Decide(context.Background(), yesNoReq)
		if out.Status != StatusFailed || out.Failure != FailNotConfigured || !strings.Contains(out.Error, "no decision provider") {
			t.Fatalf("%+v", out)
		}
	})
	t.Run("malformed request", func(t *testing.T) {
		out := Service{RouteFor: func(string) (Route, error) { return Route{}, errors.New("unreached") }}.Decide(context.Background(), Request{Point: "x", Kind: YesNo})
		if out.Failure != FailUnsupported {
			t.Fatalf("%+v", out)
		}
	})
}

func TestCertaintyWithoutConfidenceNeverPasses(t *testing.T) {
	if Certainty(Result{Kind: Choice, Choice: "a"}) != 0 {
		t.Fatal("a choice without confidence must not pass a threshold")
	}
}

func TestRecordsKeepNoQuestionOrContext(t *testing.T) {
	dir := t.TempDir()
	out := serviceFor(t, "openai/choice.response.json", nil).Decide(context.Background(), choiceReq)
	out.Task = "fix-checkout"
	for range 3 {
		if err := Append(dir, out); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(dir + "/" + RecordFile)
	if strings.Contains(string(data), "blank screen") || strings.Contains(string(data), "Which process") {
		t.Fatalf("record carries the question or context: %s", data)
	}
	f, _ := os.OpenFile(dir+"/"+RecordFile, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString("{torn\n")
	f.Close()
	recent, bad, err := Recent(dir, 2)
	if err != nil || len(recent) != 2 || bad != 1 || recent[1].Task != "fix-checkout" {
		t.Fatalf("recent = %+v bad=%d err=%v", recent, bad, err)
	}
	if none, _, err := Recent(t.TempDir(), 5); none != nil || err != nil {
		t.Fatalf("missing log: %v %v", none, err)
	}
}

func TestRecordsOmitProviderErrorsThatEchoRequestInput(t *testing.T) {
	dir := t.TempDir()
	out := Outcome{
		At:      fixed().Format(time.RFC3339),
		Point:   choiceReq.Point,
		Kind:    choiceReq.Kind,
		Status:  StatusFailed,
		Failure: FailRejected,
		Error:   "provider echoed: " + choiceReq.Question + "; " + ticket["ticket"],
	}
	if err := Append(dir, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dir + "/" + RecordFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), choiceReq.Question) || strings.Contains(string(data), ticket["ticket"]) || strings.Contains(string(data), `"error"`) {
		t.Fatalf("record carries provider error input: %s", data)
	}
}

// TestLiveDecisionSmoke calls a real provider. It is opt-in because it
// spends money: HOMONTO_LIVE_DECISIONS=openai-decisions|systemone, with
// HOMONTO_LIVE_DECISIONS_KEY, _MODEL and (systemone) _ENDPOINT.
func TestLiveDecisionSmoke(t *testing.T) {
	name := os.Getenv("HOMONTO_LIVE_DECISIONS")
	if name == "" {
		t.Skip("opt-in: set HOMONTO_LIVE_DECISIONS to run a paid decision call")
	}
	proto, ok := LookupProtocol(name)
	if !ok {
		t.Fatalf("unknown protocol %q", name)
	}
	endpoint := os.Getenv("HOMONTO_LIVE_DECISIONS_ENDPOINT")
	if endpoint == "" {
		endpoint = proto.DefaultEndpoint()
	}
	p := &HTTPProvider{Name: "live", Protocol: proto, Endpoint: endpoint, Model: os.Getenv("HOMONTO_LIVE_DECISIONS_MODEL"), APIKey: os.Getenv("HOMONTO_LIVE_DECISIONS_KEY"), Timeout: 60 * time.Second}
	for _, req := range []Request{yesNoReq, choiceReq, scoreReq} {
		res, err := p.Decide(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", req.Kind, err)
		}
		data, _ := json.Marshal(res)
		t.Logf("%s: %s usage=%+v", req.Kind, data, p.LastAnswer().Usage)
	}
}
