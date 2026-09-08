package ontocli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/destlock"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

// newCmd builds the "onto new <change-name>" subcommand: it enforces
// ontoFramework.Gate(dir) (the framework-install precondition) and
// ontoFramework.ValidChangeName before scaffolding a new change-workspace
// skeleton, and performs no writes at all if either check fails or the change
// directory already exists.
func newCmd() *cobra.Command {
	var (
		dir      string
		workflow string
		repos    []string
		bases    []string
	)

	cmd := &cobra.Command{
		Use:   "new <change-name>",
		Short: "Create a new change-workspace skeleton, if the onto framework is installed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNewWithBases(cmd, dir, args[0], workflow, repos, bases)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to create the change in")
	cmd.Flags().StringVar(&workflow, "workflow", "full", "workflow for the change: full, fix, or tweak")
	cmd.Flags().StringSliceVar(&repos, "repo", nil, "declared repository to include (repeatable; required with config schema 2)")
	cmd.Flags().StringArrayVar(&bases, "base", nil, "schema-2 source base alias=branch (repeat per repo; default: current branch and HEAD)")
	return cmd
}

// runNew enforces ontoFramework.Gate(root) then
// ontoFramework.ValidChangeName(name), refuses to clobber an existing
// docs/changes/<name> directory, and only then scaffolds onto-state.yaml plus an
// empty proposal.md (and, for the fix/tweak presets, tasks.md — full derives its
// task list in design). Each file is written only if absent. It reports the
// created change and its files.
func runNew(cmd *cobra.Command, root, name, workflow string) error {
	return runNewWithRepos(cmd, root, name, workflow, nil)
}

// runNewWithRepos records the selected declared-repo aliases with a new
// change. Names are validated before any scaffold write; the config repository
// is implicit and therefore never accepted as an alias.
func runNewWithRepos(cmd *cobra.Command, root, name, workflow string, repos []string) error {
	return runNewWithBases(cmd, root, name, workflow, repos, nil)
}

