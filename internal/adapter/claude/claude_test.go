package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/tidwall/gjson"
)

func noSecret() *secret.Resolver {
	return &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
}

func apply(t *testing.T, a *Adapter, c *config.Config, st *state.State) {
	t.Helper()
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

// A user-scoped MCP targeting claude projects into ~/.claude.json using
// Claude's stdio schema: command is a string, args a separate array, env flat.
func TestUserMCPProjection(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())
	st, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &config.Config{MCPs: map[string]config.MCP{
		"codegraph": {Command: []string{"codegraph", "serve", "--mcp"}, Env: map[string]string{"K": "v"}, Targets: []string{"claude"}},
	}}
	apply(t, a, c, st)

	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatalf("claude config missing: %v", err)
	}
	if gjson.GetBytes(raw, "mcpServers.codegraph.type").String() != "stdio" ||
		gjson.GetBytes(raw, "mcpServers.codegraph.command").String() != "codegraph" ||
		gjson.GetBytes(raw, "mcpServers.codegraph.args.0").String() != "serve" ||
		gjson.GetBytes(raw, "mcpServers.codegraph.args.1").String() != "--mcp" ||
		gjson.GetBytes(raw, "mcpServers.codegraph.env.K").String() != "v" {
		t.Fatalf("claude mcpServers entry wrong:\n%s", raw)
	}
	if gjson.GetBytes(raw, "mcpServers.codegraph.args.#").Int() != 2 {
		t.Fatalf("args must be the command tail only:\n%s", raw)
	}
	if _, ok := st.Get("claude", "mcp.codegraph"); !ok {
		t.Fatal("state must record mcp.codegraph under the claude partition")
	}

	// Idempotent: a second plan is a pure no-op.
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range cs.Changes {
		if ch.Action != adapter.ActionNoop {
			t.Fatalf("second plan not idempotent: %+v", ch)
		}
	}
}

// A project-scoped MCP projects into the config repo's .mcp.json, not the
// user-level ~/.claude.json.
func TestProjectMCPProjection(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	a := New(home, t.TempDir()).WithProjectRoot(root)
	st, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := &config.Config{MCPs: map[string]config.MCP{
		"demo": {Command: []string{"srv"}, Scope: "project", Targets: []string{"claude"}},
	}}
	apply(t, a, c, st)

	raw, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatalf("project .mcp.json missing: %v", err)
	}
	if gjson.GetBytes(raw, "mcpServers.demo.command").String() != "srv" {
		t.Fatalf("project mcpServers entry wrong:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("a project-scoped claude MCP must not write the user ~/.claude.json")
	}
}

// Unmanaged content in ~/.claude.json survives projection byte-for-byte in
// value terms: a foreign server and foreign top-level keys stay, while only
// the managed entry is added.
func TestForeignContentPreserved(t *testing.T) {
	home := t.TempDir()
	foreign := `{"firstRun": true, "mcpServers": {"user-own": {"type": "stdio", "command": "keep-me"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{MCPs: map[string]config.MCP{
		"demo": {Command: []string{"srv"}, Targets: []string{"claude"}},
	}}
	apply(t, a, c, st)

	raw, _ := os.ReadFile(filepath.Join(home, ".claude.json"))
	if gjson.GetBytes(raw, "firstRun").Bool() != true {
		t.Fatalf("foreign top-level key lost:\n%s", raw)
	}
	if gjson.GetBytes(raw, "mcpServers.user-own.command").String() != "keep-me" {
		t.Fatalf("foreign server lost:\n%s", raw)
	}
	if gjson.GetBytes(raw, "mcpServers.demo.command").String() != "srv" {
		t.Fatalf("managed server missing:\n%s", raw)
	}
}

// Structured records left by pre-v0.13.0 claude installs (settings, plugins,
// plugin configs, marketplaces) are retired state-only once the adapter is
// activated; the on-disk values homonto no longer manages are left untouched.
func TestStaleStructuredRecordsRetiredStateOnly(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	st.Set("claude", "setting.model", `"opus"`, "h1")
	st.Set("claude", "plugin.hud", "true", "h2")
	st.Set("claude", "pluginconfig.hud", "{}", "h3")
	st.Set("claude", "marketplace.acme", "{}", "h4")
	st.Set("claude", "projsetting.model", `"opus"`, "h5")

	c := &config.Config{MCPs: map[string]config.MCP{
		"demo": {Command: []string{"srv"}, Targets: []string{"claude"}},
	}}
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	retired := map[string]bool{}
	for _, ch := range cs.Changes {
		if ch.Action == adapter.ActionDelete && staleStructuredKey(ch.Key) {
			retired[ch.Key] = true
		}
	}
	for _, key := range []string{"setting.model", "plugin.hud", "pluginconfig.hud", "marketplace.acme", "projsetting.model"} {
		if !retired[key] {
			t.Fatalf("stale key %s not planned for state-only retirement: %+v", key, cs.Changes)
		}
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"setting.model", "plugin.hud", "pluginconfig.hud", "marketplace.acme", "projsetting.model"} {
		if _, ok := st.Get("claude", key); ok {
			t.Fatalf("stale key %s must be gone from state after apply", key)
		}
	}
	// State-only: homonto manages no settings file in this slice, so none may
	// be created by the retirement pass.
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("state-only retirement must not write a settings file")
	}
}

// $CLAUDE_CONFIG_DIR (absolute) relocates both the config directory and the
// MCP registry file; a relative override is ignored, matching Claude's own
// absolute-path requirement.
func TestConfigDirOverride(t *testing.T) {
	home := t.TempDir()
	override := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", override)

	if got := ConfigDir(home); got != override {
		t.Fatalf("ConfigDir = %q, want the override %q", got, override)
	}
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{MCPs: map[string]config.MCP{
		"demo": {Command: []string{"srv"}, Targets: []string{"claude"}},
	}}
	apply(t, a, c, st)
	if _, err := os.Stat(filepath.Join(override, ".claude.json")); err != nil {
		t.Fatalf("override .claude.json missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("the override must keep the registry file out of $HOME")
	}

	t.Setenv("CLAUDE_CONFIG_DIR", "relative/path")
	if got := ConfigDir(home); got != filepath.Join(home, ".claude") {
		t.Fatalf("a relative override must be ignored, got %q", got)
	}
}

// Setting an absolute $CLAUDE_CONFIG_DIR after servers were applied to the
// default registry must fail the plan naming both paths — applying under the
// override would strand the managed servers (and any resolved credential env)
// in $HOME/.claude.json with nothing left to prune them. User-rewritten
// entries in the old file do not block the plan.
func TestRegistryRelocationRefusesToStrand(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{MCPs: map[string]config.MCP{
		"demo": {Command: []string{"srv"}, Targets: []string{"claude"}},
	}}
	apply(t, a, c, st)

	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(t.TempDir(), "cfg"))
	_, err := a.Plan(c, st)
	if err == nil {
		t.Fatal("planning under a relocated registry with stranded servers must fail")
	}
	for _, want := range []string{"CLAUDE_CONFIG_DIR", ".claude.json", "demo"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("relocation error must mention %q, got: %v", want, err)
		}
	}

	// The user rewrites the old entry: no longer homonto's applied value, so
	// there is nothing provably stranded and the plan proceeds.
	doc := []byte(`{"mcpServers": {"demo": {"type": "stdio", "command": "user-own", "args": [], "env": {}}}}`)
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), doc, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Plan(c, st); err != nil {
		t.Fatalf("a user-owned old entry must not block the plan: %v", err)
	}
}
