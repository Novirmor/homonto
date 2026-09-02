package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

const sample = `
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]

[mcps.brave]
command = ["npx", "-y", "server-brave"]
env = { BRAVE_API_KEY = "${pass:ai/brave}" }
targets = ["opencode"]

[frameworks.onto]
source = "builtin:onto"
scope = "project"

[skills.graphify]
source = "local:graphify"
scope = "project"

[skills.demo-skill]
source = "builtin:onto"
scope = "user"
targets = ["opencode"]

[commands.review]
source = "builtin:review"
scope = "project"
targets = ["opencode"]

[plugins.opencode.quota]
source = "@slkiser/opencode-quota"

[settings.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"
variant = "cheap"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
`

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "homonto.toml")
	if err := os.WriteFile(p, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.MCPs["codegraph"].Command; len(got) != 3 || got[0] != "codegraph" {
		t.Fatalf("codegraph command = %v", got)
	}
	if got := c.MCPs["brave"].Env["BRAVE_API_KEY"]; got != "${pass:ai/brave}" {
		t.Fatalf("brave env = %q", got)
	}
	// OpenCode is the only adapter since v0.13.0, so an absent targets list
	// defaults to exactly ["opencode"].
	if got := c.MCPs["codegraph"].TargetsOrAll(); len(got) != 1 || got[0] != "opencode" {
		t.Fatalf("default targets = %v", got)
	}
	if got := c.MCPs["brave"].TargetsOrAll(); len(got) != 1 || got[0] != "opencode" {
		t.Fatalf("brave targets = %v", got)
	}
	if got := c.Settings.OpenCode["model"]; got != "anthropic/claude-opus-4-8" {
		t.Fatalf("opencode model = %v", got)
	}
	if got := c.Frameworks["onto"].Scope; got != "project" {
		t.Fatalf("framework onto scope = %q", got)
	}
	if got := c.Skills["graphify"].Source; got != "local:graphify" {
		t.Fatalf("skill graphify source = %q", got)
	}
	opencodeSkills := c.SkillEntriesForTool("opencode")
	if len(opencodeSkills) != 2 || opencodeSkills[0].Name != "demo-skill" || opencodeSkills[1].Name != "graphify" {
		t.Fatalf("opencode skill entries = %#v", opencodeSkills)
	}
	if got := c.Subagents["onto-reviewer"].OpenCode.Model; got != "anthropic/claude-opus-4-8" {
		t.Fatalf("onto-reviewer opencode override model = %q", got)
	}
	if got := c.Subagents["onto-explorer"].OpenCode.Variant; got != "cheap" {
		t.Fatalf("onto-explorer opencode variant = %q, want cheap", got)
	}
	// Plugin declaration tables parse into per-tool maps keyed by decl name,
	// carrying source and (default-true) enabled.
	oc := c.Plugins.OpenCode["quota"]
	if oc.Source != "@slkiser/opencode-quota" || !oc.IsEnabled() {
		t.Fatalf("opencode plugin quota = %#v (enabled default should be true)", oc)
	}
}

// TestLoadPluginEnabledSemantics covers the enabled flag: omitted defaults to
// true (enabled), false disables.
func TestLoadPluginEnabledSemantics(t *testing.T) {
	doc := "[plugins.opencode.on]\nsource = \"on@m\"\n" +
		"[plugins.opencode.off]\nsource = \"off@m\"\nenabled = false\n"
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !c.Plugins.OpenCode["on"].IsEnabled() {
		t.Fatalf("plugin with omitted enabled should default enabled")
	}
	if c.Plugins.OpenCode["off"].IsEnabled() {
		t.Fatalf("plugin with enabled=false should be disabled")
	}
}

// TestLoadRejectsRemovedClaudePlugins: Claude Code support was removed in
// v0.13.0, so any [plugins.claude.<name>] declaration — whatever its fields —
// is rejected at load naming the plugin, not decoded into a surface that no
// longer projects.
func TestLoadRejectsRemovedClaudePlugins(t *testing.T) {
	err := loadDoc(t, "[plugins.claude.hud]\nsource = \"hud@official\"\nenabled = true\n")
	if err == nil {
		t.Fatal("a [plugins.claude] declaration must be rejected at load")
	}
	for _, want := range []string{"plugins.claude.hud", "removed in v0.13.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not mention %q", err, want)
		}
	}
}

// TestLoadRejectsOpenCodePluginConfig: OpenCode has no per-plugin config on disk
// (its plugins are a plain array), so a [plugins.opencode.<name>.config] table
// cannot project. Load must fail naming the plugin.
func TestLoadRejectsOpenCodePluginConfig(t *testing.T) {
	doc := "[plugins.opencode.q]\nsource = \"q\"\n" +
		"[plugins.opencode.q.config]\nfoo = \"bar\"\n"
	err := loadDoc(t, doc)
	if err == nil {
		t.Fatal("opencode plugin config accepted; want load error")
	}
	if !strings.Contains(err.Error(), strconv.Quote("q")) {
		t.Fatalf("error does not name the plugin %q: %v", "q", err)
	}
}

// TestLoadRejectsEmptyPluginSource: a plugin declaration whose source is empty
// (or whitespace) cannot project anywhere, so Load must fail naming the plugin.
func TestLoadRejectsEmptyPluginSource(t *testing.T) {
	for _, tc := range []struct{ label, doc, name string }{
		{"opencode missing source", "[plugins.opencode.hud]\n", "hud"},
		{"opencode empty source", "[plugins.opencode.hud]\nsource = \"\"\n", "hud"},
		{"opencode whitespace source", "[plugins.opencode.q]\nsource = \"   \"\n", "q"},
	} {
		err := loadDoc(t, tc.doc)
		if err == nil {
			t.Fatalf("%s: empty source accepted; want load error", tc.label)
		}
		if !strings.Contains(err.Error(), strconv.Quote(tc.name)) {
			t.Fatalf("%s: error does not name the plugin %q: %v", tc.label, tc.name, err)
		}
	}
}

