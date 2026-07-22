package ontocli

import (
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// statusCmd builds the "onto status" subcommand: a strictly read-only,
// config-independent diagnostic that inspects an existing workspace's
// docs/changes/*/onto-state.yaml files. It never constructs a homonto
// config/engine, never reads homonto.toml, and performs zero writes.
func statusCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Report the derived phase of each active onto change (read-only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd, dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to inspect")
	return cmd
}

func runStatus(cmd *cobra.Command, root string) error {
	// Enumerate change directories first, then classify each — so a change
	// directory whose state file was deleted surfaces as a missing-state row
	// instead of silently vanishing (F14). The "archive" directory holds
	// archived changes one level deeper and is skipped here.
	changesDir := filepath.Join(root, "docs", "changes")
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // no changes dir: nothing to report, still read-only
		}
		return err
	}

	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" {
			continue
		}
		changeDir := filepath.Join(changesDir, e.Name())
		st, class, classErr := ontostate.Classify(changeDir)
		switch class {
		case "missing-state":
			cmd.Printf("%s: missing-state\n", e.Name())
		case "malformed":
			cmd.Printf("%s: malformed (%v)\n", e.Name(), classErr)
		default: // valid — label by the enumerated directory (consistent with doctor)
			// Surface a claim the artifacts cannot support: the derived
			// WORKING phase differs from the recorded one (the state file is
			// a cache of truth; see ontostate.DeriveWorkingPhase).
			working := ""
			if derived := ontostate.DeriveWorkingPhase(changeDir, st); derived != st.Phase {
				working = " (working: " + derived + ")"
			}
			if skeletonErr := ontostate.ValidateSkeleton(changeDir); skeletonErr != nil {
				cmd.Printf("%s: %s%s — skeleton: %v\n", e.Name(), st.Phase, working, skeletonErr)
			} else {
				cmd.Printf("%s: %s%s — skeleton ok\n", e.Name(), st.Phase, working)
			}
		}
	}

	return nil
}
