package cli

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/app"
	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/spf13/cobra"
)

// Version is the build version. It is initialized to buildinfo.DevVersion
// (the single source of the dev literal shared by every homonto binary)
// and is a constant-initialized string so the linker's -X stamp takes
// effect; when unstamped — `go install ...@tag` applies no ldflags —
// buildinfo.Resolve recovers the module version.
var Version = buildinfo.DevVersion

// NewRootCmd builds Homonto's command tree.
//
// The surface is deliberately small and splits three ways: workspace
// lifecycle (init, status, doctor, update), workflow (task or change), and
// the protocol a host speaks (next, report, decide, accept-edit, guard,
// host). A command in one group never reaches into another's decisions.
func NewRootCmd() *cobra.Command { return NewWorkflowRootCmd(nil) }

// NewWorkflowRootCmd builds the command tree with an injectable workspace
// opener. A nil opener uses the real workspace; tests supply their own.
func NewWorkflowRootCmd(opener Opener) *cobra.Command {
	if opener == nil {
		opener = defaultOpener
	}
	version := buildinfo.Resolve(Version, buildinfo.DevVersion)
	root := &cobra.Command{
		Use:           "homonto",
		Short:         "Explicit AI coding workflows with binary-owned state",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("workspace", "", "workspace root (default: the working directory)")
	root.AddCommand(
		versionCmd(version),
		initCmd(),
		statusCmd(opener),
		doctorCmd(opener),
		handoffCmd(opener),
		attachCmd(opener),
		taskCmd(opener),
		changeCmd(opener),
		nextCmd(opener),
		reportCmd(opener),
		decideCmd(opener),
		acceptEditCmd(opener),
		guardCmd(opener),
		hostCmd(opener),
		workflowUpdateCmd(),
	)
	return root
}

// versionCmd prints the build version.
func versionCmd(version string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the homonto version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("homonto %s\n", version)
			return nil
		},
	}
}

// Opener opens the workspace a command operates on. Tests substitute one
// that returns an App over a fixture workspace.
//
// The read-only flag is part of the signature because the resume probe
// must not change anything, and a test opener that ignored it would let
// the probe's read-only contract pass untested.
type Opener func(ctx context.Context, root string, readOnly bool) (*app.App, error)

// defaultOpener opens the real workspace.
func defaultOpener(ctx context.Context, root string, readOnly bool) (*app.App, error) {
	return app.Open(ctx, app.Options{Root: root, ReadOnly: readOnly})
}

// open resolves the workspace flag and opens the workspace for work.
func open(cmd *cobra.Command, opener Opener) (*app.App, error) {
	return openWith(cmd, opener, false)
}

// openReadOnly opens the workspace without changing anything.
func openReadOnly(cmd *cobra.Command, opener Opener) (*app.App, error) {
	return openWith(cmd, opener, true)
}

func openWith(cmd *cobra.Command, opener Opener, readOnly bool) (*app.App, error) {
	root, err := cmd.Flags().GetString("workspace")
	if err != nil {
		return nil, fmt.Errorf("cli: read --workspace: %w", err)
	}
	return opener(cmd.Context(), root, readOnly)
}
