package baseadapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
)

// TestHasAnyPrefix verifies the shared managed-key matcher: hit on any listed
// prefix, miss on both a different prefix and a bare key with no dot.
func TestHasAnyPrefix(t *testing.T) {
	if !HasAnyPrefix("skill.notes", "skill.", "command.") {
		t.Errorf("HasAnyPrefix(skill.notes, skill., command.) = false, want true")
	}
	if HasAnyPrefix("mcp.notes", "skill.", "command.") {
		t.Errorf("HasAnyPrefix(mcp.notes, skill., command.) = true, want false")
	}
	if HasAnyPrefix("skills", "skill.") {
		t.Errorf("HasAnyPrefix(skills, skill.) = true, want false (prefix only)")
	}
}

// TestMCPProjected verifies the shared projection guard: a server projects
// only when it targets the tool (explicitly or by default-all) and has a
// command to run.
func TestMCPProjected(t *testing.T) {
	cmd := []string{"serve"}
	cases := []struct {
		name string
		m    config.MCP
		tool string
		want bool
	}{
		{"targets tool with command", config.MCP{Command: cmd, Targets: []string{"claude"}}, "claude", true},
		{"default targets all", config.MCP{Command: cmd}, "opencode", true},
		{"targets other tool only", config.MCP{Command: cmd, Targets: []string{"claude"}}, "opencode", false},
		{"no command", config.MCP{Targets: []string{"claude"}}, "claude", false},
	}
	for _, tc := range cases {
		if got := MCPProjected(tc.m, tc.tool); got != tc.want {
			t.Errorf("MCPProjected(%s) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestFilterDesired verifies each structproj namespace sees only the keys it
// owns — the returned map is a copy, never the input aliased.
func TestFilterDesired(t *testing.T) {
	in := map[string]string{"mcp.a": "1", "mcp.b": "2", "setting.x": "3"}
	got := FilterDesired(in, "mcp.")
	if len(got) != 2 || got["mcp.a"] != "1" || got["mcp.b"] != "2" {
		t.Errorf("FilterDesired(mcp.) = %v, want the two mcp entries", got)
	}
}

// TestFilterChanges verifies each structproj namespace applies only the
// changes it owns, order preserved, and that a filter matching nothing yields
// nil (not an empty slice) so append chains stay honest.
func TestFilterChanges(t *testing.T) {
	in := []adapter.Change{
		{Key: "mcp.a", Action: adapter.ActionCreate},
		{Key: "setting.x", Action: adapter.ActionUpdate},
		{Key: "mcp.b", Action: adapter.ActionDelete},
	}
	got := FilterChanges(in, "mcp.")
	if len(got) != 2 || got[0].Key != "mcp.a" || got[1].Key != "mcp.b" {
		t.Errorf("FilterChanges(mcp.) = %v, want [mcp.a mcp.b] in order", got)
	}
	if FilterChanges(in, "tui.") != nil {
		t.Errorf("FilterChanges(no match) = non-nil, want nil")
	}
}

// TestReadStandardizedJSON verifies the three first-projection behaviors: a
// missing file yields a standardized empty object, a valid document
// round-trips standardized, and a non-object root is an error naming the path.
func TestReadStandardizedJSON(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "absent.json")
	doc, err := ReadStandardizedJSON(missing)
	if err != nil {
		t.Fatalf("missing file: %v", err)
	}
	if strings.TrimSpace(string(doc)) != "{}" {
		t.Errorf("missing file standardized = %q, want {}", doc)
	}

	valid := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(valid, []byte("{\"model\":\"opus\",}"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err = ReadStandardizedJSON(valid)
	if err != nil {
		t.Fatalf("valid file: %v", err)
	}
	if !strings.Contains(string(doc), "\"model\"") {
		t.Errorf("valid file standardized = %q, want the model key", doc)
	}

	root := filepath.Join(dir, "array.json")
	if err := os.WriteFile(root, []byte("[1,2]"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = ReadStandardizedJSON(root)
	if err == nil || !strings.Contains(err.Error(), root) {
		t.Errorf("array root error = %v, want an error naming %q", err, root)
	}
}
