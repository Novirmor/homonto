package tocli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

const repoGitTimeout = 30 * time.Second

func repoGitOutput(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), repoGitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cannot determine git state for %s: %w", dir, err)
	}
	return out, nil
}

func sourceCommonDir(dir string) (string, error) {
	inside, err := repoGitOutput(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(inside)) != "true" {
		return "", fmt.Errorf("source %s is not a usable git worktree", dir)
	}
	out, err := repoGitOutput(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(strings.TrimSuffix(string(out), "\n"))
}

// resolvedSources checks recorded authority before consulting execution bindings.
// Old unscoped records remain git-blind, even after a configuration upgrade.
func resolvedSources(root string, st tostate.State) (workspace.Layout, map[string]string, error) {
	if err := st.Validate(); err != nil {
		return workspace.Layout{}, nil, err
	}
	if st.RepoMode != tostate.SourceExplicit && len(st.Repos) == 0 {
		return workspace.Layout{}, nil, nil
	}
	l, err := workspace.LoadRoot(root)
	if err != nil {
		return l, nil, err
	}
	if l.ExplicitRepos() != (st.RepoMode == tostate.SourceExplicit) {
		return l, nil, fmt.Errorf("source scope changed between legacy and explicit configuration; restore the recorded configuration or explicitly migrate the change")
	}
	for _, alias := range st.Repos {
		if l.Repos[alias] == "" {
			return l, nil, fmt.Errorf("repo %q is no longer declared under [repos]", alias)
		}
	}
	for alias, base := range st.RepoBases {
		dir := l.Repos[alias]
		if alias == "" {
			dir = l.ConfigRoot
		}
		actual, err := sourceCommonDir(dir)
		if err != nil {
			return l, nil, err
		}
		if actual != base.GitCommonDir {
			return l, nil, fmt.Errorf("source authority identity changed for repo %q: recorded %s, configured %s; restore the declaration, do not substitute a different repository", alias, base.GitCommonDir, actual)
		}
	}
	dirs, err := workspace.SourceDirs(root, "to", st.Change, st.Repos)
	return l, dirs, err
}

// dirtyPaths probes only execution checkouts. --no-renames ensures a move from
// source into ignored records still exposes its source deletion.
func dirtyPaths(dir string, ignores ...string) ([]string, error) {
	out, err := repoGitOutput(dir, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return nil, err
	}
	if len(out) > 0 && out[len(out)-1] != 0 {
		return nil, fmt.Errorf("cannot parse git state for %s: unterminated porcelain record", dir)
	}
	var paths []string
	for _, record := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if record == "" {
			continue
		}
		if len(record) < 4 || record[2] != ' ' {
			return nil, fmt.Errorf("cannot parse git state for %s: malformed porcelain record", dir)
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
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func requireCleanScope(root string, st tostate.State) error {
	l, dirs, err := resolvedSources(root, st)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(dirs))
	for alias := range dirs {
		names = append(names, alias)
	}
	sort.Strings(names)
	var reports []string
	for _, alias := range names {
		var ignores []string
		authority := l.Repos[alias]
		if alias == "" {
			authority = l.ConfigRoot
		}
		rel, err := filepath.Rel(authority, l.WorkflowRoot)
		if err != nil {
			return err
		}
		if rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if l.ExplicitRepos() && l.GitMode == "existing" {
				// Records are not source, including their counterpart in a bound checkout.
				ignores = []string{rel}
			} else if alias == "" {
				ignores = []string{filepath.Join(rel, "tasks", ".to.lock"), filepath.Join(rel, "tasks", st.Change)}
			}
		}
		paths, err := dirtyPaths(dirs[alias], ignores...)
		if err != nil {
			return fmt.Errorf("source repo %q: %w", alias, err)
		}
		if len(paths) > 0 {
			label := fmt.Sprintf("declared repo %q", alias)
			if alias == "" {
				label = "config repo"
			}
			reports = append(reports, fmt.Sprintf("%s at %s has uncommitted paths: %q", label, dirs[alias], paths))
		}
	}
	if len(reports) > 0 {
		return fmt.Errorf("%s; preserve or isolate this work (for example in a registered worktree), or explicitly choose cleanup after inspection; no files were discarded", strings.Join(reports, "; "))
	}
	return nil
}
