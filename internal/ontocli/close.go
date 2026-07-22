package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// closeCmd builds the "onto close <change>" subcommand: it enforces
// ontoFramework.Gate(dir), validates the change name, and only then attempts to
// archive a change already at the terminal "close" phase. It writes nothing and
// moves nothing unless every precondition below passes.
func closeCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "close <change>",
		Short: "Archive a change that has reached the close phase, if all gates pass",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClose(cmd, dir, args[0])
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// closeEvidenceGate refuses close unless the loaded state carries the
// close-phase evidence tokens its workflow produces (B1: the token is present
// and well-formed, not merely artifact files on disk). Every workflow requires
// verify.result==pass and close.merged==true; a full workflow additionally
// requires guides resolved (updated or waived:<reason>). fix/tweak presets are
// gated on the reduced set they actually produce and do NOT require guides. An
// empty workflow is treated as full (strictest, fail-safe). Each missing token
// yields an error naming exactly what is absent; the caller archives nothing.
func closeEvidenceGate(st ontostate.State) error {
	if st.Verify.Result != "pass" {
		result := st.Verify.Result
		if result == "" {
			result = "unset"
		}
		return fmt.Errorf("onto close: missing passing verification (verify.result=%s); run and record a passing verification before close", result)
	}
	if !st.Close.Merged {
		return fmt.Errorf("onto close: change not merged (close.merged=false); mark the change merged before close")
	}
	// guides are required only for the full workflow; the reduced fix/tweak
	// presets never produce them. Empty workflow is treated as full.
	if st.Workflow == "full" || st.Workflow == "" {
		if !ontostate.GuidesResolved(st.Guides) {
			guides := st.Guides
			if guides == "" {
				guides = "unset"
			}
			return fmt.Errorf("onto close: unresolved guides (guides=%s); update or waive guides before close", guides)
		}
	}
	// Last, the final-confirmation token: the user's explicit yes to the close
	// plan. It is checked after the plan's own inputs so an earlier gap
	// surfaces as itself, and before any mutation so a declined gate leaves
	// the repo untouched.
	if st.CloseConfirmed == "" {
		return fmt.Errorf("onto close: close not confirmed: answer the final-confirmation gate, then run `onto set close-confirmed %s \"<evidence>\"`", st.Change)
	}
	return nil
}

// runClose enforces, in order: ontoFramework.Gate(root);
// ontoFramework.ValidChangeName(name); that
// docs/changes/<name>/onto-state.yaml loads; that its phase is "close" (the
// terminal phase reached via repeated "onto advance"); that the workflow's
// close-phase evidence tokens are present and well-formed
// (closeEvidenceGate); that every
// dependency named in st.Deps has already been archived
// (ontostate.DepsResolved); that the worktree is clean and that cleanliness
// is determinable; and that the dated archive target does not already
// exist (no-clobber). Only once all of these pass does it mark the state
// Archived, save it, and move the change directory into
// docs/changes/archive/<date>-<name>/.
func runClose(cmd *cobra.Command, root, name string) error {
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}

	if err := ontoFramework.ValidChangeName(name); err != nil {
		return err
	}

	changeDir := filepath.Join(root, "docs", "changes", name)
	statePath := filepath.Join(changeDir, "onto-state.yaml")

	st, err := ontostate.Load(statePath)
	if err != nil {
		return fmt.Errorf("onto close: loading %s: %w", statePath, err)
	}
	// Validate before closeEvidenceGate reads workflow/guides: an unknown
	// workflow value would otherwise skip the guides gate (close only checks
	// `full`/empty), and a malformed guides value like "waived:" (empty reason)
	// is accepted by GuidesResolved but rejected by ValidGuides. Load migrates
	// but does not validate (F9).
	if err := st.Validate(); err != nil {
		return fmt.Errorf("onto close: %w", err)
	}

	// Abandoned is the UNSUCCESSFUL terminal state; archiving is the successful
	// one. Without this guard a change abandoned at phase close still passed
	// every evidence gate below and archived as a success — a contradictory
	// terminal (archived+abandoned) that then falsely resolved other changes'
	// dependencies.
	if st.Abandoned {
		return fmt.Errorf("onto close: change %q is abandoned (the unsuccessful terminal state); an abandoned change is never archived as a success", name)
	}

	if st.Phase != "close" {
		return fmt.Errorf("onto close: change %q is at phase %q; run `onto advance` until it reaches close", name, st.Phase)
	}

	if err := closeEvidenceGate(st); err != nil {
		return err
	}

	unresolved := ontostate.DepsResolved(root, st.Deps)
	if len(unresolved) > 0 {
		return fmt.Errorf("onto close: unresolved dependencies: %v", unresolved)
	}

	dirt, determinable := worktreeDirt(root, name)
	if !determinable {
		return fmt.Errorf("onto close: cannot verify worktree is clean; refusing close")
	}
	// Same carve-out as advance-to-close: another change's uncommitted docs
	// are gated by that change's own close, not this one's.
	if blocking := blockingDirt(dirt); len(blocking) > 0 {
		return fmt.Errorf("onto close: dirty worktree blocks close: %s", dirtGateError(blocking, len(dirt), name))
	}

	archiveDir := filepath.Join(root, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-"+name)
	if _, err := os.Stat(archiveDir); err == nil {
		return fmt.Errorf("onto close: archive target already exists: %s", archiveDir)
	}

	// Move FIRST, then record archived:true inside the moved directory. The old
	// order (flag, then move) had a crash window that left `archived: true` at
	// the ORIGINAL path — the exact state doctor flags as corrupt — and the
	// rollback could not run across a crash; recovery was then blocked by this
	// command's own dirty-worktree check. With move-first, a crash between the
	// two steps leaves the change correctly archived with a stale flag, which is
	// benign: presence under archive/ is what dependency resolution keys on.
	if err := os.MkdirAll(filepath.Join(root, "docs", "changes", "archive"), 0o755); err != nil {
		return fmt.Errorf("onto close: creating archive directory: %w", err)
	}
	if err := os.Rename(changeDir, archiveDir); err != nil {
		return fmt.Errorf("onto close: moving %s to %s: %w", changeDir, archiveDir, err)
	}
	st.Archived = true
	if err := ontostate.Save(filepath.Join(archiveDir, "onto-state.yaml"), st); err != nil {
		// Roll the move back so a failed close leaves the change fully
		// un-archived rather than archived-with-a-false-flag. If even the
		// roll-back rename fails, say so explicitly instead of silently keeping
		// half a close.
		if rbErr := os.Rename(archiveDir, changeDir); rbErr != nil {
			return fmt.Errorf("onto close: recording archived flag failed (%v) AND rolling the move back failed (%v); the change is at %s with archived:false — move it back to %s by hand", err, rbErr, archiveDir, changeDir)
		}
		return fmt.Errorf("onto close: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s: archived to %s\n", name, archiveDir)
	return nil
}
