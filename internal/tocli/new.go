package tocli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"
)

// newCmd builds "to new <change-name>": it enforces toFramework.Gate(dir) and
// toFramework.ValidChangeName before scaffolding a change directory with
// to-state.yaml (phase plan) and an empty plan.md, and performs no writes if
// either check fails or the change already exists (active or archived).
func newCmd() *cobra.Command {
	var (
		dir      string
		jsonMode bool
		repos    []string
		bases    []string
	)

	cmd := &cobra.Command{
		Use:   "new <change-name>",
		Short: "Create a new change (phase plan), if the to framework is installed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNewWithRepos(cmd, dir, args[0], jsonMode, repos, bases)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to create the change in")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	cmd.Flags().StringSliceVar(&repos, "repo", nil, "declared repository to include (repeatable)")
	cmd.Flags().StringArrayVar(&bases, "base", nil, "integration base alias=branch (repeatable; config schema 2 only; defaults to each source's current branch)")
	return cmd
}

func runNew(cmd *cobra.Command, root, name string, jsonMode bool) error {
	return runNewWithRepos(cmd, root, name, jsonMode, nil, nil)
}

// runNewWithRepos records a selected cross-repo scope without creating any
// workflow files outside the config repository.
func runNewWithRepos(cmd *cobra.Command, root, name string, jsonMode bool, repos, bases []string) error {
	if err := toFramework.Gate(root); err != nil {
		return err
	}
	if err := toFramework.ValidChangeName(name); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(root, "homonto.toml"))
	if err != nil {
		return fmt.Errorf("to new: %w", err)
	}
	var cfg struct {
		SchemaVersion int `toml:"schema_version"`
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("to new: %w", err)
	}
	l := workspace.Layout{SchemaVersion: cfg.SchemaVersion}
	// The gate is sufficient for unscoped legacy changes. Do not resolve unused
	// repo declarations: they may be offline and are not part of this change.
	if len(repos) > 0 || (cfg.SchemaVersion != 0 && cfg.SchemaVersion != 1) {
		l, err = workspace.LoadRoot(root)
		if err != nil {
			return fmt.Errorf("to new: %w", err)
		}
	}
	if l.ExplicitRepos() && len(repos) == 0 {
		return fmt.Errorf("to new: schema_version = 2 requires at least one selected --repo; the config directory is not an implicit source")
	}
	if !l.ExplicitRepos() && len(bases) > 0 {
		return fmt.Errorf("to new: --base requires config schema_version = 2")
	}
	names := append([]string(nil), repos...)
	sort.Strings(names)
	st := tostate.State{
		SchemaVersion: tostate.CurrentSchemaVersion,
		ID:            opid.New().NewID(),
		Change:        name, Phase: tostate.PhasePlan, Created: todayFn(), Repos: names,
		RepoMode:  tostate.SourceLegacy,
		RepoBases: map[string]tostate.RepoBase{},
	}
	if l.ExplicitRepos() {
		st.RepoMode = tostate.SourceExplicit
	}
	for i, repo := range names {
		if repo == "" || (i > 0 && repo == names[i-1]) || l.Repos[repo] == "" {
			return fmt.Errorf("to new: repo %q must be a unique declared [repos] name", repo)
		}
	}
	baseBranches := make(map[string]string, len(bases))
	for _, selection := range bases {
		alias, branch, ok := strings.Cut(selection, "=")
		if !ok || alias == "" || branch == "" {
			return fmt.Errorf("to new: --base %q must be alias=branch", selection)
		}
		if i := sort.SearchStrings(names, alias); i == len(names) || names[i] != alias {
			return fmt.Errorf("to new: --base repo %q is not selected by --repo", alias)
		}
		if _, exists := baseBranches[alias]; exists {
			return fmt.Errorf("to new: duplicate --base for repo %q", alias)
		}
		baseBranches[alias] = branch
	}
	authorities := append([]string(nil), names...)
	if !l.ExplicitRepos() && len(names) > 0 {
		authorities = append(authorities, "")
	}
	for _, repo := range authorities {
		dir := l.Repos[repo]
		if repo == "" {
			dir = l.ConfigRoot
		}
		// Validate identity without rejecting or changing dirty original work.
		identity, err := sourceCommonDir(dir)
		if err != nil {
			return fmt.Errorf("to new: source repo %q: %w", repo, err)
		}
		base := tostate.RepoBase{GitCommonDir: identity}
		if l.ExplicitRepos() {
			branch := baseBranches[repo]
			ref := "HEAD"
			if branch == "" {
				out, err := repoGitOutput(dir, "symbolic-ref", "--quiet", "HEAD")
				if err != nil {
					return fmt.Errorf("to new: repo %q requires a current local branch or an explicit --base %s=branch: %w", repo, repo, err)
				}
				branch = strings.TrimSuffix(string(out), "\n")
			} else {
				ref = "refs/heads/" + strings.TrimPrefix(branch, "refs/heads/")
			}
			if strings.HasPrefix(branch, "refs/") && !strings.HasPrefix(branch, "refs/heads/") {
				return fmt.Errorf("to new: repo %q base %q must name a local branch", repo, branch)
			}
			branch = strings.TrimPrefix(branch, "refs/heads/")
			if branch == "" || branch == "HEAD" || strings.HasPrefix(branch, "-") {
				return fmt.Errorf("to new: repo %q base %q must name a local branch", repo, branch)
			}
			if _, err := repoGitOutput(dir, "check-ref-format", "refs/heads/"+branch); err != nil {
				return fmt.Errorf("to new: repo %q base %q must name a local branch: %w", repo, branch, err)
			}
			// Verify the exact ref before revision parsing, which can otherwise
			// resolve a similarly named tag through Git's ref-name guessing.
			if _, err := repoGitOutput(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err != nil {
				return fmt.Errorf("to new: repo %q base %q must be a known local branch: %w", repo, branch, err)
			}
			commit, err := repoGitOutput(dir, "rev-parse", "--verify", "--end-of-options", ref+"^{commit}")
			if err != nil {
				return fmt.Errorf("to new: repo %q base %q must be a known local branch with a commit: %w", repo, branch, err)
			}
			base.BaseRef = strings.TrimSpace(string(commit))
			base.BaseBranch = branch
		}
		st.RepoBases[repo] = base
	}
	if err := st.Validate(); err != nil {
		return err
	}
	if err := validateWorkflowDir(root, changeDir(root, name)); err != nil {
		return fmt.Errorf("to new: unsafe workflow path: %w", err)
	}
	// The lock is the first workflow-state write. Record its owner first so a
	// crash cannot leave custom-root state without a migration marker.
	if err := workcli.MarkWorkflowState(root); err != nil {
		return fmt.Errorf("to new: recording workflow root: %w", err)
	}
	nameUnlock, err := workcli.LockChangeNames(root)
	if err != nil {
		return fmt.Errorf("to new: %w", err)
	}
	defer nameUnlock()
	unlock, err := lock(root)
	if err != nil {
		return err
	}
	defer unlock()

	// Hold the lifecycle lock across the registry check and record creation.
	// Archived names are reusable only after their retained bindings are removed.
	if l.ExplicitRepos() {
		if err := workspace.EnsureNameAvailable(l, "to", name); err != nil {
			return fmt.Errorf("to new: %w", err)
		}
	}
	if _, err := os.Stat(changeDir(root, name)); err == nil {
		return fmt.Errorf("to new: change %q already exists at %s", name, changeDir(root, name))
	}
	// Global-name uniqueness (ADR 0042): the same active name in the onto
	// workflow is resolved by demotion, not duplication.
	if sib, err := workcli.SiblingChangeDir(root, "changes", name); err == nil {
		if _, serr := os.Lstat(sib); serr == nil {
			return fmt.Errorf("to new: change %q is already active in the onto workflow (%s); demote it (`onto demote %s --yes`) or pick another name", name, sib, name)
		}
	}

	if err := os.MkdirAll(changeDir(root, name), 0o755); err != nil {
		return fmt.Errorf("to new: creating %s: %w", changeDir(root, name), err)
	}

	if err := tostate.Save(statePath(root, name), st); err != nil {
		return fmt.Errorf("to new: %w", err)
	}
	if err := os.WriteFile(planPath(root, name), []byte{}, 0o644); err != nil {
		return fmt.Errorf("to new: creating %s: %w", planPath(root, name), err)
	}
	if jsonMode {
		return printJSON(cmd, map[string]any{
			"change": name,
			"phase":  st.Phase,
			"dir":    changeDir(root, name),
			"files":  []string{statePath(root, name), planPath(root, name)},
		})
	}
	cmd.Printf("created change %q at %s\n", name, changeDir(root, name))
	cmd.Printf("  %s\n", statePath(root, name))
	cmd.Printf("  %s\n", planPath(root, name))
	return nil
}
