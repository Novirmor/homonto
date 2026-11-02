package cli

import (
	"github.com/Novirmor/homonto/internal/buildinfo"
	"github.com/Novirmor/homonto/internal/hostruntime"
	"github.com/spf13/cobra"
)

// runtimeCmd serves the host-neutral draft/publication core to a harness
// shim over stdio (ADR 0090). The OpenCode workflow plugin starts it; it is
// not meant to be run by hand.
func runtimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Workflow runtime services for harness plugins (internal)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Serve the hosting draft/publication core over stdio JSON-RPC (started by the workflow plugin)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return hostruntime.NewServer(cmd.InOrStdin(), cmd.OutOrStdout(), buildinfo.Resolve(Version, buildinfo.DevVersion)).Serve()
		},
	})
	return cmd
}
