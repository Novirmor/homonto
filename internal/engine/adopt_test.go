package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/secret"
)

const adoptTOML = `
[settings.opencode]
model = "opus"
`

// A non-secret key already present on disk equal to desired, with EMPTY state,
// is planned as `adopt`. Applying it through the engine must record the key in
// state (via the per-adapter State.Save) and must not error — adopt carries no
// secret to resolve, so it is skipped in the engine's secret-resolve loop.
func TestApplyAdoptRecordsStateThroughEngine(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(cfgPath, []byte(adoptTOML), 0o644); err != nil {
		t.Fatal(err)
	}

	build := func() *Engine {
		e, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
		return e
	}

	// Seed disk: a first apply projects model=opus into opencode.jsonc and
	// records it in state.
	seed := build()
	sets, err := seed.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Apply(context.Background(), sets); err != nil {
		t.Fatalf("seed apply: %v", err)
	}

	// Drop the state so the on-disk key is now unmanaged: disk == desired but
	// absent from state is exactly the adopt precondition.
	if err := os.Remove(filepath.Join(repo, ".homonto", "state.json")); err != nil {
		t.Fatalf("clear state: %v", err)
	}

	e := build()
	sets, err = e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if findChange(sets, "adopt", "setting.model") == nil {
		t.Fatalf("precondition: expected adopt for setting.model, got %+v", sets)
	}

	// The adopt path must not be routed through secret resolution and must
	// persist via the per-adapter save.
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply of adopt must not error: %v", err)
	}
	if _, ok := e.State.Get("opencode", "setting.model"); !ok {
		t.Fatal("adopt did not record setting.model in engine state")
	}
	if _, err := os.Stat(filepath.Join(repo, ".homonto", "state.json")); err != nil {
		t.Fatalf("state not persisted to disk: %v", err)
	}
}

// findChange returns the first change across all sets matching action and key.
func findChange(sets []adapter.ChangeSet, action adapter.Action, key string) *adapter.Change {
	for i := range sets {
		for j := range sets[i].Changes {
			if sets[i].Changes[j].Action == action && sets[i].Changes[j].Key == key {
				return &sets[i].Changes[j]
			}
		}
	}
	return nil
}