// TestLoadRejectsDuplicatePluginSource: two decl names sharing one source would
// collide on the single projected key (keyed by source), giving a
// last-writer-wins, iteration-order-dependent plan. Load must reject it.
func TestLoadRejectsDuplicatePluginSource(t *testing.T) {
	doc := "[plugins.opencode.hud]\nsource = \"hud@npm\"\n" +
		"[plugins.opencode.hud-off]\nsource = \"hud@npm\"\nenabled = false\n"
	err := loadDoc(t, doc)
	if err == nil {
		t.Fatal("duplicate source accepted; want load error")
	}
	if !strings.Contains(err.Error(), "hud@npm") {
		t.Fatalf("error does not name the shared source: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestLoadRejectsIndexLikeNames reproduces the verify round's corruption
// finding: sjson treats all-digit keys ("0") and "-" + digits ("-1") as array
// indices, so [mcps."0"] silently turns mcpServers into a JSON ARRAY. Empty
// names address nothing. All such names must be a clear load-time error for
// every key homonto writes into a tool file.
func TestLoadRejectsIndexLikeNames(t *testing.T) {
	bad := []struct{ label, doc, name string }{
		{"mcp empty", "[mcps.\"\"]\ncommand = [\"x\"]\n", ""},
		{"mcp zero", "[mcps.\"0\"]\ncommand = [\"x\"]\n", "0"},
		{"mcp minus-one", "[mcps.\"-1\"]\ncommand = [\"x\"]\n", "-1"},
		{"opencode setting", "[settings.opencode]\n\"-1\" = \"x\"\n", "-1"},
		{"opencode plugin", "[plugins.opencode.\"\"]\nsource = \"x\"\n", ""},
	}
	for _, tc := range bad {
		p := filepath.Join(t.TempDir(), "homonto.toml")
		if err := os.WriteFile(p, []byte(tc.doc), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		if err == nil {
			t.Fatalf("%s: name %q accepted; want load error", tc.label, tc.name)
		}
		if !strings.Contains(err.Error(), strconv.Quote(tc.name)) {
			t.Fatalf("%s: error does not name the entry %q: %v", tc.label, tc.name, err)
		}
	}
	good := []string{"corp.internal", "a0", "0a", "v2", "-x1"}
	for _, name := range good {
		p := filepath.Join(t.TempDir(), "homonto.toml")
		doc := "[mcps." + strconv.Quote(name) + "]\ncommand = [\"x\"]\n"
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(p); err != nil {
			t.Fatalf("valid name %q rejected: %v", name, err)
		}
	}
}

// TestLoadParsesTUIOpenCode: a [tui.opencode] table parses into TUI.OpenCode as
// a free-form map, mirroring [settings.opencode]. These keys project to a
// second managed file (~/.config/opencode/tui.json).
func TestLoadParsesTUIOpenCode(t *testing.T) {
	doc := "[tui.opencode]\ntheme = \"gruvbox\"\nscroll_speed = 3\n"
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.TUI.OpenCode["theme"]; got != "gruvbox" {
		t.Fatalf("tui.opencode theme = %#v; want \"gruvbox\"", got)
	}
	if got, ok := c.TUI.OpenCode["scroll_speed"].(int64); !ok || got != 3 {
		t.Fatalf("tui.opencode scroll_speed = %#v; want int64(3)", c.TUI.OpenCode["scroll_speed"])
	}
}

// TestLoadRejectsTUIIndexLikeName: like [settings.opencode], a [tui.opencode]
// key that sjson would treat as an array index ("0", "-1") or an empty key
// would corrupt tui.json. Load must reject it naming the offending entry.
func TestLoadRejectsTUIIndexLikeName(t *testing.T) {
	for _, tc := range []struct{ label, doc, name string }{
		{"tui zero", "[tui.opencode]\n\"0\" = \"x\"\n", "0"},
		{"tui minus-one", "[tui.opencode]\n\"-1\" = \"x\"\n", "-1"},
		{"tui empty", "[tui.opencode]\n\"\" = \"x\"\n", ""},
	} {
		err := loadDoc(t, tc.doc)
		if err == nil {
			t.Fatalf("%s: name %q accepted; want load error", tc.label, tc.name)
		}
		if !strings.Contains(err.Error(), strconv.Quote(tc.name)) {
			t.Fatalf("%s: error does not name the entry %q: %v", tc.label, tc.name, err)
		}
	}
}

func loadDoc(t *testing.T, doc string) error {
	t.Helper()
	_, err := loadDocCfg(t, doc)
	return err
}

// loadDocCfg is loadDoc for callers that need to inspect what parsed, not only
// whether it did.
func loadDocCfg(t *testing.T, doc string) (*Config, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(p)
}

// TestSubagentModeValidation: subagents accept link (default) and copy; an
// unknown mode is invalid.
func TestSubagentModeValidation(t *testing.T) {
	base := func(mode string) string {
		m := ""
		if mode != "" {
			m = "mode=\"" + mode + "\"\n"
		}
		return "[subagents.x]\nsource=\"builtin:architect\"\nscope=\"user\"\n" + m + modelsFor("x")
	}
	for _, mode := range []string{"", "link", "copy"} {
		if err := loadDoc(t, base(mode)); err != nil {
			t.Fatalf("mode %q must load: %v", mode, err)
		}
	}
	if err := loadDoc(t, base("bogus")); err == nil {
		t.Fatal("an unknown subagent mode must be rejected")
	}
}

// TestAgentSupersededIntoSubagent: an [agents.<name>] is folded into an
// equivalent copy-mode [subagents.<name>] at load (Option C), the agents table is
// cleared, and a declared agent supersedes an explicit same-name subagent.
func TestAgentSupersededIntoSubagent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	load := func(doc string) *Config {
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		c, err := Load(p)
		if err != nil {
			t.Fatalf("load: %v\n%s", err, doc)
		}
		return c
	}

	// A builtin agent supersedes to a COPY-mode subagent (builtin was copy-only).
	c := load("[agents.rev]\nsource=\"builtin:code-reviewer\"\ntargets=[\"opencode\"]\n" + modelsFor("rev"))
	if len(c.Agents) != 0 {
		t.Fatal("the [agents] table must be cleared after supersede")
	}
	sa, ok := c.Subagents["rev"]
	if !ok {
		t.Fatal("agent rev was not superseded into a subagent")
	}
	if sa.Mode != "copy" {
		t.Fatalf("a builtin agent must supersede to copy mode, got %q", sa.Mode)
	}
	if sa.Scope != "user" {
		t.Fatalf("a superseded agent must keep user scope, got %q", sa.Scope)
	}

	// A declared [agents.X] wins over an explicit [subagents.X] of the same name.
	// After the fold dup's source is local:dup, which the must-declare check
	// skips (only builtin: sources are rendered through agentfm), so no
	// per-tool block is required.
	c2 := load("[agents.dup]\nsource=\"local:dup\"\nmode=\"copy\"\ntargets=[\"opencode\"]\n" +
		"[subagents.dup]\nsource=\"builtin:architect\"\nscope=\"project\"\ntargets=[\"opencode\"]\n")
	if got := c2.Subagents["dup"].Source; got != "local:dup" {
		t.Fatalf("the agent declaration must win the name; subagent source = %q", got)
	}
}

// TestSubagentScopeDefaultsToProject: an omitted [subagents.<name>] scope is no
// longer an error — it defaults to project (Option C step 1). An explicit scope
// is still honored, and skills/commands still require scope.
func TestSubagentScopeDefaultsToProject(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	load := func(doc string) (*Config, error) {
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return Load(p)
	}

	c, err := load("[subagents.architect]\nsource=\"builtin:architect\"\n" + modelsFor("architect"))
	if err != nil {
		t.Fatalf("omitted subagent scope should default to project, not error: %v", err)
	}
	if got := c.Subagents["architect"].Scope; got != "project" {
		t.Fatalf("omitted subagent scope = %q, want \"project\"", got)
	}

	c2, err := load("[subagents.architect]\nsource=\"builtin:architect\"\nscope=\"user\"\n" + modelsFor("architect"))
	if err != nil {
		t.Fatalf("explicit subagent scope: %v", err)
	}
	if got := c2.Subagents["architect"].Scope; got != "user" {
		t.Fatalf("explicit subagent scope = %q, want \"user\"", got)
	}

	// Skills still require an explicit scope.
	if err := loadDoc(t, "[skills.s]\nsource=\"local:s\"\n"); err == nil {
		t.Fatal("a skill with no scope must still be rejected")
	}
}

// TestLoadRejectsUnknownTargets reproduces NEXT_AGENT gap #3: an MCP whose
// targets name a tool other than opencode (a silent typo) matches no adapter
// and is silently projected nowhere. Load must fail naming the unknown target.
// Targets naming a removed tool get their own removal message — see
// TestRemovedToolTargetsRejected.
func TestLoadRejectsUnknownTargets(t *testing.T) {
	bad := []struct{ label, doc, offender string }{
		{"typo", "[mcps.x]\ncommand=[\"c\"]\ntargets=[\"claud\"]\n", "claud"},
		{"unknown tool", "[mcps.x]\ncommand=[\"c\"]\ntargets=[\"vscode\"]\n", "vscode"},
		{"one good one bad", "[mcps.x]\ncommand=[\"c\"]\ntargets=[\"opencode\",\"opencde\"]\n", "opencde"},
	}
	for _, tc := range bad {
		err := loadDoc(t, tc.doc)
		if err == nil {
			t.Fatalf("%s: unknown target %q accepted; want load error", tc.label, tc.offender)
		}
		if !strings.Contains(err.Error(), strconv.Quote(tc.offender)) {
			t.Fatalf("%s: error does not name the offender %q: %v", tc.label, tc.offender, err)
		}
	}
	if err := loadDoc(t, "[mcps.x]\ncommand=[\"c\"]\ntargets=[\"opencode\"]\n"); err != nil {
		t.Fatalf("valid targets rejected: %v", err)
	}
	// No targets means the default (opencode) — still valid.
	if err := loadDoc(t, "[mcps.x]\ncommand=[\"c\"]\n"); err != nil {
		t.Fatalf("default targets rejected: %v", err)
	}
}

// TestLoadRejectsEmptyCommand reproduces gap #3: an MCP with no runnable
// command is skipped by both adapters (desired() len(Command)==0), a silent
// no-op. Load must fail naming the MCP that cannot project.
func TestLoadRejectsEmptyCommand(t *testing.T) {
	for _, tc := range []struct{ label, doc string }{
		{"missing command", "[mcps.foo]\ntargets=[\"opencode\"]\n"},
		{"empty command", "[mcps.foo]\ncommand=[]\n"},
	} {
		err := loadDoc(t, tc.doc)
		if err == nil {
			t.Fatalf("%s: accepted; want load error", tc.label)
		}
		if !strings.Contains(err.Error(), strconv.Quote("foo")) {
			t.Fatalf("%s: error does not name the MCP %q: %v", tc.label, "foo", err)
		}
	}
}

// TestLoadRejectsReservedSettingKeys: a [settings.opencode] key that collides
// with a structure homonto itself manages in opencode.jsonc (mcp/plugin) must
// be a load error, not a silent fight at apply.
func TestLoadRejectsReservedSettingKeys(t *testing.T) {
	for _, tc := range []struct{ label, doc, key string }{
		{"opencode mcp", "[settings.opencode]\nmcp={}\n", "mcp"},
		{"opencode plugin", "[settings.opencode]\nplugin=[]\n", "plugin"},
		{"opencode model variant", "[settings.opencode]\nmodel_variant=\"high\"\n", "model_variant"},
	} {
		err := loadDoc(t, tc.doc)
		if err == nil {
			t.Fatalf("%s: reserved key accepted; want load error", tc.label)
		}
		if !strings.Contains(err.Error(), strconv.Quote(tc.key)) {
			t.Fatalf("%s: error does not name the key %q: %v", tc.label, tc.key, err)
		}
	}
	// Exact collisions only: non-colliding keys load normally, including the
	// names homonto once managed for Claude (they are plain keys here).
	for _, ok := range []string{
		"[settings.opencode]\nenabledPlugins={}\n",
		"[settings.opencode]\nmcpServers={}\n",
		"[settings.opencode]\nmodel=\"anthropic/claude-opus-4-8\"\n",
	} {
		if err := loadDoc(t, ok); err != nil {
			t.Fatalf("non-reserved settings rejected: %v (doc %q)", err, ok)
		}
	}
}

// TestLoadRejectsRemovedClaudeSettings: Claude Code support was removed in
// v0.13.0, so any [settings.claude] key — including the enabledPlugins /
// mcpServers / pluginConfigs / extraKnownMarketplaces keys homonto itself once
// managed there — is rejected at load naming the key, pointing at
// [settings.opencode] or deletion.
func TestLoadRejectsRemovedClaudeSettings(t *testing.T) {
	for _, key := range []string{"enabledPlugins", "mcpServers", "pluginConfigs", "extraKnownMarketplaces", "model"} {
		err := loadDoc(t, "[settings.claude]\n"+key+" = \"x\"\n")
		if err == nil {
			t.Fatalf("settings.claude.%s accepted; want the removal error", key)
		}
		for _, want := range []string{"settings.claude." + key, "removed in v0.13.0"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("error %v does not mention %q", err, want)
			}
		}
	}
}

func TestLoadRejectsBadResourceNames(t *testing.T) {
	for _, tc := range []struct{ kind, table, name string }{
		{"framework", "frameworks", "../evil"},
		{"skill", "skills", ".."},
		{"command", "commands", ""},
		{"subagent", "subagents", "a/b"},
		{"subagent", "subagents", `a\b`},
		{"skill", "skills", "0"},
	} {
		doc := "[" + tc.table + "." + strconv.Quote(tc.name) + "]\nsource=\"local:x\"\nscope=\"project\"\n"
		err := loadDoc(t, doc)
		if err == nil {
			t.Fatalf("%s name %q accepted; want load error", tc.kind, tc.name)
		}
		if !strings.Contains(err.Error(), strconv.Quote(tc.name)) {
			t.Fatalf("error for %q does not name the entry: %v", tc.name, err)
		}
	}
}

func TestLoadRejectsResourceWithoutExplicitScope(t *testing.T) {
	err := loadDoc(t, "[skills.graphify]\nsource=\"local:graphify\"\n")
	if err == nil {
		t.Fatal("resource without scope accepted; want load error")
	}
	for _, want := range []string{"skills.graphify", "scope"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not mention %q", err, want)
		}
	}
}

func TestLoadRejectsInvalidResourceScope(t *testing.T) {
	err := loadDoc(t, "[commands.review]\nsource=\"builtin:review\"\nscope=\"global\"\n")
	if err == nil {
		t.Fatal("scope global accepted; want load error")
	}
	for _, want := range []string{`"global"`, "user", "project"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not mention %q", err, want)
		}
	}
}

