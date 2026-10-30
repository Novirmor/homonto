package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/plan"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/tidwall/gjson"
)

const e2eTOML = `
[skills.graphify]
source = "builtin:handoff"
scope = "user"

[settings.opencode]
model = "opus"
provider = { brave = { options = { apiKey = "${pass:ai/brave}" } } }
`

func TestEndToEndApplyIsIdempotent(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(e2eTOML), 0o644)
	os.MkdirAll(filepath.Join(repo, "content", "skills", "graphify"), 0o755)

	build := func() *Engine {
		e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "brave-secret", nil }}
		return e
	}

	e := build()
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}

	// settings projected into opencode.jsonc
	oc, _ := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.jsonc"))
	if gjson.GetBytes(oc, "model").String() != "opus" {
		t.Fatalf("opencode setting missing:\n%s", oc)
	}
	// secret resolved on disk, skill linked
	if gjson.GetBytes(oc, "provider.brave.options.apiKey").String() != "brave-secret" {
		t.Fatalf("secret not resolved on disk: %s", oc)
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "opencode", "skills", "graphify")); err != nil {
		t.Fatal("opencode skill link missing")
	}

	// Second apply: no changes, including the secret-backed setting.
	e2 := build()
	sets2, _ := e2.Plan()
	if plan.HasChanges(sets2) {
		t.Fatalf("second apply not idempotent: %s", plan.Render(sets2))
	}
}
