package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/jsonutil"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workflowstatus"
)

func TestWorkflowBindingCoordinatorAndGithubScope(t *testing.T) {
	root := t.TempDir()
	for _, source := range []string{"", "builtin:h", "local:h"} {
		for _, name := range []string{"homonto", "coordinator"} {
			e := &Engine{ConfigPath: filepath.Join(root, "selected.toml"), ProjectRoot: root, Cfg: &config.Config{
				Frameworks: map[string]config.Resource{"h": {Source: source}},
				Subagents:  map[string]config.Subagent{name: {Source: "builtin:homonto"}},
			}}
			data, err := e.workflowBinding()
			if err != nil {
				t.Fatal(err)
			}
			var binding struct {
				Coordinator   string `json:"coordinator"`
				GithubEnabled bool   `json:"githubEnabled"`
			}
			if err := json.Unmarshal(data, &binding); err != nil {
				t.Fatal(err)
			}
			if binding.Coordinator != name || binding.GithubEnabled != (source == "builtin:h") {
				t.Fatalf("source=%q alias=%q binding=%s", source, name, data)
			}
		}
	}
}

// TestBundledPluginMaterializesAndProjects (A3): with the onto framework
// declared, the permission-observer plugin materializes under
// .homonto/catalog/plugins/; declaring it in [plugins.opencode] projects its
// materialized path into opencode.jsonc; a re-apply is a no-op.
func TestBundledPluginMaterializesAndProjects(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(`
[frameworks.onto]
source = "builtin:onto"
scope = "project"

`+ontoFrameworkModels+`

[plugins.opencode.permission-observer]
source = "permission-observer"
`), 0o644)

	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Plugin materialized as owned catalog content.
	pluginEntry := filepath.Join(repo, ".homonto", "catalog", "plugins", "permission-observer", "plugin.ts")
	if _, err := os.Stat(pluginEntry); err != nil {
		t.Fatalf("bundled plugin not materialized: %v", err)
	}

	// Projected into the plugin array by materialized path.
	cfgPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Dir(pluginEntry)
	if !containsString(string(data), want) {
		t.Fatalf("plugin array entry missing materialized path %q:\n%s", want, data)
	}

	// Idempotent re-apply.
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range sets {
		for _, c := range cs.Changes {
			if c.Action != "noop" && c.Action != "adopt" {
				t.Fatalf("re-apply not clean: %s %s", c.Action, c.Key)
			}
		}
	}
}

