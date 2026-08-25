package gitx

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// worktreeRegisteredWith is the free-function form of Service.worktreeRegistered
// so effects (which carry only a Runner) can check registration during
// recovery dispatch.
func worktreeRegisteredWith(ctx context.Context, r Runner, repoDir, path string) (WorktreeEntry, bool, error) {
	out, err := r.Run(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return WorktreeEntry{}, false, fmt.Errorf("gitx: worktree list of %s: %w", repoDir, err)
	}
	var entries []WorktreeEntry
	var cur WorktreeEntry
	seen := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
			if seen {
				entries = append(entries, cur)
				cur = WorktreeEntry{}
				seen = false
			}
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
			seen = true
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			cur.Branch = ""
		}
	}
	if seen {
		entries = append(entries, cur)
	}
	for _, e := range entries {
		if samePath(e.Path, path) {
			return e, true, nil
		}
	}
	return WorktreeEntry{}, false, nil
}

// WorktreeInventory parses git worktree list --porcelain of repoDir into
// entries (path, HEAD, checked-out branch). It is the recovery inventory:
// orphaned worktrees are pruned only through explicit Cleanup, never
// automatically during active work.
func (s *Service) WorktreeInventory(ctx context.Context, repoDir string) ([]WorktreeEntry, error) {
	out, err := s.runner.Run(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("gitx: worktree list of %s: %w", repoDir, err)
	}
	var entries []WorktreeEntry
	var cur WorktreeEntry
	seen := false
	for _, line := range strings.Split(out, "\n") {
		switch {
		case line == "":
			if seen {
				entries = append(entries, cur)
				cur = WorktreeEntry{}
				seen = false
			}
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
			seen = true
		case strings.HasPrefix(line, "HEAD "):
			cur.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			cur.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "detached":
			cur.Branch = ""
		}
	}
	if seen {
		entries = append(entries, cur)
	}
	return entries, nil
}

// worktreeRegistered reports whether path is a registered worktree of
// repoDir and returns its entry.
func (s *Service) worktreeRegistered(ctx context.Context, repoDir, path string) (WorktreeEntry, bool, error) {
	entries, err := s.WorktreeInventory(ctx, repoDir)
	if err != nil {
		return WorktreeEntry{}, false, err
	}
	for _, e := range entries {
		if samePath(e.Path, path) {
			return e, true, nil
		}
	}
	return WorktreeEntry{}, false, nil
}

// samePath compares two paths physically where both exist, falling back to
// lexical equality otherwise (a registered worktree whose directory was
// deleted still matches its path).
func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA == nil && errB == nil {
		return ra == rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// DirtyPaths returns the repo-root-relative paths with uncommitted
// changes, so a caller can refuse before it starts rather than partway
// through.
func DirtyPaths(ctx context.Context, r Runner, dir string) ([]string, error) {
	return dirtyPaths(ctx, r, dir)
}

// dirtyPaths returns the repo-root-relative paths with uncommitted changes
// — modified, staged, renamed, or untracked-but-not-ignored — from
// git status --porcelain=v1 -z.
func dirtyPaths(ctx context.Context, r Runner, dir string) ([]string, error) {
	out, err := r.Run(ctx, dir, "status", "--porcelain=v1", "-z")
	if err != nil {
		return nil, fmt.Errorf("gitx: status of %s: %w", dir, err)
	}
	segs := strings.Split(out, "\x00")
	var paths []string
	for i := 0; i < len(segs); i++ {
		seg := segs[i]
		if seg == "" {
			continue
		}
		if len(seg) < 4 {
			return nil, fmt.Errorf("gitx: status of %s: unparseable record %q", dir, seg)
		}
		path := seg[3:]
		if path == "" {
			return nil, fmt.Errorf("gitx: status of %s: empty path in record %q", dir, seg)
		}
		paths = append(paths, path)
		switch seg[0] {
		case 'R', 'C':
			i++
			if i >= len(segs) || segs[i] == "" {
				return nil, fmt.Errorf("gitx: status of %s: rename %q without destination", dir, seg)
			}
			paths = append(paths, segs[i])
		}
	}
	return paths, nil
}

// revParse resolves rev (plus suffix) to its full object id, verifying the
// object exists.
func revParse(ctx context.Context, r Runner, dir, rev, suffix string) (string, error) {
	out, err := r.Run(ctx, dir, "rev-parse", "--verify", rev+suffix)
	if err != nil {
		return "", fmt.Errorf("gitx: rev-parse %s%s in %s: %w", rev, suffix, dir, err)
	}
	return strings.TrimSpace(out), nil
}

// isAncestor reports whether a is an ancestor of b (both commit-ish).
func isAncestor(ctx context.Context, r Runner, dir, a, b string) (bool, error) {
	_, err := r.Run(ctx, dir, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var ce *CommandError
	if errors.As(err, &ce) && ce.ExitCode == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitx: merge-base --is-ancestor %s %s in %s: %w", a, b, dir, err)
}

// inCherryPick reports whether a cherry-pick is in progress in dir
// (CHERRY_PICK_HEAD exists).
func inCherryPick(ctx context.Context, r Runner, dir string) bool {
	_, err := r.Run(ctx, dir, "rev-parse", "-q", "--verify", "CHERRY_PICK_HEAD")
	return err == nil
}

// conflictedFiles returns the unmerged paths of a cherry-pick in progress.
func conflictedFiles(ctx context.Context, r Runner, dir string) ([]string, error) {
	out, err := r.Run(ctx, dir, "diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, fmt.Errorf("gitx: conflicted files of %s: %w", dir, err)
	}
	var files []string
	for _, seg := range strings.Split(out, "\x00") {
		if seg != "" {
			files = append(files, seg)
		}
	}
	return files, nil
}
