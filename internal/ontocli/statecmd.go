package ontocli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// stateCmd builds "onto state <change> [--json]": a read-only structured read of
// a change's full validated state and derived phase. It writes nothing and is
// not gated on the framework install.
func stateCmd() *cobra.Command {
	var (
		dir    string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "state <change>",
		Short: "Print a change's full state (use --json for a machine-readable read)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ontoFramework.ValidChangeName(name); err != nil {
				return err
			}
			changeDir := filepath.Join(changesDir(dir), name)
			var st ontostate.State
			if _, err := os.Stat(changeDir); os.IsNotExist(err) {
				archiveDir, archivedState, archiveErr := locateArchive(dir, name)
				if archiveErr != nil {
					return archiveErr
				}
				if _, sidecarErr := os.Stat(filepath.Join(archiveDir, ".onto", "integration.json")); os.IsNotExist(sidecarErr) && archivedState.Archived && !archivedState.IntegrationRequired {
					return fmt.Errorf("onto-state: no active change %q", name)
				}
				changeDir, st = archiveDir, archivedState
			} else {
				var err error
				st, err = ontostate.LoadChange(changeDir)
				if err != nil {
					return err
				}
			}
			// Validate (DerivePhase's old contract) before deriving: a
			// malformed state must not be reported as a healthy phase.
			if _, err := st.DerivePhase(); err != nil {
				return err
			}
			// The WORKING phase is derived from artifacts, not echoed from the
			// claim — the state file is a cache of truth (see
			// ontostate.DeriveWorkingPhase for the evidence table).
			phase := ontostate.DeriveWorkingPhase(changeDir, st)
			if asJSON {
				payload := struct {
					ontostate.State
					DerivedPhase  string `json:"derived_phase"`
					PhaseMismatch bool   `json:"phase_mismatch,omitempty"`
				}{State: st, DerivedPhase: phase, PhaseMismatch: phase != st.Phase}
				b, err := json.MarshalIndent(payload, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			// Text output keeps the CLAIMED phase as the primary value (what
			// the state file records) and annotates a disagreeing derivation,
			// mirroring `onto status` — so text consumers see both sides.
			if phase != st.Phase {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (working: %s)\n", name, st.Phase, phase)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", name, st.Phase)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full state as JSON")
	return cmd
}
