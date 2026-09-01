package tocli

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/spf13/cobra"
)

// doneCmd builds "to done <change-name> --verified": mark a change done and
// archive it. --verified is REQUIRED but self-asserted — the binary records
// the checkbox, it does not observe evidence. --evidence records an optional
// text (the literal verify command and outcome) verbatim alongside it, making
// a real verification distinguishable from a skipped one in the archive; it
// is still never checked. Real verification rigor lives in the to-done skill.
//
// Re-running done on a change whose state is already done but which never
// made it into the archive (a crash between the state write and the rename)
// completes the archive move instead of refusing.
func doneCmd() *cobra.Command {
	var (
		dir      string
		jsonMode bool
		verified bool
		evidence string
	)

	cmd := &cobra.Command{
		Use:   "done <change-name> --verified",
		Short: "Mark a change done and archive it (requires the self-asserted --verified)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDone(cmd, dir, args[0], verified, evidence, jsonMode)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	cmd.Flags().BoolVar(&verified, "verified", false, "assert that the change was verified (recorded verbatim, not checked)")
	cmd.Flags().StringVar(&evidence, "evidence", "", "what was verified and how (recorded verbatim, not checked)")
	return cmd
}

func runDone(cmd *cobra.Command, root, name string, verified bool, evidence string, jsonMode bool) error {
	if err := toFramework.Gate(root); err != nil {
		return err
	}
	if !verified {
		return fmt.Errorf("to done: --verified is required; verify the change first (the to-done skill), then assert it")
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
	case tostate.PhaseDo:
		// the one phase done may leave from
	case tostate.PhaseDone:
		// A crash between the terminal state write and the archive rename left
		// this change done-but-active; converge by completing the move.
		completed = true
	case tostate.PhasePlan:
		return fmt.Errorf("to done: change %q is still at plan; advance it with `to phase %s` first", name, name)
	default:
		return fmt.Errorf("to done: change %q is %s, which is terminal", name, st.Phase)
	}
	// Cross-repo changes are terminal only when every selected worktree is
	// determinably clean. The config repo is implicit; unscoped legacy changes
	// preserve to's historical git-blind completion behavior.
	if err := requireCleanScope(root, st.Repos); err != nil {
		return fmt.Errorf("to done: cannot finish cross-repo change %q: %w", name, err)
	}

	var dest string
	if completed {
		dest, err = completeArchive(root, st)
	} else {
		st.Phase = tostate.PhaseDone
		st.Verified = true
		st.Evidence = evidence
		st.Finished = todayFn()
		dest, err = finishAndArchive(root, st)
	}
	if err != nil {
		return fmt.Errorf("to done: %w", err)
	}

	if jsonMode {
		return printJSON(cmd, map[string]any{
			"change": name, "phase": tostate.PhaseDone, "verified": true, "archived": dest,
		})
	}
	if completed {
		cmd.Printf("change %q was already done; completed the archive at %s\n", name, dest)
	} else {
		cmd.Printf("change %q done, archived at %s\n", name, dest)
	}
	return nil
}
