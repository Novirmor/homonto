package remote

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestFetchGitErrorRedactsCommandOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake git uses a POSIX shell")
	}
	for _, locator := range []string{
		"https://user:s3cret@host/repo?token=s3cret",
		"https://user:s3cret'quote@host/repo?token=s3cret",
	} {
		t.Run(locator, func(t *testing.T) {
			bin := t.TempDir()
			diagnostic := "fatal: unable to access '" + locator + "': transport failed"
			script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' %s >&2\nexit 1\n", strconv.Quote(diagnostic))
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			_, _, err := fetchGit(context.Background(), "git+"+locator+"#v1", DefaultLimits)
			if err == nil {
				t.Fatal("want git error")
			}
			if strings.Contains(err.Error(), "s3cret") {
				t.Fatalf("git diagnostics leaked credentials: %v", err)
			}
			if !strings.Contains(err.Error(), "transport failed") {
				t.Fatalf("redaction lost the non-secret diagnostic: %v", err)
			}
		})
	}
}

func TestFetchGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--quiet")
	if err := os.WriteFile(filepath.Join(repo, "agent.md"), []byte("# a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "--quiet", "-m", "init")
	revb, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	rev := strings.TrimSpace(string(revb))

	src, err := ParseRemoteSource("remote:git+file://" + repo + "#" + rev)
	if err != nil {
		t.Fatal(err)
	}
	tree, _, err := Fetch(context.Background(), src, DefaultLimits)
	if err != nil {
		t.Fatalf("git fetch: %v", err)
	}
	if len(tree.Files) != 1 || tree.Files[0].Path != "agent.md" {
		t.Fatalf("unexpected tree: %+v", tree.Files)
	}
}
