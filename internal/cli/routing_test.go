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