func TestLoadRejectsInvalidResourceSource(t *testing.T) {
	for _, source := range []string{"", "https://example.com/x", "github:owner/repo", "builtin:", "local:"} {
		doc := "[skills.graphify]\nsource=" + strconv.Quote(source) + "\nscope=\"project\"\n"
		err := loadDoc(t, doc)
		if err == nil {
			t.Fatalf("source %q accepted; want load error", source)
		}
		if !strings.Contains(err.Error(), strconv.Quote(source)) {
			t.Fatalf("error %v does not name source %q", source, err)
		}
	}
}

func TestLoadRejectsUnknownResourceTargets(t *testing.T) {
	err := loadDoc(t, "[subagents.architect]\nsource=\"builtin:architect\"\nscope=\"project\"\ntargets=[\"claud\"]\n")
	if err == nil {
		t.Fatal("unknown target accepted; want load error")
	}
	if !strings.Contains(err.Error(), strconv.Quote("claud")) {
		t.Fatalf("error does not name unknown target: %v", err)
	}
}

// TestLoadRejectsRemovedClaudeSubagentBlock: a [subagents.<name>.claude] model
// block names a tool whose support was removed in v0.13.0. Load must reject it
// naming the block and pointing the migration at [subagents.<name>.opencode] —
// not silently drop a model declaration the user wrote.
func TestLoadRejectsRemovedClaudeSubagentBlock(t *testing.T) {
	err := loadDoc(t, `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"

[subagents.onto-reviewer.claude]
model = "opus"

[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
`)
	if err == nil {
		t.Fatal("a [subagents.<name>.claude] block must be rejected at load")
	}
	for _, want := range []string{"subagents.onto-reviewer.claude", "removed in v0.13.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not mention %q", err, want)
		}
	}
}

