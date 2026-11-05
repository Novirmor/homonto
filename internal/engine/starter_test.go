package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/starter"
)

var starterModels = map[string]string{"opencode": "anthropic/claude-sonnet-5-5", "claude": "sonnet"}

func applyStarter(t *testing.T, sel starter.Selection) {
	t.Helper()
	doc, err := starter.Render(sel)
	if err != nil {
		t.Fatal(err)
	}
	home, repo := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, "homonto")
	if err != nil {
		t.Fatalf("generated config does not build:\n%s\n%v", doc, err)
	}
	sets, err := e.Plan()
	if err != nil {
		t.Fatalf("generated config does not plan:\n%s\n%v", doc, err)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("generated config does not apply:\n%s\n%v", doc, err)
	}
	for _, w := range e.Warnings {
		if strings.Contains(w, "skipped") {
			t.Errorf("an adapter was skipped: %s", w)
		}
	}
}

// Every starter cell plans and applies (M5.1): each host × harness pairing
// with every engine, each engine alone on each harness, and everything at
// once. A cell that cannot is not offered by homonto init.
func TestEveryStarterSelectionPlansAndApplies(t *testing.T) {
	models := func(harnesses ...string) map[string]string {
		out := map[string]string{}
		for _, h := range harnesses {
			out[h] = starterModels[h]
		}
		return out
	}
	for _, cell := range starter.Cells() {
		sel := starter.Selection{Harnesses: []string{cell[1]}, Engines: starter.Engines, Models: models(cell[1])}
		if cell[0] != "" {
			sel.Hosts = []string{cell[0]}
		}
		t.Run("host="+cell[0]+"/harness="+cell[1], func(t *testing.T) { applyStarter(t, sel) })
		if cell[0] != "" {
			hostOnly := sel
			hostOnly.Engines = nil
			t.Run("host="+cell[0]+"/harness="+cell[1]+"/no-engine", func(t *testing.T) { applyStarter(t, hostOnly) })
		}
	}
	for _, h := range starter.Harnesses {
		for _, eng := range starter.Engines {
			sel := starter.Selection{Harnesses: []string{h.ID}, Engines: []string{eng}, Models: models(h.ID)}
			t.Run("harness="+h.ID+"/engine="+eng, func(t *testing.T) { applyStarter(t, sel) })
		}
	}
	all := starter.Selection{Hosts: []string{"github", "gitlab", "bitbucket"}, Harnesses: []string{"opencode", "claude"},
		Engines: starter.Engines, Models: models("opencode", "claude"), Tmp: true}
	t.Run("everything", func(t *testing.T) { applyStarter(t, all) })
}
