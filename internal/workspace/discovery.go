// Package workspace discovers member repositories on disk, creates the
// control repository that anchors a workspace, locates the workspace that
// owns a directory, and reconciles a configured workspace manifest against
// what is actually on disk.
//
// Discovery follows the workspace trust defaults (ADR 0024): it proposes,
// humans confirm. Scans never follow symlinks, apply the standard
// exclusion set, stop six directories below the scan root, and classify a
// directory as a git member only when it is itself a repository root
// (TopLevel == the directory); a plain subdirectory of a git member is
// that member's business, not a separate candidate.
package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// MaxDepth is how many directories below the scan root discovery
// descends.
const MaxDepth = 6

// excludedNames are directory names never entered during a scan.
var excludedNames = map[string]bool{
	".git":         true,
	".homonto":     true,
	"node_modules": true,
	"vendor":       true,
	".venv":        true,
	"dist":         true,
	"build":        true,
	"target":       true,
}

// manifests are the file names (checked before the *.sln glob) that mark a
// non-git directory as a project candidate.
var manifests = []string{
	"go.mod", "package.json", "pyproject.toml", "Cargo.toml",
	"pom.xml", "build.gradle", "build.gradle.kts", "Gemfile",
}

// Candidate is one discovered member proposal. Scans propose; the human
// confirms; nothing here is persisted.
type Candidate struct {
	// Path is the canonical absolute path of the candidate directory.
	Path string
	// Kind is git when the directory is a repository root, non_git when it
	// carries a recognized manifest (or was named explicitly).
	Kind workspacecfg.MemberKind
	// Manifest is the recognized manifest file name for a non_git
	// candidate, empty for git candidates and explicit paths without one.
	Manifest string
	// Git carries repository facts for git candidates; nil otherwise.
	Git *gitx.Repository
}

// ScanOptions tunes a scan. ExplicitPaths bypasses manifest detection: a
// named non-git directory without any manifest is still a candidate.
// Paths may be absolute or relative to the scan root, and must resolve
// inside it.
type ScanOptions struct {
	ExplicitPaths []string
}

// Scanner classifies candidate members below a root. The zero value uses
// the real git binary.
type Scanner struct {
	// Git inspects repositories; nil means gitx.ExecRunner{}.
	Git gitx.Runner
}

// Scan walks root (depth-limited, exclusions applied, symlinks never
// followed) and returns candidates sorted by path.
func (s Scanner) Scan(ctx context.Context, root string, opts ScanOptions) ([]Candidate, error) {
	canon, err := CanonicalPath(root)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(canon); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("workspace: scan root %s: not a directory", canon)
	}
	runner := s.Git
	if runner == nil {
		runner = gitx.ExecRunner{}
	}

	byPath := map[string]Candidate{}
	err = filepath.WalkDir(canon, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			return nil // regular files and symlinks are never candidates
		}
		if p != canon {
			if excludedNames[d.Name()] {
				return fs.SkipDir
			}
			if depth(canon, p) > MaxDepth {
				return fs.SkipDir
			}
		}
		cand, ok, err := s.classify(ctx, runner, p)
		if err != nil {
			return err
		}
		if ok {
			byPath[p] = cand
		}
		if ok && cand.Kind == workspacecfg.KindGit {
			// Everything inside a git member belongs to that member.
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("workspace: scan %s: %w", canon, err)
	}

	for _, raw := range opts.ExplicitPaths {
		p := raw
		if !filepath.IsAbs(p) {
			p = filepath.Join(canon, p)
		}
		cp, err := CanonicalPath(p)
		if err != nil {
			return nil, err
		}
		if cp != canon && !within(canon, cp) {
			return nil, fmt.Errorf("workspace: explicit path %q resolves outside scan root %s", raw, canon)
		}
		if _, ok := byPath[cp]; ok {
			continue
		}
		info, err := os.Stat(cp)
		if err != nil {
			return nil, fmt.Errorf("workspace: explicit path %q: %w", raw, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("workspace: explicit path %q is not a directory", raw)
		}
		cand, ok, err := s.classify(ctx, runner, cp)
		if err != nil {
			return nil, err
		}
		if !ok {
			// Explicit paths bypass manifest detection: a plain
			// directory is a non_git candidate even without one.
			cand = Candidate{Path: cp, Kind: workspacecfg.KindNonGit}
		}
		byPath[cp] = cand
	}

	out := make([]Candidate, 0, len(byPath))
	for _, c := range byPath {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// classify decides whether dir is a candidate. A directory that has a .git
// entry but is not a usable repository is an error, not a silent skip.
func (s Scanner) classify(ctx context.Context, runner gitx.Runner, dir string) (Candidate, bool, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		repo, isGit, err := gitx.Inspect(ctx, runner, dir)
		if err != nil {
			return Candidate{}, false, err
		}
		if !isGit {
			return Candidate{}, false, fmt.Errorf("workspace: %s: .git exists but is not a usable repository", dir)
		}
		if repo.TopLevel == dir {
			return Candidate{Path: dir, Kind: workspacecfg.KindGit, Git: &repo}, true, nil
		}
		// A subdirectory of a git repository: covered by its member.
		return Candidate{}, false, nil
	}
	if m := manifestAt(dir); m != "" {
		return Candidate{Path: dir, Kind: workspacecfg.KindNonGit, Manifest: m}, true, nil
	}
	return Candidate{}, false, nil
}

// manifestAt returns the recognized manifest file name directly inside
// dir, or "".
func manifestAt(dir string) string {
	for _, m := range manifests {
		if info, err := os.Stat(filepath.Join(dir, m)); err == nil && !info.IsDir() {
			return m
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries { // ReadDir sorts by name: deterministic
		if e.IsDir() || e.Type()&fs.ModeSymlink != 0 {
			continue
		}
		if strings.HasSuffix(e.Name(), ".sln") {
			return e.Name()
		}
	}
	return ""
}

// depth returns how many directories p sits below root.
func depth(root, p string) int {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// within reports whether sub is root or inside it (both canonical).
func within(root, sub string) bool {
	rel, err := filepath.Rel(root, sub)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, "../")
}
