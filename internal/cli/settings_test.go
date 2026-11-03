package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const settingsCfg = `
[engines.no]
source = "builtin:no"
scope = "project"

[engines.no.settings]
isolation = "worktree"

[subagents.no-implementer.opencode]
model = "openai/gpt-5"

[subagents.no-reviewer.opencode]
model = "openai/gpt-5"
`

// TestSettingsAppearInExplainAndPlan: the resolved package settings, with
// defaults marked, read the same through explain (text and JSON, the path the
// no execution reference uses) and plan (ADR 0095).
func TestSettingsAppearInExplainAndPlan(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(cfg, []byte(settingsCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, home, "", "explain", "settings", "--config", cfg)
	if err != nil || !strings.Contains(out, `setting: engines.no.isolation = "worktree"`) || strings.Contains(out, "(default)") {
		t.Fatalf("explain settings: %v\n%s", err, out)
	}
	out, err = runCmd(t, home, "", "explain", "settings", "no", "--json", "--config", cfg)
	if err != nil {
		t.Fatalf("explain settings no --json: %v\n%s", err, out)
	}
	var got struct {
		Package  string `json:"package"`
		Settings []struct {
			Name    string `json:"name"`
			Value   any    `json:"value"`
			Default bool   `json:"default"`
		} `json:"settings"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if got.Package != "no" || len(got.Settings) != 1 || got.Settings[0].Value != "worktree" || got.Settings[0].Default {
		t.Fatalf("settings = %+v", got)
	}
	if _, err := runCmd(t, home, "", "explain", "settings", "onto", "--config", cfg); err == nil {
		t.Fatal("a package with no settings was explained")
	}
	out, err = runCmd(t, home, "", "plan", "--config", cfg)
	if err != nil || !strings.Contains(out, `setting: engines.no.isolation = "worktree"`) {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if err := os.WriteFile(cfg, []byte(strings.Replace(settingsCfg, "[engines.no.settings]\nisolation = \"worktree\"\n", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err = runCmd(t, home, "", "plan", "--config", cfg)
	if err != nil || !strings.Contains(out, `setting: engines.no.isolation = "ask" (default)`) {
		t.Fatalf("plan default: %v\n%s", err, out)
	}
}
