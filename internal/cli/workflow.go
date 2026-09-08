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
	cmd.AddCommand(workflowSnapshotCmd())
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
			cmd.Println(string(data))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the stable machine-readable snapshot")
	return cmd
}
