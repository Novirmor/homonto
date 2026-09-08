package tocli

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/spf13/cobra"
)

// abandonCmd builds "to abandon <change-name>": the terminal exit without
// done. It works from any non-terminal phase and archives the change so the
// active listing stays clean. Re-running it on an abandoned-but-active change
// (a crash between the state write and the rename) completes the archive.
func abandonCmd() *cobra.Command {
	var (
		dir      string
		jsonMode bool
	)

	cmd := &cobra.Command{
		Use:   "abandon <change-name>",
		Short: "Abandon a change (terminal) and archive it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAbandon(cmd, dir, args[0], jsonMode)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	return cmd
}

func runAbandon(cmd *cobra.Command, root, name string, jsonMode bool) error {
	if err := toFramework.Gate(root); err != nil {
		return err
	}
	unlock, err := lock(root)
	if err != nil {
		return err
	}
	defer unlock()

	st, err := loadChange(root, name)
	if err != nil {
		return err
	}

	completed := false
	switch st.Phase {
	case tostate.PhaseAbandoned:
		completed = true
	case tostate.PhaseDone:
		return fmt.Errorf("to abandon: change %q is done; re-run `to done %s --verified` to complete its archive", name, name)
	default:
		// any non-terminal phase may abandon
	}

	var dest string
	if completed {
		dest, err = completeArchive(cmd.Context(), root, st)
	} else {
		st.Phase = tostate.PhaseAbandoned
		st.Finished = todayFn()
		dest, err = finishAndArchive(cmd.Context(), root, st)
	}
	if err != nil {
		return fmt.Errorf("to abandon: %w", err)
	}

	if jsonMode {
		return printJSON(cmd, map[string]any{"change": name, "phase": tostate.PhaseAbandoned, "archived": dest})
	}
	if completed {
		cmd.Printf("change %q was already abandoned; completed the archive at %s\n", name, dest)
	} else {
		cmd.Printf("change %q abandoned, archived at %s\n", name, dest)
	}
	return nil
}
