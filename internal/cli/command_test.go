package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitScaffoldsRepoAndSkipsExisting: `init <dir>` creates the starter files
// and reports each, and a second run is a no-op (existing files are skipped),
// so re-running init never clobbers a user's edits.
func TestInitScaffoldsRepoAndSkipsExisting(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()

	out, err := runCmd(t, home, "", "init", dir)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for _, want := range []string{"homonto.toml", ".gitignore", ".env.example",
		filepath.Join("homonto", "skills", ".gitkeep")} {
		p := filepath.Join(dir, want)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("init did not create %s: %v", want, err)
		}
		if !strings.Contains(out, p) {
			t.Fatalf("init output did not report %s\n%s", p, out)
		}
	}

	// Mark the config so we can prove the second run leaves it untouched.
	cfg := filepath.Join(dir, "homonto.toml")
	if err := os.WriteFile(cfg, []byte("# user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out2, err := runCmd(t, home, "", "init", dir)
	if err != nil {
		t.Fatalf("second init: %v\n%s", err, out2)
	}
	if strings.Contains(out2, cfg) {
		t.Fatalf("second init re-created an existing file:\n%s", out2)
	}
	if b, _ := os.ReadFile(cfg); string(b) != "# user edit\n" {
		t.Fatalf("second init clobbered an existing config: %q", string(b))
	}
}

// TestPlanReportsConfigLoadError: a command over an invalid config surfaces a
// clear, non-nil error naming the problem instead of proceeding or panicking —
// the command-level guard on bad input reaching the CLI.
func TestPlanReportsConfigLoadError(t *testing.T) {
	home := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "homonto.toml")
	// "mcp" is a reserved settings.opencode key (config.Load rejects it —
	// homonto itself manages the mcp structure in opencode.jsonc).
	if err := os.WriteFile(cfg, []byte("[settings.opencode]\nmcp = {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, home, "", "plan", "--config", cfg)
	if err == nil {
		t.Fatalf("plan accepted an invalid config; want error\n%s", out)
	}
	if !strings.Contains(err.Error(), "mcp") {
		t.Fatalf("error does not name the offending key: %v", err)
	}
}

// TestPlanReportsMissingConfig: a missing config file is a clear error, not a
// silent empty plan.
func TestPlanReportsMissingConfig(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist.toml")
	out, err := runCmd(t, home, "", "plan", "--config", missing)
	if err == nil {
		t.Fatalf("plan accepted a missing config; want error\n%s", out)
	}
}
