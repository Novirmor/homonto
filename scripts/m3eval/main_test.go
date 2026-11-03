package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/decision"
)

const repoRoot = "../.."

func TestDecisionSetIsValid(t *testing.T) {
	items, err := loadItems(filepath.Join(repoRoot, "testdata/eval/m3/decisions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, it := range items {
		kinds[string(it.Request.Kind)]++
	}
	if kinds["choice"] < 4 || kinds["yes-no"] < 2 || kinds["score"] < 2 {
		t.Fatalf("the set must cover every question type: %v", kinds)
	}
}

func writeEvalConfig(t *testing.T, openaiURL, jevURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "eval.toml")
	body := "[decisions.providers.openai]\nprotocol = \"openai-decisions\"\nendpoint = \"" + openaiURL + "\"\nmodel = \"gpt-6-luna\"\napi_key_env = \"EVAL_KEY\"\n" +
		"[decisions.providers.jev]\nprotocol = \"systemone\"\nendpoint = \"" + jevURL + "\"\nmodel = \"typesafe/jev-1.14\"\napi_key_env = \"EVAL_KEY\"\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDecisionsDryRunCallsNothing(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ }))
	defer srv.Close()
	var out strings.Builder
	err := runDecisions(context.Background(), []string{"--config", writeEvalConfig(t, srv.URL, srv.URL), "--set", filepath.Join(repoRoot, "testdata/eval/m3/decisions.jsonl")}, &out)
	if err != nil || calls != 0 || !strings.Contains(out.String(), "dry run: 12 items × 2 providers (jev, openai) = 24 calls") {
		t.Fatalf("err=%v calls=%d out=%s", err, calls, out.String())
	}
}

func TestDecisionProviderSelectionRejectsInvalidLists(t *testing.T) {
	path := writeEvalConfig(t, "http://127.0.0.1", "http://127.0.0.1")
	set := filepath.Join(repoRoot, "testdata/eval/m3/decisions.jsonl")
	for _, providers := range []string{"openai,openai", "openai,"} {
		err := runDecisions(context.Background(), []string{"--config", path, "--set", set, "--providers", providers}, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "--providers") {
			t.Errorf("providers %q: %v", providers, err)
		}
	}
	var out strings.Builder
	if err := runDecisions(context.Background(), []string{"--config", path, "--set", set, "--providers", "openai, jev"}, &out); err != nil || !strings.Contains(out.String(), "2 providers (openai, jev)") {
		t.Fatalf("trimmed providers: err=%v out=%s", err, out.String())
	}
}