// TestLoadRequiresModelsForFrameworkExpandedSubagents locks in the
// framework-expanded half of the must-declare check (I1): a config that
// installs a framework but omits the per-tool [subagents.<name>.<tool>]
// model blocks for its expanded builtin subagents fails at load. Without
// this check the framework's agents would render through agentfm with no
// model line — a silent default R1 forbids. The explicit-walk alone misses
// them because the expanded agents are absent from c.Subagents.
func TestLoadRequiresModelsForFrameworkExpandedSubagents(t *testing.T) {
	doc := `
[frameworks.onto]
source = "builtin:onto"
scope = "project"
`
	err := loadDoc(t, doc)
	if err == nil {
		t.Fatal("framework expanding builtin subagents with no [subagents.<name>.<tool>] model accepted; want load error")
	}
	// The error names the first expanded builtin (alphabetically) and its
	// enabled tool. onto is the onto framework's primary dispatcher, expanded
	// first in sorted order; opencode is the only adapter since v0.13.0.
	for _, want := range []string{"subagents.onto.opencode", "model is required"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v does not mention %q", err, want)
		}
	}
}

// TestLoadRequiresModelForFrameworkAgentOutsideExplicitAliasTargets: an
// explicit alias's override covers only the catalog agent it names — every
// OTHER agent the framework expands still needs its own
// [subagents.<name>.opencode] block, and omitting one must fail at load.
func TestLoadRequiresModelForFrameworkAgentOutsideExplicitAliasTargets(t *testing.T) {
	doc := `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.alias]
source = "builtin:onto-reviewer"
scope = "project"
targets = ["opencode"]
[subagents.alias.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-4-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
`
	err := loadDoc(t, doc)
	if err == nil || !strings.Contains(err.Error(), "subagents.onto-skeptic.opencode model is required") {
		t.Fatalf("a framework agent not covered by the explicit alias must fail at load, got: %v", err)
	}
}

// A route naming only a model is complete. effort/variant were once mandatory
// while being projected nowhere — homonto forced you to write a field it then
// discarded, and never checked, so configs filled up with values no tool accepts
// ("effort = normal", "variant = max"). They are optional and validated now.
func TestLoadAcceptsModelWithoutEffortOrVariant(t *testing.T) {
	doc := `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"

[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
`
	if err := loadDoc(t, doc); err != nil {
		t.Fatalf("a route naming just a model is complete; got: %v", err)
	}
}

// The model spec is validated against what OpenCode — the only adapter since
// v0.13.0 — can actually express, so a value the tool would silently ignore is
// a load error naming the offender instead.
func TestLoadValidatesModelSpecPerTool(t *testing.T) {
	doc := func(route string) string {
		return `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"
targets = ["opencode"]

[subagents.onto-reviewer.opencode]
` + route + `
`
	}

	for _, tc := range []struct {
		name, doc, wantErr string
	}{
		{
			name:    "opencode rejects effort, which it has no concept of",
			doc:     doc("model = \"anthropic/claude-opus-4-8\"\neffort = \"high\"\n"),
			wantErr: "OpenCode has no effort setting",
		},
		{
			name:    "opencode rejects a model ID containing a variant suffix",
			doc:     doc("model = \"openai/gpt-5#high\"\n"),
			wantErr: "must not include a #variant suffix",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := loadDoc(t, tc.doc)
			if err == nil {
				t.Fatalf("want a load error mentioning %q, got none", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v does not explain %q", err, tc.wantErr)
			}
		})
	}

	for _, tc := range []struct{ name, doc string }{
		{"opencode accepts a provider-defined variant", doc("model = \"anthropic/claude-opus-4-8\"\nvariant = \"thinking\"\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := loadDoc(t, tc.doc); err != nil {
				t.Fatalf("want accepted, got: %v", err)
			}
		})
	}
}

