package cli

import (
	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/engine"
	"github.com/spf13/cobra"
)

// updateCmd re-materializes the embedded catalog and re-projects everything so
// the installed content matches the running binary, after printing the version
// transition. It shares apply's plan → confirm → apply flow; the difference is
// the version banner and the intent ("bring everything up to this version").
func updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update installed content to this binary's version",
		Long: "Re-materialize the embedded catalog (frameworks, skills, commands, " +
			"subagents) and re-project it, so a freshly installed homonto/onto binary " +
			"can bring all managed content up to its version in one command. It does " +
			"not download or replace the binaries themselves — install those the usual " +
			"way (go install @latest, or the release archives).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			yes, _ := cmd.Flags().GetBool("yes")
			return runApply(cmd, cfgPath, yes, false, func(e *engine.Engine) {
				printVersionTransition(cmd, e)
			})
		},
	}
	cmd.Flags().Bool("yes", false, "skip confirmation")
	return cmd
}

// printVersionTransition writes the binary, catalog, and per-framework version
// changes this update will record. It reads recorded versions from state (the
// values from the LAST apply) and the embedded versions from the catalog, so it
// shows the exact "from → to" the projection is about to make.
func printVersionTransition(cmd *cobra.Command, e *engine.Engine) {
	binary := buildinfo.Resolve(Version, buildinfo.DevVersion)
	if rec := e.State.HomontoVersionRecorded(); rec != "" && rec != binary {
		cmd.Printf("homonto %s → %s\n", rec, binary)
	} else {
		cmd.Printf("homonto %s\n", binary)
	}

	cl, err := e.Cfg.FrameworkCatalog()
	if err != nil {
		return
	}
	embedded := cl.Version()
	switch recorded := e.State.CatalogVersionRecorded(); {
	case recorded == "":
		cmd.Printf("catalog: (none) → %s\n", embedded)
	case recorded == embedded:
		cmd.Printf("catalog: up to date at %s\n", embedded)
	default:
		cmd.Printf("catalog: %s → %s\n", recorded, embedded)
	}

	for name, r := range e.Cfg.Frameworks {
		catName, ok := config.FrameworkCatalogName(name, r.Source)
		if !ok {
			continue
		}
		embv, ok := cl.FrameworkVersion(catName)
		if !ok {
			continue
		}
		recv := e.State.FrameworkVersions[name]
		if recv == embv {
			continue
		}
		if recv == "" {
			recv = "(none)"
		}
		cmd.Printf("  framework %s: %s → %s\n", name, recv, embv)
	}
}
