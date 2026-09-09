package catalog_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/agentfm"
	"github.com/noviopenworks/homonto/internal/catalog"
	"github.com/noviopenworks/homonto/internal/ontocli"
	"github.com/noviopenworks/homonto/internal/tocli"
	"github.com/noviopenworks/homonto/internal/workspace"
	"gopkg.in/yaml.v3"
)

func TestWorkspaceCommandsMatchCoordinatorPermissions(t *testing.T) {
	cl, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	content, known, err := cl.SubagentContent("homonto")
	if err != nil || !known {
		t.Fatalf("coordinator content: known=%t, %v", known, err)
	}
	for _, proxy := range []string{"", "none", "rtk"} {
		rendered, err := agentfm.Render("homonto", content, "opencode", &agentfm.RenderContext{
			ShellProxy: proxy, Overrides: map[string]agentfm.ModelSpec{"homonto": {Model: "provider/model"}},
		})
		if err != nil {
			t.Fatal(err)
		}
		var header struct {
			Permission map[string]yaml.Node `yaml:"permission"`
		}
		if err := yaml.Unmarshal([]byte(strings.SplitN(string(rendered), "---\n", 3)[1]), &header); err != nil {
			t.Fatal(err)
		}
		bash := header.Permission["bash"]
		if bash.Kind != yaml.MappingNode {
			t.Fatal("coordinator must have ordered Bash rules")
		}
		check := func(request, want string) {
			t.Helper()
			got := ""
			for i := 0; i < len(bash.Content); i += 2 {
				expr := strings.ReplaceAll(regexp.QuoteMeta(bash.Content[i].Value), `\*`, ".*")
				expr = strings.ReplaceAll(expr, `\?`, ".")
				if regexp.MustCompile("(?s)^" + expr + "$").MatchString(request) {
					got = bash.Content[i+1].Value
				}
			}
			if got != want {
				t.Errorf("proxy=%s request %q = %s, want %s", proxy, request, got, want)
			}
		}
		reference := catalog.RenderWorkspace(workspace.Layout{SchemaVersion: 2, ConfigRoot: "/work/config's home", ConfigPath: "/work/config's home/homonto.toml"})
		found := 0
		for _, line := range strings.Split(string(reference), "\n") {
			if !strings.HasPrefix(line, "onto status ") && !strings.HasPrefix(line, "to status ") {
				continue
			}
			found++
			check(line, "allow")
			check("rtk proxy "+line, "allow")
			check("rtk "+line, "allow") // Unknown wrappers inherit trusted shell execution.
		}
		if found != 2 {
			t.Fatalf("expected both generated status commands, got %d", found)
		}
		for _, executable := range []string{"onto", "to"} {
			for _, tc := range []struct {
				args          []string
				command, want string
			}{
				{[]string{"status", "--dir", "/workspace"}, "status", "allow"},
				{[]string{"--dir", "/workspace", "status"}, "status", "ask"},
				{[]string{"status", "--help"}, "status", "allow"},
				{[]string{"help", "status"}, "help", "allow"},
				{[]string{"--dir", "/workspace", "bypass", "change"}, "bypass", "ask"},
				{[]string{"--dir=/workspace", "bypass", "change"}, "bypass", "ask"},
				{[]string{"--help", "bypass", "change"}, "bypass", "ask"},
				{[]string{"bypass", "--help"}, "bypass", "deny"},
				{[]string{"help", "bypass"}, "help", "allow"},
				{[]string{"new", "bypass-fix", "--dir", "/workspace"}, "new", "allow"},
			} {
				root := ontocli.NewRootCmd()
				if executable == "to" {
					root = tocli.NewRootCmd()
				}
				root.InitDefaultHelpCmd()
				root.InitDefaultHelpFlag()
				// Resolve only. Never Execute: bypass fixtures must not mutate records.
				command, _, err := root.Find(tc.args)
				if err != nil || command.Name() != tc.command {
					t.Fatalf("%s %v resolves to %v, %v; want %s", executable, tc.args, command, err, tc.command)
				}
				request := executable + " " + strings.Join(tc.args, " ")
				check(request, tc.want)
				check("rtk proxy "+request, tc.want)
				check("rtk "+request, tc.want)
			}
		}
	}
}
