package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// newGitDir creates a directory that passes the worktree check (a .git entry;
// a plain directory stands in for a real clone — the check tests presence,
// not git metadata).
func newGitDir(t *testing.T, parent, name string) string {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// writeReposConfig writes a homonto.toml declaring the given [repos] table
// under root and returns the config path.
func writeReposConfig(t *testing.T, root, reposToml string) string {
	t.Helper()
	p := filepath.Join(root, "homonto.toml")
	if err := os.WriteFile(p, []byte(reposToml), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReposLoadResolvesAbsolute verifies a valid [repos] table loads and
// RepoDirs carries each entry resolved to its absolute directory, so later
// stages and plan context never re-interpret relative paths.
func TestReposLoadResolvesAbsolute(t *testing.T) {
	base := t.TempDir()
	repo := t.TempDir()
	svcA := newGitDir(t, repo, "service-a")
	svcB := newGitDir(t, repo, "service-b")
	cfgPath := writeReposConfig(t, base,
		"[repos]\nservice-a = \""+filepath.Join(repo, "service-a")+"\"\nservice-b = \""+filepath.Join(repo, "service-b")+"\"\n")

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := c.RepoDirs()
	if len(got) != 2 || got["service-a"] != svcA || got["service-b"] != svcB {
		t.Errorf("RepoDirs = %v, want %s and %s resolved absolute", got, svcA, svcB)
	}
}

// TestReposRelativePathResolvesAgainstConfig verifies a relative repo path
// resolves against the config file's directory, not the process cwd — the
// same anchoring local: framework roots use.
func TestReposRelativePathResolvesAgainstConfig(t *testing.T) {
	base := t.TempDir()
	svc := newGitDir(t, base, "svc")
	if err := os.MkdirAll(filepath.Join(base, "cfg"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeReposConfig(t, filepath.Join(base, "cfg"), "[repos]\nsvc = \"../svc\"\n")

	c, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.RepoDirs()["svc"] != svc {
		t.Errorf("RepoDirs[svc] = %q, want %q", c.RepoDirs()["svc"], svc)
	}
}

// TestReposMissingDirectoryFailsLoad verifies a declared repo whose directory
// does not exist fails at load naming the repo and the resolved path.
func TestReposMissingDirectoryFailsLoad(t *testing.T) {
	base := t.TempDir()
	cfgPath := writeReposConfig(t, base, "[repos]\nghost = \""+filepath.Join(base, "nope")+"\"\n")
	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "repos.ghost") || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("load of missing repo = %v, want an error naming repos.ghost and does-not-exist", err)
	}
}

// TestReposNonGitDirectoryFailsLoad verifies a plain directory (no .git) is
// rejected: declared repos must be worktrees, because the staged cross-repo
// stages key git-aware behavior (onto dirt, drift attribution) on that fact.
func TestReposNonGitDirectoryFailsLoad(t *testing.T) {
	base := t.TempDir()
	plain := filepath.Join(base, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeReposConfig(t, base, "[repos]\nplain = \""+plain+"\"\n")
	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "not a git worktree") {
		t.Errorf("load of non-git repo = %v, want a not-a-git-worktree error", err)
	}
}

func TestReposWildcardPathFailsLoad(t *testing.T) {
	base := t.TempDir()
	for _, name := range []string{"service*", "service?"} {
		cfgPath := writeReposConfig(t, base, "[repos]\nservice = "+strconv.Quote(filepath.Join(base, name))+"\n")
		if _, err := Load(cfgPath); err == nil || !strings.Contains(err.Error(), "wildcard") {
			t.Errorf("load of wildcard repo path %q = %v, want wildcard rejection", name, err)
		}
	}
}

// TestReposDuplicateResolutionFailsLoad verifies two names resolving to one
// repository are rejected — one entry per repository keeps stage-2 per-repo
// state partitions unambiguous. The two entries use different raw strings
// ("svc" vs "svc/.") so the resolved-duplicate check is what fires, not the
// raw-path shape check.
func TestReposDuplicateResolutionFailsLoad(t *testing.T) {
	base := t.TempDir()
	svc := newGitDir(t, base, "svc")
	cfgPath := writeReposConfig(t, base,
		"[repos]\na = \""+svc+"\"\nb = \""+svc+"/.\"\n")
	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "same repository") {
		t.Errorf("load of duplicate repos = %v, want a same-repository error", err)
	}
}

// TestReposSelfReferenceFailsLoad verifies the config repo itself cannot be
// declared: it is implicit, and already holds the designated state.
func TestReposSelfReferenceFailsLoad(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := writeReposConfig(t, base, "[repos]\nself = \".\"\n")
	_, err := Load(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "config repository itself") {
		t.Errorf("load of self-referencing repo = %v, want a config-repo-is-implicit error", err)
	}
}

// TestReposBadNameAndEmptyPathFailShape verifies the pure-shape rules: a
// path-like name is not a usable identifier, and an empty path declares
// nothing.
func TestReposBadNameAndEmptyPathFailShape(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	cfgPath := writeReposConfig(t, base, "[repos]\n\"a/b\" = \".\"\n")
	if _, err := Load(cfgPath); err == nil || !strings.Contains(err.Error(), "not a plain name") {
		t.Errorf("load of path-like repo name = %v, want a plain-name error", err)
	}

	cfgPath = writeReposConfig(t, base, "[repos]\nsvc = \"\"\n")
	if _, err := Load(cfgPath); err == nil || !strings.Contains(err.Error(), "empty path") {
		t.Errorf("load of empty repo path = %v, want an empty-path error", err)
	}
}

// TestRepoTargetMustBeDeclaredAndProjectScoped verifies the repo = rules:
// the name must exist under [repos] and the resource must be project-scoped
// (a repo-tagged user-scope resource is a category error), and frameworks
// take no repo at all.
func TestRepoTargetMustBeDeclaredAndProjectScoped(t *testing.T) {
	base := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	svc := newGitDir(t, base, "svc")

	load := func(body string) error {
		t.Helper()
		p := filepath.Join(base, "homonto.toml")
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		return err
	}

	// Undeclared repo name.
	if err := load("[skills.x]\nsource=\"local:x\"\nscope=\"project\"\nrepo=\"ghost\"\n"); err == nil || !strings.Contains(err.Error(), "not declared under [repos]") {
		t.Errorf("undeclared repo = %v, want a not-declared error", err)
	}
	// Repo requires project scope.
	if err := load("[repos]\nsvc = \"" + svc + "\"\n\n[skills.x]\nsource=\"local:x\"\nscope=\"user\"\nrepo=\"svc\"\n"); err == nil || !strings.Contains(err.Error(), "requires scope") {
		t.Errorf("user-scope repo skill = %v, want a scope error", err)
	}
	// MCPs: repo requires project scope too (default is user).
	if err := load("[repos]\nsvc = \"" + svc + "\"\n\n[mcps.x]\ncommand=[\"x\"]\nrepo=\"svc\"\ntargets=[\"opencode\"]\n"); err == nil || !strings.Contains(err.Error(), "requires scope") {
		t.Errorf("user-scope repo mcp = %v, want a scope error", err)
	}
	// Frameworks take no repo.
	if err := load("[repos]\nsvc = \"" + svc + "\"\n\n[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\nrepo=\"svc\"\n"); err == nil || !strings.Contains(err.Error(), "config repository only") {
		t.Errorf("repo on framework = %v, want a config-repo-only error", err)
	}
	// The legal shapes load.
	if err := load("[repos]\nsvc = \"" + svc + "\"\n\n[skills.x]\nsource=\"local:x\"\nscope=\"project\"\nrepo=\"svc\"\n\n[mcps.y]\ncommand=[\"y\"]\nscope=\"project\"\nrepo=\"svc\"\ntargets=[\"opencode\"]\n"); err != nil {
		t.Errorf("valid repo targets = %v, want clean load", err)
	}
}
