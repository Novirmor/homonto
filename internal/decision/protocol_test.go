package decision

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var ticket = map[string]string{"ticket": "Checkout shows a blank screen after paying."}

var (
	yesNoReq  = Request{Point: "example-workflow.is-bug", Kind: YesNo, Question: "Is this a bug report?", Context: ticket}
	choiceReq = Request{Point: "example-workflow.classify", Kind: Choice, Question: "Which process handles this issue?", Options: []string{"bug", "feature", "question"}, Context: ticket}
	scoreReq  = Request{Point: "example-workflow.urgency", Kind: Score, Question: "How urgent is this?", Scale: &Scale{Min: 0, Max: 10}, Levels: []string{"low", "medium", "high"}, Context: ticket}
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sameJSON(t *testing.T, got, want []byte) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("request body\n got: %s\nwant: %s", got, want)
	}
}

// serve answers every POST with status and body, and captures the request.
type served struct {
	srv   *httptest.Server
	calls atomic.Int32
	body  []byte
	auth  string
	path  string
}

func serve(t *testing.T, status int, body []byte) *served {
	t.Helper()
	s := &served{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		s.auth, s.path = r.Header.Get("Authorization"), r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		s.body = buf
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func provider(p Protocol, endpoint, model string) *HTTPProvider {
	return &HTTPProvider{Name: "test-" + p.Name(), Protocol: p, Endpoint: endpoint, Model: model, APIKey: "sk-secret-123", Timeout: 2 * time.Second}
}

// TestProtocolContracts sends each question type through each protocol to
// a server replaying the documented response, and checks both the wire
// request and the decoded, validated result.
func TestProtocolContracts(t *testing.T) {
	for _, tc := range []struct {
		protocol Protocol
		model    string
		fixture  string
		req      Request
		check    func(t *testing.T, r Result)
		usage    Usage
	}{
		{OpenAIDecisions{}, "gpt-6-luna", "openai/predicate", yesNoReq, func(t *testing.T, r Result) {
			if *r.Probability != 0.93 || r.Confidence != nil {
				t.Fatalf("%+v", r)
			}
		}, Usage{InputTokens: 61, OutputTokens: 1}},
		{OpenAIDecisions{}, "gpt-6-luna", "openai/choice", choiceReq, func(t *testing.T, r Result) {
			if r.Choice != "bug" || *r.Confidence != 0.88 {
				t.Fatalf("%+v", r)
			}
		}, Usage{InputTokens: 74, OutputTokens: 1}},
		{OpenAIDecisions{}, "gpt-6-luna", "openai/score", scoreReq, func(t *testing.T, r Result) {
			if *r.Score != 7.5 || *r.Confidence != 0.6 { // 1.5 of levels 0..2 on 0..10
				t.Fatalf("%+v", r)
			}
		}, Usage{InputTokens: 70, OutputTokens: 1}},
		{SystemOne{}, "typesafe/jev-1.14", "systemone/noul", yesNoReq, func(t *testing.T, r Result) {
			if *r.Probability != 0.96 || r.Confidence != nil {
				t.Fatalf("%+v", r)
			}
		}, Usage{InputTokens: 120, OutputTokens: 12, CostUSD: ptr(0.000005)}},
		{SystemOne{}, "typesafe/jev-1.14", "systemone/choice", choiceReq, func(t *testing.T, r Result) {
			if r.Choice != "feature" || *r.Confidence != 0.75 {
				t.Fatalf("%+v", r)
			}
		}, Usage{InputTokens: 140, OutputTokens: 20, CostUSD: ptr(0.000006)}},
		{SystemOne{}, "typesafe/jev-1.14", "systemone/score", scoreReq, func(t *testing.T, r Result) {
			if *r.Score != 9.95 || *r.Confidence != 0.81 {
				t.Fatalf("%+v", r)
			}
		}, Usage{InputTokens: 130, OutputTokens: 18, CostUSD: ptr(0.000005)}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			s := serve(t, 200, fixture(t, tc.fixture+".response.json"))
			p := provider(tc.protocol, s.srv.URL+"/v1/decide", tc.model)
			res, err := p.Decide(context.Background(), tc.req)
			if err != nil {
				t.Fatal(err)
			}
			sameJSON(t, s.body, fixture(t, tc.fixture+".request.json"))
			if s.auth != "Bearer sk-secret-123" || s.path != "/v1/decide" {
				t.Fatalf("auth %q path %q", s.auth, s.path)
			}
			if res.Provider != p.Name || res.Kind != tc.req.Kind {
				t.Fatalf("%+v", res)
			}
			tc.check(t, res)
			if got := p.LastAnswer().Usage; !reflect.DeepEqual(got, tc.usage) {
				t.Fatalf("usage = %+v, want %+v", got, tc.usage)
			}
		})
	}
}

func ptr(v float64) *float64 { return &v }

func TestProviderFailureClasses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		req    Request
		class  string
		want   string
	}{
		{"auth", 401, string(mustRead(t, "openai/error-401.response.json")), yesNoReq, FailAuth, "Incorrect API key provided."},
		{"rate limited", 429, `{"error":{"message":"slow down"}}`, yesNoReq, FailRateLimited, "slow down"},
		{"gateway timeout", 524, `{"error":{"code":524,"message":"Provider request timed out at edge network"}}`, yesNoReq, FailTimeout, "timed out"},
		{"rejected", 400, string(mustRead(t, "systemone/error-400.response.json")), yesNoReq, FailRejected, "Box questions are not supported"},
		{"unknown model", 404, `{"error":{"message":"model not found"}}`, yesNoReq, FailUnavailable, "model not found"},
		{"server error", 503, `upstream down`, yesNoReq, FailUnavailable, "upstream down"},
		{"payment required", 402, `{"error":{"message":"insufficient credits"}}`, yesNoReq, FailUnavailable, "insufficient credits"},
		{"malformed body", 200, `not json`, yesNoReq, FailInvalid, "decode openai-decisions response"},
		{"wrong answer type", 200, `{"answers":[{"type":"choice","name":"answer","choice":"bug"}]}`, yesNoReq, FailInvalid, `answered a "choice" for a yes-no question`},
		{"choice outside options", 200, `{"answers":[{"type":"choice","name":"answer","choice":"wontfix","confidence":0.9}]}`, choiceReq, FailInvalid, "must name one of the options"},
		{"probability out of range", 200, `{"answers":[{"type":"predicate","name":"answer","probability":1.7}]}`, yesNoReq, FailInvalid, "probability in [0, 1]"},
		{"score off the levels", 200, `{"answers":[{"type":"score","name":"answer","score":5}]}`, scoreReq, FailInvalid, "outside the 3 levels"},
		{"refusal", 200, string(mustRead(t, "openai/refusal.response.json")), yesNoReq, FailRefused, "declined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := serve(t, tc.status, []byte(tc.body))
			_, err := provider(OpenAIDecisions{}, s.srv.URL, "gpt-6-luna").Decide(context.Background(), tc.req)
			if ClassOf(err) != tc.class || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v (class %q), want %s containing %q", err, ClassOf(err), tc.class, tc.want)
			}
		})
	}
}

