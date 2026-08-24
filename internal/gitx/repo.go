package gitx

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// Remote is one configured git remote (fetch URL).
type Remote struct {
	Name string
	URL  string
}

// Repository describes a git working tree found on disk.
type Repository struct {
	// TopLevel is the absolute working-tree root.
	TopLevel string
	// CommonDir is the absolute path of the shared repository directory
	// (".git" itself, or the main ".git/worktrees/<name>" directory for a
	// linked worktree).
	CommonDir string
	// Remotes are the configured remotes, sorted by name.
	Remotes []Remote
}

// Init runs git init with a deterministic initial branch (main).
func Init(ctx context.Context, r Runner, dir string) error {
	if _, err := r.Run(ctx, dir, "init", "-b", "main"); err != nil {
		return fmt.Errorf("gitx: init %s: %w", dir, err)
	}
	return nil
}

// Inspect reports whether path is inside a git working tree and, if so,
// that repository's facts: TopLevel is the enclosing working-tree root
// (even when path is a subdirectory), CommonDir the shared repository
// directory. A path outside any working tree is a clean negative (isGit
// false, nil error); callers that know a .git entry exists must treat a
// negative as corruption, since rev-parse refusing under those conditions
// means the repository is unusable.
func Inspect(ctx context.Context, r Runner, path string) (Repository, bool, error) {
	out, err := r.Run(ctx, path, "rev-parse", "--show-toplevel", "--git-common-dir")
	if err != nil {
		if IsNotRepository(err) {
			return Repository{}, false, nil
		}
		return Repository{}, false, fmt.Errorf("gitx: inspect %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		return Repository{}, false, fmt.Errorf("gitx: inspect %s: rev-parse printed %d lines, want 2", path, len(lines))
	}
	top := strings.TrimSpace(lines[0])
	common := strings.TrimSpace(lines[1])
	if !filepath.IsAbs(top) {
		return Repository{}, false, fmt.Errorf("gitx: inspect %s: toplevel %q is not absolute", path, top)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(top, common)
	}
	common = filepath.Clean(common)

	remotes, err := readRemotes(ctx, r, path)
	if err != nil {
		return Repository{}, false, err
	}
	return Repository{TopLevel: top, CommonDir: common, Remotes: remotes}, true, nil
}

// readRemotes parses `git remote -v`, keeping one entry per remote (the
// fetch URL), sorted by name.
func readRemotes(ctx context.Context, r Runner, dir string) ([]Remote, error) {
	out, err := r.Run(ctx, dir, "remote", "-v")
	if err != nil {
		return nil, fmt.Errorf("gitx: remotes of %s: %w", dir, err)
	}
	var remotes []Remote
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasSuffix(line, "(fetch)") {
			continue
		}
		line = strings.TrimSuffix(line, "(fetch)")
		name, url, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok {
			name, url, ok = strings.Cut(strings.TrimSpace(line), " ")
		}
		if !ok || name == "" || url == "" {
			return nil, fmt.Errorf("gitx: remotes of %s: unparseable line %q", dir, line)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		remotes = append(remotes, Remote{Name: name, URL: url})
	}
	sort.Slice(remotes, func(i, j int) bool { return remotes[i].Name < remotes[j].Name })
	return remotes, nil
}
