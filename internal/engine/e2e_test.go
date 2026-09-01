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
[mcps.codegraph]
command = ["codegraph","serve","--mcp"]

[mcps.brave]
command = ["npx","server-brave"]
env = { BRAVE_API_KEY = "${pass:ai/brave}" }
targets = ["opencode"]

[skills.graphify]
source = "local:graphify"
scope = "user"

[settings.opencode]
model = "opus"
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

	// codegraph projected into opencode.jsonc
	oc, _ := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.jsonc"))
	if gjson.GetBytes(oc, "mcp.codegraph.type").String() != "local" ||
		gjson.GetBytes(oc, "mcp.codegraph.command.0").String() != "codegraph" ||
		gjson.GetBytes(oc, "mcp.codegraph.command.1").String() != "serve" {
		t.Fatalf("opencode mcp missing:\n%s", oc)
	}
	// secret resolved on disk, skill linked
	if gjson.GetBytes(oc, "mcp.brave.environment.BRAVE_API_KEY").String() != "brave-secret" {
		t.Fatalf("secret not resolved on disk: %s", oc)
	}
	if _, err := os.Lstat(filepath.Join(home, ".config", "opencode", "skills", "graphify")); err != nil {
		t.Fatal("opencode skill link missing")
	}

	// Second apply: no changes, including the secret-backed MCP.
	e2 := build()
	sets2, _ := e2.Plan()
	if plan.HasChanges(sets2) {
		t.Fatalf("second apply not idempotent: %s", plan.Render(sets2))
	}
}
