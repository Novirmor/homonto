package cli

import (
	"fmt"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/spf13/cobra"
)

func finalSkepticCmd() *cobra.Command {
	return &cobra.Command{
		Use: "final-skeptic [to|onto|no]", Short: "Print the final skeptic policy", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			engine := "to"
			if len(args) != 0 {
				engine = args[0]
			}
			if engine != "to" && engine != "onto" && engine != "no" {
				return fmt.Errorf("final-skeptic: unknown engine %q", engine)
			}
			path, _ := cmd.Flags().GetString("config")
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			cmd.Println(cfg.Workflow.FinalSkepticFor(engine))
			return nil
		},
	}
}
