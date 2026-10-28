package workspacemigration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// Recover resumes the exact journaled operation or restores its captured
// preimages. It never resets a branch, removes an unknown source worktree, or
// overwrites a file whose bytes are neither this run's preimage nor postimage.
func Recover(configPath, runID, action, expectedPlanHash string) (RecoveryResult, error) {
	if action != "resume" && action != "restore" {
		return RecoveryResult{}, fmt.Errorf("workspace migration: recover action must be resume or restore")
	}
	if !migrationrecord.SafeRunID(runID) || !validDigest(expectedPlanHash) {
		return RecoveryResult{}, fmt.Errorf("workspace migration: recover requires a valid run ID and reviewed plan hash")
	}
	if err := requireMigrationFilesystemSupport(); err != nil {
		return RecoveryResult{}, err
	}
	result := RecoveryResult{RunID: runID, Action: action}
	err := withRecoveryMigrationLocks(configPath, runID, expectedPlanHash, func(layout workspace.Layout, material recoveryMaterial) error {
		if material.retired {
			result.Status = "preparation-cleared"
			return nil
		}
		if material.orphan {
			if err := discardPreparationLocked(layout, runID, nil); err != nil {
				return err
			}
			result.Status = "preparation-cleared"
			return nil
		}
		if material.intent != nil {
			if action == "restore" {
				if err := discardPreparationLocked(layout, material.intent.RunID, material.intent); err != nil {
					return err
				}
				result.Status = "preparation-cleared"
				return nil
			}
			journal, err := resumePreparationLocked(layout, *material.intent)
			if err != nil {
				return err
			}
			if err := resumeJournalLocked(layout, journal); err != nil {
				return err
			}
			result.Status = journal.Phase
			return nil
		}
		if material.journal == nil {
			return fmt.Errorf("workspace migration: recovery material is absent")
		}
		var err error
		switch action {
		case "resume":
			err = resumeJournalLocked(layout, material.journal)
		case "restore":
			err = restoreJournalLocked(layout, material.journal)
		}
		if err != nil {
			return err
		}
		result.Status = material.journal.Phase
		return nil
	})
	return result, err
}

func restoreJournalLocked(layout workspace.Layout, journal *privateJournal) error {
	if err := validateJournalLayout(*journal, layout); err != nil {
		return err
	}
	if err := verifyRecordsGitBackup(layout.WorkflowRoot, journal.RecordsBackup); err != nil {
		return err
	}
	if journal.Phase == "complete" {
		return fmt.Errorf("workspace migration: completed migration cannot be restored automatically; assess a new recovery plan")
	}
	if journal.Phase == "restored" {
		if journal.RestoreCommit != "" {
			if err := verifyRestoreCommit(layout.WorkflowRoot, *journal); err != nil {
				return err
			}
		}
		return verifyJournalWrites(*journal, false)
	}
	if err := verifyJournalInputsLocked(layout, *journal); err != nil {
		return err
	}
	if err := discoverCommittedProgress(layout.WorkflowRoot, journal); err != nil {
		return err
	}
	if err := verifyRestorableWrites(*journal); err != nil {
		return err
	}
	journal.Phase = "restoring"
	if err := saveJournal(*journal); err != nil {
		return err
	}
	writes := sortedJournalWrites(journal.Writes)
	for i := len(writes) - 1; i >= 0; i-- {
		if _, err := applyJournalWrite(*journal, writes[i], false); err != nil {
			return err
		}
	}
	if err := restoreRecordsHistory(layout.WorkflowRoot, journal); err != nil {
		return err
	}
	// Commit hooks can mutate source refs, source worktrees, or records after
	// the restore tree was prepared. Re-run the complete input check before the
	// journal is allowed to report a restored phase.
	if err := verifyJournalInputsLocked(layout, *journal); err != nil {
		return err
	}
	if journal.RestoreCommit != "" {
		if err := verifyRestoreCommit(layout.WorkflowRoot, *journal); err != nil {
			return err
		}
	}
	if err := verifyJournalWrites(*journal, false); err != nil {
		return err
	}
	journal.Phase = "restored"
	return saveJournal(*journal)
}

