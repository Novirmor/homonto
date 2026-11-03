package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func examplesDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "examples"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestPackageCheckExamplesAndBuiltins is the conformance gate task check runs
// through `task contracts`: the examples and every shipped package conform.
func TestPackageCheckExamplesAndBuiltins(t *testing.T) {
	ex := examplesDir(t)
	out, err := runCmd(t, t.TempDir(), "", "package", "check", "--builtin",
		filepath.Join(ex, "example-engine"), filepath.Join(ex, "example-workflow"), filepath.Join(ex, "example-service"))
	if err != nil {
		t.Fatalf("package check: %v\n%s", err, out)
	}
	for _, want := range []string{"ok example-engine (engine 0.1.0)", "ok example-workflow (workflow 0.1.0)", "ok example-service (service 0.1.0)", "ok onto (engine", "ok host-gh (service"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func writePackage(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for path, data := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPackageCheckFailures(t *testing.T) {
	ex := examplesDir(t)
	engine := "manifest_schema = 2\nname = \"bad\"\nkind = \"engine\"\nversion = \"0.1.0\"\n[engine]\nlifecycle = \"stateless\"\nentry = [\"bad\"]\noperations = [\"start\", \"resume\"]\nstages = [\"work\"]\n[skills]\nbad = \"skills/bad\"\n"
	for _, tc := range []struct {
		name string
		dirs func() []string
		want string
	}{
		{"workflow without its non-builtin service", func() []string { return []string{filepath.Join(ex, "example-workflow")} }, `default package "example-service", which is not a known package`},
		{"legacy manifest", func() []string {
			return []string{writePackage(t, map[string]string{"framework.toml": "name = \"bad\"\nversion = \"0.1.0\"\n"})}
		}, "manifest: manifest_schema 0 carries no contract"},
		{"skill without frontmatter name", func() []string {
			return []string{writePackage(t, map[string]string{"framework.toml": engine, "skills/bad/SKILL.md": "no frontmatter\n"})}
		}, `skills: skill "bad": SKILL.md has no --- frontmatter`},
		{"unrenderable worker", func() []string {
			return []string{writePackage(t, map[string]string{
				"framework.toml":      engine + "[subagents]\nworker = \"subagents/worker.md\"\n",
				"skills/bad/SKILL.md": "---\nname: bad\ndescription: d\n---\n",
				"subagents/worker.md": "---\nname: worker\nhomonto:\n  bash: false\n  bash_deny: [\"x\"]\n---\n",
			})}
		}, `subagents: subagent "worker"`},
		{"no arguments", func() []string { return nil }, "name at least one package directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCmd(t, t.TempDir(), "", append([]string{"package", "check"}, tc.dirs()...)...)
			if err == nil || !strings.Contains(out+err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q\n%s", err, tc.want, out)
			}
		})
	}
}
