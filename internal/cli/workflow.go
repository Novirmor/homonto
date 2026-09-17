package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/workflowstatus"
	"github.com/spf13/cobra"
)

func workflowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workflow",
		Short: "Read workflow progress without changing it",
	}
	cmd.AddCommand(workflowSnapshotCmd(), workflowHandoffCmd())
	return cmd
}

func workflowHandoffCmd() *cobra.Command {
	var workflow, change, identity string
	var jsonMode bool
	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Read bounded recovery context for an exact workflow generation",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !jsonMode {
				return fmt.Errorf("workflow handoff: pass --json for machine-readable recovery context")
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			handoff, err := workflowstatus.ReadHandoff(cfgPath, workflow, change, identity)
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(handoff, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return err
		},
	}
	cmd.Flags().StringVar(&workflow, "workflow", "", "workflow: onto or to")
	cmd.Flags().StringVar(&change, "change", "", "exact change name")
	cmd.Flags().StringVar(&identity, "identity", "", "generation identity from workflow snapshot")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit structured recovery context")
	return cmd
}

func workflowSnapshotCmd() *cobra.Command {
	var jsonMode bool
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Print onto and to progress and terminal records (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !jsonMode {
				return fmt.Errorf("workflow snapshot: pass --json for the machine-readable snapshot")
			}
			cfgPath, _ := cmd.Flags().GetString("config")
			configPath, err := filepath.Abs(cfgPath)
			if err != nil {
				return err
			}
			data, err := json.MarshalIndent(workflowstatus.ReadConfig(configPath), "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return err
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the stable machine-readable snapshot")
	return cmd
}
