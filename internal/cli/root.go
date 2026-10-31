package cli

import (
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/noviopenworks/homonto/internal/fsutil"
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
		Short:         "Agent workflows for OpenCode, installed from one declarative config",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "homonto.toml", "path to homonto config")
	// A config reached through a symlinked directory names the same project
	// as its real path. Only the directory resolves: a symlinked homonto.toml
	// must not move the project root to wherever the file lives.
	root.PersistentPreRun = func(cmd *cobra.Command, _ []string) {
		if f := cmd.Flags().Lookup("config"); f != nil && f.Changed {
			p := f.Value.String()
			if abs, err := filepath.Abs(p); err == nil {
				_ = f.Value.Set(filepath.Join(fsutil.RealDir(filepath.Dir(abs)), filepath.Base(abs)))
			}
		}
	}

	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print the homonto version",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("homonto %s\n", version)
			return nil
		},
	})
	root.AddCommand(planCmd(), applyCmd(), updateCmd(), statusCmd(), doctorCmd(), initCmd(), cacheCmd(), explainCmd(), permissionsCmd(), snapshotCmd(), workflowCmd())
	root.AddCommand(workspaceCmd(), worktreeCmd(), tmpCmd())
	return root
}
