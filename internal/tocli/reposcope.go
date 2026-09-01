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
	return worktreeDirtyIgnoring(dir, "")
}

// worktreeDirtyIgnoring is the same bounded porcelain probe, except for one
// known internal path. `to done` holds docs/tasks/.to.lock while checking its
// terminal gate; that lock serializes the operation and must not make the
// config repo appear dirty by itself.
func worktreeDirtyIgnoring(dir, ignore string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), repoGitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain", "-z").Output()
	if err != nil {
		return false, fmt.Errorf("cannot determine git state for %s", dir)
	}
	for _, record := range strings.Split(string(out), "\x00") {
		if len(record) < 4 || record[2] != ' ' {
			continue
		}
		if record[3:] != ignore {
			return true, nil
		}
	}
	return false, nil
}

// requireCleanScope checks the config repo and every selected external repo
// immediately before a scoped change records its terminal state.
func requireCleanScope(root string, repos []string) error {
	if len(repos) == 0 {
		return nil
	}
	if dirty, err := worktreeDirtyIgnoring(root, "docs/tasks/.to.lock"); err != nil {
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