func TestWorkflowBridgeProjectsForFrameworkAndHonorsOptOut(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	configPath := filepath.Join(repo, "homonto.toml")
	base := `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

` + ontoFrameworkModels + "\n[integrations.opencode]\nworkflow_bridge = true\n"
	if err := os.WriteFile(configPath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), configPath, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(repo, ".opencode", "plugins", "homonto-workflow.ts")
	src := filepath.Join(repo, ".homonto", "catalog", "plugins", "homonto-workflow", "plugin.ts")
	want := filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow/plugin.ts")
	if target, err := os.Readlink(dst); err != nil || target != want {
		t.Fatalf("workflow bridge = %q, %v; want relative symlink to %q", target, err, want)
	}
	if resolved, err := filepath.EvalSymlinks(dst); err != nil || resolved != src {
		t.Fatalf("workflow bridge resolves to %q, %v; want %q", resolved, err, src)
	}

	if err := os.WriteFile(configPath, []byte(strings.Replace(base, "workflow_bridge = true", "workflow_bridge = false", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err = Build(context.Background(), configPath, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("disabled workflow bridge still exists: %v", err)
	}
}

func TestBuiltinWorkflowDefaultsToV2Directory(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	configPath := filepath.Join(repo, "homonto.toml")
	pluginTestWrite(t, configPath, "[frameworks.onto]\nsource = 'builtin:onto'\nscope = 'project'\n"+ontoFrameworkModels)
	e, err := Build(context.Background(), configPath, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(e.workflowBridgeDestination()); !os.IsNotExist(err) {
		t.Fatalf("V1 bridge installed by default: %v", err)
	}
	if target, err := os.Readlink(e.workflowContextDestination()); err != nil ||
		target != filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow") {
		t.Fatalf("V2 directory link = %q, %v", target, err)
	}
	if e.CatalogNeedsMaterialize() {
		t.Fatal("V2 default did not converge")
	}
}

func TestWorkflowBridgeSurvivesProjectMove(t *testing.T) {
	for _, schema := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			parent, home := t.TempDir(), t.TempDir()
			repo := filepath.Join(parent, "old", "project")
			configPath := filepath.Join(repo, "selected config.toml")
			text := fmt.Sprintf("schema_version = %d\n[frameworks.onto]\nsource = 'builtin:onto'\nscope = 'project'\n", schema) + ontoFrameworkModels + "\n[integrations.opencode]\nworkflow_bridge = true\n"
			pluginTestWrite(t, configPath, text)
			e, err := Build(context.Background(), configPath, home, "homonto")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			want := filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow/plugin.ts")
			checkLink := func(root string) {
				t.Helper()
				dst := filepath.Join(root, ".opencode/plugins/homonto-workflow.ts")
				if target, err := os.Readlink(dst); err != nil || target != want {
					t.Fatalf("link target = %q, %v; want %q", target, err, want)
				}
				if resolved, err := filepath.EvalSymlinks(dst); err != nil || resolved != filepath.Join(root, ".homonto/catalog/plugins/homonto-workflow/plugin.ts") {
					t.Fatalf("link does not resolve within moved project: %q, %v", resolved, err)
				}
			}
			checkLink(repo)
			moved := filepath.Join(parent, "new", "project")
			if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(repo, moved); err != nil {
				t.Fatal(err)
			}
			checkLink(moved) // the existing link must resolve even before reapply
			configPath = filepath.Join(moved, filepath.Base(configPath))
			e, err = Build(context.Background(), configPath, home, "homonto")
			if err != nil {
				t.Fatal(err)
			}
			if !e.workflowBridgePresent() {
				t.Fatal("relative link lost ownership after move")
			}
			if !e.CatalogNeedsMaterialize() {
				t.Fatal("moved config binding not detected")
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			checkLink(moved)
			data, err := os.ReadFile(e.workflowBindingPath())
			if err != nil {
				t.Fatal(err)
			}
			var binding struct{ ConfigPath, ConfigRoot string }
			if err := json.Unmarshal(data, &binding); err != nil {
				t.Fatal(err)
			}
			if binding.ConfigPath != configPath || binding.ConfigRoot != moved {
				t.Fatalf("stale moved binding: %+v", binding)
			}
			if e.CatalogNeedsMaterialize() {
				t.Fatal("moved project did not converge")
			}
			before, err := os.Lstat(e.workflowBridgeDestination())
			if err != nil {
				t.Fatal(err)
			}
			sets := mustPlan(t, e)
			for _, cs := range sets {
				for _, c := range cs.Changes {
					if c.Action != "noop" && c.Action != "adopt" {
						t.Fatalf("reapply not idempotent: %+v", c)
					}
				}
			}
			if err := e.Apply(context.Background(), sets); err != nil {
				t.Fatal(err)
			}
			if after, err := os.Lstat(e.workflowBridgeDestination()); err != nil || !os.SameFile(before, after) {
				t.Fatalf("no-op replaced bridge link: %v", err)
			}
			pluginTestWrite(t, configPath, strings.Replace(text, "workflow_bridge = true", "workflow_bridge = false", 1))
			e, err = Build(context.Background(), configPath, home, "homonto")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(e.workflowBridgeDestination()); !os.IsNotExist(err) {
				t.Fatalf("moved bridge not removed on opt-out: %v", err)
			}
			if e.CatalogNeedsMaterialize() {
				t.Fatal("opt-out did not converge")
			}
		})
	}
}

func TestWorkflowBridgeMigratesOnlyCurrentAbsoluteLink(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			repo, home := t.TempDir(), t.TempDir()
			configPath := filepath.Join(repo, "homonto.toml")
			pluginTestWrite(t, configPath, "[integrations.opencode]\nworkflow_bridge = true\n")
			e, err := Build(context.Background(), configPath, home, "homonto")
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			dst := e.workflowBridgeDestination()
			if err := os.Remove(dst); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(e.workflowBridgeSource(), dst); err != nil {
				t.Fatal(err)
			}
			pluginTestWrite(t, configPath, fmt.Sprintf("[integrations.opencode]\nworkflow_bridge = %t\n", enabled))
			e, err = Build(context.Background(), configPath, home, "homonto")
			if err != nil {
				t.Fatal(err)
			}
			if e.workflowBridgePresent() || !e.CatalogNeedsMaterialize() {
				t.Fatal("absolute link incorrectly treated as converged")
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			if enabled {
				want := filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow/plugin.ts")
				if target, err := os.Readlink(dst); err != nil || target != want {
					t.Fatalf("migration target = %q, %v", target, err)
				}
			} else if _, err := os.Lstat(dst); !os.IsNotExist(err) {
				t.Fatalf("absolute owned link not removed: %v", err)
			}
			if e.CatalogNeedsMaterialize() {
				t.Fatal("migration/removal did not converge")
			}
		})
	}
}

func TestWorkflowBridgePreservesForeignTargets(t *testing.T) {
	for _, kind := range []string{"file", "directory", "relative", "absolute", "stale-absolute", "noncanonical-relative", "parent-symlink"} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/%t", kind, enabled), func(t *testing.T) {
				repo, foreign := t.TempDir(), t.TempDir()
				configPath := filepath.Join(repo, "homonto.toml")
				pluginTestWrite(t, configPath, fmt.Sprintf("[integrations.opencode]\nworkflow_bridge = %t\n", enabled))
				e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
				if err != nil {
					t.Fatal(err)
				}
				dst := e.workflowBridgeDestination()
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatal(err)
				}
				target := ""
				switch kind {
				case "file":
					pluginTestWrite(t, dst, "foreign plugin")
				case "directory":
					pluginTestWrite(t, filepath.Join(dst, "keep"), "foreign directory")
				case "relative":
					target = "../../foreign.ts"
				case "absolute":
					target = filepath.Join(foreign, "plugin.ts")
				case "stale-absolute":
					target = filepath.Join(foreign, ".homonto/catalog/plugins/homonto-workflow/plugin.ts")
				case "noncanonical-relative":
					target = "../../.homonto/catalog/plugins/homonto-workflow/../homonto-workflow/plugin.ts"
				case "parent-symlink":
					if err := os.Remove(filepath.Dir(dst)); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(foreign, filepath.Dir(dst)); err != nil {
						t.Fatal(err)
					}
					pluginTestWrite(t, filepath.Join(foreign, "homonto-workflow.ts"), "foreign plugin")
				}
				if target != "" {
					if err := os.Symlink(target, dst); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.Lstat(dst)
				if err != nil {
					t.Fatal(err)
				}
				if e.workflowBridgePresent() {
					t.Fatal("foreign projection considered owned")
				}
				err = e.Apply(context.Background(), mustPlan(t, e))
				if err == nil || !containsString(err.Error(), "workflow bridge:") {
					t.Fatalf("foreign projection not clearly refused: %v", err)
				}
				if after, err := os.Lstat(dst); err != nil || !os.SameFile(before, after) {
					t.Fatalf("foreign projection replaced: %v", err)
				}
				if target != "" {
					if got, err := os.Readlink(dst); err != nil || got != target {
						t.Fatalf("foreign target changed: %q, %v", got, err)
					}
				} else {
					path, want := dst, "foreign plugin"
					if kind == "directory" {
						path, want = filepath.Join(dst, "keep"), "foreign directory"
					}
					if data, err := os.ReadFile(path); err != nil || string(data) != want {
						t.Fatalf("foreign content changed: %q, %v", data, err)
					}
				}
			})
		}
	}
}

