// Package tocli implements the `to` binary: the minimal coding framework's
// bookkeeper. It is the sole writer of to-state.yaml, it is git-blind, and
// it enforces no evidence gates — `done --verified` is a self-asserted
// checkbox, not a guarantee. Rigor lives in the to-* skills, not here.
package tocli

import (
	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/spf13/cobra"
)

// Version is the to build version. It is initialized to buildinfo.DevVersion
// (the single source of the dev literal shared by every homonto binary) and a
// constant-initialized string so the linker's -X stamp takes effect; when
// unstamped (e.g. `go install ...@tag`, which applies no ldflags) buildinfo.
// Resolve recovers the module version.
var Version = buildinfo.DevVersion

// NewRootCmd builds the root cobra command and registers subcommands.
func NewRootCmd() *cobra.Command {
	version := buildinfo.Resolve(Version, buildinfo.DevVersion)
	root := &cobra.Command{
		Use:           "to",
		Short:         "Minimal coding-framework bookkeeper",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the to version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("to %s\n", version)
			return nil
		},
	})
	root.AddCommand(initCmd())
	root.AddCommand(newCmd())
	root.AddCommand(statusCmd())
	root.AddCommand(phaseCmd())
	root.AddCommand(doneCmd())
	root.AddCommand(abandonCmd())
	root.AddCommand(handoffCmd())
	root.AddCommand(doctorCmd())
	root.AddCommand(promoteCmd())
	return root
}
