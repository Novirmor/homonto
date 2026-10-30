package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
)

func buildMinimalEngine(t *testing.T) *Engine {
	t.Helper()
	home := t.TempDir()
	repo := t.TempDir()
	toml := "[settings.opencode]\nshare = \"manual\"\n"
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	return buildEngine(t, home, repo)
}

// TestApply_RejectsUnknownTool proves Apply fails closed (not silently skips) on
// a change set whose tool is not a registered adapter.
func TestApply_RejectsUnknownTool(t *testing.T) {
	e := buildMinimalEngine(t)
	sets := []adapter.ChangeSet{{Tool: "ghost-tool", Changes: []adapter.Change{
		{Action: adapter.ActionCreate, Key: "setting.x", New: `{"a":1}`},
	}}}
	err := e.Apply(context.Background(), sets)
	if err == nil || !strings.Contains(err.Error(), "ghost-tool") {
		t.Fatalf("Apply should abort on unknown tool, got %v", err)
	}
}

// TestApply_RejectsUnknownAction proves Apply fails closed on an operation whose
// action is not one of the defined operations.
func TestApply_RejectsUnknownAction(t *testing.T) {
	e := buildMinimalEngine(t)
	tool := e.Adapters[0].Name()
	sets := []adapter.ChangeSet{{Tool: tool, Changes: []adapter.Change{
		{Action: adapter.Action("frobnicate"), Key: "setting.x", New: `{"a":1}`},
	}}}
	err := e.Apply(context.Background(), sets)
	if err == nil || !strings.Contains(err.Error(), "frobnicate") {
		t.Fatalf("Apply should abort on unknown action, got %v", err)
	}
}
