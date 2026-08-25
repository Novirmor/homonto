package cli

import (
	"strings"

	"github.com/noviopenworks/homonto/internal/app"

	"github.com/noviopenworks/homonto/internal/task"
	"github.com/spf13/cobra"
)

// taskCmd is the Task workflow's own command group: starting a task,
// seeing where one is, and stopping one. Everything a host does with an
// issued action lives under the protocol commands instead.
func taskCmd(opener Opener) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Start, inspect, and abandon tasks",
	}
	cmd.AddCommand(taskStartCmd(opener), taskStatusCmd(opener), taskAbandonCmd(opener))
	return cmd
}

func taskStartCmd(opener Opener) *cobra.Command {
	var goal string
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Start a task",
		Long: "Start a task called <name>. The task's document is created at " +
			"active/<name>/tasks.md in the control repository; run `homonto next` " +
			"to get the first assignments.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			st, err := a.StartTask(cmd.Context(), task.StartInput{Name: args[0], Goal: goal})
			if err != nil {
				return err
			}
			cmd.Printf("started task %s (%s) at %s\n", st.Name, st.WorkID, st.Step)
			return nil
		},
	}
	cmd.Flags().StringVar(&goal, "goal", "", "the task's outcome statement")
	return cmd
}

func taskStatusCmd(opener Opener) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [name-or-id]",
		Short: "Show where a task is",
		Long: "Show a task's step and the fingerprints it rests on. With no " +
			"argument, shows every task. The step is reconciled against the " +
			"world first, so what is printed is where the task actually is.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			states, err := statusStates(cmd, a, args)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd, states)
			}
			if len(states) == 0 {
				cmd.Println("no tasks")
				return nil
			}
			for _, st := range states {
				cmd.Printf("%s  %s  %s  generation %d\n", st.WorkID, st.Name, st.Step, st.Generation)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine-readable form")
	return cmd
}

// statusStates resolves which tasks status should report on, reconciling
// each one so a reported step is never a stale one.
func statusStates(cmd *cobra.Command, a *app.App, args []string) ([]task.State, error) {
	if len(args) == 0 {
		all, err := a.Tasks(cmd.Context())
		if err != nil {
			return nil, err
		}
		out := make([]task.State, 0, len(all))
		for _, st := range all {
			reconciled, _, err := a.Reconcile(cmd.Context(), st.WorkID)
			if err != nil {
				return nil, err
			}
			out = append(out, reconciled)
		}
		return out, nil
	}
	id, err := a.ResolveWork(cmd.Context(), args[0])
	if err != nil {
		return nil, err
	}
	st, invalidations, err := a.Reconcile(cmd.Context(), id)
	if err != nil {
		return nil, err
	}
	for _, inv := range invalidations {
		cmd.PrintErrf("returned to %s: %s (%s)\n", inv.ReturnTo, inv.Detail, strings.Join(inv.Evidence, ", "))
	}
	return []task.State{st}, nil
}

func taskAbandonCmd(opener Opener) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "abandon [name-or-id]",
		Short: "Stop a task",
		Long: "Stop a task. Its isolation areas, branches, and evidence are left " +
			"exactly where they are: abandoning is a decision to stop working, " +
			"not an instruction to destroy the work.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			id, err := a.ResolveWork(cmd.Context(), selector)
			if err != nil {
				return err
			}
			st, err := a.AbandonTask(cmd.Context(), id)
			if err != nil {
				return err
			}
			cmd.Printf("abandoned task %s (%s)\n", st.Name, st.WorkID)
			cmd.Printf("its isolation areas and evidence were left in place for external handling\n")
			return nil
		},
	}
	return cmd
}