// TestDecisionsEvalGradesPerProviderAndKind runs the eval against fake
// providers: one always right, one always uncertain-wrong.
func TestDecisionsEvalGradesPerProviderAndKind(t *testing.T) {
	t.Setenv("EVAL_KEY", "k")
	openai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Questions []struct{ Type string } }
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch req.Questions[0].Type {
		case "choice":
			_, _ = w.Write([]byte(`{"answers":[{"type":"choice","name":"answer","choice":"bug","confidence":0.9}],"usage":{"input_tokens":50}}`))
		case "predicate":
			_, _ = w.Write([]byte(`{"answers":[{"type":"predicate","name":"answer","probability":0.8}],"usage":{"input_tokens":30}}`))
		}
	}))
	defer openai.Close()
	jev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(429)
	}))
	defer jev.Close()
	set := filepath.Join(t.TempDir(), "set.jsonl")
	if err := os.WriteFile(set, []byte(`{"id":"a","request":{"point":"eval.c","kind":"choice","question":"q","options":["bug","feature"]},"expect":"bug"}
{"id":"b","request":{"point":"eval.c","kind":"choice","question":"q","options":["bug","feature"]},"expect":"feature"}
{"id":"c","request":{"point":"eval.y","kind":"yes-no","question":"q"},"expect":"yes"}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(t.TempDir(), "results.jsonl")
	var out strings.Builder
	if err := runDecisions(context.Background(), []string{"--config", writeEvalConfig(t, openai.URL, jev.URL), "--set", set, "--paid", "--out", results}, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"| jev | choice | 2 | — | 0 | 2 (rate-limited 2) |",
		"| openai | choice | 2 | 1/2 | 0 | 0 |",
		"| openai | yes-no | 1 | 1/1 | 0 | 0 | 0.04 |",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary lacks %q:\n%s", want, out.String())
		}
	}
	data, _ := os.ReadFile(results)
	if strings.Count(string(data), "\n") != 6 {
		t.Fatalf("results:\n%s", data)
	}
	var capped strings.Builder
	if err := runDecisions(context.Background(), []string{"--config", writeEvalConfig(t, openai.URL, jev.URL), "--set", set, "--paid", "--max-calls", "5"}, &capped); err == nil || !strings.Contains(err.Error(), "6 calls exceed --max-calls 5") {
		t.Fatalf("cap: %v", err)
	}
}

func TestRoutingDryRunLoadsTasksAndArms(t *testing.T) {
	var out strings.Builder
	arms := "static=" + filepath.Join(repoRoot, "testdata/eval/m3/arms/static.toml") + ",auto=" + filepath.Join(repoRoot, "testdata/eval/m3/arms/auto.toml")
	if err := runRouting(context.Background(), []string{"--tasks", filepath.Join(repoRoot, "testdata/eval/m3/tasks"), "--arms", arms, "--reps", "2"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry run: 3 tasks × 2 arms (auto, static) × 2 reps = 12 OpenCode runs") {
		t.Fatalf("out = %s", out.String())
	}
}

func TestRoutingInputValidation(t *testing.T) {
	arm := filepath.Join(repoRoot, "testdata/eval/m3/arms/static.toml")
	if err := runRouting(context.Background(), []string{"--tasks", filepath.Join(repoRoot, "testdata/eval/m3/tasks"), "--arms", "static=" + arm, "--reps", "0"}, io.Discard); err == nil || !strings.Contains(err.Error(), "--reps must be positive") {
		t.Fatalf("reps: %v", err)
	}
	if _, err := parseArms("one=" + arm + ",one=" + arm); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate arms: %v", err)
	}
	tasks := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tasks, "bad", "repo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tasks, "bad", "task.toml"), []byte("prompt = \"p\"\nagent = \"a\"\nverify = \"true\"\ntimeout = \"never\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTasks(tasks); err == nil || !strings.Contains(err.Error(), "must be a positive duration") {
		t.Fatalf("timeout: %v", err)
	}
}

func TestScoreExpectationMustFitItsScale(t *testing.T) {
	it := Item{Request: decision.Request{Point: "eval.score", Kind: decision.Score, Question: "q", Scale: &decision.Scale{Min: 0, Max: 10}, Levels: []string{"low", "high"}}, Expect: json.RawMessage(`[0,11]`)}
	if _, _, err := expectation(it); err == nil {
		t.Fatal("accepted expectation outside its score scale")
	}
}

func TestDecisionSetRejectsTrailingJSON(t *testing.T) {
	set := filepath.Join(t.TempDir(), "set.jsonl")
	if err := os.WriteFile(set, []byte(`{"id":"x","request":{"point":"eval.x","kind":"yes-no","question":"q"},"expect":"yes"}{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadItems(set); err == nil || !strings.Contains(err.Error(), "exactly one JSON value") {
		t.Fatalf("trailing JSON: %v", err)
	}
}

// TestTaskFixturesFailBeforeTheFix keeps the eval meaningful: a task whose
// verify command already passes measures nothing.
func TestTaskFixturesFailBeforeTheFix(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test in each fixture")
	}
	tasks, err := loadTasks(filepath.Join(repoRoot, "testdata/eval/m3/tasks"))
	if err != nil {
		t.Fatal(err)
	}
	for name, task := range tasks {
		dir := t.TempDir()
		if err := copyTree(filepath.Join(repoRoot, "testdata/eval/m3/tasks", name, "repo"), dir); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("sh", "-c", task.Verify)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Errorf("%s: verify passes before any change:\n%s", name, out)
		}
	}
}

func TestEventCostSumsCostedSteps(t *testing.T) {
	events := []byte(`{"type":"step_finish","part":{"cost":0.012,"tokens":{"input":100}}}
{"type":"text","part":{"text":"hi"}}
{"type":"step_finish","part":{"cost":0.003,"tokens":{"input":20}}}
not json
`)
	if c := eventCost(events); c == nil || *c < 0.01499 || *c > 0.01501 {
		t.Fatalf("cost = %v", c)
	}
	if eventCost([]byte(`{"type":"text"}`)) != nil {
		t.Fatal("cost from nothing")
	}
}
