package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/agentfm"
)

const claudeConfig = `
harnesses = ["opencode", "claude"]

[engines.onto]
source = "builtin:onto"
scope = "project"

[services.host-gh]
source = "builtin:host-gh"
scope = "project"

[workflows.h-workflow-gh]
source = "builtin:h-workflow-gh"
scope = "project"

[routing.default.opencode]
model = "p/default"

[routing.default.claude]
model = "sonnet"

[routing.engines.onto.stages.verify.claude]
model = "opus"

[subagents.mine]
source = "local:mine"
scope = "project"
`

func claudeEngine(t *testing.T, doc string) (*Engine, string, string) {
	t.Helper()
	home, repo := t.TempDir(), t.TempDir()
	for name, data := range map[string]string{"homonto.toml": doc, "content/subagents/mine.md": mineAgent} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, "content")
	if err != nil {
		t.Fatal(err)
	}
	return e, home, repo
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return v
}

// One config projects the same builtins natively onto both harnesses: Claude
// Code gets its own agent files, settings, hooks and MCP server (M4.4).
func TestClaudeProjectionAlongsideOpenCode(t *testing.T) {
	e, home, repo := claudeEngine(t, claudeConfig)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	// Skills and commands are the same content on both harnesses.
	for _, path := range []string{".claude/skills/onto/SKILL.md", ".opencode/skills/onto/SKILL.md", ".claude/commands/onto.md"} {
		if _, err := os.Stat(filepath.Join(repo, path)); err != nil {
			t.Errorf("%s not projected: %v", path, err)
		}
	}
	// Agents render natively per harness from one intent.
	skeptic, err := os.ReadFile(filepath.Join(repo, ".claude/agents/onto-skeptic.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`model: "opus"`, "disallowedTools: Edit, Write, MultiEdit, NotebookEdit, Bash, AskUserQuestion, Agent, mcp__homonto"} {
		if !strings.Contains(string(skeptic), want) {
			t.Errorf("claude skeptic lacks %q:\n%s", want, skeptic)
		}
	}
	if strings.Contains(string(skeptic), "homonto:\n") || strings.Contains(string(skeptic), "mode:") {
		t.Errorf("claude skeptic carries OpenCode or neutral frontmatter:\n%s", skeptic)
	}
	implementer, err := os.ReadFile(filepath.Join(repo, ".claude/agents/onto-implementer.md"))
	if err != nil {
		t.Fatal(err)
	}
	policy, ok, err := agentfm.ClaudePolicyOf(implementer)
	if err != nil || !ok || len(policy.Bash) == 0 || len(policy.PathDeny) == 0 {
		t.Fatalf("implementer policy: %+v %v %v", policy, ok, err)
	}
	if d := policy.Evaluate("Write", map[string]any{"file_path": filepath.Join(repo, "docs", "changes", "x", "tasks.md")}, repo); d.Action != "deny" {
		t.Errorf("implementer may write workflow records: %+v (policy %+v)", d, policy)
	}
	mine, err := os.ReadFile(filepath.Join(repo, ".claude/agents/mine.md"))
	if err != nil || !strings.Contains(string(mine), `model: "sonnet"`) || !strings.Contains(string(mine), "disallowedTools: Edit") {
		t.Errorf("external intent agent not rendered for Claude Code: %v\n%s", err, mine)
	}
	coordinator, err := os.ReadFile(filepath.Join(repo, ".claude/agents/homonto.md"))
	if err != nil || strings.Contains(string(coordinator), "mcp__homonto") {
		t.Errorf("coordinator must keep its runtime tools: %v\n%s", err, coordinator)
	}

	settings := readJSON(t, filepath.Join(repo, ".claude/settings.json"))
	if settings["agent"] != "homonto" {
		t.Errorf("session agent = %v", settings["agent"])
	}
	hooks := settings["hooks"].(map[string]any)
	for _, event := range []string{"PreToolUse", "SessionStart", "PostToolUse", "PermissionRequest", "SubagentStop"} {
		group := hooks[event].([]any)[0].(map[string]any)
		command := group["hooks"].([]any)[0].(map[string]any)["command"].(string)
		if !strings.HasPrefix(command, "homonto runtime hook --config '"+e.ConfigPath+"'") || !strings.Contains(command, "--agents-dir '"+filepath.Join(repo, ".claude", "agents")+"'") {
			t.Errorf("%s hook command: %s", event, command)
		}
	}
	perms := settings["permissions"].(map[string]any)
	if got := perms["ask"].([]any); len(got) != 1 || got[0] != "mcp__homonto__homonto_github_publish" {
		t.Errorf("publication must ask: %v", got)
	}
	if got := perms["deny"].([]any); got[0] != "Bash(homonto runtime:*)" {
		t.Errorf("hook relay not denied to the model: %v", got)
	}
	mcp := readJSON(t, filepath.Join(repo, ".mcp.json"))["mcpServers"].(map[string]any)["homonto"].(map[string]any)
	args, _ := json.Marshal(mcp["args"])
	if mcp["command"] != "homonto" || string(args) != `["runtime","mcp","--harness","claude","--config",`+string(must(json.Marshal(e.ConfigPath)))+`,"--host","github"]` {
		t.Errorf("mcp server: %v %s", mcp["command"], args)
	}

	// The harness map covers both, and the projection is idempotent.
	harness, err := os.ReadFile(filepath.Join(e.CatalogDir(), "homonto", "references", "harness.md"))
	if err != nil || !strings.Contains(string(harness), "## Claude Code") || !strings.Contains(string(harness), "## OpenCode") {
		t.Errorf("harness map: %v", err)
	}
	e2, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, "content")
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range mustPlan(t, e2) {
		for _, ch := range cs.Changes {
			if ch.Action != "noop" {
				t.Errorf("replan not idempotent: %s %s %s", cs.Tool, ch.Action, ch.Key)
			}
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// Without Claude Code in the config nothing Claude is built or written.
func TestClaudeIsNotProjectedUntilTargeted(t *testing.T) {
	e, _, repo := claudeEngine(t, strings.Replace(claudeConfig, `harnesses = ["opencode", "claude"]`, "", 1))
	for _, a := range e.Adapters {
		if a.Name() == "claude" {
			t.Fatal("claude adapter built without a Claude target")
		}
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".claude")); !os.IsNotExist(err) {
		t.Fatalf(".claude written without a Claude target: %v", err)
	}
}

// A Claude-targeted agent without a Claude model fails closed at load.
func TestClaudeAgentNeedsAClaudeModel(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	doc := strings.Replace(claudeConfig, "[routing.default.claude]\nmodel = \"sonnet\"\n", "", 1)
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, "content")
	if err == nil {
		e, _ := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, "content")
		if e != nil {
			_, err = e.Plan()
		}
	}
	if err == nil || !strings.Contains(err.Error(), ".claude") {
		t.Fatalf("missing Claude model: %v", err)
	}
}

