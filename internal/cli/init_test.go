package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A selection writes a complete, valid starting configuration (M5.1).
func TestInitWritesASelectedStartingConfiguration(t *testing.T) {
	dir := t.TempDir()
	out, err := runCmd(t, t.TempDir(), "", "init", dir, "--harness", "opencode", "--harness", "claude",
		"--host", "gitlab", "--engine", "to", "--model", "opencode=anthropic/claude-sonnet-5-5", "--model", "claude=sonnet", "--tmp")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "homonto.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`harnesses = ["opencode", "claude"]`, "[engines.to]", "[workflows.h-workflow-gl]", "[routing.default.claude]", "[tmp]"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("generated config lacks %q:\n%s", want, data)
		}
	}
	for _, f := range []string{".gitignore", ".env.example"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("%s not scaffolded: %v", f, err)
		}
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, ".homonto-init-*")); len(matches) > 0 {
		t.Errorf("validation left a temp file: %v", matches)
	}
}

// A refused selection writes nothing at all.
func TestInitRefusesBeforeWriting(t *testing.T) {
	for name, args := range map[string][]string{
		"unsupported harness": {"--harness", "codex", "--engine", "to", "--model", "codex=x"},
		"missing model":       {"--harness", "claude", "--engine", "to"},
		"invalid model":       {"--harness", "opencode", "--engine", "to", "--model", "opencode=sonnet"},
		"missing repo":        {"--harness", "opencode", "--engine", "to", "--model", "opencode=a/b", "--repo", "api=../nope"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if out, err := runCmd(t, t.TempDir(), "", append([]string{"init", dir}, args...)...); err == nil {
				t.Fatalf("init accepted %v:\n%s", args, out)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != 0 {
				t.Errorf("a refused init wrote %d entries", len(entries))
			}
		})
	}
}

// init never edits an existing config, with or without selections.
func TestInitNeverEditsAnExistingConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "homonto.toml")
	if err := os.WriteFile(p, []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, t.TempDir(), "", "init", dir, "--harness", "opencode", "--engine", "no", "--model", "opencode=a/b"); err == nil || !strings.Contains(err.Error(), "never edits an existing config") {
		t.Fatalf("err = %v", err)
	}
	if data, _ := os.ReadFile(p); string(data) != "# mine\n" {
		t.Errorf("existing config changed: %q", data)
	}
}
