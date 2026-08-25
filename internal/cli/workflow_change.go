package cli

import (
	"strings"

	"github.com/noviopenworks/homonto/internal/app"
	"github.com/noviopenworks/homonto/internal/change"
	"github.com/spf13/cobra"
)

// changeCmd is the Change workflow's own command group. Everything a host
// does with an issued action lives under the protocol commands instead,
// which serve both workflows.
func changeCmd(opener Opener) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change",
		Short: "Start, inspect, and abandon changes",
	}
	cmd.AddCommand(changeStartCmd(opener), changeStatusCmd(opener), changeAbandonCmd(opener))
	return cmd
}

func changeStartCmd(opener Opener) *cobra.Command {
	var request string
	cmd := &cobra.Command{
		Use:   "start <name>",
		Short: "Open a change classification candidate",
		Long: "Start classifying a change called <name>. This does NOT create a " +
			"change: it opens a local candidate, dispatches read-only explorers " +
			"and a skeptic, and then asks a human to confirm fix, tweak, or full. " +
			"Nothing is written under docs/homonto/changes until that confirmation, " +
			"so abandoning a candidate leaves the repository exactly as it was.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			st, resp, err := a.StartChangePreflight(cmd.Context(), change.PreflightInput{
				Name: args[0], Request: request,
			})
			if err != nil {
				return err
			}
			cmd.Printf("classifying %s (%s)\n", st.Name, st.WorkID)
			cmd.Printf("nothing is created until you confirm the path\n")
			return renderNext(cmd, resp)
		},
	}
	cmd.Flags().StringVar(&request, "request", "", "what the change should do")
	_ = cmd.MarkFlagRequired("request")
	return cmd
}

func changeStatusCmd(opener Opener) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status [name-or-id]",
		Short: "Show where a change is",
		Long: "Show a change's path and step. With no argument, shows every " +
			"change and every open classification candidate. A confirmed change " +
			"is reconciled against the world first, so what is printed is where " +
			"it actually is.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			states, err := changeStates(cmd, a, args)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd, states)
			}
			if len(states) == 0 {
				cmd.Println("no changes")
				return nil
			}
			for _, st := range states {
				cmd.Printf("%s  %s  %s  %s  generation %d\n",
					st.WorkID, st.Name, st.Path, st.Step, st.Generation)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine-readable form")
	return cmd
}

// changeStates resolves which changes status reports on, reconciling each
// so a reported step is never a stale one.
func changeStates(cmd *cobra.Command, a *app.App, args []string) ([]change.State, error) {
	if len(args) == 0 {
		all, err := a.Changes(cmd.Context())
		if err != nil {
			return nil, err
		}
		out := make([]change.State, 0, len(all))
		for _, st := range all {
			reconciled, _, err := a.ReconcileChange(cmd.Context(), st.WorkID)
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
	st, invalidations, err := a.ReconcileChange(cmd.Context(), id)
	if err != nil {
		return nil, err
	}
	for _, inv := range invalidations {
		cmd.PrintErrf("returned to %s: %s (%s)\n",
			inv.ReturnTo, inv.Detail, strings.Join(inv.Evidence, ", "))
	}
	return []change.State{st}, nil
}

func changeAbandonCmd(opener Opener) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "abandon [name-or-id]",
		Short: "Stop a change or drop a classification candidate",
		Long: "Stop a change. Its isolation areas, branches, and evidence are left " +
			"exactly where they are: abandoning is a decision to stop working, " +
			"not an instruction to destroy the work. Dropping an unconfirmed " +
			"candidate removes nothing, because nothing was created.",
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
			kind, err := a.WorkKindOf(cmd.Context(), id)
			if err != nil {
				return err
			}
			if kind == app.WorkPreflight {
				pre, err := a.AbandonChangePreflight(cmd.Context(), id)
				if err != nil {
					return err
				}
				cmd.Printf("dropped the classification candidate %s (%s)\n", pre.Name, pre.WorkID)
				cmd.Printf("nothing had been created, so nothing was removed\n")
				return nil
			}
			st, err := a.AbandonChange(cmd.Context(), id)
			if err != nil {
				return err
			}
			cmd.Printf("abandoned change %s (%s)\n", st.Name, st.WorkID)
			cmd.Printf("its isolation areas and evidence were left in place for external handling\n")
			return nil
		},
	}
	return cmd
}
