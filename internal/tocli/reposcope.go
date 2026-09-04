package tocli

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/workcli"
)

const repoGitTimeout = 30 * time.Second

// scopeDirs resolves a change's recorded aliases against the config repo's
// current [repos] table. It deliberately stores names rather than paths so a
// stale path is never trusted at the terminal gate.
func scopeDirs(root string, repos []string) ([]string, map[string]string, error) {
	names := append([]string(nil), repos...)
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		if name == "" || seen[name] {
			return nil, nil, fmt.Errorf("invalid cross-repo scope")
		}
		seen[name] = true
	}
	if len(names) == 0 {
		return names, nil, nil
	}
	cfg, err := config.Load(filepath.Join(root, "homonto.toml"))
	if err != nil {
		return nil, nil, fmt.Errorf("load declared repos: %w", err)
	}
	dirs := cfg.RepoDirs()
	for _, name := range names {
		if _, ok := dirs[name]; !ok {
			return nil, nil, fmt.Errorf("repo %q is no longer declared under [repos]", name)
		}
	}
	return names, dirs, nil
}

// worktreeDirty returns an error rather than treating unavailable git as
// clean. It is used only by cross-repo workflows; legacy to changes remain
// intentionally git-blind.
func worktreeDirty(dir string) (bool, error) {
	return worktreeDirtyIgnoring(dir)
}

// worktreeDirtyIgnoring is the same bounded porcelain probe, except for known
// internal paths. A directory argument ignores that directory and its contents.
func worktreeDirtyIgnoring(dir string, ignores ...string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), repoGitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all").Output()
	if err != nil {
		return false, fmt.Errorf("cannot determine git state for %s", dir)
	}
	if len(out) > 0 && out[len(out)-1] != 0 {
		return false, fmt.Errorf("cannot parse git state for %s: unterminated porcelain record", dir)
	}
	for _, record := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if record == "" {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return false, fmt.Errorf("cannot parse git state for %s: malformed porcelain record", dir)
		}
		path := filepath.ToSlash(record[3:])
		ignored := false
		for _, ignore := range ignores {
			ignore = filepath.ToSlash(ignore)
			if path == ignore || strings.HasPrefix(path, strings.TrimSuffix(ignore, "/")+"/") {
				ignored = true
				break
			}
		}
		if !ignored {
			return true, nil
		}
	}
	return false, nil
}

// requireCleanScope checks the config repo and every selected external repo
// immediately before a scoped change records its terminal state. The active
// change workspace is ignored because done moves its uncommitted verification
// record and terminal state into the archive atomically.
func requireCleanScope(root, change string, repos []string) error {
	if len(repos) == 0 {
		return nil
	}
	workflowRoot := workcli.WorkflowRootOrDefault(root)
	workflowRel, err := filepath.Rel(root, workflowRoot)
	if err != nil {
		return fmt.Errorf("workflow root: %w", err)
	}
	if dirty, err := worktreeDirtyIgnoring(root, filepath.Join(workflowRel, "tasks", ".to.lock"), filepath.Join(workflowRel, "tasks", change)); err != nil {
		return err
	} else if dirty {
		return fmt.Errorf("config repo has uncommitted paths")
	}
	names, dirs, err := scopeDirs(root, repos)
	if err != nil {
		return err
	}
	for _, name := range names {
		dirty, err := worktreeDirty(dirs[name])
		if err != nil {
			return fmt.Errorf("declared repo %q: %w", name, err)
		}
		if dirty {
			return fmt.Errorf("declared repo %q has uncommitted paths", name)
		}
	}
	return nil
}