// A per-subagent [subagents.<name>.opencode] block is validated against the
// same rules as the must-declare check. The model is required of declared
// builtin subagents; an override may set variant alone but a tune-only entry
// still must name an agent a framework or explicit declaration installs.
func TestLoadValidatesSubagentModelOverride(t *testing.T) {
	// doc returns a config that declares onto-skeptic explicitly (so the
	// must-declare check applies) plus the given override block.
	doc := func(block string) string {
		return `
[subagents.onto-skeptic]
source = "builtin:onto-skeptic"
scope = "project"
targets = ["opencode"]

` + block + `
`
	}

	t.Run("opencode override with model + variant loads", func(t *testing.T) {
		c, err := loadDocCfg(t, doc("[subagents.onto-skeptic.opencode]\nmodel = \"anthropic/claude-opus-4-8\"\nvariant = \"thinking\"\n"))
		if err != nil {
			t.Fatalf("override with model + variant should load: %v", err)
		}
		if got := c.Subagents["onto-skeptic"].ModelOverrideFor("opencode"); got.Model != "anthropic/claude-opus-4-8" || got.Variant != "thinking" {
			t.Fatalf("override = %#v; want the declared model and variant", got)
		}
		// Claude Code support was removed in v0.13.0; the claude route is a
		// removal detector and resolves to no override at all.
		if got := c.Subagents["onto-skeptic"].ModelOverrideFor("claude"); got.IsSet() {
			t.Fatalf("ModelOverrideFor(\"claude\") = %#v; want the zero route", got)
		}
	})

	// A tune-only entry projects nothing and declares no agent — it only
	// retunes what a framework already installed, so it must load without
	// declaring anything itself.
	t.Run("tune-only entries load over a framework", func(t *testing.T) {
		if err := loadDoc(t, `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

`+ontoFrameworkModels()); err != nil {
			t.Fatalf("tune-only blocks over a framework must load: %v", err)
		}
	})

	t.Run("an override is validated too", func(t *testing.T) {
		err := loadDoc(t, doc("[subagents.onto-skeptic.opencode]\nmodel = \"anthropic/claude-opus-4-8\"\neffort = \"turbo\"\n"))
		if err == nil || !strings.Contains(err.Error(), "OpenCode has no effort setting") {
			t.Fatalf("want the override's effort rejected, got: %v", err)
		}
		if !strings.Contains(err.Error(), "subagents.onto-skeptic.opencode") {
			t.Fatalf("error must name the offending override: %v", err)
		}
	})

	// A tune-only entry declares no agent and no targets of its own, but its
	// override is still stamped into a rendered file — it must be validated,
	// not skipped because the entry itself projects nothing.
	t.Run("a tune-only entry's override is still validated", func(t *testing.T) {
		err := loadDoc(t, `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-explorer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
effort = "turbo"
`)
		if err == nil || !strings.Contains(err.Error(), "OpenCode has no effort setting") {
			t.Fatalf("a tune-only entry's bad effort must still be rejected, got: %v", err)
		}
		if !strings.Contains(err.Error(), "subagents.onto-skeptic.opencode") {
			t.Fatalf("error must name the offending override: %v", err)
		}
	})

	// A tune-only entry naming an agent nothing installs was a total silent
	// no-op: it loaded, planned, and applied clean while retuning nothing.
	t.Run("a tune-only typo is a load error", func(t *testing.T) {
		err := loadDoc(t, `[frameworks.onto]
source = "builtin:onto"
scope = "project"

`+ontoFrameworkModels()+`
[subagents.onto-skepic.opencode]
model = "anthropic/claude-opus-4-8"
`)
		if err == nil || !strings.Contains(err.Error(), "not installed") {
			t.Fatalf("a tune-only entry for an unknown agent must fail naming the typo, got: %v", err)
		}
	})

	// Overrides on local:/remote: sources were validated as if meaningful, then
	// silently discarded — local/remote content is projected verbatim and never
	// rendered, so the override could never apply.
	t.Run("an override on a non-builtin source is a load error", func(t *testing.T) {
		err := loadDoc(t, `[subagents.mine]
source = "local:mine"
scope = "project"
targets = ["opencode"]
[subagents.mine.opencode]
model = "anthropic/claude-opus-4-8"
`)
		if err == nil || !strings.Contains(err.Error(), "never apply") {
			t.Fatalf("an override on a local: source must be rejected, got: %v", err)
		}
	})
}

// Two entries resolving to the same builtin with conflicting overrides used to
// make the winner Go map-iteration luck — a different render (and a different
// materialize fingerprint) every run, so apply re-materialized forever. The
// conflict is judged per catalog name regardless of the entries' own targets.
func TestConflictingOverridesRejectedAcrossTargets(t *testing.T) {
	doc := `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.a]
source = "builtin:onto-skeptic"
scope = "project"
targets = ["opencode"]
[subagents.a.opencode]
model = "anthropic/claude-opus-4-8"
variant = "thinking"

[subagents.b]
source = "builtin:onto-skeptic"
scope = "project"
[subagents.b.opencode]
model = "anthropic/claude-opus-4-8"
variant = "fast"

` + modelsFor("onto", "onto-explorer", "onto-reviewer", "onto-implementer")
	err := loadDoc(t, doc)
	if err == nil || !strings.Contains(err.Error(), "must agree") {
		t.Fatalf("conflicting overrides for one builtin must be a deterministic load error, got: %v", err)
	}
}

// Legacy [agents.X] wins the declaration over a same-named [subagents.X], but
// used to overwrite the whole struct — silently deleting the subagents entry's
// per-tool tune blocks, which [agents.X] has no syntax to express.
func TestLegacyAgentsFoldPreservesTuneBlocks(t *testing.T) {
	c, err := loadDocCfg(t, `
[agents.foo]
source = "builtin:onto-skeptic"

[subagents.foo.opencode]
model = "anthropic/claude-opus-4-8"
variant = "thinking"

[frameworks.onto]
source = "builtin:onto"
scope = "project"

`+modelsFor("onto", "onto-explorer", "onto-reviewer", "onto-implementer"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.Subagents["foo"].OpenCode.Variant; got != "thinking" {
		t.Fatalf("the tune block must survive the [agents.X] fold, got variant %q", got)
	}
}

// Validation used to trim whitespace while the render did not: `model = "x "`
// passed validation, then missed the model map at render and silently dropped
// its variant. Values are now trimmed once, at load.
func TestModelRouteValuesTrimmedAtLoad(t *testing.T) {
	c, err := loadDocCfg(t, `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"
targets = ["opencode"]

[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8 "
variant = " thinking"
`)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	r := c.Subagents["onto-reviewer"].OpenCode
	if r.Model != "anthropic/claude-opus-4-8" || r.Variant != "thinking" {
		t.Fatalf("route values must be trimmed at load, got model=%q variant=%q", r.Model, r.Variant)
	}
}

func TestLoadDoesNotRequireModelsForSkillsOnly(t *testing.T) {
	err := loadDoc(t, "[skills.graphify]\nsource=\"local:graphify\"\nscope=\"project\"\n")
	if err != nil {
		t.Fatalf("skills-only config required model routing: %v", err)
	}
}

// TestEnabledModelTools locks the rule that model routing is derived only from
// frameworks/commands/subagents — [skills.*] never counts, because skills-only
// configs do not need models. Returns the sorted union of targeted tools
// (OpenCode is the only adapter since v0.13.0).
func TestEnabledModelTools(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  *Config
		want []string
	}{
		{
			name: "skills only does not enable any model tool",
			cfg: &Config{Skills: map[string]Resource{
				"x": {Source: "local:x", Scope: "user"},
			}},
			want: []string{},
		},
		{
			name: "single command with one target",
			cfg: &Config{Commands: map[string]Resource{
				"x": {Source: "builtin:x", Scope: "project", Targets: []string{"opencode"}},
			}},
			want: []string{"opencode"},
		},
		{
			name: "framework with no targets defaults to opencode",
			cfg: &Config{Frameworks: map[string]Resource{
				"x": {Source: "builtin:x", Scope: "project"},
			}},
			want: []string{"opencode"},
		},
		{
			name: "mixed frameworks+subagents+skills union (skills ignored)",
			cfg: &Config{
				Frameworks: map[string]Resource{
					"a": {Source: "builtin:a", Scope: "project"},
				},
				Subagents: map[string]Subagent{
					"b": {Source: "builtin:b", Scope: "project", Targets: []string{"opencode"}},
				},
				Skills: map[string]Resource{
					"c": {Source: "local:c", Scope: "user"},
				},
			},
			want: []string{"opencode"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.EnabledModelTools()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("EnabledModelTools = %v, want %v", got, tc.want)
			}
		})
	}
}

