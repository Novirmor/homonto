package ontocli

import (
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// abandonCmd builds the "onto abandon <change>" subcommand: it marks a change as
// the unsuccessful terminal state (cancelled without completing), distinct from
// the successful close/archived terminal. It enforces ontoFramework.Gate(dir)
// and a valid change name, and writes nothing unless every precondition below
// passes.
func abandonCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "abandon <change>",
		Short: "Mark a change abandoned (the unsuccessful terminal state)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAbandon(cmd, dir, args[0])
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// runAbandon enforces, in order: ontoFramework.Gate(root);
// ontoFramework.ValidChangeName(name); that
// docs/changes/<name>/onto-state.yaml loads; that the change is not already
// archived (a completed change is not abandonable). It then sets Abandoned=true
// and saves. It is idempotent: abandoning an already-abandoned change succeeds
// and rewrites the same terminal state.
func runAbandon(cmd *cobra.Command, root, name string) error {
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}
	if err := ontoFramework.ValidChangeName(name); err != nil {
		return err
	}

	unlockWs, err := lockOnto(root)
	if err != nil {
		return err
	}
	defer unlockWs()

	statePath := filepath.Join(changesDir(root), name, "onto-state.yaml")
	st, err := ontostate.Load(statePath)
	if err != nil {
		return fmt.Errorf("onto abandon: loading %s: %w", statePath, err)
	}

	if st.Archived {
		return fmt.Errorf("onto abandon: %q is already archived; a completed change is not abandonable", name)
	}

	if st.Abandoned {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: already abandoned\n", name)
		return nil
	}

	st.Abandoned = true
	if err := ontostate.Save(statePath, st); err != nil {
		return fmt.Errorf("onto abandon: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s: abandoned (was at phase %s)\n", name, st.Phase)
	return nil
}
