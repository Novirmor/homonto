package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The claude target is opt-in and, in its first slice, MCP-only: an MCP may
// target claude (alone or alongside opencode) at user or repo-tagged project
// scope, while non-MCP resources naming claude stay fail-closed.
func TestClaudeMCPTarget(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	load := func(doc string) (*Config, error) {
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return Load(p)
	}

	cfg, err := load("[mcps.demo]\ncommand=[\"srv\"]\ntargets=[\"claude\"]\n")
	if err != nil {
		t.Fatalf("claude-only MCP target must load: %v", err)
	}
	if !cfg.TargetsTool("claude") {
		t.Fatal("TargetsTool(claude) must report true for an explicit claude target")
	}

	cfg, err = load("[mcps.demo]\ncommand=[\"srv\"]\ntargets=[\"opencode\",\"claude\"]\n")
	if err != nil {
		t.Fatalf("dual MCP target must load: %v", err)
	}
	if !cfg.TargetsTool("claude") || !cfg.TargetsTool("opencode") {
		t.Fatal("TargetsTool must report both tools for a dual target")
	}

	// Omitted targets stay OpenCode-only: claude is never implied.
	cfg, err = load("[mcps.demo]\ncommand=[\"srv\"]\n")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TargetsTool("claude") {
		t.Fatal("TargetsTool(claude) must be false when targets are omitted")
	}

	// Repo-targeted claude MCPs load: per-repo multi-tool fan-out projects
	// them into the repository's own .mcp.json (state partition shared per
	// repo, keyed by tool id).
	tmp := t.TempDir()
	svc := filepath.Join(tmp, "svc")
	if err := os.MkdirAll(filepath.Join(svc, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "homonto.toml"), []byte(
		"[repos]\nsvc = \"svc\"\n\n[mcps.demo]\ncommand=[\"srv\"]\nscope=\"project\"\nrepo=\"svc\"\ntargets=[\"claude\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(tmp, "homonto.toml")); err != nil {
		t.Fatalf("a repo-targeted claude MCP must load: %v", err)
	}
}

// TargetsTool answers the adapter-selection question for every resource kind:
// omitted targets select opencode only, an explicit target selects exactly the
// named tools, and tune-only subagent entries select nothing.
func TestTargetsTool(t *testing.T) {
	cfg := &Config{}
	if cfg.TargetsTool("opencode") {
		t.Fatal("an empty config declares nothing: no tool has a footprint")
	}
	if cfg.TargetsTool("claude") {
		t.Fatal("an empty config selects no opt-in tool")
	}
	// Tool-scoped surfaces without a targets field still select their tool.
	cfg = &Config{Settings: Settings{OpenCode: map[string]any{"theme": "dark"}}}
	if !cfg.TargetsTool("opencode") {
		t.Fatal("[settings.opencode] selects opencode")
	}

	cfg = &Config{
		Skills:     map[string]Resource{"s": {Source: "local:s", Scope: "project", Targets: []string{"claude"}}},
		Commands:   map[string]Resource{"c": {Source: "local:c", Scope: "project", Targets: []string{"opencode"}}},
		Frameworks: map[string]Resource{"f": {Source: "builtin:to", Scope: "project"}},
	}
	if !cfg.TargetsTool("claude") {
		t.Fatal("a skill targeting claude selects claude")
	}

	// A tune-only entry projects no agent and selects no tool.
	steps := 3
	cfg = &Config{
		Subagents: map[string]Subagent{
			"tuned": {OpenCode: ModelRoute{Model: "m", Steps: &steps}},
		},
	}
	if cfg.TargetsTool("claude") {
		t.Fatal("a tune-only entry must not select an opt-in tool")
	}
}

// Claude Code reserves the "workspace" server name and skips it at load; a
// claude-targeted MCP by that name would project clean and never run.
func TestClaudeReservedMCPNameRejected(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "homonto.toml")
	if err := os.WriteFile(p, []byte("[mcps.workspace]\ncommand=[\"srv\"]\ntargets=[\"claude\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil {
		t.Fatal("the reserved claude server name must be rejected")
	}
	if want := "reserved server name"; !strings.Contains(err.Error(), want) {
		t.Fatalf("rejection must name the reservation, got: %v", err)
	}
	// The same name stays legal for opencode.
	if err := os.WriteFile(p, []byte("[mcps.workspace]\ncommand=[\"srv\"]\ntargets=[\"opencode\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("opencode may use the name: %v", err)
	}
}
