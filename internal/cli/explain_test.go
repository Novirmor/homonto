package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// explainCfg declares one direct mcp (with an unresolved secret reference),
// one setting, and the onto framework so framework origins are covered too.
const explainCfg = `
[settings.opencode]
theme = "opencode-dark"

[mcps.brave]
command = ["npx", "-y", "@example/server-brave"]
env = { BRAVE_API_KEY = "${EXPLAIN_SECRET_TOKEN}" }

[frameworks.onto]
source = "builtin:onto"
scope = "project"

[subagents.onto.opencode]
model = "openai/gpt-5"

[subagents.onto-explorer.opencode]
model = "openai/gpt-5-mini"

[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-4-8"

[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-4"

[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-4-8"
`

func explainSetup(t *testing.T) (home, cfg string) {
	t.Helper()
	home = t.TempDir()
	repo := t.TempDir()
	cfg = filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(cfg, []byte(explainCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXPLAIN_SECRET_TOKEN", "resolved-hunter2-value")
	if out, err := runCmd(t, home, "", "apply", "--yes", "--config", cfg); err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	return home, cfg
}

// TestExplainShowsOriginsAndHistory: after an apply, every declared resource
// explains its origin (direct vs framework), destination, and the operation
// that created it; the framework's transitive resources carry the framework
// origin with its provider.
func TestExplainShowsOriginsAndHistory(t *testing.T) {
	home, cfg := explainSetup(t)

	out, err := runCmd(t, home, "", "explain", "--config", cfg)
	if err != nil {
		t.Fatalf("explain: %v\n%s", err, out)
	}
	for _, want := range []string{
		"setting", "theme", "direct",
		"mcp", "brave",
		"skill", "onto", "framework:onto",
		"subagent", "onto-reviewer",
		"last: create (", // an operation with an id and timestamp
	} {
		if !strings.Contains(out, want) {
			t.Errorf("explain missing %q:\n%s", want, out)
		}
	}
	// The resolved secret must never appear; only the unresolved reference
	// lives in state and even that is not printed (values are never shown).
	if strings.Contains(out, "resolved-hunter2-value") {
		t.Errorf("explain leaked the resolved secret:\n%s", out)
	}
}

// TestExplainTombstoneAfterRemoval: de-declaring the mcp and re-applying
// leaves a tombstone; explain reports the removal with its operation.
func TestExplainTombstoneAfterRemoval(t *testing.T) {
	home, cfg := explainSetup(t)

	// Drop the whole mcp block by rewriting the config without it.
	without := ""
	for _, line := range strings.Split(explainCfg, "\n") {
		if strings.HasPrefix(line, "[mcps.brave]") {
			continue
		}
		if strings.Contains(line, "BRAVE_API_KEY") || strings.Contains(line, "command = [\"npx\"") {
			continue
		}
		without += line + "\n"
	}
	if err := os.WriteFile(cfg, []byte(without), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(t, home, "", "apply", "--yes", "--config", cfg); err != nil {
		t.Fatalf("re-apply: %v\n%s", err, out)
	}

	out, err := runCmd(t, home, "", "explain", "mcp", "--config", cfg)
	if err != nil {
		t.Fatalf("explain mcp: %v\n%s", err, out)
	}
	if !strings.Contains(out, "brave") || !strings.Contains(out, "removed") {
		t.Errorf("tombstone not reported:\n%s", out)
	}
}

// TestExplainSelectors: unknown kind fails listing valid kinds; unknown name
// fails; --json emits parseable, sorted rows.
func TestExplainSelectors(t *testing.T) {
	home, cfg := explainSetup(t)

	if _, err := runCmd(t, home, "", "explain", "bogus", "--config", cfg); err == nil {
		t.Fatal("unknown kind must fail")
	} else if !strings.Contains(err.Error(), "valid:") {
		t.Fatalf("unknown kind error must list kinds: %v", err)
	}
	if _, err := runCmd(t, home, "", "explain", "mcp", "nonexistent", "--config", cfg); err == nil {
		t.Fatal("unknown name must fail")
	}

	out, err := runCmd(t, home, "", "explain", "--json", "--config", cfg)
	if err != nil {
		t.Fatalf("explain --json: %v\n%s", err, out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("explain --json not parseable: %v\n%s", err, out)
	}
	if len(rows) < 5 {
		t.Fatalf("expected several rows, got %d:\n%s", len(rows), out)
	}
	for _, forbidden := range []string{"resolved-hunter2-value", "desired"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("explain JSON contains %q:\n%s", forbidden, out)
		}
	}
}

// TestExplainStateCarriesProvenance: the state file itself records origins
// and last events after apply (schema 3), and a no-op re-apply does not mint
// a new operation.
func TestExplainStateCarriesProvenance(t *testing.T) {
	home, cfg := explainSetup(t)
	stateFile := filepath.Join(filepath.Dir(cfg), ".homonto", "state.json")

	data, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"schemaVersion": 3`) {
		t.Errorf("state not schema 3:\n%s", data)
	}
	if !strings.Contains(string(data), `"origin"`) || !strings.Contains(string(data), `"lastEvent"`) {
		t.Errorf("state lacks provenance:\n%s", data)
	}

	// A no-op apply must not rewrite history with a fresh operation.
	if out, err := runCmd(t, home, "", "apply", "--yes", "--config", cfg); err != nil {
		t.Fatalf("no-op apply: %v\n%s", err, out)
	}
	after, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), `"lastEvent"`) {
		t.Errorf("no-op apply erased provenance:\n%s", after)
	}
}
