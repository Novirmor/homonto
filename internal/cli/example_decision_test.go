package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildHomonto compiles the homonto binary for tests that run plugin
// scripts, which call it as a command.
func buildHomonto(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the homonto binary")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "homonto")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// TestExampleWorkflowClassifiesWithTypedDecisions is the M3 done-when for
// #60: the external example workflow, installed through apply, runs its
// projected classify script, which asks a typed decision through each
// protocol, and the answer picks which configured process runs. An
// uncertain or failed decision makes the plugin ask instead of guessing.
func TestExampleWorkflowClassifiesWithTypedDecisions(t *testing.T) {
	bin := buildHomonto(t)
	var issueSeen []string
	answer := func(body string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			data, _ := json.Marshal(req)
			issueSeen = append(issueSeen, string(data))
			_, _ = w.Write([]byte(body))
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	openai := answer(`{"model":"gpt-6-luna","answers":[{"type":"choice","name":"answer","choice":"bug","confidence":0.92}],"usage":{"input_tokens":80,"output_tokens":1}}`)
	jev := answer(`{"model":"typesafe/jev-1.14","answers":{"answer":{"choice":"feature","confidence":0.55}},"usage":{"input_tokens":90,"output_tokens":3,"cost":0.00001}}`)
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	t.Cleanup(down.Close)

	ex := examplesDir(t)
	project := t.TempDir()
	home := t.TempDir()
	issue := filepath.Join(project, "issue.txt")
	if err := os.WriteFile(issue, []byte("Checkout shows a blank screen after paying."), 0o644); err != nil {
		t.Fatal(err)
	}
	config := func(provider string, minConfidence string) {
		body := `[engines.example-engine]
source = "local:` + filepath.Join(ex, "example-engine") + `"
scope = "project"
[workflows.example-workflow]
source = "local:` + filepath.Join(ex, "example-workflow") + `"
scope = "project"
[services.example-service]
source = "local:` + filepath.Join(ex, "example-service") + `"
scope = "project"

[workflows.example-workflow.processes.bug]
steps = ["reproduce", "label bug", "hand to engine"]
[workflows.example-workflow.processes.feature]
steps = ["label feature", "ask for acceptance criteria"]
[workflows.example-workflow.processes.question]
steps = ["answer", "close"]

[routing.default.opencode]
model = "test/model"

[decisions.providers.openai]
protocol = "openai-decisions"
endpoint = "` + openai.URL + `"
model = "gpt-6-luna"
api_key_env = "EXAMPLE_DECISION_KEY"
[decisions.providers.jev]
protocol = "systemone"
endpoint = "` + jev.URL + `"
model = "typesafe/jev-1.14"
api_key_env = "EXAMPLE_DECISION_KEY"
[decisions.providers.down]
protocol = "systemone"
endpoint = "` + down.URL + `"
model = "typesafe/jev-1.14"
api_key_env = "EXAMPLE_DECISION_KEY"

[decisions.points."example-workflow.classify"]
provider = "` + provider + `"
` + minConfidence
		if err := os.WriteFile(filepath.Join(project, "homonto.toml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = project
		cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+filepath.Dir(bin)+string(os.PathListSeparator)+os.Getenv("PATH"), "EXAMPLE_DECISION_KEY=k")
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s%s", name, args, err, out, stderr.String())
		}
		return strings.TrimSpace(string(out))
	}

	config("openai", "")
	run(bin, "apply", "--yes")
	scripts, _ := filepath.Glob(filepath.Join(project, ".opencode", "skill*", "example-triage", "scripts", "classify.sh"))
	if len(scripts) != 1 {
		t.Fatalf("classify.sh is not projected: %v", scripts)
	}
	classify := scripts[0]
	var processes struct {
		Processes map[string]struct{ Steps []string }
	}
	if err := json.Unmarshal([]byte(run(bin, "explain", "settings", "example-workflow", "--json")), &processes); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ provider, minConfidence, want string }{
		{"openai", "", "bug"},                    // native OpenAI Decisions
		{"jev", "", "feature"},                   // System One-compatible
		{"jev", "min_confidence = 0.8\n", "ask"}, // 0.55 is uncertain: ask, never guess
		{"down", "", "ask"},                      // provider unavailable
	} {
		config(tc.provider, tc.minConfidence)
		got := run("sh", classify, issue)
		if got != tc.want {
			t.Fatalf("%s %q: classify printed %q, want %q", tc.provider, tc.minConfidence, got, tc.want)
		}
		if got != "ask" && len(processes.Processes[got].Steps) == 0 {
			t.Fatalf("decision %q selects no configured process: %+v", got, processes)
		}
	}
	if len(issueSeen) != 3 || !strings.Contains(issueSeen[0], "blank screen") || !strings.Contains(issueSeen[1], `"state":{"issue":`) {
		t.Fatalf("providers did not receive the issue as evidence: %v", issueSeen)
	}
	// status prints for humans (cobra's default stream, stderr).
	cmd := exec.Command(bin, "status")
	cmd.Dir, cmd.Env = project, append(os.Environ(), "HOME="+home)
	raw, _ := cmd.CombinedOutput()
	status := string(raw)
	for _, want := range []string{"example-workflow.classify answered bug confidence=0.92 via openai", "example-workflow.classify uncertain feature confidence=0.55 via jev", "example-workflow.classify failed (unavailable) via down"} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
}
