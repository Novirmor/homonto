package workspacemigration

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// Verify validates a completed migration without emitting any private backup
// material. It is intentionally independent of a still-present input manifest:
// the committed receipt and private journal preserve the reviewed identity.
func Verify(configPath, runID string) (Verification, error) {
	if !migrationrecord.SafeRunID(runID) {
		return Verification{}, fmt.Errorf("workspace migration: invalid run ID")
	}
	configPath, err := absolutePath(configPath)
	if err != nil {
		return Verification{}, err
	}
	l, err := workspace.Load(configPath)
	if err != nil {
		return Verification{}, err
	}
	journal, err := loadJournal(l.WorkflowRoot, runID)
	if err != nil {
		return Verification{}, err
	}
	return verifyJournalLocked(l, &journal)
}

func verifyJournalLocked(layout workspace.Layout, journal *privateJournal) (Verification, error) {
	return verifyJournalInvariant(layout, journal, true, true, false, migrationStatusIgnored(*journal))
}

// verifyJournalInvariant checks every final ownership, history, source, and
// output invariant while the journal is still pending. Completion is written
// only after this succeeds with the marker active.
func verifyJournalInvariant(layout workspace.Layout, journal *privateJournal, requireComplete, requireMarker, allowUnpublishedCompletion bool, ignored map[string]bool) (Verification, error) {
	if err := validateJournalLayout(*journal, layout); err != nil {
		return Verification{}, err
	}
	if requireComplete && journal.Phase != "complete" {
		return Verification{}, fmt.Errorf("workspace migration: run %s is %s; recover it before verification", journal.RunID, journal.Phase)
	}
	if !requireComplete && journal.Phase != "pending-finalization" {
		return Verification{}, fmt.Errorf("workspace migration: run %s is not pending finalization", journal.RunID)
	}
	if err := verifyJournalInputsWithIgnored(layout, *journal, ignored); err != nil {
		return Verification{}, err
	}
	if err := verifyJournalRecordsWorkspace(layout, *journal, true, ignored); err != nil {
		return Verification{}, err
	}
	if requireMarker {
		verifyWrites := verifyJournalWrites
		if allowUnpublishedCompletion {
			verifyWrites = verifyJournalWritesBeforeCompletion
		}
		if err := verifyWrites(*journal, true); err != nil {
			return Verification{}, err
		}
	} else if err := verifyJournalWritesForFinalization(*journal); err != nil {
		return Verification{}, err
	}
	if err := verifyMigrationCommit(layout.WorkflowRoot, *journal); err != nil {
		return Verification{}, err
	}
	if err := verifyProofCommit(layout.WorkflowRoot, *journal); err != nil {
		return Verification{}, err
	}
	if _, err := migrationGit(layout.WorkflowRoot, "merge-base", "--is-ancestor", journal.ProofCommit, "HEAD"); err != nil {
		return Verification{}, fmt.Errorf("workspace migration: proof commit is no longer retained by records HEAD: %w", err)
	}
	receipt, err := migrationrecord.LoadReceipt(layout.WorkflowRoot, journal.RunID)
	if err != nil {
		return Verification{}, err
	}
	proof, err := migrationrecord.LoadCommitProof(layout.WorkflowRoot, journal.RunID)
	if err != nil {
		return Verification{}, err
	}
	if err := verifyReceipt(layout, *journal, receipt, proof, requireMarker); err != nil {
		return Verification{}, err
	}
	completion, ok := journalWrite(*journal, journalKindCompletion)
	if !ok {
		return Verification{}, fmt.Errorf("workspace migration: completion witness operation is absent from journal")
	}
	if requireComplete && len(completion.Postimage) == 0 {
		return Verification{}, fmt.Errorf("workspace migration: completed journal lacks its authorization witness")
	}
	if len(completion.Postimage) != 0 && requireMarker && !allowUnpublishedCompletion {
		if err := migrationrecord.ValidateCompletionWitness(layout.WorkflowRoot, journal.RunID); err != nil {
			return Verification{}, err
		}
	}
	entries, err := workspace.ListWorktrees(layout)
	if err != nil {
		return Verification{}, err
	}
	if err := verifyBindings(entries, receipt.Bindings, journal.RunID); err != nil {
		return Verification{}, err
	}
	return Verification{RunID: journal.RunID, PlanHash: journal.PlanHash, Status: journal.Phase, MigrationCommit: journal.MigrationCommit, ProofCommit: journal.ProofCommit, RecordWrites: len(receipt.RecordWrites), RetiredRecords: len(receipt.Retired), Bindings: len(receipt.Bindings)}, nil
}

func verifyJournalWrites(journal privateJournal, post bool) error {
	for _, write := range journal.Writes {
		root, err := journalWriteRoot(journal, write)
		if err != nil {
			return err
		}
		data, mode, exists, err := readOptionalRegular(root, write.Path)
		if err != nil {
			return err
		}
		if write.Kind == journalKindCompletion && len(write.Postimage) == 0 && journal.Phase != "complete" {
			if writeMatches(write, data, mode, exists, false) {
				continue
			}
			return fmt.Errorf("workspace migration: %s content differs from the journaled completion preimage", write.Path)
		}
		if !writeMatches(write, data, mode, exists, post) {
			return fmt.Errorf("workspace migration: %s content differs from the journaled %simage", write.Path, map[bool]string{true: "post", false: "pre"}[post])
		}
	}
	return nil
}