func mustRead(t *testing.T, name string) []byte { return fixture(t, name) }

func TestProviderTimeoutAndUnreachable(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	p := provider(SystemOne{}, slow.URL, "typesafe/jev-1.14")
	p.Timeout = 50 * time.Millisecond
	if _, err := p.Decide(context.Background(), yesNoReq); ClassOf(err) != FailTimeout {
		t.Fatalf("slow: %v", err)
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	if _, err := provider(SystemOne{}, url, "m").Decide(context.Background(), yesNoReq); ClassOf(err) != FailUnavailable {
		t.Fatalf("closed: %v", err)
	}
}

// TestScoreWithoutLevelsNeverCallsTheProvider is the capability check: a
// request the protocol cannot express fails before the network.
func TestScoreWithoutLevelsNeverCallsTheProvider(t *testing.T) {
	for _, proto := range []Protocol{OpenAIDecisions{}, SystemOne{}} {
		s := serve(t, 200, []byte(`{}`))
		req := scoreReq
		req.Levels = nil
		_, err := provider(proto, s.srv.URL, "m").Decide(context.Background(), req)
		if ClassOf(err) != FailUnsupported || !strings.Contains(err.Error(), "scores on named levels") || s.calls.Load() != 0 {
			t.Fatalf("%s: err = %v calls = %d", proto.Name(), err, s.calls.Load())
		}
	}
}

// TestCredentialNeverLeaks: a provider that echoes the bearer token in its
// error body must not get it into the error or the outcome.
func TestCredentialNeverLeaks(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key ` + r.Header.Get("Authorization") + `"}}`))
	}))
	defer echo.Close()
	p := provider(OpenAIDecisions{}, echo.URL, "gpt-6-luna")
	out := Service{RouteFor: func(string) (Route, error) { return Route{Provider: p}, nil }}.Decide(context.Background(), yesNoReq)
	data, _ := json.Marshal(out)
	if strings.Contains(string(data), "sk-secret-123") || !strings.Contains(out.Error, "[redacted]") {
		t.Fatalf("outcome leaks or does not redact: %s", data)
	}
}

func TestProtocolRegistry(t *testing.T) {
	if got := strings.Join(Protocols(), ","); got != "openai-decisions,systemone" {
		t.Fatalf("protocols = %s", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate registration did not panic")
		}
	}()
	RegisterProtocol(SystemOne{})
}
