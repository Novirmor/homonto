package cli

import (
	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/spf13/cobra"
)

// Version is the homonto build version. It is initialized to buildinfo.
// DevVersion (the single source of the dev literal shared by every homonto
// binary) and is a constant-initialized string so the linker's -X stamp takes
// effect; when unstamped (e.g. `go install ...@tag`, which applies no ldflags)
// buildinfo.Resolve recovers the module version.
var Version = buildinfo.DevVersion

// NewRootCmd builds the root cobra command and registers subcommands.
func NewRootCmd() *cobra.Command {
	version := buildinfo.Resolve(Version, buildinfo.DevVersion)
	root := &cobra.Command{
		Use:           "homonto",
		Short:         "Declarative config for AI coding tools",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "homonto.toml", "path to homonto config")

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the homonto version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("homonto %s\n", version)
			return nil
		},
	})
	root.AddCommand(planCmd(), applyCmd(), updateCmd(), statusCmd(), doctorCmd(), initCmd(), cacheCmd())
	return root
}