func TestWorkflowBridgeRefusesForeignPluginFile(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".opencode", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(repo, ".opencode", "plugins", "homonto-workflow.ts")
	if err := os.WriteFile(dst, []byte("foreign"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(configPath, []byte(`
[frameworks.onto]
source = "builtin:onto"
scope = "project"

`+ontoFrameworkModels), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), configPath, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	err = e.Apply(context.Background(), mustPlan(t, e))
	if err == nil || !containsString(err.Error(), "not a homonto-managed symlink") {
		t.Fatalf("foreign bridge error = %v", err)
	}
}

func TestWorkflowContextMaterializesAndRepairsWithoutFrameworks(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	for _, filename := range []string{"homonto.toml", "selected config.toml"} {
		configPath := filepath.Join(repo, filename)
		pluginTestWrite(t, configPath, "[integrations.opencode]\nworkflow_context = true\n")
		e, err := Build(context.Background(), configPath, home, "homonto")
		if err != nil {
			t.Fatal(err)
		}
		if !e.CatalogNeedsMaterialize() {
			t.Fatal("new context binding not detected")
		}
		p, err := e.planCatalog()
		if err != nil || p == nil || len(p.plugins) != 1 || p.plugins[0] != workflowBridgePlugin || len(p.skills)+len(p.commands)+len(p.subagents) != 0 {
			t.Fatalf("context-only catalog = %+v, %v", p, err)
		}
		apply := func() {
			t.Helper()
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			if e.CatalogNeedsMaterialize() {
				t.Fatal("context catalog did not converge")
			}
		}
		apply()
		dst := e.workflowContextDestination()
		want := filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow")
		if target, err := os.Readlink(dst); err != nil || target != want {
			t.Fatalf("context link = %q, %v; want %q", target, err, want)
		}
		for _, file := range []string{"plugin.ts", "index.ts", "tui.tsx", "rpc.ts", "v2.ts", "runner.ts"} {
			info, err := os.Lstat(filepath.Join(dst, file))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("projected %s missing or not regular: %v", file, err)
			}
		}
		if _, err := os.Lstat(dst + ".ts"); !os.IsNotExist(err) {
			t.Fatalf("old context file link survived: %v", err)
		}
		if _, err := os.Lstat(e.workflowBridgeDestination()); !os.IsNotExist(err) {
			t.Fatalf("unexpected legacy link: %v", err)
		}
		before, err := os.Lstat(dst)
		if err != nil {
			t.Fatal(err)
		}
		apply()
		if after, err := os.Lstat(dst); err != nil || !os.SameFile(before, after) {
			t.Fatalf("no-op replaced context link: %v", err)
		}
		for _, file := range []string{"index.ts", "tui.tsx", "rpc.ts", "v2.ts", "runner.ts", "binding.json"} {
			path := filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin, file)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if !e.CatalogNeedsMaterialize() {
				t.Fatalf("missing %s invisible to materialization gate", file)
			}
			apply()
		}
		helper := filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin, "runner.ts")
		helperBytes, err := os.ReadFile(helper)
		if err != nil {
			t.Fatal(err)
		}
		pluginTestWrite(t, helper, "stale helper from a previous catalog")
		e.State.SetRenderFingerprint("previous-catalog-fingerprint")
		if !e.CatalogNeedsMaterialize() {
			t.Fatal("stale catalog fingerprint invisible to materialization gate")
		}
		apply()
		if data, err := os.ReadFile(helper); err != nil || string(data) != string(helperBytes) {
			t.Fatalf("catalog refresh did not replace stale helper: %v", err)
		}
		pluginTestWrite(t, e.workflowBindingPath(), `{"version":1,"configPath":"wrong.toml"}`)
		if !e.CatalogNeedsMaterialize() {
			t.Fatal("binding mismatch invisible to materialization gate")
		}
		apply()
		data, err := os.ReadFile(e.workflowBindingPath())
		if err != nil {
			t.Fatal(err)
		}
		var binding struct {
			Version       int
			ConfigPath    string
			ConfigRoot    string
			Coordinator   string
			GithubEnabled bool
		}
		if err := json.Unmarshal(data, &binding); err != nil {
			t.Fatal(err)
		}
		if binding.Version != 1 || binding.ConfigPath != configPath || binding.ConfigRoot != repo || binding.Coordinator != "homonto" || binding.GithubEnabled {
			t.Fatalf("context binding = %+v", binding)
		}
	}
}

