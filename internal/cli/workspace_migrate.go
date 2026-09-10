package cli

import (
	"encoding/json"
	"fmt"

	"github.com/noviopenworks/homonto/internal/workspacemigration"
	"github.com/spf13/cobra"
)

func workspaceMigrateCmd() *cobra.Command {
	migrate := &cobra.Command{
		Use:   "migrate",
		Short: "Read-only planning for the supported legacy records migration",
		Args:  cobra.NoArgs,
	}
	var manifest string
	var jsonMode bool
	plan := &cobra.Command{
		Use:   "plan",
		Short: "Inventory the legacy records migration without writing workspace state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !jsonMode {
				return fmt.Errorf("workspace migrate plan requires --json")
			}
			configPath, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			result, err := workspacemigration.Build(configPath, manifest)
			if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(result); encodeErr != nil {
				return encodeErr
			}
			if err != nil {
				return fmt.Errorf("workspace migration plan blocked; inspect JSON output")
			}
			return nil
		},
	}
	plan.Flags().StringVar(&manifest, "manifest", "", "path to the explicit migration source manifest")
	plan.Flags().BoolVar(&jsonMode, "json", false, "emit the deterministic migration plan as JSON")
	_ = plan.MarkFlagRequired("manifest")
	migrate.AddCommand(plan)
	return migrate
}
