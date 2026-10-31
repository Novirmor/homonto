package cli

import (
	"github.com/Novirmor/homonto/internal/scaffold"
	"github.com/spf13/cobra"
)

func initCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [dir]",
		Short: "Scaffold a new homonto repo",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			created, updated, err := scaffold.Init(dir)
			if err != nil {
				return err
			}
			for _, p := range created {
				cmd.Println("created", p)
			}
			for _, p := range updated {
				cmd.Println("updated", p)
			}
			return nil
		},
	}
}
