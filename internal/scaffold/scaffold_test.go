package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/config"
)

// The scaffolded homonto.toml ships commented examples of every resource kind.
// A user uncomments the ones they want, so each example MUST be current, valid
// config: a stale format (e.g. the removed list-style [plugins] or [skills]
// own=[]) or an internally-inconsistent set (a tool targeted by a framework but
// missing its model routes) fails the instant it is uncommented. Reconstruct the
// fully uncommented config and run it through the real config.Load — the full
// parse+validate path, not just a struct decode.
func TestScaffoldExamplesUseCurrentFormatAndValidate(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Init(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "homonto.toml"))
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder
	for _, line := range strings.Split(string(raw), "\n") {
		body := strings.TrimPrefix(strings.TrimPrefix(line, "# "), "#")
		trimmed := strings.TrimSpace(body)
		// Uncomment only genuine config lines (a table header or a key = value);
		// leave prose header comments and blank lines untouched.
		if strings.HasPrefix(trimmed, "[") || strings.Contains(trimmed, " = ") {
			b.WriteString(body)
		} else {
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	uncommented := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(uncommented, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(uncommented)
	if err != nil {
		t.Fatalf("scaffolded examples do not load (parse+validate) when uncommented: %v\n---\n%s", err, b.String())
	}
	// Sanity-check the reconstruction actually enabled the plugin example in the
	// current per-plugin table form.
	if len(cfg.Plugins.OpenCode) == 0 {
		t.Error("expected the uncommented scaffold to declare a [plugins.opencode.<name>] example")
	}
}

func TestInitCreatesFilesAndSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "homonto.toml"), []byte("# mine\n"), 0o644)

	created, _, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range created {
		if filepath.Base(p) == "homonto.toml" {
			t.Fatal("must not recreate existing homonto.toml")
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "homonto.toml")); string(b) != "# mine\n" {
		t.Fatal("existing config overwritten")
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal(".gitignore not created")
	}
	if _, err := os.Stat(filepath.Join(dir, "homonto", "skills")); err != nil {
		t.Fatal("homonto/skills not created")
	}
}
