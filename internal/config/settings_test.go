package config

import (
	"strings"
	"testing"
)

const settingsWorkflow = `manifest_schema = 2
name = "flow"
kind = "workflow"
version = "0.1.0"
[compat]
contract = "1.0"
[workflow]
entry = ["go"]
[commands]
go = "commands/go.md"
[settings.mode]
type = "string"
enum = ["quick", "deep"]
default = "quick"
description = "Review depth"
[settings.limit]
type = "integer"
required = true
description = "Maximum open items"
[settings.labels]
type = "list"
[processes]
description = "Named triage processes"
[processes.fields.steps]
type = "list"
required = true
[processes.fields.strict]
type = "boolean"
default = false
`

func loadSettings(t *testing.T, table string) (*Config, error) {
	t.Helper()
	path := writeTree(t, t.TempDir(), map[string]string{
		"flow/framework.toml": settingsWorkflow,
		"flow/commands/go.md": "Go.\n",
		"homonto.toml":        "[workflows.flow]\nsource = \"local:./flow\"\nscope = \"project\"\n" + table,
	})
	return Load(path)
}

func TestPackageSettingsValidation(t *testing.T) {
	for _, tc := range []struct {
		name, table, want string
	}{
		{"valid", "[workflows.flow.settings]\nlimit = 3\nlabels = [\"bug\"]\n[workflows.flow.processes.triage]\nsteps = [\"read\", \"label\"]\n", ""},
		{"unknown key", "[workflows.flow.settings]\nlimit = 3\nlimt = 4\n", "workflows.flow.settings.limt is not a known setting (known: labels, limit, mode)"},
		{"type error", "[workflows.flow.settings]\nlimit = \"three\"\n", `workflows.flow.settings.limit: "three" is not an integer`},
		{"enum miss", "[workflows.flow.settings]\nlimit = 3\nmode = \"medium\"\n", `workflows.flow.settings.mode: "medium" is not one of quick, deep`},
		{"list of non-strings", "[workflows.flow.settings]\nlimit = 3\nlabels = [1]\n", "workflows.flow.settings.labels: [1] is not a list of strings"},
		{"missing required", "[workflows.flow.settings]\nmode = \"deep\"\n", "workflows.flow.settings.limit is required (Maximum open items)"},
		{"missing required without a table", "", "workflows.flow.settings.limit is required"},
		{"process missing required field", "[workflows.flow.settings]\nlimit = 3\n[workflows.flow.processes.triage]\nstrict = true\n", "workflows.flow.processes.triage.steps is required"},
		{"process unknown field", "[workflows.flow.settings]\nlimit = 3\n[workflows.flow.processes.triage]\nsteps = []\nstage = \"build\"\n", "workflows.flow.processes.triage.stage is not a known setting (known: steps, strict)"},
		{"bad process name", "[workflows.flow.settings]\nlimit = 3\n[workflows.flow.processes.Triage]\nsteps = []\n", `"Triage" is not a process name`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadSettings(t, tc.table)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestResolvedSettingsFillDefaults(t *testing.T) {
	c, err := loadSettings(t, "[workflows.flow.settings]\nlimit = 3\n[workflows.flow.processes.triage]\nsteps = [\"read\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := c.ResolvedSettings()
	if err != nil || len(resolved) != 1 {
		t.Fatalf("resolved = %+v, %v", resolved, err)
	}
	got := map[string]SettingValue{}
	for _, s := range resolved[0].Settings {
		got[s.Name] = s
	}
	if v := got["mode"]; v.Value != "quick" || !v.Default {
		t.Errorf("mode = %+v, want the manifest default", v)
	}
	if v := got["limit"]; v.Value != int64(3) || v.Default {
		t.Errorf("limit = %+v, want the configured 3", v)
	}
	if v := got["labels"]; v.Value != nil || !v.Default {
		t.Errorf("labels = %+v, want unset", v)
	}
	if resolved[0].Processes["triage"] == nil {
		t.Errorf("process definition missing: %+v", resolved[0].Processes)
	}
}

func TestSettingsBelongOnlyToPackages(t *testing.T) {
	for _, tc := range []struct{ name, config, want string }{
		{"skill", "[skills.grilling]\nsource = \"builtin:grilling\"\nscope = \"project\"\n[skills.grilling.settings]\nx = 1\n", "skills.grilling declares settings or processes"},
		{"package without a schema", "[engines.onto]\nsource = \"builtin:onto\"\nscope = \"project\"\n[engines.onto.settings]\nx = 1\n" + ontoFrameworkModels(), `engines.onto.settings: package "onto" declares no settings`},
		{"package without processes", "[engines.no]\nsource = \"builtin:no\"\nscope = \"project\"\n[engines.no.processes.p]\nx = 1\n" + noModels, `engines.no.processes: package "no" interprets no process definitions`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTree(t, t.TempDir(), map[string]string{"homonto.toml": tc.config})
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

const noModels = "[subagents.no-implementer.opencode]\nmodel = \"m/x\"\n[subagents.no-reviewer.opencode]\nmodel = \"m/x\"\n"

func TestNoEngineIsolationSetting(t *testing.T) {
	for _, tc := range []struct{ value, want string }{
		{"", "ask"},
		{"[engines.no.settings]\nisolation = \"worktree\"\n", "worktree"},
	} {
		path := writeTree(t, t.TempDir(), map[string]string{"homonto.toml": "[engines.no]\nsource = \"builtin:no\"\nscope = \"project\"\n" + tc.value + noModels})
		c, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := c.ResolvedSettings()
		if err != nil || len(resolved) != 1 || resolved[0].Package != "no" || resolved[0].Settings[0].Value != tc.want {
			t.Fatalf("%q: resolved = %+v, %v", tc.value, resolved, err)
		}
	}
	path := writeTree(t, t.TempDir(), map[string]string{"homonto.toml": "[engines.no]\nsource = \"builtin:no\"\nscope = \"project\"\n[engines.no.settings]\nisolation = \"sometimes\"\n" + noModels})
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), `engines.no.settings.isolation: "sometimes" is not one of ask, existing, worktree`) {
		t.Fatalf("err = %v", err)
	}
}
