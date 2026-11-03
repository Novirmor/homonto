package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decisionServer answers like a provider of the given protocol.
func decisionServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func decideConfig(t *testing.T, openaiURL, systemoneURL string) string {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "homonto.toml")
	body := `[engines.no]
source = "builtin:no"
scope = "project"
[routing.default.opencode]
model = "p/m"

[decisions]
default = "openai"

[decisions.providers.openai]
protocol = "openai-decisions"
endpoint = "` + openaiURL + `"
model = "gpt-6-luna"
api_key_env = "TEST_DECISION_KEY"

[decisions.providers.jev]
protocol = "systemone"
endpoint = "` + systemoneURL + `"
model = "typesafe/jev-1.14"
api_key_env = "TEST_DECISION_KEY"

[decisions.points."no.next-step"]
provider = "jev"
min_confidence = 0.9
`
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfg
}

type outcome struct {
	Status, Failure, Provider, Protocol, Point, Error string
	Result                                            *struct {
		Choice      string
		Probability *float64
	}
}

func decide(t *testing.T, cfg, request string, args ...string) (outcome, int) {
	t.Helper()
	exitCode = 0
	out, err := runCmd(t, t.TempDir(), request, append([]string{"decide", "--config", cfg}, args...)...)
	if err != nil {
		t.Fatalf("decide: %v\n%s", err, out)
	}
	var o outcome
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	return o, exitCode
}

func TestDecideRoutesPointsToTheirProviders(t *testing.T) {
	t.Setenv("TEST_DECISION_KEY", "test-key")
	openai := decisionServer(t, 200, `{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"answer","probability":0.2}],"usage":{"input_tokens":10,"output_tokens":1}}`)
	jev := decisionServer(t, 200, `{"model":"typesafe/jev-1.14","answers":{"answer":{"choice":"repair","confidence":0.7}},"usage":{"input_tokens":10,"output_tokens":2,"cost":0.00001}}`)
	cfg := decideConfig(t, openai.URL, jev.URL)

	o, code := decide(t, cfg, `{"point":"no.needs-review","kind":"yes-no","question":"Needs review?","context":{"diff":"x"}}`, "--task", "t1")
	if code != 0 || o.Status != "answered" || o.Provider != "openai" || o.Protocol != "openai-decisions" || *o.Result.Probability != 0.2 {
		t.Fatalf("default point: %d %+v", code, o)
	}
	// The listed point goes to jev, whose 0.7 confidence is below its 0.9.
	o, code = decide(t, cfg, `{"point":"no.next-step","kind":"choice","question":"Next?","options":["repair","verify"]}`)
	if code != decideUncertain || o.Status != "uncertain" || o.Provider != "jev" || o.Result.Choice != "repair" {
		t.Fatalf("listed point: %d %+v", code, o)
	}
	status, _ := runCmd(t, t.TempDir(), "", "status", "--config", cfg)
	for _, want := range []string{"no.needs-review answered p(yes)=0.20 via openai task t1", "no.next-step uncertain repair confidence=0.70 via jev"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(cfg), ".homonto", "decisions.jsonl"))
	if strings.Count(string(log), "\n") != 2 || strings.Contains(string(log), "Needs review?") || strings.Contains(string(log), "test-key") {
		t.Fatalf("log:\n%s", log)
	}
}

func TestDecideFailureExitCodes(t *testing.T) {
	refusing := decisionServer(t, 200, `{"answers":[{"type":"refusal","name":"answer"}]}`)
	down := decisionServer(t, 503, `{"error":{"message":"overloaded"}}`)
	cfg := decideConfig(t, refusing.URL, down.URL)
	req := `{"point":"no.next-step","kind":"choice","question":"Next?","options":["repair","verify"]}`

	t.Setenv("TEST_DECISION_KEY", "")
	if o, code := decide(t, cfg, req); code != decideFailed || o.Failure != "not-configured" || !strings.Contains(o.Error, "TEST_DECISION_KEY is not set") {
		t.Fatalf("missing key: %d %+v", code, o)
	}
	t.Setenv("TEST_DECISION_KEY", "test-key")
	if o, code := decide(t, cfg, req, "--no-record"); code != decideFailed || o.Failure != "unavailable" {
		t.Fatalf("down: %d %+v", code, o)
	}
	if o, code := decide(t, cfg, `{"point":"no.x","kind":"yes-no","question":"q"}`); code != decideRefused || o.Failure != "refused" {
		t.Fatalf("refused: %d %+v", code, o)
	}
	exitCode = 0
	if _, err := runCmd(t, t.TempDir(), `{"point":"no.x","kind":"yes-no","question":"q","answer":"yes"}`, "decide", "--config", cfg); err == nil || !strings.Contains(err.Error(), `unknown field "answer"`) {
		t.Fatalf("unknown request field: %v", err)
	}
}
