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
