package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const routingCfg = `
[engines.no]
source = "builtin:no"
scope = "project"

[routing.default.opencode]
model = "p/default"

[routing.engines.no.stages.review.opencode]
model = "p/review"
`

func routingSetup(t *testing.T) (home, cfg string) {
	t.Helper()
	home = t.TempDir()
	cfg = filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(cfg, []byte(routingCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(t, home, "", "apply", "--yes", "--config", cfg); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	return home, cfg
}

func TestExplainRoutingNamesTheSelector(t *testing.T) {
	home, cfg := routingSetup(t)
	out, err := runCmd(t, home, "", "explain", "routing", "--config", cfg)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"route: no-reviewer → p/review (routing.engines.no.stages.review.opencode; static)",
		"route: no-implementer → p/default (routing.default.opencode; static)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "discovery:") {
		t.Errorf("static policies must not discover:\n%s", out)
	}
	out, err = runCmd(t, home, "", "explain", "routing", "no-reviewer", "--json", "--config", cfg)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var report struct {
		Routes []struct {
			Agent, Selector string
			Selection       struct{ Model string }
		}
		Discovery string
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil || len(report.Routes) != 1 || report.Routes[0].Selection.Model != "p/review" || report.Discovery != "not-run" {
		t.Fatalf("json = %+v %v\n%s", report, err, out)
	}
	if _, err := runCmd(t, home, "", "explain", "routing", "ghost", "--config", cfg); err == nil || !strings.Contains(err.Error(), `no rendered agent named "ghost"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestStatusShowsAppliedRoutes(t *testing.T) {
	home, cfg := routingSetup(t)
	out, _ := runCmd(t, home, "", "status", "--config", cfg)
	if !strings.Contains(out, "route: no-reviewer → p/review (routing.engines.no.stages.review.opencode; static)") {
		t.Fatalf("status:\n%s", out)
	}
	out, _ = runCmd(t, home, "", "status", "--output", "json", "--config", cfg)
	var payload struct {
		Routing struct {
			AppliedAt string
			Routes    []struct{ Agent string }
		}
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || payload.Routing.AppliedAt == "" || len(payload.Routing.Routes) == 0 {
		t.Fatalf("json = %+v %v\n%s", payload, err, out)
	}
}

// With both harnesses targeted, status shows the Claude Code routes the
// last apply stamped next to the opencode ones (M4.6).
func TestStatusShowsClaudeRoutes(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "homonto.toml")
	doc := "harnesses = [\"opencode\", \"claude\"]\n" + routingCfg + `
[routing.default.claude]
model = "sonnet"

[routing.engines.no.stages.review.claude]
model = "opus"
`
	if err := os.WriteFile(cfg, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(t, home, "", "apply", "--yes", "--config", cfg); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	out, _ := runCmd(t, home, "", "status", "--config", cfg)
	for _, want := range []string{
		"\nroute: no-reviewer → p/review (routing.engines.no.stages.review.opencode; static)",
		"claude route: no-reviewer → opus (routing.engines.no.stages.review.claude; static)",
		"claude route: no-implementer → sonnet (routing.default.claude; static)",
	} {
		if !strings.Contains("\n"+out, want) {
			t.Errorf("status lacks %q:\n%s", want, out)
		}
	}
	out, _ = runCmd(t, home, "", "status", "--output", "json", "--config", cfg)
	var payload struct {
		Routing struct {
			Routes    []struct{ Tool string }
			Harnesses map[string]struct {
				Routes []struct{ Agent, Tool string }
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil || len(payload.Routing.Routes) == 0 || payload.Routing.Routes[0].Tool != "opencode" ||
		len(payload.Routing.Harnesses["claude"].Routes) == 0 || payload.Routing.Harnesses["claude"].Routes[0].Tool != "claude" {
		t.Fatalf("json = %+v %v\n%s", payload, err, out)
	}
}

// A routing.json from before it held every harness still reads: status
// shows its opencode routes.
func TestStatusReadsOpenCodeOnlyRoutingRecord(t *testing.T) {
	home, cfg := routingSetup(t)
	old := `{
  "appliedAt": "2026-11-01T00:00:00Z",
  "routes": [
    {"agent": "no-reviewer", "tool": "opencode", "from": "policy", "selector": "routing.engines.no.stages.review.opencode",
     "selection": {"model": "p/old", "mode": "static", "reason": "static"}}
  ],
  "discovery": "not-run"
}
`
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg), ".homonto", "routing.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, home, "", "status", "--config", cfg)
	if err != nil || !strings.Contains(out, "route: no-reviewer → p/old (routing.engines.no.stages.review.opencode; static)") || strings.Contains(out, "claude route:") {
		t.Fatalf("status: %v\n%s", err, out)
	}
}
