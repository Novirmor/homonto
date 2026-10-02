package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/state"
)

// The fixtures under testdata/ are byte-for-byte captures of what Claude Code
// 2.1.285 itself wrote for `claude mcp add` (sanitized to the mcpServers
// subtree, which is all `mcp add` contributes at each scope):
//
//	claude mcp add --scope user    demoU --env K=v -- srv --flag
//	claude mcp add --scope project demoP -- srvP
//
// Live discovery was verified against the same binary: after `homonto apply`
// projected a server into a scratch $HOME/.claude.json, `claude mcp list`
// listed it with the exact command and attempted to start it (failing only
// because the fake executable does not exist). The assertions below pin the
// format against the captured bytes so a Claude schema change fails here
// without needing the binary on PATH.
func fixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return doc
}

func servers(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	srv, ok := doc["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("fixture has no mcpServers object: %v", doc)
	}
	return srv
}

// mcpValue must reproduce claude's own user-scope entry, value for value,
// for the same declaration (command tail as args, env flat, type stdio).
func TestMCPValueMatchesClaudeWrittenUserEntry(t *testing.T) {
	fixtureEntry := servers(t, fixture(t, "mcp-user.claude-2.1.285.json"))["demoU"]
	want, ok := fixtureEntry.(map[string]any)
	if !ok {
		t.Fatalf("fixture entry is not an object: %v", fixtureEntry)
	}
	raw, ok := mcpValue(config.MCP{
		Command: []string{"srv", "--flag"},
		Env:     map[string]string{"K": "v"},
		Targets: []string{"claude"},
	})
	if !ok {
		t.Fatal("mcpValue declined a targeted MCP with a command")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("mcpValue produced invalid JSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("homonto entry != claude-written entry:\n got  %#v\n want %#v", got, want)
	}
}

// A claude-written project .mcp.json must be recognized as already-desired:
// planning an equivalent declaration over the fixture bytes yields no create
// or update — only the state-side adopt of the pre-existing entry. This is
// the compatibility statement: homonto's value and claude's value agree,
// including the empty "args"/"env" keys Claude Code writes for a bare
// command (mcpValue emits them for exactly this reason).
func TestClaudeWrittenProjectDocAdopts(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	// Seed the project .mcp.json with claude's own bytes.
	raw, err := os.ReadFile(filepath.Join("testdata", "mcp-project.claude-2.1.285.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	a := New(home, t.TempDir()).WithProjectRoot(root)
	st, _ := state.Load(t.TempDir())
	c := &config.Config{MCPs: map[string]config.MCP{
		"demoP": {Command: []string{"srvP"}, Scope: "project", Targets: []string{"claude"}},
	}}
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	adopted := 0
	for _, ch := range cs.Changes {
		switch ch.Action {
		case adapter.ActionAdopt:
			if ch.Key != "projmcp.demoP" {
				t.Fatalf("unexpected adopt: %+v", ch)
			}
			adopted++
		case adapter.ActionCreate, adapter.ActionUpdate, adapter.ActionDelete:
			t.Fatalf("claude-written entry must not be rewritten, got %+v", ch)
		}
	}
	// An empty plan must not pass: the claude-written entry HAS to surface as
	// an adopt, or the adapter is not planning project MCPs at all.
	if adopted != 1 {
		t.Fatalf("expected exactly one adopt of projmcp.demoP over the claude-written doc, got %d in %+v", adopted, cs.Changes)
	}
	// The adopt records a desired value that omits claude's empty keys; a
	// following apply+plan cycle converges to a pure no-op.
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	cs2, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range cs2.Changes {
		if ch.Action != adapter.ActionNoop {
			t.Fatalf("not converged after adopt: %+v", ch)
		}
	}
}
