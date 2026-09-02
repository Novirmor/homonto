package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/secret"
)

// TestBundledPluginMaterializesAndProjects (A3): with the onto framework
// declared, the permission-observer plugin materializes under
// .homonto/catalog/plugins/; declaring it in [plugins.opencode] projects its
// materialized path into opencode.jsonc; a re-apply is a no-op.
func TestBundledPluginMaterializesAndProjects(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(`
[frameworks.onto]
source = "builtin:onto"
scope = "project"

`+ontoFrameworkModels+`

[plugins.opencode.permission-observer]
source = "permission-observer"
`), 0o644)

	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Plugin materialized as owned catalog content.
	pluginEntry := filepath.Join(repo, ".homonto", "catalog", "plugins", "permission-observer", "plugin.ts")
	if _, err := os.Stat(pluginEntry); err != nil {
		t.Fatalf("bundled plugin not materialized: %v", err)
	}

	// Projected into the plugin array by materialized path.
	cfgPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(repo, ".homonto", "catalog", "plugins", "permission-observer")
	if !containsString(string(data), want) {
		t.Fatalf("plugin array entry missing materialized path %q:\n%s", want, data)
	}

	// Idempotent re-apply.
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range sets {
		for _, c := range cs.Changes {
			if c.Action != "noop" && c.Action != "adopt" {
				t.Fatalf("re-apply not clean: %s %s", c.Action, c.Key)
			}
		}
	}
}

func containsString(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