func TestWorkflowContextSwitchesOwnedLinks(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	configPath := filepath.Join(repo, "homonto.toml")
	base := "[frameworks.onto]\nsource = 'builtin:onto'\nscope = 'project'\n" + ontoFrameworkModels
	for _, mode := range []string{"legacy", "context", "legacy", "context", "disabled"} {
		text := base
		switch mode {
		case "legacy":
			text += "\n[integrations.opencode]\nworkflow_bridge = true\n"
		case "context":
			text += "\n[integrations.opencode]\nworkflow_context = true\n"
		case "disabled":
			text += "\n[integrations.opencode]\nworkflow_context = false\nworkflow_bridge = false\n"
		}
		pluginTestWrite(t, configPath, text)
		e, err := Build(context.Background(), configPath, home, "homonto")
		if err != nil {
			t.Fatal(err)
		}
		if !e.CatalogNeedsMaterialize() {
			t.Fatalf("switch to %s invisible to materialization gate", mode)
		}
		if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
			t.Fatal(err)
		}
		for _, link := range e.workflowPluginLinks() {
			if !link.enabled {
				if _, err := os.Lstat(link.destination); !os.IsNotExist(err) {
					t.Fatalf("%s: inactive link survives: %s, %v", mode, link.destination, err)
				}
			} else if resolved, err := filepath.EvalSymlinks(link.destination); err != nil || resolved != link.source {
				t.Fatalf("%s: active link resolves to %q, %v", mode, resolved, err)
			}
		}
		if mode == "disabled" {
			if info, err := os.Stat(filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin)); err != nil || !info.IsDir() {
				t.Fatalf("disabled links removed materialized catalog directory: %v", err)
			}
		}
		if e.CatalogNeedsMaterialize() {
			t.Fatalf("switch to %s did not converge", mode)
		}
	}
}

func TestWorkflowContextAdoptsDirectoryLinkAndMigratesOldFile(t *testing.T) {
	for _, targetKind := range []string{"relative", "absolute"} {
		t.Run(targetKind, func(t *testing.T) {
			repo := t.TempDir()
			configPath := filepath.Join(repo, "homonto.toml")
			pluginTestWrite(t, configPath, "[integrations.opencode]\nworkflow_context = true\n")
			e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
			if err != nil {
				t.Fatal(err)
			}
			dst := e.workflowContextDestination()
			old := dst + ".ts"
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			dirSource := filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin)
			dirTarget := filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow")
			if targetKind == "absolute" {
				dirTarget = dirSource
			}
			if err := os.Symlink(dirTarget, dst); err != nil {
				t.Fatal(err)
			}
			oldTarget := filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow/v2.ts")
			if err := os.Symlink(oldTarget, old); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(dst)
			if err != nil {
				t.Fatal(err)
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			if target, err := os.Readlink(dst); err != nil || target != filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow") {
				t.Fatalf("directory link target = %q, %v", target, err)
			}
			if _, err := os.Lstat(old); !os.IsNotExist(err) {
				t.Fatalf("owned old file link survived: %v", err)
			}
			if targetKind == "relative" {
				if after, err := os.Lstat(dst); err != nil || !os.SameFile(before, after) {
					t.Fatalf("adoption replaced relative directory link: %v", err)
				}
			}
			if e.CatalogNeedsMaterialize() {
				t.Fatal("adoption did not converge")
			}
		})
	}
}

func TestWorkflowContextMigrationPreflightsAllEndpoints(t *testing.T) {
	for _, conflict := range []string{"old-file", "directory", "legacy"} {
		for _, kind := range []string{"file", "directory", "symlink"} {
			t.Run(conflict+"/"+kind, func(t *testing.T) {
				repo := t.TempDir()
				configPath := filepath.Join(repo, "homonto.toml")
				pluginTestWrite(t, configPath, "[integrations.opencode]\nworkflow_bridge = true\n")
				e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
				if err != nil {
					t.Fatal(err)
				}
				if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
					t.Fatal(err)
				}
				old := e.workflowContextDestination() + ".ts"
				if err := os.Symlink(filepath.FromSlash("../../.homonto/catalog/plugins/homonto-workflow/v2.ts"), old); err != nil {
					t.Fatal(err)
				}
				path := map[string]string{"old-file": old, "directory": e.workflowContextDestination(), "legacy": e.workflowBridgeDestination()}[conflict]
				if conflict != "directory" {
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				}
				switch kind {
				case "file":
					pluginTestWrite(t, path, "foreign")
				case "directory":
					pluginTestWrite(t, filepath.Join(path, "keep"), "foreign")
				case "symlink":
					if err := os.Symlink("../../foreign", path); err != nil {
						t.Fatal(err)
					}
				}
				before := pluginTestTree(t, filepath.Dir(path))
				pluginTestWrite(t, configPath, "[integrations.opencode]\nworkflow_context = true\nworkflow_bridge = false\n")
				e, err = Build(context.Background(), configPath, e.Home, "homonto")
				if err != nil {
					t.Fatal(err)
				}
				repoBefore := pluginTestTree(t, repo)
				if err := e.Apply(context.Background(), mustPlan(t, e)); err == nil || !containsString(err.Error(), "workflow bridge:") {
					t.Fatalf("conflict not refused: %v", err)
				}
				if !reflect.DeepEqual(before, pluginTestTree(t, filepath.Dir(path))) {
					t.Fatal("conflict changed an existing plugin endpoint")
				}
				if !reflect.DeepEqual(repoBefore, pluginTestTree(t, repo)) {
					t.Fatal("conflict mutated project before all endpoints were checked")
				}
			})
		}
	}
}

