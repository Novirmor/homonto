package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/secret"
)

// TestUnparseableToolFileSkipsAdapterWithWarning: with OpenCode the only
// adapter, the old cross-tool isolation scenario (a broken tool file must not
// block the OTHER tool) is gone — there is no other tool. What remains of the
// fail-soft contract is that Plan does not hard-fail on an unparseable tool
// file: the adapter is skipped with a warning (which the CLI turns into a
// non-zero apply exit), and it contributes no changes to the plan.
func TestUnparseableToolFileSkipsAdapterWithWarning(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(`
[mcps.codegraph]
command = ["codegraph","serve"]
targets = ["opencode"]
`), 0o644)
	// broken JSONC that Standardize cannot parse
	ocDir := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(ocDir, 0o755)
	os.WriteFile(filepath.Join(ocDir, "opencode.jsonc"), []byte(`{ "plugin": [ this is not json `), 0o644)

	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}

	sets, err := e.Plan()
	if err != nil {
		t.Fatalf("plan should not hard-fail on a broken tool file: %v", err)
	}
	if len(e.Warnings) == 0 || !strings.Contains(strings.Join(e.Warnings, "\n"), "opencode") {
		t.Fatalf("expected a skip warning naming the opencode adapter, got %v", e.Warnings)
	}
	if len(sets) != 0 {
		t.Fatalf("a skipped adapter must contribute no changes, got %+v", sets)
	}
	// The corrupt file is never touched by a skipped adapter.
	if b, rerr := os.ReadFile(filepath.Join(ocDir, "opencode.jsonc")); rerr != nil || !strings.Contains(string(b), "this is not json") {
		t.Fatalf("skipped adapter's tool file must be left untouched, got %q (%v)", string(b), rerr)
	}
}
