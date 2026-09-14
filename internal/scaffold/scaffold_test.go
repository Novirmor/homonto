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

func TestInitCreatesStarterFilesWithoutLocalContentDirectory(t *testing.T) {
	dir := t.TempDir()
	created, updated, err := Init(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 3 || len(updated) != 0 {
		t.Errorf("init: created %v, updated %v; want three created files and no updates", created, updated)
	}
	for _, name := range []string{"homonto.toml", ".gitignore", ".env.example"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("starter file %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "homonto")); !os.IsNotExist(err) {
		t.Fatalf("default init must not create homonto/: stat error = %v", err)
	}
}

func TestInitPreservesExistingLocalSkillContent(t *testing.T) {
	for _, legacyKeep := range []bool{false, true} {
		name := "without gitkeep"
		if legacyKeep {
			name = "with legacy gitkeep"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if _, _, err := Init(dir); err != nil {
				t.Fatal(err)
			}
			skill := filepath.Join("homonto", "skills", "custom", "SKILL.md")
			if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(skill)), 0o755); err != nil {
				t.Fatal(err)
			}
			content := map[string]string{
				"homonto.toml": "[skills.custom]\nsource = \"local:custom\"\nscope = \"project\"\n",
				skill:          "# My local skill\nUser-authored instructions.\n",
			}
			keep := filepath.Join("homonto", "skills", ".gitkeep")
			if legacyKeep {
				content[keep] = "# user-owned placeholder\n"
			}
			for path, body := range content {
				if err := os.WriteFile(filepath.Join(dir, path), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			created, updated, err := Init(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(created) != 0 || len(updated) != 0 {
				t.Errorf("re-init must be a no-op: created %v, updated %v", created, updated)
			}
			for path, want := range content {
				got, err := os.ReadFile(filepath.Join(dir, path))
				if err != nil || string(got) != want {
					t.Errorf("re-init changed %s: got %q, error %v; want %q", path, got, err, want)
				}
			}
			if !legacyKeep {
				if _, err := os.Stat(filepath.Join(dir, keep)); !os.IsNotExist(err) {
					t.Errorf("init must not add a gitkeep to local skills: stat error = %v", err)
				}
			}
		})
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
	if _, err := os.Stat(filepath.Join(dir, "homonto")); !os.IsNotExist(err) {
		t.Fatalf("init must not create homonto/: stat error = %v", err)
	}
}
