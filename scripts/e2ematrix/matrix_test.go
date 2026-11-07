package e2ematrix

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvidenceProvenanceAndSelection(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"scripts", "internal/starter", "bin"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	script, err := os.ReadFile("../e2e-matrix.sh")
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, content string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("scripts/e2e-matrix.sh", string(script), 0o755)
	write("internal/starter/starter.go", `"e2e/onto"`, 0o644)
	write("bin/docker", "#!/bin/sh\nexit 0\n", 0o755)
	write(".gitignore", "test/e2e/.out/\n", 0o644)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=test", "-c", "user.email=test@example.com", "commit", "-qm", "fixture")
	run := func(args ...string) (string, error) {
		t.Helper()
		cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts/e2e-matrix.sh")}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "PATH="+filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run("onto"); err != nil || !strings.Contains(out, "tree clean, image built") {
		t.Fatalf("clean run: %v\n%s", err, out)
	}
	if out, err := run("--no-build", "onto"); err != nil || !strings.Contains(out, "image unknown") || !strings.Contains(out, "not release evidence") {
		t.Fatalf("reused image: %v\n%s", err, out)
	}
	write("untracked", "new source", 0o644)
	if out, err := run("onto"); err != nil || !strings.Contains(out, "tree dirty") || !strings.Contains(out, "not release evidence") {
		t.Fatalf("dirty run: %v\n%s", err, out)
	}
	if out, err := run("misspelled"); err == nil || !strings.Contains(out, "unknown cell") {
		t.Fatalf("unknown selection: %v\n%s", err, out)
	}
}
