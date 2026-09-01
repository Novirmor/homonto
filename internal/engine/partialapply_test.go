package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
)

type failingAdapter struct{}

func (failingAdapter) Name() string { return "boom" }
func (failingAdapter) Plan(*config.Config, *state.State) (adapter.ChangeSet, error) {
	return adapter.ChangeSet{Tool: "boom"}, nil
}
func (failingAdapter) Apply(*config.Config, adapter.ChangeSet, *secret.Resolver, *state.State) error {
	// Deliberately does NOT contain the tool name: the engine must add it.
	return errors.New("adapter exploded")
}
func (failingAdapter) ObserveHashes(*state.State) (map[string]string, error) {
	return map[string]string{}, nil
}

// Deep review: state was saved only after ALL adapters succeeded, so a partial
// apply left no record at all — the next run treated already-written values as
// unknown provenance. State must be saved after each adapter's successful
// Apply, so an earlier adapter's record survives a later adapter's failure.
func TestPartialApplyPersistsEarlierAdapterState(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "homonto.toml")
	os.WriteFile(cfgPath, []byte("[mcps.codegraph]\ncommand = [\"codegraph\",\"serve\"]\ntargets = [\"opencode\"]\n"), 0o644)

	e, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	e.Adapters = append(e.Adapters, failingAdapter{})

	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	err = e.Apply(context.Background(), sets)
	if err == nil {
		t.Fatal("expected apply to fail on the failing adapter")
	}
	// Verify round 1: with several adapters, an unwrapped error ("adapter
	// exploded") leaves the user guessing which tool broke. The engine must
	// name it.
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("apply error does not name the failing adapter: %v", err)
	}
	st, err := state.Load(filepath.Join(repo, ".homonto"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("opencode", "mcp.codegraph"); !ok {
		t.Fatal("opencode's applied state lost because a later adapter failed")
	}
}