func runNewWithBases(cmd *cobra.Command, root, name, workflow string, repos, bases []string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}

	if err := ontoFramework.ValidChangeName(name); err != nil {
		return err
	}

	if !ontostate.ValidWorkflow(workflow) {
		return fmt.Errorf("onto new: workflow %q is not one of full|fix|tweak", workflow)
	}
	names, _, err := scopeDirs(root, repos)
	if err != nil {
		return fmt.Errorf("onto new: %w", err)
	}
	st := ontostate.State{
		Change: name, ID: ontostate.NewID(), Workflow: workflow,
		Phase: "open", Created: time.Now().Format("2006-01-02"), Repos: names,
	}
	layout, err := workspace.LoadScopeRoot(root, names)
	if err != nil {
		return err
	}
	if len(bases) > 0 && !layout.ExplicitRepos() {
		return fmt.Errorf("onto new: --base requires config schema 2")
	}
	if layout.ExplicitRepos() {
		if len(names) == 0 {
			return fmt.Errorf("onto new: config schema 2 requires nonempty selected --repo")
		}
		selected := map[string]bool{}
		for _, alias := range names {
			selected[alias] = true
		}
		overrides := map[string]string{}
		for _, value := range bases {
			alias, branch, ok := strings.Cut(value, "=")
			if !ok || !selected[alias] || overrides[alias] != "" || branch == "" {
				return fmt.Errorf("onto new: --base %q requires a unique selected alias=branch", value)
			}
			if strings.HasPrefix(branch, "refs/") && !strings.HasPrefix(branch, "refs/heads/") {
				return fmt.Errorf("onto new: --base %q must name a literal local branch", value)
			}
			branch = strings.TrimPrefix(branch, "refs/heads/")
			if err := validateBranchName(layout.Repos[alias], branch); err != nil {
				return fmt.Errorf("onto new: --base %q: %w", value, err)
			}
			overrides[alias] = branch
		}
		st.RepoMode = "explicit"
		st.RepoBases = make(map[string]ontostate.RepoBase, len(names))
		for _, alias := range names {
			source := layout.Repos[alias]
			branch := overrides[alias]
			ref := "HEAD"
			if branch == "" {
				branch, err = currentBranch(source)
				if err != nil {
					return fmt.Errorf("repository %s: %w", alias, err)
				}
			} else {
				ref = "refs/heads/" + branch
			}
			head, err := resolveCommit(source, ref)
			if err != nil {
				return fmt.Errorf("repository %s: %w", alias, err)
			}
			identity, err := sourceIdentity(source)
			if err != nil {
				return err
			}
			st.RepoBases[alias] = ontostate.RepoBase{BaseRef: head, BaseBranch: branch, GitCommonDir: identity}
		}
		if err := st.Validate(); err != nil {
			return err
		}
		if err := workspace.EnsureNameAvailable(layout, "onto", name); err != nil {
			return fmt.Errorf("onto new: %w", err)
		}
	}
	changeDir := filepath.Join(changesDir(root), name)
	if err := workcli.ValidateWorkflowPath(root, changeDir); err != nil {
		return fmt.Errorf("onto new: unsafe workflow path: %w", err)
	}
	// Record the root before the destination lock creates workflow state. A
	// crash later in scaffolding must still leave the state owner discoverable.
	if err := workcli.MarkWorkflowState(root); err != nil {
		return fmt.Errorf("onto new: recording workflow root: %w", err)
	}
	nameUnlock, err := workcli.LockChangeNames(root)
	if err != nil {
		return fmt.Errorf("onto new: %w", err)
	}
	defer nameUnlock()

	// Destination reservation shared with `to promote` (ADR 0028): both
	// create under docs/changes, and a promotion renaming its result into
	// place must not race a concurrent create of the same name. Held across
	// the existence check and scaffold.
	unlock, err := destlock.Acquire(root)
	if err != nil {
		return fmt.Errorf("onto new: %w", err)
	}
	defer unlock()

	if _, err := os.Stat(changeDir); err == nil {
		return fmt.Errorf("onto new: change %q already exists at %s", name, changeDir)
	}
	// Global-name uniqueness (ADR 0042): the same active name in the `to`
	// workflow is resolved by promotion, not duplication.
	if sib, err := workcli.SiblingChangeDir(root, "tasks", name); err == nil {
		if _, serr := os.Lstat(sib); serr == nil {
			return fmt.Errorf("onto new: change %q is already active in the `to` workflow (%s); promote it (`to promote %s --yes`) or pick another name", name, sib, name)
		}
	}
	if archiveDir, archivedState, err := locateArchive(root, name); err == nil {
		if !archivedState.Archived || !ontostate.ArchiveIntegrationComplete(archiveDir, archivedState) {
			return fmt.Errorf("onto new: latest archive for %q has not completed integration", name)
		}
	} else if !errors.Is(err, errArchiveNotFound) {
		return fmt.Errorf("onto new: cannot validate latest archive for %q: %w", name, err)
	}

	if err := os.MkdirAll(changeDir, 0o755); err != nil {
		return fmt.Errorf("onto new: creating %s: %w", changeDir, err)
	}

	statePath := filepath.Join(changeDir, "onto-state.yaml")
	if err := ontostate.Save(statePath, st); err != nil {
		return fmt.Errorf("onto new: %w", err)
	}

	// Scaffold the open-phase skeleton. A full change writes its task list from
	// the confirmed design (onto-design creates tasks.md), so `new` only lays down
	// proposal.md; the fix/tweak presets skip design and decompose at open-lite,
	// so they also get tasks.md now. This matches RequiredArtifacts(open, …).
	files := []string{"proposal.md"}
	if workflow == "fix" || workflow == "tweak" {
		files = append(files, "tasks.md")
	}
	for _, f := range files {
		path := filepath.Join(changeDir, f)
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
			return fmt.Errorf("onto new: creating %s: %w", path, err)
		}
	}
	cmd.Printf("created change %q at %s\n", name, changeDir)
	cmd.Printf("  %s\n", statePath)
	for _, f := range files {
		cmd.Printf("  %s\n", filepath.Join(changeDir, f))
	}

	return nil
}