func TestWorkflowContextDirectoryLinkSurvivesMove(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "old")
	configPath := filepath.Join(repo, "selected config.toml")
	pluginTestWrite(t, configPath, "[integrations.opencode]\nworkflow_context = true\n")
	e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "new")
	if err := os.Rename(repo, moved); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(moved, ".opencode/plugins/homonto-workflow-context")
	if resolved, err := filepath.EvalSymlinks(filepath.Join(dst, "index.ts")); err != nil || resolved != filepath.Join(moved, ".homonto/catalog/plugins/homonto-workflow/index.ts") {
		t.Fatalf("moved directory link resolves to %q: %v", resolved, err)
	}
	e, err = Build(context.Background(), filepath.Join(moved, "selected config.toml"), e.Home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if !e.workflowBridgePresent() || !e.CatalogNeedsMaterialize() {
		t.Fatal("move broke link ownership or failed to notice stale binding")
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	if e.CatalogNeedsMaterialize() {
		t.Fatal("move did not converge")
	}
}

func TestWorkflowContextRejectsSymlinkedCatalogDirectory(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	configPath := filepath.Join(repo, "homonto.toml")
	pluginTestWrite(t, configPath, "[integrations.opencode]\nworkflow_context = true\n")
	e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	dst := e.workflowContextDestination()
	before, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin)
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	pluginTestWrite(t, filepath.Join(outside, "index.ts"), "foreign")
	if err := os.Symlink(outside, source); err != nil {
		t.Fatal(err)
	}
	outsideBefore := pluginTestTree(t, outside)
	if !e.CatalogNeedsMaterialize() {
		t.Fatal("symlinked source hidden by materialization gate")
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err == nil || !containsString(err.Error(), "catalog:") {
		t.Fatalf("symlinked source not refused: %v", err)
	}
	if after, err := os.Lstat(dst); err != nil || !os.SameFile(before, after) {
		t.Fatalf("projected link changed after source rejection: %v", err)
	}
	if !reflect.DeepEqual(outsideBefore, pluginTestTree(t, outside)) {
		t.Fatal("symlinked source changed outside content")
	}
}

func TestWorkflowContextSwitchConflictPreservesActiveLink(t *testing.T) {
	for _, contextEnabled := range []bool{false, true} {
		for _, kind := range []string{"file", "directory", "symlink", "parent-symlink"} {
			t.Run(fmt.Sprintf("context=%t/%s", contextEnabled, kind), func(t *testing.T) {
				repo, home := t.TempDir(), t.TempDir()
				configPath := filepath.Join(repo, "homonto.toml")
				build := func(enabled bool) *Engine {
					t.Helper()
					pluginTestWrite(t, configPath, fmt.Sprintf("[integrations.opencode]\nworkflow_context = %t\nworkflow_bridge = %t\n", enabled, !enabled))
					e, err := Build(context.Background(), configPath, home, "homonto")
					if err != nil {
						t.Fatal(err)
					}
					return e
				}
				e := build(contextEnabled)
				if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
					t.Fatal(err)
				}
				active, next := e.workflowBridgeDestination(), e.workflowContextDestination()
				if contextEnabled {
					active, next = next, active
				}
				before, err := os.Lstat(active)
				if err != nil {
					t.Fatal(err)
				}
				dir := filepath.Dir(active)
				external := t.TempDir()
				switch kind {
				case "file":
					pluginTestWrite(t, next, "foreign plugin")
				case "directory":
					pluginTestWrite(t, filepath.Join(next, "keep"), "foreign plugin")
				case "symlink":
					if err := os.Symlink("../../foreign.ts", next); err != nil {
						t.Fatal(err)
					}
				case "parent-symlink":
					external = filepath.Join(external, "plugins")
					if err := os.Rename(dir, external); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(external, dir); err != nil {
						t.Fatal(err)
					}
				}
				plugins, outside := pluginTestTree(t, dir), pluginTestTree(t, external)
				e = build(!contextEnabled)
				if !e.CatalogNeedsMaterialize() {
					t.Fatal("switch conflict hidden from materialization gate")
				}
				if err := e.Apply(context.Background(), mustPlan(t, e)); err == nil || !containsString(err.Error(), "workflow bridge:") {
					t.Fatalf("switch conflict not refused: %v", err)
				}
				if after, err := os.Lstat(active); err != nil || !os.SameFile(before, after) {
					t.Fatalf("switch conflict removed or replaced prior active link: %v", err)
				}
				if !reflect.DeepEqual(plugins, pluginTestTree(t, dir)) || !reflect.DeepEqual(outside, pluginTestTree(t, external)) {
					t.Fatal("switch conflict changed plugin paths or foreign content")
				}
			})
		}
	}
}

