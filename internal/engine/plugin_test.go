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
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/jsonutil"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workflowstatus"
)

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
	want := pluginEntry
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

` + ontoFrameworkModels
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

	if err := os.WriteFile(configPath, []byte(base+`
[integrations.opencode]
workflow_bridge = false
`), 0o644); err != nil {
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

func TestWorkflowBridgeSurvivesProjectMove(t *testing.T) {
	for _, schema := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(schema), func(t *testing.T) {
			parent, home := t.TempDir(), t.TempDir()
			repo := filepath.Join(parent, "old", "project")
			configPath := filepath.Join(repo, "selected config.toml")
			text := fmt.Sprintf("schema_version = %d\n[frameworks.onto]\nsource = 'builtin:onto'\nscope = 'project'\n", schema) + ontoFrameworkModels
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
			pluginTestWrite(t, configPath, text+"\n[integrations.opencode]\nworkflow_bridge = false\n")
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
				cmd := exec.CommandContext(ctx, "node", script, e.workflowBridgeDestination(), configPath, fixture)
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
				want, err := json.MarshalIndent(map[string]any{"version": 1, "configPath": configPath, "configRoot": repo}, "", "  ")
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
	entry := filepath.Join(e.PluginCatalogRoot, "permission-observer", "plugin.ts")
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
			if plugin == filepath.Dir(entry) {
				t.Fatal("directory entry survived repair")
			}
			found = found || plugin == entry
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
	old, err := json.Marshal(filepath.Dir(entry))
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
