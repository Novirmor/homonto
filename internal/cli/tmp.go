package cli

import (
	"encoding/json"

	"github.com/noviopenworks/homonto/internal/workflowcleanup"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

func tmpCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "tmp", Short: "Manage the declared [tmp] scratch directory"}
	var yes, jsonMode bool
	clean := &cobra.Command{
		Use: "clean", Short: "Remove scratch that belongs to no active change (preview without --yes)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := cmd.Flags().GetString("config")
			if err != nil {
				return err
			}
			l, err := workspace.Load(path)
			if err != nil {
				return err
			}
			r, err := workflowcleanup.TmpClean(l, yes)
			if err != nil {
				return err
			}
			if jsonMode {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(r)
			}
			verb := "would remove"
			if yes {
				verb = "removed"
			}
			for _, rel := range r.Removed {
				cmd.Printf("%s %s\n", verb, rel)
			}
			for _, rel := range r.Kept {
				cmd.Printf("kept %s (active change)\n", rel)
			}
			if len(r.Removed) == 0 {
				cmd.Println("Nothing to clean.")
			} else if !yes {
				cmd.Println("Run with --yes to remove.")
			}
			return nil
		},
	}
	clean.Flags().BoolVar(&yes, "yes", false, "remove the listed scratch")
	clean.Flags().BoolVar(&jsonMode, "json", false, "emit schema-versioned JSON")
	cmd.AddCommand(clean)
	return cmd
}
