package workspace

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

var runner = gitx.ExecRunner{}

// newScanner returns a Scanner backed by the real git runner.
func newScanner(t *testing.T) Scanner {
	t.Helper()
	return Scanner{Git: runner}
}

// CanonicalPathOf canonicalizes path or fails the test.
func CanonicalPathOf(t *testing.T, path string) string {
	t.Helper()
	p, err := CanonicalPath(path)
	if err != nil {
		t.Fatalf("CanonicalPath(%s): %v", path, err)
	}
	return p
}

// initRepo creates a real git repository at dir.
func initRepo(t *testing.T, dir string) string {
	t.Helper()
	mkdir(t, dir)
	if err := gitx.Init(context.Background(), runner, dir); err != nil {
		t.Fatalf("git init %s: %v", dir, err)
	}
	return dir
}

// manifestDirs creates one directory per manifest file directly under root.
func manifestDirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		dir := filepath.Join(root, strings.TrimSuffix(n, filepath.Ext(n))+"-proj")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", n, err)
		}
	}
}

func mkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestScanSixLevelsAndExclusions(t *testing.T) {
	root := t.TempDir()
	canon := CanonicalPathOf(t, root)

	// A candidate exactly six directories deep is found…
	deep := filepath.Join(root, "l1", "l2", "l3", "l4", "l5", "l6")
	mkdir(t, deep)
	writeFile(t, filepath.Join(deep, "go.mod"), "module deep\n")
	// …one level deeper is not.
	tooDeep := filepath.Join(root, "l1", "l2", "l3", "l4", "l5", "l6", "l7")
	mkdir(t, tooDeep)
	writeFile(t, filepath.Join(tooDeep, "go.mod"), "module toodeep\n")

	for _, excluded := range []string{".homonto", "node_modules", "vendor", ".venv", "dist", "build", "target"} {
		writeFile(t, filepath.Join(root, "sub", excluded, "hidden", "go.mod"), "module hidden\n")
	}
	// .git is seeded under an excluded parent: a bare .git directory under
	// a walked directory is corruption and fails the scan closed (covered
	// by TestScanCorruptGitDirFailsClosed), so exclusion of .git is
	// observable only inside already-skipped trees.
	writeFile(t, filepath.Join(root, "node_modules", ".git", "hidden", "go.mod"), "module hidden\n")

	got, err := newScanner(t).Scan(context.Background(), canon, ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %v, want exactly the depth-6 directory", paths(got))
	}
	if got[0].Path != CanonicalPathOf(t, deep) {
		t.Errorf("candidate path = %q, want %q", got[0].Path, deep)
	}
	if got[0].Kind != workspacecfg.KindNonGit || got[0].Manifest != "go.mod" {
		t.Errorf("candidate = %+v, want non_git with go.mod manifest", got[0])
	}
	if got[0].Git != nil {
		t.Errorf("non-git candidate carries Git = %+v", got[0].Git)
	}
}

func paths(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Path
	}
	return out
}

func TestScanManifestDetection(t *testing.T) {
	root := t.TempDir()
	manifestDirs(t, root,
		"go.mod", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml",
		"build.gradle", "build.gradle.kts", "Gemfile", "solution.sln",
	)
	// Not a recognized manifest.
	writeFile(t, filepath.Join(root, "notes", "README.md"), "hi\n")

	got, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 9 {
		t.Fatalf("candidates = %v, want 9 manifest hits", paths(got))
	}
	byManifest := map[string]Candidate{}
	for _, c := range got {
		byManifest[c.Manifest] = c
	}
	for _, m := range []string{"go.mod", "package.json", "pyproject.toml", "Cargo.toml", "pom.xml", "build.gradle", "build.gradle.kts", "Gemfile", "solution.sln"} {
		c, ok := byManifest[m]
		if !ok {
			t.Errorf("manifest %q not detected", m)
			continue
		}
		if c.Kind != workspacecfg.KindNonGit {
			t.Errorf("manifest %q classified %q, want non_git", m, c.Kind)
		}
	}
}

func TestScanClassifiesGitAndSkipsNested(t *testing.T) {
	root := t.TempDir()
	app := initRepo(t, filepath.Join(root, "app"))
	// A nested independent repo inside a git member is not discovered…
	initRepo(t, filepath.Join(app, "inner"))
	// …nor is a manifest directory inside the git member.
	writeFile(t, filepath.Join(app, "tool", "go.mod"), "module tool\n")
	writeFile(t, filepath.Join(root, "docs", "package.json"), "{}\n")

	got, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := []string{CanonicalPathOf(t, app), CanonicalPathOf(t, filepath.Join(root, "docs"))}
	sort.Strings(want)
	if len(got) != 2 {
		t.Fatalf("candidates = %v, want %v", paths(got), want)
	}
	var appCand, docsCand *Candidate
	for i := range got {
		switch got[i].Path {
		case want[0]:
			appCand = &got[i]
		case want[1]:
			docsCand = &got[i]
		}
	}
	if appCand == nil || docsCand == nil {
		t.Fatalf("missing candidates: %v", paths(got))
	}
	if appCand.Kind != workspacecfg.KindGit || appCand.Git == nil {
		t.Errorf("git candidate = %+v, want kind git with repository facts", *appCand)
	}
	if appCand.Git.TopLevel != CanonicalPathOf(t, app) {
		t.Errorf("git TopLevel = %q, want %q", appCand.Git.TopLevel, app)
	}
	if docsCand.Kind != workspacecfg.KindNonGit || docsCand.Manifest != "package.json" {
		t.Errorf("docs candidate = %+v, want non_git package.json", *docsCand)
	}
}