// One selector structure routes both harnesses (M4.6): the same agents get a
// policy from the same selector on each, with each harness's own model ids.
func TestRoutingSelectsTheSameSelectorsOnBothHarnesses(t *testing.T) {
	e, _, _ := claudeEngine(t, claudeConfig+`
[routing.engines.onto.stages.verify.opencode]
model = "p/verify"
`)
	oc, err := e.RoutesFor("opencode")
	if err != nil {
		t.Fatal(err)
	}
	cc, err := e.RoutesFor("claude")
	if err != nil {
		t.Fatal(err)
	}
	byAgent := func(r RouteReport) map[string]ResolvedRoute {
		m := map[string]ResolvedRoute{}
		for _, route := range r.Routes {
			m[route.Agent] = route
		}
		return m
	}
	o, c := byAgent(oc), byAgent(cc)
	if len(o) == 0 || len(o) != len(c) {
		t.Fatalf("route sets differ: %d opencode, %d claude", len(o), len(c))
	}
	for agent, or := range o {
		cr := c[agent]
		if strings.TrimSuffix(or.Selector, ".opencode") != strings.TrimSuffix(cr.Selector, ".claude") || or.From != cr.From {
			t.Errorf("%s: opencode %s/%s, claude %s/%s", agent, or.From, or.Selector, cr.From, cr.Selector)
		}
	}
	if c["onto-skeptic"].Selection == nil || c["onto-skeptic"].Selection.Model != "opus" || o["onto-skeptic"].Selection.Model != "p/verify" {
		t.Errorf("verify-stage agent: claude %+v, opencode %+v", c["onto-skeptic"].Selection, o["onto-skeptic"].Selection)
	}
	if c["onto-implementer"].Selection.Model != "sonnet" || o["onto-implementer"].Selection.Model != "p/default" {
		t.Errorf("default-routed agent: claude %+v, opencode %+v", c["onto-implementer"].Selection, o["onto-implementer"].Selection)
	}
}