func TestWorkflowContextPreservesForeignPaths(t *testing.T) {
	for _, mode := range []string{"context", "legacy", "disabled"} {
		for _, kind := range []string{"file", "directory", "symlink", "parent-symlink"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				repo, foreign := t.TempDir(), t.TempDir()
				configPath := filepath.Join(repo, "homonto.toml")
				pluginTestWrite(t, configPath, fmt.Sprintf("[integrations.opencode]\nworkflow_context = %t\nworkflow_bridge = %t\n", mode == "context", mode == "legacy"))
				e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
				if err != nil {
					t.Fatal(err)
				}
				dst := e.workflowContextDestination()
				if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "file":
					pluginTestWrite(t, dst, "foreign context plugin")
				case "directory":
					pluginTestWrite(t, filepath.Join(dst, "keep"), "foreign context plugin")
				case "symlink":
					if err := os.Symlink("../../foreign.ts", dst); err != nil {
						t.Fatal(err)
					}
				case "parent-symlink":
					if err := os.Remove(filepath.Dir(dst)); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(foreign, filepath.Dir(dst)); err != nil {
						t.Fatal(err)
					}
					pluginTestWrite(t, dst, "foreign context plugin")
				}
				before := pluginTestTree(t, filepath.Dir(dst))
				external := pluginTestTree(t, foreign)
				if !e.CatalogNeedsMaterialize() {
					t.Fatal("foreign context path hidden from materialization gate")
				}
				if err := e.Apply(context.Background(), mustPlan(t, e)); err == nil || !containsString(err.Error(), "workflow bridge:") {
					t.Fatalf("foreign context path not refused: %v", err)
				}
				if !reflect.DeepEqual(before, pluginTestTree(t, filepath.Dir(dst))) {
					t.Fatal("foreign context path changed")
				}
				if !reflect.DeepEqual(external, pluginTestTree(t, foreign)) {
					t.Fatal("foreign context content changed")
				}
			})
		}
	}
}

func TestWorkflowContextRuntimeUsesMaterializedConfigBinding(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable; V2 projected context runtime contract not run")
	}
	if out, err := exec.Command("node", "--input-type=module", "-e", "process.exit(process.features.typescript ? 0 : 1)").CombinedOutput(); err != nil {
		t.Skipf("Node with native type stripping required (22.18+ or compatible newer release); V2 context runtime contract not run: %v\n%s", err, out)
	}
	for _, schema := range []int{0, 1, 2} {
		for _, filename := range []string{"homonto.toml", "selected config.toml"} {
			t.Run(fmt.Sprintf("schema%d/%s", schema, filename), func(t *testing.T) {
				repo := t.TempDir()
				configPath := filepath.Join(repo, filename)
				pluginTestWrite(t, filepath.Join(repo, "Cargo.toml"), "[package]\nname = 'not-homonto'\n")
				pluginTestWrite(t, filepath.Join(repo, "homonto.toml"), "invalid = [")
				pluginTestWrite(t, configPath, fmt.Sprintf("schema_version = %d\n[integrations.opencode]\nworkflow_context = true\n", schema))
				e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
				if err != nil {
					t.Fatal(err)
				}
				if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
					t.Fatal(err)
				}
				if schema == 2 {
					if err := workflowroot.WriteLayoutMarker(workflowroot.LayoutMarker{SchemaVersion: 2, ConfigPath: configPath, WorkflowRoot: e.WorkspaceLayout.WorkflowRoot, GitMode: e.WorkspaceLayout.GitMode}); err != nil {
						t.Fatal(err)
					}
				}
				pluginTestWrite(t, filepath.Join(repo, "docs/changes/one/onto-state.yaml"), "schema_version: 2\nid: id-one\nchange: one\nworkflow: full\nphase: build\n")
				pluginTestWrite(t, filepath.Join(repo, "docs/changes/one/tasks.md"), "- [x] first\n- [ ] second\n")
				snapshot := workflowstatus.ReadConfig(configPath)
				if len(snapshot.Findings) != 0 || len(snapshot.Changes) != 1 {
					t.Fatalf("backend snapshot: %+v", snapshot)
				}
				data, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				fixture := filepath.Join(repo, "snapshot.json")
				pluginTestWrite(t, fixture, string(data))
				launch := filepath.Join(repo, "source/nested")
				if err := os.MkdirAll(launch, 0o755); err != nil {
					t.Fatal(err)
				}
				script, err := filepath.Abs("../workflowstatus/testdata/context-runtime.mjs")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "node", script, filepath.Join(e.workflowContextDestination(), "index.ts"), configPath, fixture)
				cmd.Dir = launch
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("native projected V2 context runtime contract: %v\n%s", err, out)
				}
			})
		}
	}
}

