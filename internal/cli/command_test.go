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
	for _, want := range []string{"homonto.toml", ".gitignore", ".env.example"} {
		p := filepath.Join(dir, want)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("init did not create %s: %v", want, err)
		}
		if !strings.Contains(out, p) {
			t.Fatalf("init output did not report %s\n%s", p, out)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "homonto")); !os.IsNotExist(err) {
		t.Fatalf("default init must not create homonto/: stat error = %v", err)
	}

	// Declare user-created local content before re-running init.
	cfg := filepath.Join(dir, "homonto.toml")
	configBody := "[skills.custom]\nsource = \"local:custom\"\nscope = \"project\"\n"
	if err := os.WriteFile(cfg, []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}
	skill := filepath.Join(dir, "homonto", "skills", "custom", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	const skillBody = "# My local skill\nUser-authored instructions.\n"
	if err := os.WriteFile(skill, []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}
	out2, err := runCmd(t, home, "", "init", dir)
	if err != nil {
		t.Fatalf("second init: %v\n%s", err, out2)
	}
	if out2 != "" {
		t.Fatalf("second init should report no changes:\n%s", out2)
	}
	if b, _ := os.ReadFile(cfg); string(b) != configBody {
		t.Fatalf("second init clobbered an existing config: %q", string(b))
	}
	if b, err := os.ReadFile(skill); err != nil || string(b) != skillBody {
		t.Fatalf("second init changed local skill content: %q, error %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "homonto", "skills", ".gitkeep")); !os.IsNotExist(err) {
		t.Fatalf("second init must not add a gitkeep: stat error = %v", err)
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

// TestPlanNamesDeclaredRepos verifies the plan context block (ADR 0024 stage
// 2): a config with [repos] lists every declared repo by name and resolved
// path, with the projection rule disclosed before its changesets.
func TestPlanNamesDeclaredRepos(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	svc := filepath.Join(repo, "service-a")
	if err := os.MkdirAll(filepath.Join(svc, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(cfgPath, []byte("[repos]\nservice-a = \"service-a\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", home)
	var out strings.Builder
	cmd := NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"plan", "--config", cfgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "repos (") || !strings.Contains(got, "service-a") || !strings.Contains(got, svc) {
		t.Errorf("plan should name the declared repo and its path:\n%s", got)
	}
	if !strings.Contains(got, "repo-tagged project resources project only into their named repo") {
		t.Errorf("plan must disclose the per-repo projection rule:\n%s", got)
	}
}