func loadTOML(t *testing.T, body string) *Config {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "homonto.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return c
}

func TestExpandedSkillsIncludeFrameworkAndDeps(t *testing.T) {
	c := loadTOML(t, `
[frameworks.onto]
source = "builtin:onto"
scope = "user"
targets = ["opencode"]

`+ontoFrameworkModels())
	got, err := c.ExpandedSkillEntriesForTool("opencode")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	byName := map[string]NamedResource{}
	for _, e := range got {
		byName[e.Name] = e
	}
	// Three of the framework's own skills (onto is self-contained, no deps).
	for _, want := range []string{"onto-open", "onto-build", "onto-no-slop"} {
		e, ok := byName[want]
		if !ok {
			t.Fatalf("expanded set missing %q; got %v", want, keysOf(byName))
		}
		if e.Resource.Source != "builtin:"+want {
			t.Fatalf("%q source = %q", want, e.Resource.Source)
		}
		// Inherits the framework declaration's scope and targets (Spec Patch #1).
		if e.Resource.Scope != "user" || len(e.Resource.Targets) != 1 || e.Resource.Targets[0] != "opencode" {
			t.Fatalf("%q did not inherit scope/targets: %+v", want, e.Resource)
		}
	}
}

func keysOf(m map[string]NamedResource) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestExpandedSkillsCollisionWithExplicit(t *testing.T) {
	c := loadTOML(t, `
[frameworks.onto]
source = "builtin:onto"
scope = "user"
targets = ["opencode"]

[skills.onto-open]
source = "builtin:onto-open"
scope = "user"
targets = ["opencode"]

`+ontoFrameworkModels())
	_, err := c.ExpandedSkillEntriesForTool("opencode")
	if err == nil || !strings.Contains(err.Error(), "onto-open") {
		t.Fatalf("expected collision error naming onto-open, got %v", err)
	}
}

// TestExpandedSkillsFrameworkVsFrameworkConflict reproduces the reviewer's
// framework-vs-framework collision path: two frameworks both expand
// "onto-open" (and the rest of the onto catalog) via the REAL embedded
// catalog, but with different scope, so the second framework's declaration
// conflicts with the first's. With per-agent model blocks required for every
// expanded subagent, the conflict may surface as a subagent collision at load
// or a skill collision at expand — both correctly identify the framework
// collision.
func TestExpandedSkillsFrameworkVsFrameworkConflict(t *testing.T) {
	doc := `
[frameworks.onto_a]
source = "builtin:onto"
scope = "user"
targets = ["opencode"]

[frameworks.onto_b]
source = "builtin:onto"
scope = "project"
targets = ["opencode"]

` + ontoFrameworkModels()
	dir := t.TempDir()
	p := filepath.Join(dir, "homonto.toml")
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err == nil {
		_, err = c.ExpandedSkillEntriesForTool("opencode")
	}
	if err == nil {
		t.Fatal("expected conflict error for two frameworks expanding the same catalog with different scope, got nil")
	}
	// The collision may surface as a skill or a subagent expansion conflict;
	// both name the offending framework or an expanded resource.
	if !strings.Contains(err.Error(), "onto-open") &&
		!strings.Contains(err.Error(), "onto_b") &&
		!strings.Contains(err.Error(), "expanded by multiple frameworks") {
		t.Fatalf("error does not name the conflicting skill, subagent, or framework: %v", err)
	}
}

// TestExpandedSkillsSameFrameworkDeclDedup reproduces the reviewer's
// same-skill-same-declaration dedup path: two frameworks both expand
// "onto-open" via the REAL embedded catalog, with IDENTICAL scope and
// targets, so they should collapse into one entry with no error.
func TestExpandedSkillsSameFrameworkDeclDedup(t *testing.T) {
	c := loadTOML(t, `
[frameworks.onto_a]
source = "builtin:onto"
scope = "user"
targets = ["opencode"]

[frameworks.onto_b]
source = "builtin:onto"
scope = "user"
targets = ["opencode"]

`+ontoFrameworkModels())
	got, err := c.ExpandedSkillEntriesForTool("opencode")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	count := 0
	for _, e := range got {
		if e.Name == "onto-open" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("onto-open should appear exactly once (deduped), got %d occurrences in %v", count, got)
	}
}

// modelsFor produces the [subagents.<name>.opencode] override block for each
// named subagent, with a valid model spec. Every declared or framework-expanded
// builtin subagent needs one now that tiers — and every tool but OpenCode —
// are gone.
func modelsFor(names ...string) string {
	var b strings.Builder
	for _, name := range names {
		b.WriteString("[subagents." + name + ".opencode]\nmodel = \"anthropic/claude-opus-4-8\"\n")
	}
	return b.String()
}

// ontoFrameworkModels is the per-agent override blocks required by the onto
// framework's five expanded subagents.
func ontoFrameworkModels() string {
	return modelsFor("onto", "onto-explorer", "onto-reviewer", "onto-implementer", "onto-skeptic")
}

