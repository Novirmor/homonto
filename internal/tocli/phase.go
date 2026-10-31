package tocli

import (
	"fmt"

	"github.com/Novirmor/homonto/internal/tostate"
	"github.com/spf13/cobra"
)

// phaseCmd builds "to phase <change-name>": the one forward transition the
// tracker has, plan → do. do has no forward `phase` transition — finishing
// is `to done` (and its self-asserted --verified), and terminal phases are
// terminal.
func phaseCmd() *cobra.Command {
	var (
		dir      string
		jsonMode bool
	)

	cmd := &cobra.Command{
		Use:   "phase <change-name>",
		Short: "Advance a change one phase forward (plan → do)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPhase(cmd, dir, args[0], jsonMode)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	return cmd
}

func runPhase(cmd *cobra.Command, root, name string, jsonMode bool) error {
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

	switch st.Phase {
	case tostate.PhasePlan:
		// fall through to the one legal advance below
	case tostate.PhaseDo:
		return fmt.Errorf("to phase: change %q is already at do; finish it with `to done %s --verified` or exit with `to abandon %s`", name, name, name)
	default:
		return fmt.Errorf("to phase: change %q is %s, which is terminal", name, st.Phase)
	}

	from := st.Phase
	st.Phase = tostate.PhaseDo
	if err := tostate.Save(statePath(root, name), st); err != nil {
		return fmt.Errorf("to phase: %w", err)
	}

	if jsonMode {
		return printJSON(cmd, map[string]string{"change": name, "from": from, "to": st.Phase})
	}
	cmd.Printf("change %q advanced %s → %s\n", name, from, st.Phase)
	return nil
}
