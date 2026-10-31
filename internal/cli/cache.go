package cli

import (
	"os"

	"github.com/Novirmor/homonto/internal/engine"
	"github.com/spf13/cobra"
)

// cacheCmd groups content-addressed remote-cache maintenance commands.
func cacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Maintain homonto's content-addressed remote cache",
	}
	cmd.AddCommand(cacheGCCmd())
	return cmd
}

// cacheGCCmd reclaims cache entries no remote lock references. It is an explicit
// maintenance operation (apply deliberately does not GC, so a config revert can
// still roll back from a warm cache).
func cacheGCCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Reclaim remote cache entries that no remote lock references",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			home, _ := os.UserHomeDir()
			e, err := engine.Build(cmd.Context(), cfgPath, home, "homonto")
			if err != nil {
				return err
			}
			e.HomontoVersion = Version
			reclaimed, err := e.GCRemoteCache(dryRun)
			if err != nil {
				return err
			}
			verb := "reclaimed"
			if dryRun {
				verb = "would reclaim"
			}
			for _, d := range reclaimed {
				cmd.Printf("%s %s\n", verb, d.String())
			}
			cmd.Printf("cache gc: %s %d unreferenced entr%s\n", verb, len(reclaimed), pluralY(len(reclaimed)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be reclaimed without deleting")
	return cmd
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
