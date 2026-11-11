package cli

import (
	"github.com/Novirmor/homonto/internal/config"
	"github.com/spf13/cobra"
)

func finalSkepticCmd() *cobra.Command {
	return &cobra.Command{
		Use: "final-skeptic", Short: "Print the to final skeptic policy", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			cmd.Println(cfg.Workflow.FinalSkepticOrDefault())
			return nil
		},
	}
}
