package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/engine"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/spf13/cobra"
)

// snapshotCmd is "homonto snapshot": undo and recover for the opt-in
// transactional apply surface (ADR 0030). apply --snapshot lives on the apply
// command; this group holds the reverse operations.
func snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Manage opt-in apply snapshots (undo, recover)",
	}
	cmd.AddCommand(snapshotUndoCmd(), snapshotRecoverCmd(), snapshotListCmd())
	return cmd
}

func snapshotUndoCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "undo <apply-id>",
		Short: "Reverse a committed snapshot apply (refuses over user edits)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			home, _ := os.UserHomeDir()
			e, err := engine.Build(cmd.Context(), cfgPath, home, "homonto")
			if err != nil {
				return err
			}
			lock, err := applylock.AcquireProcess(e.StateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(), "undo apply %s? [y/N] ", args[0])
				r := bufio.NewReader(os.Stdin)
				line, _ := r.ReadString('\n')
				if strings.ToLower(strings.TrimSpace(line)) != "y" {
					cmd.Println("Aborted.")
					return nil
				}
			}
			if err := e.UndoSnapshot(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "undo: apply %s reversed; state restored to before the snapshot\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "skip confirmation")
	return cmd
}

func snapshotRecoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover <apply-id>",
		Short: "Finish an interrupted snapshot apply by rolling it back",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			home, _ := os.UserHomeDir()
			e, err := engine.Build(cmd.Context(), cfgPath, home, "homonto")
			if err != nil {
				return err
			}
			// The process lock: a killed apply no longer holds it, so recovery
			// can always start (ADR 0030).
			lock, err := applylock.AcquireProcess(e.StateDir)
			if err != nil {
				return err
			}
			defer lock.Release()
			if err := e.RecoverSnapshot(args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "recover: apply %s rolled back; run `homonto plan` to converge\n", args[0])
			return nil
		},
	}
	return cmd
}

func snapshotListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List snapshot applies (committed and incomplete)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			home, _ := os.UserHomeDir()
			e, err := engine.Build(cmd.Context(), cfgPath, home, "homonto")
			if err != nil {
				return err
			}
			// List everything; the status marks incomplete ones for recovery.
			all, err := snapshotListAll(e)
			if err != nil {
				return err
			}
			if len(all) == 0 {
				cmd.Println("no snapshots")
				return nil
			}
			for _, s := range all {
				mark := string(s.Status)
				if s.Status == snapshot.StatusPrepared {
					mark = "INCOMPLETE — `homonto snapshot recover " + s.ID + "`"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-10s %s\n", s.ID, mark)
			}
			return nil
		},
	}
	return cmd
}

// snapshotListAll returns every apply ID with its status.
func snapshotListAll(e *engine.Engine) ([]struct {
	ID     string
	Status snapshot.Status
}, error) {
	ids, err := snapshot.List(e.StateDir)
	if err != nil {
		return nil, err
	}
	var out []struct {
		ID     string
		Status snapshot.Status
	}
	for _, id := range ids {
		j, ok, err := snapshot.Load(e.StateDir, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, struct {
				ID     string
				Status snapshot.Status
			}{ID: id, Status: j.Status})
		}
	}
	return out, nil
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