func TestWorkflowBridgeRuntimeUsesMaterializedConfigBinding(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable; projection runtime contract not run")
	}
	for _, schema := range []int{0, 1, 2} {
		for _, filename := range []string{"homonto.toml", "selected config.toml"} {
			t.Run(fmt.Sprintf("schema%d/%s", schema, filename), func(t *testing.T) {
				repo := t.TempDir()
				configPath := filepath.Join(repo, filename)
				pluginTestWrite(t, filepath.Join(repo, "Cargo.toml"), "[package]\nname = 'not-homonto'\n")
				pluginTestWrite(t, filepath.Join(repo, "homonto.toml"), "invalid = [")
				// No framework/references are needed to bind an explicitly enabled bridge.
				pluginTestWrite(t, configPath, fmt.Sprintf("schema_version = %d\n[integrations.opencode]\nworkflow_bridge = true\n", schema))
				e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
				if err != nil {
					t.Fatal(err)
				}
				if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
					t.Fatal(err)
				}
				if schema == 2 {
					if err := workflowroot.WriteLayoutMarker(workflowroot.LayoutMarker{SchemaVersion: 2, ConfigPath: configPath, WorkflowRoot: e.WorkspaceLayout.WorkflowRoot, GitMode: e.WorkspaceLayout.GitMode}); err != nil {
						t.Fatal(err)
					}
				}
				pluginTestWrite(t, filepath.Join(repo, "docs/changes/one/onto-state.yaml"), "schema_version: 2\nid: id-one\nchange: one\nworkflow: full\nphase: build\n")
				pluginTestWrite(t, filepath.Join(repo, "docs/changes/one/tasks.md"), "- [x] first\n- [ ] second\n")
				snapshot := workflowstatus.ReadConfig(configPath)
				if len(snapshot.Findings) != 0 || len(snapshot.Changes) != 1 {
					t.Fatalf("backend snapshot: %+v", snapshot)
				}
				data, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				fixture := filepath.Join(repo, "snapshot.json")
				pluginTestWrite(t, fixture, string(data))
				handoff, err := workflowstatus.ReadHandoff(configPath, "onto", "one", snapshot.Changes[0].Identity)
				if err != nil {
					t.Fatal(err)
				}
				handoffData, err := json.Marshal(handoff)
				if err != nil {
					t.Fatal(err)
				}
				handoffFixture := filepath.Join(repo, "handoff.json")
				pluginTestWrite(t, handoffFixture, string(handoffData))
				launch := filepath.Join(repo, "source/nested")
				if err := os.MkdirAll(launch, 0o755); err != nil {
					t.Fatal(err)
				}
				script, err := filepath.Abs("../workflowstatus/testdata/plugin-runtime.mjs")
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "node", script, e.workflowBridgeDestination(), configPath, fixture, handoffFixture)
				cmd.Dir = launch
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("native projected TypeScript runtime contract: %v\n%s", err, out)
				}
			})
		}
	}
}

func TestWorkflowBindingRepairsAndConfigSwitchesAllSchemas(t *testing.T) {
	for _, schema := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			repo, home := t.TempDir(), t.TempDir()
			pluginTestWrite(t, filepath.Join(repo, "Cargo.toml"), "[package]\nname = 'not-homonto'\n")
			var fingerprint string
			for _, filename := range []string{"homonto.toml", "selected config.toml"} {
				configPath := filepath.Join(repo, filename)
				pluginTestWrite(t, configPath, fmt.Sprintf("schema_version = %d\n[integrations.opencode]\nworkflow_bridge = true\n", schema))
				cwd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				rel, err := filepath.Rel(cwd, configPath)
				if err != nil {
					t.Fatal(err)
				}
				e, err := Build(context.Background(), rel, home, "homonto")
				if err != nil {
					t.Fatal(err)
				}
				if e.ConfigPath != configPath {
					t.Fatalf("Build lost selected filename: %q", e.ConfigPath)
				}
				if !e.CatalogNeedsMaterialize() {
					t.Fatal("new config binding appears up to date")
				}
				if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
					t.Fatal(err)
				}
				if next := e.State.RenderFingerprintRecorded(); next == fingerprint {
					t.Fatal("config filename absent from fingerprint")
				} else {
					fingerprint = next
				}
				bindingPath := e.workflowBindingPath()
				want, err := json.MarshalIndent(map[string]any{"version": 1, "configPath": configPath, "configRoot": repo, "coordinator": "homonto", "githubEnabled": false}, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				check := func() {
					t.Helper()
					data, err := os.ReadFile(bindingPath)
					if err != nil {
						t.Fatal(err)
					}
					if string(jsonutil.Canonical(string(data))) != string(jsonutil.Canonical(string(want))) {
						t.Fatalf("binding = %s, want %s", data, want)
					}
					if e.CatalogNeedsMaterialize() {
						t.Fatal("binding did not converge")
					}
				}
				check()
				stamp := time.Unix(123456789, 0)
				if err := os.Chtimes(bindingPath, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
					t.Fatal(err)
				}
				if info, err := os.Stat(bindingPath); err != nil || !info.ModTime().Equal(stamp) {
					t.Fatalf("no-op rewrote binding: %v", err)
				}
				for _, damage := range []string{"deleted", "changed", "directory"} {
					if err := os.Remove(bindingPath); err != nil {
						t.Fatal(err)
					}
					switch damage {
					case "changed":
						pluginTestWrite(t, bindingPath, `{"version":1,"configPath":"wrong.toml"}`)
					case "directory":
						if err := os.Mkdir(bindingPath, 0o755); err != nil {
							t.Fatal(err)
						}
					}
					if !e.CatalogNeedsMaterialize() {
						t.Fatalf("%s binding is invisible to the no-op apply gate", damage)
					}
					sets := mustPlan(t, e)
					for _, cs := range sets {
						for _, c := range cs.Changes {
							if c.Action != "noop" && c.Action != "adopt" {
								t.Fatalf("expected catalog-only repair, got %+v", c)
							}
						}
					}
					if err := e.Apply(context.Background(), sets); err != nil {
						t.Fatal(err)
					}
					check()
				}
			}
		})
	}
}