// TestExpandedCommandsExplicit: an explicit [commands.X] entry projects for its
// targeted tool with source and scope preserved.
func TestExpandedCommandsExplicit(t *testing.T) {
	c := loadTOML(t, `
[commands.example-command]
source = "builtin:example-command"
scope = "project"
targets = ["opencode"]
`)

	got, err := c.ExpandedCommandEntriesForTool("opencode")
	if err != nil {
		t.Fatalf("expand opencode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "example-command" {
		t.Fatalf("opencode commands = %v", got)
	}
	if got[0].Resource.Source != "builtin:example-command" || got[0].Resource.Scope != "project" {
		t.Fatalf("example-command resource = %+v", got[0].Resource)
	}
}

// A skill and a command may share a name: separate namespaces, both returned.
func TestSkillAndCommandMayShareName(t *testing.T) {
	c := loadTOML(t, `
[skills.shared]
source = "builtin:shared"
scope = "user"
targets = ["opencode"]

[commands.shared]
source = "builtin:shared"
scope = "user"
targets = ["opencode"]
`)

	skills, err := c.ExpandedSkillEntriesForTool("opencode")
	if err != nil {
		t.Fatalf("skills: %v", err)
	}
	commands, err := c.ExpandedCommandEntriesForTool("opencode")
	if err != nil {
		t.Fatalf("commands: %v", err)
	}
	if len(skills) != 1 || skills[0].Name != "shared" {
		t.Fatalf("skills = %v", skills)
	}
	if len(commands) != 1 || commands[0].Name != "shared" {
		t.Fatalf("commands = %v", commands)
	}
}

// The framework loop must not crash or invent commands when a framework
// declares no [commands] table: only explicit commands survive. No builtin
// framework is commandless (onto ships commands), so this uses a local:
// skills-only framework root.
func TestExpandedCommandsFrameworkWithoutCommandsNoOps(t *testing.T) {
	dir := t.TempDir()
	fwRoot := filepath.Join(dir, "skillsonly")
	if err := os.MkdirAll(filepath.Join(fwRoot, "skills", "sk"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fwRoot, "framework.toml"), []byte("name = \"skillsonly\"\nversion = \"0.1.0\"\n[skills]\nsk = \"skills/sk\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fwRoot, "skills", "sk", "SKILL.md"), []byte("sk"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `
[frameworks.skillsonly]
source = "local:skillsonly"
scope = "user"
targets = ["opencode"]

[commands.example-command]
source = "builtin:example-command"
scope = "user"
targets = ["opencode"]
`
	if err := os.WriteFile(filepath.Join(dir, "homonto.toml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(filepath.Join(dir, "homonto.toml"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	got, err := c.ExpandedCommandEntriesForTool("opencode")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(got) != 1 || got[0].Name != "example-command" {
		t.Fatalf("commands = %v, want only example-command", got)
	}
}

// TestExpandedSubagentsExplicit: an explicit [subagents.X] entry projects for
// its targeted tool.
func TestExpandedSubagentsExplicit(t *testing.T) {
	c := loadTOML(t, `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"
targets = ["opencode"]
`+modelsFor("onto-reviewer"))

	got, err := c.ExpandedSubagentEntriesForTool("opencode")
	if err != nil {
		t.Fatalf("opencode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "onto-reviewer" {
		t.Fatalf("opencode subagents = %+v, want [onto-reviewer]", got)
	}
}

func TestExpandedSubagentsFrameworkInheritsScopeTargets(t *testing.T) {
	c := loadTOML(t, `
[frameworks.onto]
source = "builtin:onto"
scope = "project"
targets = ["opencode"]
`+ontoFrameworkModels())

	got, err := c.ExpandedSubagentEntriesForTool("opencode")
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	var nav *NamedResource
	for i := range got {
		if got[i].Name == "onto-explorer" {
			nav = &got[i]
		}
	}
	if nav == nil {
		t.Fatal("onto-explorer not expanded for opencode")
	}
	if nav.Resource.Scope != "project" || nav.Resource.Source != "builtin:onto-explorer" {
		t.Fatalf("onto-explorer inherited wrong scope/source: %+v", nav.Resource)
	}
}

func TestExpandedSubagentsExplicitVsFrameworkCollision(t *testing.T) {
	// onto-explorer is declared explicitly AND expanded by the onto framework.
	// The collision surfaces at load time because validateSubagentOverrides
	// expands the framework catalog to check tune-only entries, so even a
	// caller that never asks for the expansion sees the failure.
	dir := t.TempDir()
	p := filepath.Join(dir, "homonto.toml")
	doc := `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.onto-explorer]
source = "builtin:onto-explorer"
scope = "user"
`
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err == nil {
		// If load did not surface it, expand must.
		if _, err := c.ExpandedSubagentEntriesForTool("opencode"); err == nil {
			t.Fatal("expected collision error: onto-explorer declared explicitly and by framework")
		}
	}
}

// EnabledModelTools already iterates c.Subagents, so a subagent targeting a
// tool with no per-tool model block must fail at Load naming the offender.
// This test locks that behavior in place.
func TestLoadRequiresModelsForSubagentTargetedTool(t *testing.T) {
	doc := `
[subagents.onto-reviewer]
source = "builtin:onto-reviewer"
scope = "project"
targets = ["opencode"]
`
	if err := loadDoc(t, doc); err == nil {
		t.Fatal("subagent enabling opencode without a model block was accepted; want load error")
	} else if !strings.Contains(err.Error(), "subagents.onto-reviewer.opencode") {
		t.Fatalf("error %v does not mention missing opencode model block", err)
	}
}

// TestLoadRejectsRemovedMarketplaces: marketplaces were a Claude-only feature,
// removed with the adapter in v0.13.0. Any [marketplaces.claude.<name>]
// declaration must fail at load naming the marketplace — including one whose
// locator fields would have been valid before. (The per-source locator
// validation died with the feature; the whole table is rejected wholesale.)
func TestLoadRejectsRemovedMarketplaces(t *testing.T) {
	for _, tc := range []struct{ label, doc, name string }{
		{"github declaration", "[marketplaces.claude.official]\nsource = \"github\"\nrepo = \"anthropics/claude-plugins\"\nauto_update = true\n", "official"},
		{"unknown source kind", "[marketplaces.claude.weird]\nsource = \"svn\"\nrepo = \"x/y\"\n", "weird"},
		{"missing locator", "[marketplaces.claude.official]\nsource = \"github\"\n", "official"},
	} {
		err := loadDoc(t, tc.doc)
		if err == nil {
			t.Fatalf("%s: accepted; want the removal error", tc.label)
		}
		for _, want := range []string{"marketplaces.claude." + tc.name, "v0.13.0"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: error %v does not mention %q", tc.label, err, want)
			}
		}
	}
}

// TestAgentsParseFullDeclaration: a fully specified [agents.<name>] parses into
// c.Agents with source/version/targets/mode preserved verbatim.

// TestAgentsRejectInvalidSource: a non builtin:/local: source is rejected,
// naming the agent.
func TestAgentsRejectInvalidSource(t *testing.T) {
	err := loadDoc(t, "[agents.review]\nsource = \"https://example.com/x\"\n")
	if err == nil {
		t.Fatalf("invalid source accepted; want load error")
	}
	if !strings.Contains(err.Error(), "review") || !strings.Contains(err.Error(), "source") {
		t.Fatalf("error does not name the agent+source: %v", err)
	}
}

// TestAgentsRejectTraversalName: an agent name with path components is rejected
// (agents are projected to files named by the agent name in later increments, so
// a "../x" name must not survive declaration).
func TestAgentsRejectTraversalName(t *testing.T) {
	err := loadDoc(t, "[agents.\"../evil\"]\nsource = \"builtin:x\"\n")
	if err == nil {
		t.Fatalf("traversal agent name accepted; want load error")
	}
	if !strings.Contains(err.Error(), "not a plain name") {
		t.Fatalf("error does not flag the bad name: %v", err)
	}
}

// TestAgentsRejectTraversalLocalSource: a local: source with path components is
// rejected — it would resolve/materialize a file outside homonto/agents/ on
// `agents add` (a config-driven path-traversal / file-exfiltration vector).
func TestAgentsRejectTraversalLocalSource(t *testing.T) {
	err := loadDoc(t, "[agents.rev]\nsource = \"local:../../secret\"\n")
	if err == nil {
		t.Fatalf("traversal local source accepted; want load error")
	}
	if !strings.Contains(err.Error(), "plain name") {
		t.Fatalf("error does not flag the bad source: %v", err)
	}
}

// TestResourcesRejectTraversalLocalSource: a local: source with path components
// is rejected for skills AND commands (F28) — the same plain-name rule subagents
// already enforce, so a local:../../x can never join a traversal suffix into a
// provider path.
func TestResourcesRejectTraversalLocalSource(t *testing.T) {
	cases := []struct {
		name string
		doc  string
	}{
		{"skill", "[skills.x]\nsource = \"local:../../etc/x\"\nscope = \"user\"\n"},
		{"command", "[commands.y]\nsource = \"local:../y\"\nscope = \"user\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadDoc(t, tc.doc)
			if err == nil {
				t.Fatalf("traversal local %s source accepted; want load error", tc.name)
			}
			if !strings.Contains(err.Error(), "plain name") {
				t.Fatalf("error does not flag the bad source: %v", err)
			}
		})
	}
}

// TestResourcesAcceptPlainLocalSource: a plain local: name passes for skills and
// commands (the non-traversal counterpart to the rejection above).
func TestResourcesAcceptPlainLocalSource(t *testing.T) {
	docs := []string{
		"[skills.x]\nsource = \"local:x\"\nscope = \"user\"\n",
		"[commands.y]\nsource = \"local:y\"\nscope = \"user\"\n",
	}
	for _, doc := range docs {
		if err := loadDoc(t, doc); err != nil {
			t.Fatalf("plain local source must load, got %v for:\n%s", err, doc)
		}
	}
}

// TestAgentsRejectInvalidMode: a mode outside copy/link is rejected, naming the
// agent and the offending mode.
func TestAgentsRejectInvalidMode(t *testing.T) {
	err := loadDoc(t, "[agents.review]\nsource = \"builtin:x\"\nmode = \"symlink\"\n")
	if err == nil {
		t.Fatalf("invalid mode accepted; want load error")
	}
	if !strings.Contains(err.Error(), "review") || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error does not name the agent+mode: %v", err)
	}
}

// TestAgentsRejectUnknownTarget: a target naming a tool other than opencode is
// rejected, naming the offending target.
func TestAgentsRejectUnknownTarget(t *testing.T) {
	err := loadDoc(t, "[agents.review]\nsource = \"builtin:x\"\ntargets = [\"vscode\"]\n")
	if err == nil {
		t.Fatalf("unknown target accepted; want load error")
	}
	if !strings.Contains(err.Error(), "vscode") {
		t.Fatalf("error does not name the unknown target: %v", err)
	}
}

// TestLegacyModelTierRejected: a config carrying a legacy [models.<tool>.<level>]
// table must fail at load naming the offender. The TOML decoder silently drops
// unknown tables, so without explicit detection an edited-for-tiers config
// would parse clean and silently lose its model declarations.
func TestLegacyModelTierRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	doc := "[subagents.onto-reviewer]\nsource=\"builtin:onto-reviewer\"\nscope=\"project\"\n" +
		modelsFor("onto-reviewer") +
		"[models.claude.architectural]\nmodel = \"opus\"\n"
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a legacy [models.*.*] table must be rejected at load")
	}
	if !strings.Contains(err.Error(), "models.claude.architectural") {
		t.Fatalf("error must name the offending table, got: %v", err)
	}
}

// TestUnknownModelTierRejected is the legacy name retained as an alias for the
// scenario above; both the rejected-key form and the typo-tier form name the
// offending [models.<tool>.<level>] table.
func TestUnknownModelTierRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	doc := "[subagents.onto-reviewer]\nsource=\"builtin:onto-reviewer\"\nscope=\"project\"\n" +
		modelsFor("onto-reviewer") +
		"[models.opencode.reviewing]\nmodel = \"anthropic/claude-opus-4-8\"\n"
	if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("a legacy [models.*.*] table must be rejected at load")
	}
	if !strings.Contains(err.Error(), "models.opencode.reviewing") {
		t.Fatalf("error must name the offending table, got: %v", err)
	}
}

// TestMCPScopeValidation: an MCP's scope is user|project (empty = user), and a
// codex target is rejected outright — the codex pilot was removed in v0.13.0,
// so no scope combination can honor it.
func TestMCPScopeValidation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	load := func(doc string) error {
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		return err
	}

	if err := load("[mcps.cg]\ncommand=[\"cg\"]\nscope=\"project\"\n"); err != nil {
		t.Fatalf("a project-scoped MCP must load: %v", err)
	}
	err := load("[mcps.cg]\ncommand=[\"cg\"]\nscope=\"global\"\n")
	if err == nil || !strings.Contains(err.Error(), `scope "global"`) {
		t.Fatalf("an invalid MCP scope must be rejected naming the value, got: %v", err)
	}
	err = load("[mcps.cg]\ncommand=[\"cg\"]\nscope=\"project\"\ntargets=[\"codex\"]\n")
	if err == nil || !strings.Contains(err.Error(), "codex") || !strings.Contains(err.Error(), "removed in v0.13.0") {
		t.Fatalf("a codex MCP target must be rejected with the removal message, got: %v", err)
	}
}