// verifyJournalWritesBeforeCompletion requires the finalized marker and every
// other write, while allowing a witness whose authenticated postimage was
// journaled immediately before a process died. ensureCompletionWitness validates
// and publishes that exact witness before completion can be recorded.
func verifyJournalWritesBeforeCompletion(journal privateJournal, post bool) error {
	for _, write := range journal.Writes {
		root, err := journalWriteRoot(journal, write)
		if err != nil {
			return err
		}
		data, mode, exists, err := readOptionalRegular(root, write.Path)
		if err != nil {
			return err
		}
		if write.Kind == journalKindCompletion {
			if writeMatches(write, data, mode, exists, false) || len(write.Postimage) != 0 && writeMatches(write, data, mode, exists, true) {
				continue
			}
			return fmt.Errorf("workspace migration: %s content differs from the journaled completion image", write.Path)
		}
		if !writeMatches(write, data, mode, exists, post) {
			return fmt.Errorf("workspace migration: %s content differs from the journaled postimage", write.Path)
		}
	}
	return nil
}

func verifyJournalWritesForFinalization(journal privateJournal) error {
	for _, write := range journal.Writes {
		root, err := journalWriteRoot(journal, write)
		if err != nil {
			return err
		}
		data, mode, exists, err := readOptionalRegular(root, write.Path)
		if err != nil {
			return err
		}
		if write.Kind == journalKindMarker {
			if writeMatches(write, data, mode, exists, false) || writeMatches(write, data, mode, exists, true) {
				continue
			}
			return fmt.Errorf("workspace migration: %s content differs from the journaled marker image", write.Path)
		}
		if write.Kind == journalKindCompletion {
			if writeMatches(write, data, mode, exists, false) || len(write.Postimage) != 0 && writeMatches(write, data, mode, exists, true) {
				continue
			}
			return fmt.Errorf("workspace migration: %s content differs from the journaled completion image", write.Path)
		}
		if !writeMatches(write, data, mode, exists, true) {
			return fmt.Errorf("workspace migration: %s content differs from the journaled postimage", write.Path)
		}
	}
	return nil
}

func verifyReceipt(layout workspace.Layout, journal privateJournal, receipt migrationrecord.Receipt, proof migrationrecord.CommitProof, requireMarker bool) error {
	if err := validateReceiptAgainstJournal(journal, receipt); err != nil {
		return err
	}
	if proof.RunID != journal.RunID || proof.MigrationCommit != journal.MigrationCommit || proof.Parent != journal.RecordsParent || proof.Tree != journal.ExpectedMigrationTree || proof.MessageSHA256 != journal.MigrationMessageSHA256 {
		return fmt.Errorf("workspace migration: public commit proof differs from private journal")
	}
	config, err := readRealRegularFile(receipt.Config.Path)
	if err != nil || migrationDigest(config) != receipt.Config.SHA256 {
		return fmt.Errorf("workspace migration: configuration fingerprint differs from receipt")
	}
	refs := []migrationrecord.FileRef{receipt.Registry}
	if requireMarker {
		refs = append(refs, receipt.LayoutMarker)
	}
	for _, ref := range refs {
		data, _, exists, err := readOptionalRegular(layout.ConfigRoot, ref.Path)
		if err != nil || !exists || migrationDigest(data) != ref.SHA256 {
			return fmt.Errorf("workspace migration: receipt output fingerprint differs at %s", ref.Path)
		}
	}
	if err := receipt.ValidateRetiredCorrespondence(layout.WorkflowRoot); err != nil {
		return fmt.Errorf("workspace migration: retired receipt correspondence is invalid: %w", err)
	}
	return nil
}

func verifyBindings(entries []workspace.Worktree, bindings []migrationrecord.Binding, runID string) error {
	actual := map[string]workspace.Worktree{}
	for _, entry := range entries {
		if entry.Origin == "legacy-migration" {
			if entry.MigrationRunID != runID {
				return fmt.Errorf("workspace migration: registry contains a different migration binding")
			}
			actual[entry.Workflow+"\x00"+entry.Change+"\x00"+entry.Repo] = entry
		}
	}
	if len(actual) != len(bindings) {
		return fmt.Errorf("workspace migration: registry binding count differs from receipt")
	}
	for _, binding := range bindings {
		key := binding.Workflow + "\x00" + binding.Change + "\x00" + binding.Repo
		entry, ok := actual[key]
		if !ok || entry.Path != binding.Path || entry.CommonDir != binding.CommonDir || entry.GitDir != binding.GitDir || entry.StateID != binding.StateID || entry.Branch != binding.Branch || entry.BaseRef != binding.BaseRef || entry.BaseTarget != binding.BaseTarget || entry.BaseCommit != binding.BaseCommit || migrationDigest([]byte(entry.Ownership)) != binding.OwnershipHash {
			return fmt.Errorf("workspace migration: registry binding differs from receipt")
		}
	}
	return nil
}
