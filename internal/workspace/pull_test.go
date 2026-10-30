package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// pullFixture returns a schema-2 layout whose alias "a" is a clone of a bare
// remote, plus a second clone used to publish upstream commits.
func pullFixture(t *testing.T) (l Layout, source, upstream string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is not installed")
	}
	root := t.TempDir()
	seed := wtRepo(t, root, "seed", "main")
	remote := filepath.Join(root, "remote.git")
	wtGit(t, root, "clone", "--quiet", "--bare", seed, remote)
	clone := func(name string) string {
		dir := filepath.Join(root, name)
		wtGit(t, root, "clone", "--quiet", remote, dir)
		wtGit(t, dir, "config", "user.name", "Pull Test")
		wtGit(t, dir, "config", "user.email", "pull@example.invalid")
		wtGit(t, dir, "config", "commit.gpgsign", "false")
		return dir
	}
	source, upstream = clone("source"), clone("upstream")
	wtWrite(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("schema_version = 2\n[workflow]\nroot = 'records'\n[repos]\na = %q\n", source))
	l, err := LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	return l, source, upstream
}

func pullCommit(t *testing.T, dir, text string) string {
	t.Helper()
	wtWrite(t, filepath.Join(dir, "tracked"), text)
	wtGit(t, dir, "commit", "--quiet", "-am", text)
	return wtGit(t, dir, "rev-parse", "HEAD")
}

func pullPublish(t *testing.T, upstream, text string) string {
	t.Helper()
	sha := pullCommit(t, upstream, text)
	wtGit(t, upstream, "push", "--quiet", "origin", "main")
	return sha
}

func TestPullBaseFastForwardsBranchNotCheckedOut(t *testing.T) {
	l, source, upstream := pullFixture(t)
	wtGit(t, source, "switch", "--quiet", "-c", "user-work")
	wtWrite(t, filepath.Join(source, "tracked"), "user edit\n")
	want := pullPublish(t, upstream, "upstream one\n")

	r, err := PullBase(l, "a", "main")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "fast-forwarded" || r.After != want || r.Upstream != "origin/main" {
		t.Fatalf("result = %+v, want fast-forward to %s", r, want)
	}
	if got := wtGit(t, source, "rev-parse", "refs/heads/main"); got != want {
		t.Fatalf("main = %s, want %s", got, want)
	}
	if got := wtGit(t, source, "branch", "--show-current"); got != "user-work" {
		t.Fatalf("user checkout moved to %q", got)
	}
	if got := string(wtRead(t, filepath.Join(source, "tracked"))); got != "user edit\n" {
		t.Fatalf("user edit lost: %q", got)
	}
}

func TestPullBaseFastForwardsCleanCheckout(t *testing.T) {
	l, source, upstream := pullFixture(t)
	want := pullPublish(t, upstream, "upstream one\n")
	r, err := PullBase(l, "a", "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "fast-forwarded" || r.After != want {
		t.Fatalf("result = %+v, want fast-forward to %s", r, want)
	}
	if got := string(wtRead(t, filepath.Join(source, "tracked"))); got != "upstream one\n" {
		t.Fatalf("checkout not updated: %q", got)
	}
}

func TestPullBaseRefusesDirtyCheckout(t *testing.T) {
	l, source, upstream := pullFixture(t)
	before := wtGit(t, source, "rev-parse", "HEAD")
	pullPublish(t, upstream, "upstream one\n")
	wtWrite(t, filepath.Join(source, "tracked"), "user edit\n")
	_, err := PullBase(l, "a", "main")
	wtError(t, err, "uncommitted changes")
	wtError(t, err, "tracked")
	if got := wtGit(t, source, "rev-parse", "HEAD"); got != before {
		t.Fatalf("dirty checkout moved to %s", got)
	}
	if got := string(wtRead(t, filepath.Join(source, "tracked"))); got != "user edit\n" {
		t.Fatalf("user edit lost: %q", got)
	}
}

func TestPullBaseUpToDateAndAhead(t *testing.T) {
	l, source, _ := pullFixture(t)
	r, err := PullBase(l, "a", "main")
	if err != nil || r.Status != "up-to-date" {
		t.Fatalf("result = %+v, %v; want up-to-date", r, err)
	}
	local := pullCommit(t, source, "local only\n")
	r, err = PullBase(l, "a", "main")
	if err != nil || r.Status != "ahead" || r.After != local {
		t.Fatalf("result = %+v, %v; want ahead at %s", r, err, local)
	}
}

func TestPullBaseRefusesDivergedBranch(t *testing.T) {
	l, source, upstream := pullFixture(t)
	local := pullCommit(t, source, "local only\n")
	pullPublish(t, upstream, "upstream one\n")
	_, err := PullBase(l, "a", "main")
	wtError(t, err, "diverged")
	if got := wtGit(t, source, "rev-parse", "HEAD"); got != local {
		t.Fatalf("diverged branch moved to %s", got)
	}
}

func TestPullBaseRefusals(t *testing.T) {
	l, source, _ := pullFixture(t)
	wtGit(t, source, "branch", "--quiet", "--no-track", "solo", "main")

	_, err := PullBase(l, "", "main")
	wtError(t, err, "invalid repository alias")
	_, err = PullBase(l, "missing", "main")
	wtError(t, err, "not in [repos]")
	_, err = PullBase(l, "a", "refs/remotes/origin/main")
	wtError(t, err, "literal local branch")
	_, err = PullBase(l, "a", "nope")
	wtError(t, err, "does not exist")
	_, err = PullBase(l, "a", "solo")
	wtError(t, err, "no remote upstream")

	remote := wtGit(t, source, "remote", "get-url", "origin")
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}
	_, err = PullBase(l, "a", "main")
	wtError(t, err, "fetching origin")
}
