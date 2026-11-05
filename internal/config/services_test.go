package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree writes files under dir and returns dir/homonto.toml.
func writeTree(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	for path, data := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "homonto.toml")
}

const customWorkflow = `manifest_schema = 2
name = "custom"
kind = "workflow"
version = "0.1.0"
[compat]
contract = "1.0"
[workflow]
entry = ["triage"]
[requires.services]
github = "host-gh"
[commands]
triage = "commands/triage.md"
`

func TestCustomWorkflowUsesAServiceWithoutAHostingWorkflow(t *testing.T) {
	path := writeTree(t, t.TempDir(), map[string]string{
		"custom/framework.toml":     customWorkflow,
		"custom/commands/triage.md": "Triage the issue.\n",
		"homonto.toml":              "[workflows.custom]\nsource = \"local:./custom\"\nscope = \"project\"\n",
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.ImpliedServices(); got["host-gh"] != "custom" || len(got) != 1 {
		t.Fatalf("implied services = %v", got)
	}
	if !c.HostingProviders()["github"] {
		t.Fatal("the default GitHub service did not enable its runtime provider")
	}
	for name := range c.Frameworks {
		if strings.HasPrefix(name, "h-workflow-") {
			t.Fatalf("a hosting workflow was installed: %s", name)
		}
	}
	if r := c.Frameworks["host-gh"]; r.Source != "builtin:host-gh" || r.Scope != "project" {
		t.Fatalf("implied service declaration = %+v", r)
	}
}

func TestDeclaredServiceReplacesTheDefault(t *testing.T) {
	path := writeTree(t, t.TempDir(), map[string]string{
		"custom/framework.toml":     customWorkflow,
		"custom/commands/triage.md": "Triage the issue.\n",
		"ghe/framework.toml": `manifest_schema = 2
name = "ghe"
kind = "service"
version = "0.1.0"
[compat]
contract = "1.0"
[service]
id = "github"
operations = ["read", "publish"]
transport = "gh"
[permissions.coordinator]
bash_ask = ["gh api -X DELETE*"]
[permissions.workers]
bash_deny = ["gh *"]
`,
		"homonto.toml": "[workflows.custom]\nsource = \"local:./custom\"\nscope = \"project\"\n[services.ghe]\nsource = \"local:./ghe\"\nscope = \"project\"\n",
	})
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.ImpliedServices()) != 0 {
		t.Fatalf("default installed despite a declared service: %v", c.ImpliedServices())
	}
	if c.HostingProviders()["github"] {
		t.Fatal("a service without runtime enabled the GitHub provider")
	}
	rules := c.InstalledServiceRules()
	if strings.Join(rules.CoordinatorAsk, ",") != "gh api -X DELETE*" || strings.Join(rules.WorkerDeny, ",") != "gh *" {
		t.Fatalf("rules = %+v", rules)
	}
}

func TestServiceDeclarationErrors(t *testing.T) {
	for _, tc := range []struct {
		name, config, want string
	}{
		{"engine under services", "[services.onto]\nsource = \"builtin:onto\"\nscope = \"project\"\n", `services.onto source "builtin:onto" is an engine, not a service`},
		{"service under workflows", "[workflows.host-gh]\nsource = \"builtin:host-gh\"\nscope = \"project\"\n", "is a service, not a workflow"},
		{"two services for one id", "[services.a]\nsource = \"builtin:host-gh\"\nscope = \"project\"\n[services.host-gh]\nsource = \"builtin:host-gh\"\nscope = \"project\"\n", `both implement service "github"`},
		{"name in two tables", "[services.x]\nsource = \"builtin:host-gh\"\nscope = \"project\"\n[engines.x]\nsource = \"builtin:no\"\nscope = \"project\"\n", `"x" is declared as both an engine and a service`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTree(t, t.TempDir(), map[string]string{"homonto.toml": tc.config})
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestHostingWorkflowsInstallTheirDefaultService(t *testing.T) {
	for workflow, provider := range map[string]string{"h-workflow-gh": "github", "h-workflow-gl": "gitlab", "h-workflow-bb": "bitbucket"} {
		c := &Config{Frameworks: map[string]Resource{workflow: {Source: "builtin:" + workflow}}}
		if err := c.ResolvePackages(); err != nil {
			t.Fatal(err)
		}
		if got := c.HostingProviders(); len(got) != 1 || !got[provider] {
			t.Errorf("%s providers = %v, want %s", workflow, got, provider)
		}
		// Resolving again (after remote roots are injected) is idempotent.
		if err := c.ResolvePackages(); err != nil || len(c.ImpliedServices()) != 1 {
			t.Errorf("%s re-resolve: %v %v", workflow, c.ImpliedServices(), err)
		}
	}
}
