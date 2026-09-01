package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/secret"
)

const cfgTOML = `
[mcps.brave]
command = ["npx","server-brave"]
env = { K = "${MISSING_VAR}" }
targets = ["opencode"]
`

func TestApplyAbortsBeforeWritingOnMissingSecret(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "homonto.toml")
	os.WriteFile(cfgPath, []byte(cfgTOML), 0o644)

	e, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}

	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), sets); err == nil {
		t.Fatal("expected apply to fail on missing secret")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.jsonc")); !os.IsNotExist(err) {
		t.Fatal("apply wrote a file despite secret failure (not two-phase)")
	}
	if _, err := os.Stat(filepath.Join(repo, ".homonto", "state.json")); !os.IsNotExist(err) {
		t.Fatal("state written despite secret failure")
	}
}

func TestRelativeContentDirResolvesAgainstConfig(t *testing.T) {
	repo := t.TempDir()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "content", "skills", "onto"), 0o755)
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte("[skills.onto]\nsource=\"local:onto\"\nscope=\"user\"\n"), 0o644)

	wd, _ := os.Getwd()
	other := t.TempDir()
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	// t.Cleanup (not defer) so the working directory is restored even if the
	// test panics or a sub-test fails midway — process-global state must not
	// leak into neighboring tests in this package.
	t.Cleanup(func() { os.Chdir(wd) })

	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, "content")
	if err != nil {
		t.Fatal(err)
	}
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}
	dst := filepath.Join(home, ".config", "opencode", "skills", "onto")
	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("link missing: %v", err)
	}
	if !filepath.IsAbs(target) {
		t.Fatalf("link target must be absolute, got %q", target)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("link dangles: %v", err)
	}
}
