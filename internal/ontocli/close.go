package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
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
// a passing verify result that agrees with verification.md, integration,
// close-plan review, and close.merged==true; a full workflow additionally
// requires guides resolved (updated or waived:<reason>). fix/tweak presets use
// the reduced guide set. An empty workflow is treated as full (strictest,
// fail-safe). Each missing token yields an error naming what is absent; the
// caller archives nothing.
func passingVerificationEvidence(changeDir string, st ontostate.State) error {
	if st.Verify.Result != "pass" {
		result := st.Verify.Result
		if result == "" {
			result = "unset"
		}
		return fmt.Errorf("missing passing verification (verify.result=%s); run and record a passing verification first", result)
	}
	line, ok := ontostate.VerificationResultLine(filepath.Join(changeDir, "verification.md"))
	if !ok {
		return fmt.Errorf("verification.md must contain exactly one canonical \"Result:\" line; write a passing report first")
	}
	if !ontostate.ResultLineIsPass(line) {
		return fmt.Errorf("verification.md says %q but verify.result=pass; the report and state must agree", line)
	}
	return nil
}

func closeEvidenceGate(root, changeDir string, st ontostate.State) error {
	if err := passingVerificationEvidence(changeDir, st); err != nil {
		return fmt.Errorf("onto close: %w", err)
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
	if st.Integration == "" {
		return fmt.Errorf("onto close: integration not recorded; run `onto set integration %s merge|pr` before close", st.Change)
	}
	if strings.TrimSpace(st.BaseRef) == "" {
		return fmt.Errorf("onto close: base_ref not recorded; run `onto set base-ref %s <commit>` before close", st.Change)
	}
	if strings.TrimSpace(st.BaseBranch) == "" {
		return fmt.Errorf("onto close: base_branch not recorded; run `onto set base-branch %s <branch>` before close", st.Change)
	}
	if err := validateBranchName(root, st.BaseBranch); err != nil {
		return fmt.Errorf("onto close: base_branch is invalid: %w", err)
	}
	// Last, the close-plan review token. It is checked after the plan's own
	// inputs so an earlier gap surfaces as itself, and before archival.
	if st.CloseConfirmed == "" {
		return fmt.Errorf("onto close: close plan not validated: review it, then run `onto set close-confirmed %s \"<evidence>\"`", st.Change)
	}
	if !st.Close.Merged {
		return fmt.Errorf("onto close: spec deltas not merged (close.merged=false); run `onto merge-deltas %s` before close", st.Change)
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
// exist (no-clobber). Only once all of these pass does it move the change
// directory into docs/changes/archive/<date>-<name>/ and save Archived=true at
// the destination.
func runClose(cmd *cobra.Command, root, name string) error {
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}

	if err := ontoFramework.ValidChangeName(name); err != nil {
		return err
	}

	unlock := acquireStateLockBestEffort(root)
	defer unlock()

	changeDir := filepath.Join(changesDir(root), name)
	statePath := filepath.Join(changeDir, "onto-state.yaml")
	if _, err := os.Stat(changeDir); os.IsNotExist(err) {
		return recoverInterruptedClose(cmd, root, name)
	} else if err != nil {
		return fmt.Errorf("onto close: inspecting %s: %w", changeDir, err)
	}

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
	if st.Change != name {
		return fmt.Errorf("onto close: state change %q does not match directory %q", st.Change, name)
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
	for _, artifact := range ontostate.RequiredArtifacts("close", st.Workflow) {
		if _, statErr := os.Stat(filepath.Join(changeDir, artifact)); statErr != nil {
			return fmt.Errorf("onto close: missing required artifact %s", artifact)
		}
	}

	if err := closeEvidenceGate(root, changeDir, st); err != nil {
		return err
	}
	if err := verifyHeadsIntact(root, st); err != nil {
		return fmt.Errorf("onto close: %w", err)
	}
	if err := validateCompletedMergeReceipt(root, changeDir, name); err != nil {
		return fmt.Errorf("onto close: merge receipt is stale or missing: %w; run `onto merge-deltas %s`", err, name)
	}

	unresolved := ontostate.DepsResolved(root, st.Deps)
	if len(unresolved) > 0 {
		return fmt.Errorf("onto close: unresolved dependencies: %v", unresolved)
	}
	integration, integrationExists, err := integrationrecord.Load(changeDir, name)
	if err != nil {
		return fmt.Errorf("onto close: %w", err)
	}
	if integrationExists {
		if err := validateIntegrationRecord(st, integration); err != nil {
			return fmt.Errorf("onto close: %w", err)
		}
		if integration.Status != integrationrecord.StatusPending {
			return fmt.Errorf("onto close: active change has an integration record with status %q", integration.Status)
		}
	}

	dirt, err := scopedWorktreeDirt(root, name, st.Repos)
	if err != nil {
		return fmt.Errorf("onto close: cannot verify scoped worktrees; refusing close: %w", err)
	}
	if integrationExists {
		dirt = ignorePendingIntegrationDirt(root, dirt, name)
	}
	if msg := scopedDirtGateError(dirt, name); msg != "" {
		return fmt.Errorf("onto close: dirty worktree blocks close: %s", msg)
	}

	archiveDir, err := archiveDestination(root, name, time.Now().Format("2006-01-02"))
	if err != nil {
		return fmt.Errorf("onto close: choosing archive destination: %w", err)
	}
	if err := fsutil.RequireRealParents(root, filepath.Dir(archiveDir)); err != nil {
		return fmt.Errorf("onto close: unsafe archive path: %w", err)
	}
	if err := os.MkdirAll(ontoArchiveDir(root), 0o755); err != nil {
		return fmt.Errorf("onto close: creating archive directory: %w", err)
	}
	// Move FIRST, then record archived:true inside the moved directory. The old
	// order (flag, then move) had a crash window that left `archived: true` at
	// the ORIGINAL path — the exact state doctor flags as corrupt — and the
	// rollback could not run across a crash; recovery was then blocked by this
	// command's own dirty-worktree check. With move-first, a crash between the
	// two steps leaves the change correctly archived with a stale flag, which is
	// benign: presence under archive/ is what dependency resolution keys on.
	if err := fsutil.RequireRealParents(root, filepath.Dir(archiveDir)); err != nil {
		return fmt.Errorf("onto close: unsafe archive path: %w", err)
	}
	if !integrationExists {
		entries, err := captureIntegrationEntries(root, st)
		if err != nil {
			return fmt.Errorf("onto close: %w", err)
		}
		integration = integrationrecord.NewPending(name, st.Integration, st.BaseBranch, entries)
		if err := integrationrecord.Save(changeDir, integration); err != nil {
			return fmt.Errorf("onto close: %w", err)
		}
	}
	if err := fsutil.RenameDurable(changeDir, archiveDir); err != nil {
		return fmt.Errorf("onto close: moving %s to %s: %w", changeDir, archiveDir, err)
	}
	st.Archived = true
	st.IntegrationRequired = true
	if err := ontostate.Save(filepath.Join(archiveDir, "onto-state.yaml"), st); err != nil {
		// Roll the move back so a failed close leaves the change fully
		// un-archived rather than archived-with-a-false-flag. If even the
		// roll-back rename fails, say so explicitly instead of silently keeping
		// half a close.
		if rbErr := fsutil.RenameDurable(archiveDir, changeDir); rbErr != nil {
			return fmt.Errorf("onto close: recording archived flag failed (%v) AND rolling the move back failed (%v); the change is at %s with archived:false — move it back to %s by hand", err, rbErr, archiveDir, changeDir)
		}
		return fmt.Errorf("onto close: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s: archived to %s; integration pending\n", name, archiveDir)
	return nil
}

func recoverInterruptedClose(cmd *cobra.Command, root, name string) error {
	archiveDir, st, err := locateArchive(root, name)
	if err != nil {
		return fmt.Errorf("onto close: active workspace is absent and recovery failed: %w", err)
	}
	if st.Archived {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: already archived at %s\n", name, archiveDir)
		return nil
	}
	if st.Abandoned || st.Phase != "close" {
		return fmt.Errorf("onto close: archive recovery requires a non-abandoned close-phase state")
	}
	for _, artifact := range ontostate.RequiredArtifacts("close", st.Workflow) {
		if _, statErr := os.Stat(filepath.Join(archiveDir, artifact)); statErr != nil {
			return fmt.Errorf("onto close: archive recovery missing required artifact %s", artifact)
		}
	}
	if err := closeEvidenceGate(root, archiveDir, st); err != nil {
		return err
	}
	if err := verifyHeadsIntact(root, st); err != nil {
		return fmt.Errorf("onto close: archive recovery: %w", err)
	}
	if err := validateCompletedMergeReceipt(root, archiveDir, name); err != nil {
		return fmt.Errorf("onto close: archive recovery has an invalid merge receipt: %w", err)
	}
	if unresolved := ontostate.DepsResolved(root, st.Deps); len(unresolved) > 0 {
		return fmt.Errorf("onto close: archive recovery has unresolved dependencies: %v", unresolved)
	}
	dirt, err := scopedWorktreeDirt(root, name, st.Repos)
	if err != nil {
		return fmt.Errorf("onto close: archive recovery cannot verify scoped worktrees: %w", err)
	}
	dirt = ignoreInterruptedArchiveMoveDirt(dirt)
	if msg := scopedDirtGateError(dirt, name); msg != "" {
		return fmt.Errorf("onto close: archive recovery dirty worktree blocks close: %s", msg)
	}
	integration, ok, err := integrationrecord.Load(archiveDir, name)
	if err != nil {
		return fmt.Errorf("onto close: archive recovery: %w", err)
	}
	if !ok {
		entries, entriesErr := captureIntegrationEntries(root, st)
		if entriesErr != nil {
			return fmt.Errorf("onto close: archive recovery: %w", entriesErr)
		}
		integration = integrationrecord.NewPending(name, st.Integration, st.BaseBranch, entries)
		if err := integrationrecord.Save(archiveDir, integration); err != nil {
			return fmt.Errorf("onto close: archive recovery: %w", err)
		}
	} else if err := validateIntegrationRecord(st, integration); err != nil {
		return fmt.Errorf("onto close: archive recovery: %w", err)
	} else if integration.Status != integrationrecord.StatusPending {
		return fmt.Errorf("onto close: archive recovery found impossible integration status %q", integration.Status)
	}
	st.Archived = true
	st.IntegrationRequired = true
	if err := ontostate.Save(filepath.Join(archiveDir, "onto-state.yaml"), st); err != nil {
		return fmt.Errorf("onto close: archive recovery: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: recovered interrupted archive at %s; integration pending\n", name, archiveDir)
	return nil
}
