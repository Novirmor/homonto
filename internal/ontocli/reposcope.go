package ontocli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// scopedDirt is the live git audit for one repository participating in an
// onto change. Only legacy states include the implicit config repository.
type scopedDirt struct {
	Name    string      `json:"name"`
	Dir     string      `json:"dir"`
	Entries []dirtEntry `json:"entries"`
}

// scopeDirs resolves the stored [repos] aliases at the moment they are used.
// A missing or renamed alias is a close-gate failure, never silently omitted.
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
	cfg, err := workspace.LoadRoot(root)
	if err != nil {
		return nil, nil, fmt.Errorf("load declared repos: %w", err)
	}
	dirs := cfg.Repos
	for _, name := range names {
		if _, ok := dirs[name]; !ok {
			return nil, nil, fmt.Errorf("repo %q is no longer declared under [repos]", name)
		}
	}
	return names, dirs, nil
}

// State, not today's config schema, determines whether config Git is a source.
func stateSourceDirs(root string, st ontostate.State) (map[string]string, error) {
	if err := st.Validate(); err != nil {
		return nil, err
	}
	var authorities map[string]string
	if st.RepoMode == "legacy" && len(st.RepoBases) != 0 {
		l, err := workspace.LoadScopeRoot(root, st.Repos)
		if err != nil {
			return nil, err
		}
		if l.ExplicitRepos() {
			return nil, fmt.Errorf("source scope changed between legacy and explicit configuration; restore the recorded configuration before continuing")
		}
		authorities = l.Repos
		authorities[""] = l.ConfigRoot
	}
	dirs, err := workspace.SourceDirs(root, "onto", st.Change, st.Repos)
	if err != nil {
		return nil, err
	}
	if st.RepoMode != "explicit" {
		if _, ok := dirs[""]; !ok {
			dirs[""] = root
		}
	} else {
		delete(dirs, "")
	}
	if st.RepoMode == "" || (st.RepoMode == "legacy" && len(st.RepoBases) == 0) {
		return dirs, nil
	}
	for alias, dir := range dirs {
		base := st.RepoBases[alias]
		paths := []string{dir}
		if authorities != nil && authorities[alias] != dir {
			paths = append(paths, authorities[alias])
		}
		for _, path := range paths {
			identity, err := sourceIdentity(path)
			if err != nil || identity != base.GitCommonDir {
				return nil, fmt.Errorf("repository %s: Git identity does not match recorded base", entryDisplayName(alias))
			}
		}
		if base.BaseRef != "" {
			if _, err := resolveCommit(dir, base.BaseRef); err != nil {
				return nil, err
			}
			if err := isAncestor(dir, base.BaseRef, "HEAD"); err != nil {
				return nil, fmt.Errorf("repository %s: base_ref is not an ancestor of source HEAD: %w", alias, err)
			}
		}
		if base.BaseBranch != "" {
			if err := validateBranchName(dir, base.BaseBranch); err != nil {
				return nil, err
			}
			if _, err := resolveCommit(dir, "refs/heads/"+base.BaseBranch); err != nil {
				return nil, err
			}
		}
	}
	return dirs, nil
}

func sourceIdentity(dir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := gitAt(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		return "", fmt.Errorf("cannot identify source Git repository %s: %w", dir, err)
	}
	return filepath.EvalSymlinks(strings.TrimSpace(string(out)))
}

// A caller's Git environment must not redirect source or records authority.
func gitAt(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	return cmd
}

func selectedSource(root string, st ontostate.State, alias string) (string, string, error) {
	dirs, err := stateSourceDirs(root, st)
	if err != nil {
		return "", "", err
	}
	if alias == "" && st.RepoMode == "explicit" {
		if len(st.Repos) != 1 {
			return "", "", fmt.Errorf("--repo is required for a multi-repository change")
		}
		alias = st.Repos[0]
	}
	dir, ok := dirs[alias]
	if !ok {
		return "", "", fmt.Errorf("repository %q is not in the change's scope", alias)
	}
	return alias, dir, nil
}

func stateWorktreeDirt(root string, st ontostate.State) ([]scopedDirt, error) {
	dirs, err := stateSourceDirs(root, st)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(dirs))
	for alias := range dirs {
		names = append(names, alias)
	}
	sort.Strings(names)
	out := make([]scopedDirt, 0, len(names))
	for _, alias := range names {
		dir := dirs[alias]
		entries, ok := worktreeDirt(dir, st.Change)
		if !ok {
			return nil, fmt.Errorf("cannot determine repository %q worktree state", entryDisplayName(alias))
		}
		// Only a real combined source/records tree has workflow exceptions.
		rel, combined := recordsGitPrefix(root, dir)
		for i := range entries {
			if alias == "" && st.RepoMode != "explicit" {
				continue
			}
			entries[i].Class = "source"
			if combined {
				prefix := filepath.ToSlash(filepath.Join(rel, "changes")) + "/"
				entries[i].Class = classifyDirt(entries[i].Path, prefix, prefix+st.Change+"/")
			}
			entries[i].BlocksClose = entries[i].Class != "change"
		}
		out = append(out, scopedDirt{Name: entryDisplayName(alias), Dir: dir, Entries: entries})
	}
	return out, nil
}

// scopedWorktreeDirt audits the config repo and every selected declared repo.
// The config-repo scan retains onto's change-artifact carve-out; external
// repositories have no central workflow tree, so every uncommitted path is
// blocking there — including paths under a docs/changes/ the sibling may
// happen to have, which the config carve-out would otherwise excuse.
func scopedWorktreeDirt(root, change string, repos []string) ([]scopedDirt, error) {
	return stateWorktreeDirt(root, ontostate.State{Change: change, Phase: "open", Repos: repos})
}

// scopedDirtGateError renders all close-blocking dirt with repository labels.
func scopedDirtGateError(repos []scopedDirt, change string) string {
	var lines []string
	for _, repo := range repos {
		for _, entry := range blockingDirt(repo.Entries) {
			lines = append(lines, fmt.Sprintf("  %s: %s %s", repo.Name, entry.Status, entry.Path))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return fmt.Sprintf("%d uncommitted path(s) must be committed or stashed first:\n%s\nrun `onto dirt %s` for the full classified list", len(lines), joinLines(lines), change)
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	for _, line := range lines[1:] {
		out += "\n" + line
	}
	return out
}
