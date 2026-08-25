package cli

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/app"
	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/spf13/cobra"
)

// NewWorkflowRootCmd builds the rewritten workflow's command tree.
//
// It is deliberately NOT registered on NewRootCmd yet: the legacy
// projector still ships from this binary and must keep working until the
// cutover, and a half-wired second root would let a user reach the new
// workflow through a binary whose other half assumes the old one. The
// cutover workstream swaps the roots; until then this tree is reachable
// only from tests and from an explicit opt-in.
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
		taskCmd(opener),
		nextCmd(opener),
		reportCmd(opener),
		decideCmd(opener),
		acceptEditCmd(opener),
		guardCmd(opener),
	)
	return root
}

// Opener opens the workspace a command operates on. Tests substitute one
// that returns an App over a fixture workspace.
type Opener func(ctx context.Context, root string) (*app.App, error)

// defaultOpener opens the real workspace.
func defaultOpener(ctx context.Context, root string) (*app.App, error) {
	return app.Open(ctx, app.Options{Root: root})
}

// open resolves the workspace flag and opens the workspace.
func open(cmd *cobra.Command, opener Opener) (*app.App, error) {
	root, err := cmd.Flags().GetString("workspace")
	if err != nil {
		return nil, fmt.Errorf("cli: read --workspace: %w", err)
	}
	return opener(cmd.Context(), root)
}