func TestPluginOnlyConfigMaterializesAndRepairsDirectoryProjection(t *testing.T) {
	repo, home := t.TempDir(), t.TempDir()
	configPath := filepath.Join(repo, "homonto.toml")
	pluginTestWrite(t, configPath, "[plugins.opencode.permission-observer]\nsource = 'permission-observer'\n")
	e, err := Build(context.Background(), configPath, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.planCatalog()
	if err != nil || p == nil {
		t.Fatalf("plugin-only materialization: %+v, %v", p, err)
	}
	if len(p.skills)+len(p.commands)+len(p.subagents) != 0 || len(p.plugins) != 1 || p.plugins[0] != "permission-observer" {
		t.Fatalf("plugin-only catalog = %+v", p)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(e.PluginCatalogRoot, "permission-observer", "index.ts")
	cfgPath := filepath.Join(home, ".config/opencode/opencode.jsonc")
	check := func() {
		t.Helper()
		if info, err := os.Stat(entry); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("entrypoint missing: %v", err)
		}
		data, err := os.ReadFile(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		data, err = jsonutil.Standardize(data)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct {
			Plugin []string `json:"plugin"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, plugin := range cfg.Plugin {
			if plugin == filepath.Join(filepath.Dir(entry), "plugin.ts") {
				t.Fatal("V1 entrypoint survived repair")
			}
			found = found || plugin == filepath.Dir(entry)
		}
		if !found {
			t.Fatalf("entrypoint absent from plugin array: %v", cfg.Plugin)
		}
		if e.CatalogNeedsMaterialize() {
			t.Fatal("plugin-only catalog did not converge")
		}
	}
	check()
	// Reproduce the old explicit bare-name projection's persisted directory value.
	old, err := json.Marshal(filepath.Join(filepath.Dir(entry), "plugin.ts"))
	if err != nil {
		t.Fatal(err)
	}
	pluginTestWrite(t, cfgPath, `{"plugin":["foreign",`+string(old)+`]}`)
	e.State.Set("opencode", "plugin.permission-observer", string(old), secret.Hash(string(old)))
	if err := e.State.Save(e.StateDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	e, err = Build(context.Background(), configPath, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if !e.CatalogNeedsMaterialize() {
		t.Fatal("missing plugin-only entrypoint appears healthy")
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	check()
	data, err := os.ReadFile(cfgPath)
	if err != nil || !containsString(string(data), "foreign") {
		t.Fatalf("foreign plugin lost: %s, %v", data, err)
	}
	for _, cs := range mustPlan(t, e) {
		for _, c := range cs.Changes {
			if c.Action != "noop" {
				t.Fatalf("not idempotent: %+v", c)
			}
		}
	}
}

func pluginTestWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pluginTestTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += target
		} else if !d.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += string(data)
		}
		out[rel] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCatalogPreflightPreservesSymlinkedRootsAndEntries(t *testing.T) {
	for _, rel := range []string{
		".homonto", ".homonto/catalog", ".homonto/catalog/plugins", ".homonto/catalog/plugins/homonto-workflow",
		".homonto/catalog/skills", ".homonto/catalog/skills/onto", ".homonto/catalog/skills/onto.staging",
		".homonto/catalog/commands", ".homonto/catalog/commands/onto.md",
		".homonto/catalog/subagents", ".homonto/catalog/subagents/onto-reviewer.md", ".homonto/catalog/plugins/obsolete",
	} {
		t.Run(rel, func(t *testing.T) {
			repo, outside := t.TempDir(), t.TempDir()
			configPath := filepath.Join(repo, "homonto.toml")
			pluginTestWrite(t, configPath, "[frameworks.onto]\nsource = 'builtin:onto'\nscope = 'project'\n"+ontoFrameworkModels)
			e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
			if err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{"sentinel", "homonto-workflow/sentinel", "catalog/plugins/homonto-workflow/sentinel", "plugins/homonto-workflow/sentinel", "onto.staging/sentinel", "obsolete/sentinel", "onto.md", "onto-reviewer.md"} {
				pluginTestWrite(t, filepath.Join(outside, p), "preserve outside bytes")
			}
			link := filepath.Join(repo, rel)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			before, external := pluginTestTree(t, repo), pluginTestTree(t, outside)
			if !e.CatalogNeedsMaterialize() {
				t.Fatal("unsafe catalog hidden behind no-op gate")
			}
			err = e.Apply(context.Background(), mustPlan(t, e))
			if err == nil || !containsString(err.Error(), "catalog:") || !containsString(err.Error(), "symlink") {
				t.Fatalf("unsafe catalog not rejected clearly: %v", err)
			}
			if !reflect.DeepEqual(before, pluginTestTree(t, repo)) {
				t.Fatal("apply wrote or removed generated content before rejecting a later unsafe root")
			}
			if !reflect.DeepEqual(external, pluginTestTree(t, outside)) {
				t.Fatal("apply deleted, created, or changed outside content")
			}
		})
	}
}

func TestWorkflowSourceSymlinkRepairDoesNotWriteOutside(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	configPath := filepath.Join(repo, "homonto.toml")
	pluginTestWrite(t, configPath, "[integrations.opencode]\nworkflow_bridge = true\n")
	e, err := Build(context.Background(), configPath, t.TempDir(), "homonto")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	source := e.workflowBridgeSource()
	want, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "sentinel.ts")
	pluginTestWrite(t, sentinel, "foreign plugin bytes")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sentinel, source); err != nil {
		t.Fatal(err)
	}
	before := pluginTestTree(t, outside)
	if !e.CatalogNeedsMaterialize() {
		t.Fatal("changed source symlink treated as healthy")
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, pluginTestTree(t, outside)) {
		t.Fatal("source repair rewrote outside data")
	}
	if info, err := os.Lstat(source); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("source remains symlinked: %v", err)
	}
	if data, err := os.ReadFile(source); err != nil || string(data) != string(want) {
		t.Fatalf("source not restored: %v", err)
	}
	if e.CatalogNeedsMaterialize() {
		t.Fatal("source/binding repair did not converge")
	}
	if !e.workflowBridgePresent() {
		t.Fatal("relative bridge ownership broken by repair")
	}
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
