package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeScript installs an executable shell script named name on PATH-less
// disk and returns its absolute path.
func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestExecRunnerForcesGitEnvironment(t *testing.T) {
	git := writeScript(t, "fake-git", `printf '%s\n%s\n%s\n' "$GIT_TERMINAL_PROMPT" "$LC_ALL" "$GIT_EDITOR"`)
	r := ExecRunner{Git: git}

	out, err := r.Run(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := out, "0\nC\ntrue\n"; got != want {
		t.Errorf("env output = %q, want %q", got, want)
	}
}

func TestExecRunnerRunsInDirWithoutShell(t *testing.T) {
	git := writeScript(t, "fake-git", `pwd
printf '%s\n' "$#" "$1" "$2"`)
	dir := t.TempDir()
	r := ExecRunner{Git: git}

	out, err := r.Run(context.Background(), dir, "rev-parse", "a b;c")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("output lines = %d (%q), want 4", len(lines), out)
	}
	if lines[0] != dir {
		t.Errorf("pwd = %q, want %q", lines[0], dir)
	}
	if lines[1] != "2" || lines[2] != "rev-parse" || lines[3] != "a b;c" {
		t.Errorf("args echoed as %q, want %q", lines[1:], "2 rev-parse a b;c")
	}
}

func TestExecRunnerNoStdin(t *testing.T) {
	// A command that reads stdin would block forever without a timeout if
	// stdin were an open pipe; /dev/null gives it immediate EOF.
	git := writeScript(t, "fake-git", `cat > /dev/null; echo done`)
	r := ExecRunner{Git: git, Timeout: 5 * time.Second}

	done := make(chan struct{})
	var err error
	go func() {
		_, err = r.Run(context.Background(), t.TempDir())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("Run blocked reading stdin")
	}
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestExecRunnerTimeoutBoundsCommand(t *testing.T) {
	git := writeScript(t, "fake-git", `sleep 10`)
	r := ExecRunner{Git: git, Timeout: 100 * time.Millisecond}

	start := time.Now()
	if _, err := r.Run(context.Background(), t.TempDir()); err == nil {
		t.Fatal("Run: expected timeout error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Run took %v, timeout not enforced", elapsed)
	}
}

func TestExecRunnerCommandErrorCarriesCodeAndStderr(t *testing.T) {
	git := writeScript(t, "fake-git", `echo boom >&2; exit 128`)
	r := ExecRunner{Git: git}

	_, err := r.Run(context.Background(), t.TempDir(), "status")
	var ce *CommandError
	if err == nil {
		t.Fatal("Run: expected error, got nil")
	}
	if !errors.As(err, &ce) {
		t.Fatalf("Run error = %T (%v), want *CommandError", err, err)
	}
	if ce.ExitCode != 128 {
		t.Errorf("ExitCode = %d, want 128", ce.ExitCode)
	}
	if !strings.Contains(ce.Stderr, "boom") {
		t.Errorf("Stderr = %q, want it to contain %q", ce.Stderr, "boom")
	}
	if !strings.Contains(ce.Error(), "boom") || !strings.Contains(ce.Error(), "status") {
		t.Errorf("Error() = %q, want stderr and args in message", ce.Error())
	}
}

func TestIsNotRepository(t *testing.T) {
	git := writeScript(t, "fake-git", `echo 'fatal: not a git repository (or any of the parent directories): .git' >&2; exit 128`)
	r := ExecRunner{Git: git}

	_, err := r.Run(context.Background(), t.TempDir(), "rev-parse")
	if err == nil {
		t.Fatal("Run: expected error")
	}
	if !IsNotRepository(err) {
		t.Errorf("IsNotRepository(%v) = false, want true", err)
	}

	gitOK := writeScript(t, "fake-git", `exit 1`)
	_, err2 := ExecRunner{Git: gitOK}.Run(context.Background(), t.TempDir(), "x")
	if IsNotRepository(err2) {
		t.Errorf("IsNotRepository(%v) = true for exit 1, want false", err2)
	}
	if IsNotRepository(nil) {
		t.Error("IsNotRepository(nil) = true, want false")
	}
}
