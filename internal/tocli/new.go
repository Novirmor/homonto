package tocli

import (
	"fmt"
	"os"

	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
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
	)

	cmd := &cobra.Command{
		Use:   "new <change-name>",
		Short: "Create a new change (phase plan), if the to framework is installed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNewWithRepos(cmd, dir, args[0], jsonMode, repos)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to create the change in")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	cmd.Flags().StringSliceVar(&repos, "repo", nil, "declared repository to include (repeatable)")
	return cmd
}

func runNew(cmd *cobra.Command, root, name string, jsonMode bool) error {
	return runNewWithRepos(cmd, root, name, jsonMode, nil)
}

// runNewWithRepos records a selected cross-repo scope without creating any
// workflow files outside the config repository.
func runNewWithRepos(cmd *cobra.Command, root, name string, jsonMode bool, repos []string) error {
	if err := toFramework.Gate(root); err != nil {
		return err
	}
	if err := toFramework.ValidChangeName(name); err != nil {
		return err
	}
	names, dirs, err := scopeDirs(root, repos)
	if err != nil {
		return fmt.Errorf("to new: %w", err)
	}
	if len(names) > 0 {
		if _, err := worktreeDirty(root); err != nil {
			return fmt.Errorf("to new: %w", err)
		}
		for _, repo := range names {
			if _, err := worktreeDirty(dirs[repo]); err != nil {
				return fmt.Errorf("to new: declared repo %q is not a usable git worktree", repo)
			}
		}
	}
	if err := workcli.ValidateWorkflowPath(root, changeDir(root, name)); err != nil {
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

	// Only an ACTIVE change blocks the name: archive dirs are date-prefixed,
	// so a finished change frees its name for reuse (recurring chores).
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

	st := tostate.State{
		Change:  name,
		Phase:   tostate.PhasePlan,
		Created: todayFn(),
		Repos:   names,
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
