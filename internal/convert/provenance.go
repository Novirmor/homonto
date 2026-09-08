package convert

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// Check before recovery as well as fresh conversion: a staged source can still
// own a registry entry, and changing the framework strands even a stable ID.
func refuseBoundWorktrees(spec directionSpec, root, source, target string) error {
	if _, err := os.Lstat(filepath.Join(root, ".homonto", "worktrees.lock")); err == nil {
		return fmt.Errorf("%s: worktree registry operation is in progress; wait for its owner or hand off registry recovery before retrying conversion", spec.name)
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(filepath.Join(root, ".homonto", "worktrees.json")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	l, err := workspace.LoadRoot(root)
	if err != nil {
		return err
	}
	entries, err := workspace.ListWorktrees(l)
	if err != nil {
		return fmt.Errorf("%s: cannot safely check worktree bindings: %w; hand off registry recovery before retrying conversion", spec.name, err)
	}
	for _, entry := range entries {
		if (entry.Workflow == spec.from.workflow && entry.Change == source) || entry.Change == target {
			return fmt.Errorf("%s: registered worktree binding for %s/%s (%s); conversion cannot transactionally rebind workflows. Continue under %s, or hand off to an explicit workspace migration that preserves and rebinds the worktree before retrying; do not delete the registry entry", spec.name, entry.Workflow, entry.Change, entry.Repo, entry.Workflow)
		}
	}
	return nil
}

func conversionGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
		return "", fmt.Errorf("promote: git %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func promotionBases(root, srcDir string) (map[string]ontostate.RepoBase, error) {
	st, err := tostate.Load(filepath.Join(srcDir, tostate.FileName))
	if err != nil {
		return nil, err
	}
	if st.RepoMode != tostate.SourceExplicit && len(st.RepoBases) == 0 {
		return nil, nil
	}
	l, err := workspace.LoadRoot(root)
	if err != nil {
		return nil, err
	}
	if l.ExplicitRepos() != (st.RepoMode == tostate.SourceExplicit) {
		return nil, fmt.Errorf("promote: source scope changed between legacy and explicit configuration; restore the recorded configuration before conversion")
	}
	aliases := append([]string(nil), st.Repos...)
	if st.RepoMode == tostate.SourceLegacy {
		aliases = append(aliases, "")
	}
	bases := make(map[string]ontostate.RepoBase, len(aliases))
	for _, alias := range aliases {
		dir := l.Repos[alias]
		if alias == "" {
			dir = l.ConfigRoot
		}
		if dir == "" {
			return nil, fmt.Errorf("promote: repo %q is no longer declared under [repos]", alias)
		}
		base := ontostate.RepoBase(st.RepoBases[alias])
		identity, err := conversionGit(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return nil, err
		}
		identity, err = filepath.EvalSymlinks(identity)
		if err != nil || identity != base.GitCommonDir {
			return nil, fmt.Errorf("promote: source authority identity changed for repo %q; restore the recorded declaration before conversion", alias)
		}
		bases[alias] = base
	}
	// Preserve creation-time anchors; legacy anchors remain optional.
	probe := ontostate.State{SchemaVersion: ontostate.CurrentSchemaVersion, Change: st.Change, Phase: "open", Repos: st.Repos, RepoMode: st.RepoMode, RepoBases: bases}
	if err := probe.Validate(); err != nil {
		return nil, fmt.Errorf("promote: invalid target provenance: %w", err)
	}
	for alias, base := range bases {
		dir := l.Repos[alias]
		if alias == "" {
			dir = l.ConfigRoot
		}
		if base.BaseRef != "" {
			if _, err := conversionGit(dir, "merge-base", "--is-ancestor", base.BaseRef, "HEAD"); err != nil {
				return nil, err
			}
		}
		if base.BaseBranch != "" {
			if _, err := conversionGit(dir, "check-ref-format", "refs/heads/"+base.BaseBranch); err != nil {
				return nil, err
			}
			if _, err := conversionGit(dir, "rev-parse", "--verify", "refs/heads/"+base.BaseBranch+"^{commit}"); err != nil {
				return nil, err
			}
		}
	}
	return bases, nil
}