// Claude Code cannot list its models, so an auto policy chooses among the
// models the config describes, and falls back when it describes none.
func TestClaudeAutoRoutingUsesDescribedModels(t *testing.T) {
	auto := strings.Replace(claudeConfig, "[routing.default.claude]\nmodel = \"sonnet\"\n", `[routing.default.claude]
mode = "auto"
prefer = "cost"
fallback = "sonnet"

[routing.models.claude.haiku]
cost = 1.0
[routing.models.claude.opus]
cost = 5.0
`, 1)
	e, _, _ := claudeEngine(t, auto)
	cc, err := e.RoutesFor("claude")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cc.Routes {
		if r.Agent == "onto-implementer" && (r.Selection == nil || r.Selection.Model != "haiku" || r.Selection.Fallback) {
			t.Fatalf("auto selection: %+v", r.Selection)
		}
	}
	none := strings.Replace(auto, "[routing.models.claude.haiku]\ncost = 1.0\n[routing.models.claude.opus]\ncost = 5.0\n", "", 1)
	e, _, _ = claudeEngine(t, none)
	cc, _ = e.RoutesFor("claude")
	for _, r := range cc.Routes {
		if r.Agent == "onto-implementer" && (r.Selection == nil || !r.Selection.Fallback || r.Selection.Model != "sonnet") {
			t.Fatalf("no described models must fall back: %+v", r.Selection)
		}
	}
}

// Doctor reports homonto's own runtime MCP registration and nothing about the
// user's servers that share .mcp.json (ADR 0107).
func TestDoctorReportsOnlyTheRuntimeMCPRegistration(t *testing.T) {
	e, _, repo := claudeEngine(t, claudeConfig)
	mcpPath := filepath.Join(repo, ".mcp.json")
	if err := os.WriteFile(mcpPath, []byte(`{"mcpServers":{"mine-secret-server":{"command":"mine"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	findings := strings.Join(e.Doctor(), "\n")
	if !strings.Contains(findings, `runtime MCP server "homonto" is not registered`) {
		t.Errorf("unregistered runtime not reported:\n%s", findings)
	}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	servers := readJSON(t, mcpPath)["mcpServers"].(map[string]any)
	if _, ok := servers["mine-secret-server"]; !ok {
		t.Fatalf("apply touched the user's own server: %v", servers)
	}
	findings = strings.Join(e.Doctor(), "\n")
	if !strings.Contains(findings, `ok: claude: runtime MCP server "homonto" registered`) {
		t.Errorf("registered runtime not reported ok:\n%s", findings)
	}
	if strings.Contains(findings, "mine-secret-server") {
		t.Errorf("doctor enumerated a user server:\n%s", findings)
	}
	servers["homonto"].(map[string]any)["args"] = []string{"runtime", "mcp"}
	data, _ := json.Marshal(map[string]any{"mcpServers": servers})
	if err := os.WriteFile(mcpPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if findings = strings.Join(e.Doctor(), "\n"); !strings.Contains(findings, "differs from the selected config") {
		t.Errorf("drifted registration not reported:\n%s", findings)
	}
}