func TestScanRootItselfIsACandidate(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)

	got, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 || got[0].Path != CanonicalPathOf(t, root) || got[0].Kind != workspacecfg.KindGit {
		t.Fatalf("candidates = %v, want only the root as git candidate", paths(got))
	}
}

func TestScanDoesNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	mkdir(t, real)
	writeFile(t, filepath.Join(real, "go.mod"), "module real\n")
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	// A symlink to a git repository is equally skipped.
	gitDir := initRepo(t, filepath.Join(root, "grepo"))
	gitLink := filepath.Join(root, "gitlink")
	if err := os.Symlink(gitDir, gitLink); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for _, c := range got {
		// A followed symlink would surface under the link's lexical path.
		if c.Path == link || c.Path == gitLink {
			t.Errorf("symlinked directory discovered as candidate %q", c.Path)
		}
	}
	if len(got) != 2 {
		t.Fatalf("candidates = %v, want real and grepo only", paths(got))
	}
}

func TestScanDeterministicSort(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"zeta", "alpha", "mid/beta", "mid/aaa"} {
		writeFile(t, filepath.Join(root, name, "go.mod"), "module "+name+"\n")
	}
	got, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if !sort.StringsAreSorted(paths(got)) {
		t.Errorf("candidates not sorted: %v", paths(got))
	}
	// A second scan returns the identical order.
	again, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	for i := range got {
		if got[i].Path != again[i].Path {
			t.Errorf("scan order unstable: %v vs %v", paths(got), paths(again))
		}
	}
}

func TestScanExplicitPathsBypassManifestDetection(t *testing.T) {
	root := t.TempDir()
	plain := filepath.Join(root, "tools") // no manifest, no .git
	mkdir(t, plain)

	got, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{ExplicitPaths: []string{plain}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %v, want explicit path only", paths(got))
	}
	if got[0].Kind != workspacecfg.KindNonGit || got[0].Manifest != "" {
		t.Errorf("explicit candidate = %+v, want non_git without manifest", got[0])
	}

	// A relative explicit path resolves against the root.
	rel, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{ExplicitPaths: []string{"tools"}})
	if err != nil {
		t.Fatalf("Scan relative: %v", err)
	}
	if len(rel) != 1 || rel[0].Path != CanonicalPathOf(t, plain) {
		t.Fatalf("relative explicit candidates = %v, want %q", paths(rel), plain)
	}

	// An explicit git path stays git.
	initRepo(t, filepath.Join(root, "app"))
	gitExplicit, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{ExplicitPaths: []string{filepath.Join(root, "app")}})
	if err != nil {
		t.Fatalf("Scan git explicit: %v", err)
	}
	if len(gitExplicit) != 1 || gitExplicit[0].Kind != workspacecfg.KindGit || gitExplicit[0].Git == nil {
		t.Fatalf("git explicit candidates = %+v, want one git candidate", gitExplicit)
	}
}

func TestScanExplicitPathErrors(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if _, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{ExplicitPaths: []string{outside}}); err == nil {
		t.Error("explicit path outside root accepted")
	}
	if _, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{ExplicitPaths: []string{filepath.Join(root, "ghost")}}); err == nil {
		t.Error("missing explicit path accepted")
	}
}

func TestScanExplicitDotDotNamedChildIsInside(t *testing.T) {
	// A child literally named "..tools" is inside the root; only real
	// parent traversal may be rejected.
	root := t.TempDir()
	child := filepath.Join(root, "..tools")
	mkdir(t, child)

	got, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{ExplicitPaths: []string{child}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 || got[0].Path != CanonicalPathOf(t, child) {
		t.Fatalf("candidates = %v, want the ..tools child", paths(got))
	}
}

func TestScanDeduplicatesExplicitAndWalked(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "package.json"), "{}\n")

	got, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root),
		ScanOptions{ExplicitPaths: []string{filepath.Join(root, "docs"), "docs"}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %v, want deduplicated single entry", paths(got))
	}
}

func TestScanCorruptGitDirFailsClosed(t *testing.T) {
	root := t.TempDir()
	mkdir(t, filepath.Join(root, "broken", ".git"))

	if _, err := newScanner(t).Scan(context.Background(), CanonicalPathOf(t, root), ScanOptions{}); err == nil {
		t.Fatal("Scan: expected error for directory with unusable .git, got nil")
	}
}

func TestScanMissingRootFails(t *testing.T) {
	if _, err := newScanner(t).Scan(context.Background(), filepath.Join(t.TempDir(), "nope"), ScanOptions{}); err == nil {
		t.Fatal("Scan: expected error for missing root, got nil")
	}
}
