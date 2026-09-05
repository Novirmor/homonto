package ontocli

import (
	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/spf13/cobra"
)

// Version is the onto build version. It is initialized to buildinfo.DevVersion
// (the single source of the dev literal shared by every homonto binary) and a
// constant-initialized string so the linker's -X stamp takes effect; when
// unstamped (e.g. `go install ...@tag`, which applies no ldflags) buildinfo.
// Resolve recovers the module version.
var Version = buildinfo.DevVersion

// NewRootCmd builds the root cobra command and registers subcommands.
func NewRootCmd() *cobra.Command {
	version := buildinfo.Resolve(Version, buildinfo.DevVersion)
	root := &cobra.Command{
		Use:           "onto",
		Short:         "Managed spec-driven workflow operator",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the onto version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("onto %s\n", version)
			return nil
		},
	})
	root.AddCommand(statusCmd())
	root.AddCommand(graphCmd())
	root.AddCommand(initCmd())
	root.AddCommand(newCmd())
	root.AddCommand(advanceCmd())
	root.AddCommand(bypassCmd())
	root.AddCommand(closeCmd())
	root.AddCommand(completeIntegrationCmd())
	root.AddCommand(abandonCmd())
	root.AddCommand(demoteCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(setCmd())
	root.AddCommand(stateCmd())
	root.AddCommand(gateCmd())
	root.AddCommand(dirtCmd())
	root.AddCommand(scaleCmd())
	root.AddCommand(mergeDeltasCmd())
	root.AddCommand(handoffCmd())
	root.AddCommand(evidenceCmd())
	root.AddCommand(traceCmd())
	return root
}