func discoverCommittedProgress(root string, journal *privateJournal) error {
	head, err := migrationGitText(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if journal.MigrationCommit == "" && head != journal.RecordsParent && journal.ExpectedMigrationTree != "" {
		candidate := *journal
		candidate.MigrationCommit = head
		if err := verifyMigrationCommit(root, candidate); err == nil {
			journal.MigrationCommit = head
			if err := saveJournal(*journal); err != nil {
				return err
			}
		}
	}
	if journal.MigrationCommit != "" && journal.ProofCommit == "" && head != journal.MigrationCommit && journal.ExpectedProofTree != "" {
		candidate := *journal
		candidate.ProofCommit = head
		if err := verifyProofCommit(root, candidate); err == nil {
			journal.ProofCommit = head
			if err := saveJournal(*journal); err != nil {
				return err
			}
		}
	}
	if journal.RestoreParent != "" && journal.RestoreCommit == "" && head != journal.RestoreParent && journal.ExpectedRestoreTree != "" {
		candidate := *journal
		candidate.RestoreCommit = head
		if err := verifyRestoreCommit(root, candidate); err == nil {
			journal.RestoreCommit = head
			if err := saveJournal(*journal); err != nil {
				return err
			}
		}
	}
	if journal.MigrationCommit != "" {
		if err := verifyMigrationCommit(root, *journal); err != nil {
			return err
		}
	}
	if journal.ProofCommit != "" {
		if err := verifyProofCommit(root, *journal); err != nil {
			return err
		}
	}
	if journal.RestoreCommit != "" {
		if err := verifyRestoreCommit(root, *journal); err != nil {
			return err
		}
	}
	return nil
}

func verifyRestorableWrites(journal privateJournal) error {
	for _, write := range journal.Writes {
		root, err := journalWriteRoot(journal, write)
		if err != nil {
			return err
		}
		data, mode, exists, err := readOptionalRegular(root, write.Path)
		if err != nil {
			return err
		}
		if !writeMatches(write, data, mode, exists, false) && !writeMatches(write, data, mode, exists, true) {
			return fmt.Errorf("workspace migration: recovery conflict at %s", write.Path)
		}
	}
	return nil
}

// restoreLogicalRecordsIndex restores only the review-bound index paths when no
// migration commit escaped. It never resets the whole index, so unrelated paths
// remain outside migration authority.
func restoreLogicalRecordsIndex(root string, journal *privateJournal) error {
	if err := validateJournalIndexRestoreOperation(*journal); err != nil {
		return err
	}
	paths, err := journalIndexPaths(*journal)
	if err != nil {
		return err
	}
	if err := ensureOnlyMigrationChanges(root, paths, migrationStatusIgnoredHeld(*journal)); err != nil {
		return err
	}
	if err := verifyJournalRecordPaths(*journal, paths, false); err != nil {
		return err
	}
	want, err := derivedJournalIndex(root, *journal, journalIndexPre)
	if err != nil {
		return err
	}
	migration, err := derivedJournalIndex(root, *journal, journalIndexMigration)
	if err != nil {
		return err
	}
	allowed := [][]recordsIndexEntry{want, migration}
	if journalProofIndexPrepared(*journal) {
		proof, err := derivedJournalIndex(root, *journal, journalIndexProof)
		if err != nil {
			return err
		}
		allowed = append(allowed, proof)
	}
	actual, err := validateRecordsIndexAgainst(root, paths, allowed...)
	if err != nil {
		return err
	}
	if !sameRecordsIndex(actual, want) {
		byPath := make(map[string]recordsIndexEntry, len(want))
		for _, entry := range want {
			byPath[entry.Path] = entry
		}
		var input bytes.Buffer
		zeroObject := strings.Repeat("0", len(journal.RecordsParent))
		for _, path := range paths {
			if entry, ok := byPath[path]; ok {
				fmt.Fprintf(&input, "%o %s\t%s\x00", entry.Mode, entry.Object, entry.Path)
				continue
			}
			fmt.Fprintf(&input, "0 %s\t%s\x00", zeroObject, path)
		}
		if _, err := migrationGitInput(root, input.Bytes(), "update-index", "-z", "--index-info"); err != nil {
			return fmt.Errorf("workspace migration: restore logical records index: %w", err)
		}
		if err := verifyJournalRecordPaths(*journal, paths, false); err != nil {
			return err
		}
		actual, err = captureRecordsIndex(root, paths)
		if err != nil {
			return err
		}
		if !sameRecordsIndex(actual, want) {
			return fmt.Errorf("workspace migration: logical records index did not restore its reviewed preimage")
		}
	}
	if journal.RestoreIndexPost == nil {
		if err := migrationBeforePostIndexSave("restore"); err != nil {
			return err
		}
		journal.RestoreIndexPost = append([]recordsIndexEntry(nil), want...)
		if err := saveJournal(*journal); err != nil {
			return err
		}
		if err := migrationAfterPostIndexSave("restore"); err != nil {
			return err
		}
	} else if !sameRecordsIndex(journal.RestoreIndexPost, want) {
		return fmt.Errorf("workspace migration: persisted logical records index postimage differs from trusted writes")
	}
	return nil
}

func restoreRecordsHistory(root string, journal *privateJournal) error {
	paths, err := journalRecordPaths(*journal, journalKindState, journalKindIgnore, journalKindReceipt, journalKindProof)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	if journal.RestoreCommit != "" {
		return verifyRestoreCommit(root, *journal)
	}
	parent := journal.MigrationCommit
	baselinePhase := journalIndexMigration
	if journal.ProofCommit != "" {
		parent = journal.ProofCommit
		baselinePhase = journalIndexProof
	}
	if parent == "" {
		return restoreLogicalRecordsIndex(root, journal)
	}
	baseline, err := derivedJournalIndex(root, *journal, baselinePhase)
	if err != nil {
		return err
	}
	expected, err := derivedJournalIndex(root, *journal, journalIndexRestore)
	if err != nil {
		return err
	}
	head, err := migrationGitText(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if head != parent {
		if journal.ExpectedRestoreTree == "" {
			return fmt.Errorf("workspace migration: records HEAD moved before restoration commit")
		}
		candidate := *journal
		candidate.RestoreCommit = head
		if err := verifyRestoreCommit(root, candidate); err != nil {
			return fmt.Errorf("workspace migration: records HEAD does not match the prepared restoration commit: %w", err)
		}
		journal.RestoreCommit = head
		return saveJournal(*journal)
	}
	stagePaths, err := restorationStagePaths(root, paths)
	if err != nil {
		return err
	}
	if len(stagePaths) == 0 {
		return nil
	}
	if err := migrationGitIdentity(root); err != nil {
		return err
	}
	if journal.RestoreParent == "" {
		journal.RestoreParent = parent
		if err := saveJournal(*journal); err != nil {
			return err
		}
	} else if journal.RestoreParent != parent {
		return fmt.Errorf("workspace migration: restoration parent changed after preparation")
	}
	tree, err := stageRecordsPaths(root, journal, stagePaths, paths, baseline, expected, &journal.RestoreIndexPost, "restore", false, false)
	if err != nil {
		return err
	}
	if journal.ExpectedRestoreTree != "" && journal.ExpectedRestoreTree != tree {
		return fmt.Errorf("workspace migration: staged restoration tree changed after preparation")
	}
	if journal.ExpectedRestoreTree == "" {
		journal.ExpectedRestoreTree = tree
		if err := saveJournal(*journal); err != nil {
			return err
		}
	}
	if _, err := migrationGit(root, "commit", "-m", journal.RestoreMessage); err != nil {
		return fmt.Errorf("workspace migration: records restoration commit remains pending: %w", err)
	}
	head, err = migrationGitText(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	journal.RestoreCommit = head
	if err := verifyRestoreCommit(root, *journal); err != nil {
		return err
	}
	return saveJournal(*journal)
}

// restorationStagePaths omits paths that were never tracked and are absent
// after restoration. Git rejects those pathspecs, while tracked deletions and
// existing files still need to be refreshed in the index.
func restorationStagePaths(root string, paths []string) ([]string, error) {
	stage := make([]string, 0, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
		if err == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("workspace migration: restoration path is not a real regular file")
			}
			stage = append(stage, path)
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		tracked, err := migrationPathTracked(root, path)
		if err != nil {
			return nil, err
		}
		if tracked {
			stage = append(stage, path)
		}
	}
	return stage, nil
}

func migrationPathTracked(root, path string) (bool, error) {
	cmd := migrationGitCommand(root, "ls-files", "--error-unmatch", "--", path)
	if output, err := cmd.CombinedOutput(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("workspace migration: git ls-files: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return true, nil
}
