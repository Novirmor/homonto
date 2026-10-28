package workspacemigration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func cleanupPrivateRecoveryTemporaryArtifacts(layout workspace.Layout, intent preparationIntent, current *privateJournal) (*privateJournal, error) {
	identity, err := loadRecoveryIdentity(intent.Workflow, intent.RunID)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		return nil, fmt.Errorf("workspace migration: durable recovery identity is missing or differs from preparation state")
	}
	if err := validateRecoveryIdentityLayout(identity, layout); err != nil {
		return nil, err
	}
	if err := recoverPendingRecoveryDescriptorStages(identity, recoveryPendingPayloadReplay(layout, identity, intent, current)); err != nil {
		return nil, err
	}
	if current == nil {
		journal, err := loadJournal(intent.Workflow, intent.RunID)
		if err == nil {
			journalIntent, intentErr := journalPreparationIntent(journal)
			if intentErr != nil || journalIntent != intent || !privateJournalInitialTemporary(journal) {
				return nil, privateJournalTemporaryConflict()
			}
			current = &journal
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	if err := validateRecoveryPayloadStore(identity, true); err != nil {
		return nil, err
	}
	if err := ensureNoUnexpectedPrivateRecoveryTemporary(identity); err != nil {
		return nil, err
	}
	if err := reconcilePreparationStatusPayload(identity, intent, current == nil); err != nil {
		return nil, err
	}
	if err := reconcilePreparationIntentPayload(identity, intent, current == nil); err != nil {
		return nil, err
	}
	if err := cleanupPrivateRecoveryBundleTemporaryArtifact(layout, identity, intent, current); err != nil {
		return nil, err
	}
	if err := cleanupPrivateJournalTemporaryArtifact(layout, identity, intent, current); err != nil {
		return nil, err
	}
	journal, err := loadJournal(intent.Workflow, intent.RunID)
	if err == nil {
		current = &journal
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := cleanupPrivateRecoveryCompletionPayload(identity, current); err != nil {
		return nil, err
	}
	return current, nil
}

func privateRecoveryPayloadConflict(purpose string) error {
	return fmt.Errorf("workspace migration: recovery conflict: private %s payload is not recognized", purpose)
}

func reconcilePrivateRecoveryPayload(identity recoveryIdentity, purpose, target string, expected []byte, publish bool) error {
	descriptor, data, exists, err := loadPrivateRecoveryPayload(identity, purpose, target)
	if err != nil {
		return privateRecoveryPayloadConflict(purpose)
	}
	if !exists {
		if !publish {
			return nil
		}
		return writePrivateRecoveryPayload(identity, purpose, target, expected)
	}
	if !bytes.Equal(data, expected) {
		return privateRecoveryPayloadConflict(purpose)
	}
	authority, err := privateRecoveryPayloadTempAuthority(identity, descriptor, data)
	if err != nil {
		return privateRecoveryPayloadConflict(purpose)
	}
	if !publish {
		return removeMigrationAuthorizedTemp(authority)
	}
	actual, mode, targetExists, err := readMigrationOptionalRegular(identity.Workflow, target)
	if err != nil {
		return err
	}
	if targetExists {
		if mode.Perm() != 0o600 || !bytes.Equal(actual, expected) {
			return privateRecoveryPayloadConflict(purpose)
		}
		return removeMigrationAuthorizedTemp(authority)
	}
	return writeMigrationRegularAuthorized(identity.Workflow, target, data, 0o600, nil, authority)
}

func reconcilePreparationStatusPayload(identity recoveryIdentity, intent preparationIntent, publish bool) error {
	data, err := journalStatusData(intent)
	if err != nil {
		return err
	}
	path, err := journalStatusPath(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	return reconcilePrivateRecoveryPayload(identity, journalKindRecoveryStatus, path, data, publish)
}

func reconcilePreparationIntentPayload(identity recoveryIdentity, intent preparationIntent, publish bool) error {
	data, err := preparationIntentData(intent)
	if err != nil {
		return err
	}
	path, err := preparationIntentPath(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	return reconcilePrivateRecoveryPayload(identity, journalKindRecoveryIntent, path, data, publish)
}

func cleanupPrivateRecoveryBundleTemporaryArtifact(layout workspace.Layout, identity recoveryIdentity, intent preparationIntent, current *privateJournal) error {
	path, err := recordsGitBackupPath(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	descriptor, data, exists, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryBundle, path)
	if err != nil {
		return privateRecoveryPayloadConflict(journalKindRecoveryBundle)
	}
	if !exists {
		return nil
	}
	if current != nil {
		if migrationDigest(data) != current.RecordsBackup.BundleSHA256 || int64(len(data)) != descriptor.Size || descriptor.Mode != 0o600 {
			return privateRecoveryPayloadConflict(journalKindRecoveryBundle)
		}
		if err := verifyRecordsGitBackupData(intent.Workflow, current.RecordsBackup, data); err != nil {
			return privateRecoveryPayloadConflict(journalKindRecoveryBundle)
		}
	} else if err := validatePreparationBundleData(layout, intent, data); err != nil {
		return privateRecoveryPayloadConflict(journalKindRecoveryBundle)
	}
	authority, err := privateRecoveryPayloadTempAuthority(identity, descriptor, data)
	if err != nil {
		return privateRecoveryPayloadConflict(journalKindRecoveryBundle)
	}
	targetExists, err := validatePrivateRecoveryPayloadTarget(identity, descriptor, data)
	if err != nil {
		return err
	}
	if !targetExists {
		if err := writeMigrationRegularAuthorized(identity.Workflow, path, data, os.FileMode(descriptor.Mode), &migrationRegularExpectation{}, authority); err != nil {
			return err
		}
	}
	return removeMigrationAuthorizedTemp(authority)
}

func cleanupPrivateJournalTemporaryArtifact(layout workspace.Layout, identity recoveryIdentity, intent preparationIntent, current *privateJournal) error {
	journalPath, err := journalPath(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	descriptor, data, exists, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryJournal, journalPath)
	if err != nil {
		return privateJournalTemporaryConflict()
	}
	if !exists {
		return nil
	}
	mode := os.FileMode(descriptor.Mode)
	candidate, err := parsePrivateJournal(intent.Workflow, intent.RunID, journalPath, data, mode)
	if err != nil {
		return privateJournalTemporaryConflict()
	}
	canonical, err := journalData(candidate)
	if err != nil || !bytes.Equal(data, canonical) {
		return privateJournalTemporaryConflict()
	}
	authority, err := privateRecoveryPayloadTempAuthority(identity, descriptor, data)
	if err != nil {
		return privateJournalTemporaryConflict()
	}
	actual, mode, targetExists, err := readMigrationOptionalRegular(identity.Workflow, journalPath)
	if err != nil {
		return err
	}
	if targetExists && mode.Perm() == os.FileMode(descriptor.Mode).Perm() && bytes.Equal(actual, data) {
		return removeMigrationAuthorizedTemp(authority)
	}
	if err := validatePrivateJournalTemporary(layout, intent, current, candidate); err != nil {
		return privateJournalTemporaryConflict()
	}
	var expected *migrationRegularExpectation
	initialCurrent := current != nil && privateJournalInitialTemporary(*current) && reflect.DeepEqual(*current, candidate)
	if current == nil || initialCurrent {
		statusData, err := journalStatusData(intent)
		if err != nil {
			return err
		}
		if targetExists && (mode.Perm() != 0o600 || !bytes.Equal(actual, statusData)) {
			return privateJournalTemporaryConflict()
		}
		expected = &migrationRegularExpectation{exists: targetExists, data: actual, mode: mode}
	} else {
		currentData, err := journalData(*current)
		if err != nil {
			return err
		}
		if !targetExists || mode.Perm() != 0o600 || !bytes.Equal(actual, currentData) {
			return privateJournalTemporaryConflict()
		}
		expected = &migrationRegularExpectation{exists: true, data: actual, mode: mode}
	}
	if err := writeMigrationRegularAuthorized(identity.Workflow, journalPath, data, os.FileMode(descriptor.Mode), expected, authority); err != nil {
		return err
	}
	return removeMigrationAuthorizedTemp(authority)
}

func cleanupPrivateRecoveryCompletionPayload(identity recoveryIdentity, current *privateJournal) error {
	if current == nil {
		return nil
	}
	write, ok := journalWrite(*current, journalKindCompletion)
	if !ok || !write.PostExists || len(write.Postimage) == 0 {
		return nil
	}
	path, err := migrationrecord.CompletionWitnessPath(identity.Workflow, identity.RunID)
	if err != nil || write.Path != path {
		return fmt.Errorf("workspace migration: completion witness is outside durable recovery authority")
	}
	publish := current.Phase != "restoring" && current.Phase != "restored"
	return reconcilePrivateRecoveryPayload(identity, journalKindRecoveryCompletion, path, write.Postimage, publish)
}

func privateJournalTemporaryConflict() error {
	return fmt.Errorf("workspace migration: recovery conflict: private journal temporary is not recognized")
}

func validatePrivateJournalTemporary(layout workspace.Layout, intent preparationIntent, current *privateJournal, candidate privateJournal) error {
	candidateIntent, err := journalPreparationIntent(candidate)
	if err != nil || candidateIntent != intent {
		return fmt.Errorf("private journal temporary identity differs from the durable preparation intent")
	}
	if err := validateJournalLayout(candidate, layout); err != nil {
		return err
	}
	if current == nil {
		if !privateJournalInitialTemporary(candidate) {
			return fmt.Errorf("private journal temporary has progress without a durable journal")
		}
	} else if !privateJournalTemporaryExtends(*current, candidate) {
		return fmt.Errorf("private journal temporary does not extend the durable journal")
	}
	return verifyPrivateJournalTemporaryProgress(layout, candidate)
}

func privateJournalInitialTemporary(journal privateJournal) bool {
	if journal.Phase != "prepared" || journal.ExpectedMigrationTree != "" || journal.MigrationCommit != "" || journal.ExpectedProofTree != "" || journal.ProofCommit != "" || journal.RestoreParent != "" || journal.ExpectedRestoreTree != "" || journal.RestoreCommit != "" || journal.MigrationIndexPost != nil || journal.ProofIndexPost != nil || journal.RestoreIndexPost != nil {
		return false
	}
	for _, write := range journal.Writes {
		if (write.Kind == journalKindProof || write.Kind == journalKindCompletion) && len(write.Postimage) != 0 {
			return false
		}
	}
	return true
}

func privateJournalTemporaryExtends(current, candidate privateJournal) bool {
	if current.Version != candidate.Version || current.RunID != candidate.RunID || current.PlanHash != candidate.PlanHash || current.ConfigPath != candidate.ConfigPath || current.ConfigRoot != candidate.ConfigRoot || current.Workflow != candidate.Workflow || current.IntentOwner != candidate.IntentOwner || current.RecordsParent != candidate.RecordsParent || current.RecordsIndexSHA256 != candidate.RecordsIndexSHA256 || current.MigrationMessage != candidate.MigrationMessage || current.MigrationMessageSHA256 != candidate.MigrationMessageSHA256 || current.ProofMessage != candidate.ProofMessage || current.RestoreMessage != candidate.RestoreMessage || current.RestoreMessageSHA256 != candidate.RestoreMessageSHA256 || !reflect.DeepEqual(current.Plan, candidate.Plan) || !reflect.DeepEqual(current.Snapshots, candidate.Snapshots) || !reflect.DeepEqual(current.Bindings, candidate.Bindings) || !reflect.DeepEqual(current.RecordsBackup, candidate.RecordsBackup) || !reflect.DeepEqual(current.RecordsIndexPre, candidate.RecordsIndexPre) || !privateJournalPhaseCanAdvance(current.Phase, candidate.Phase) || !privateJournalWritesExtend(current.Writes, candidate.Writes) {
		return false
	}
	for _, fields := range [][2]string{
		{current.ExpectedMigrationTree, candidate.ExpectedMigrationTree},
		{current.MigrationCommit, candidate.MigrationCommit},
		{current.ExpectedProofTree, candidate.ExpectedProofTree},
		{current.ProofCommit, candidate.ProofCommit},
		{current.RestoreParent, candidate.RestoreParent},
		{current.ExpectedRestoreTree, candidate.ExpectedRestoreTree},
		{current.RestoreCommit, candidate.RestoreCommit},
	} {
		if fields[0] != "" && fields[0] != fields[1] {
			return false
		}
	}
	for _, entries := range []struct {
		current   []recordsIndexEntry
		candidate []recordsIndexEntry
	}{
		{current.MigrationIndexPost, candidate.MigrationIndexPost},
		{current.ProofIndexPost, candidate.ProofIndexPost},
		{current.RestoreIndexPost, candidate.RestoreIndexPost},
	} {
		if entries.current != nil && !reflect.DeepEqual(entries.current, entries.candidate) {
			return false
		}
	}
	return true
}

func privateJournalPhaseCanAdvance(current, candidate string) bool {
	if current == candidate {
		return true
	}
	switch current {
	case "prepared":
		return candidate == "applying" || candidate == "restoring"
	case "applying":
		return candidate == "pending-finalization" || candidate == "restoring"
	case "pending-finalization":
		return candidate == "complete" || candidate == "restoring"
	case "restoring":
		return candidate == "restored"
	default:
		return false
	}
}

func privateJournalWritesExtend(current, candidate []privateWrite) bool {
	if len(current) != len(candidate) {
		return false
	}
	byKey := make(map[string]privateWrite, len(current))
	for _, write := range current {
		byKey[journalWriteKey(write.Scope, write.Kind, write.Path)] = write
	}
	for _, write := range candidate {
		previous, ok := byKey[journalWriteKey(write.Scope, write.Kind, write.Path)]
		if !ok || previous.PreExists != write.PreExists || previous.PreMode != write.PreMode || !bytes.Equal(previous.Preimage, write.Preimage) || previous.PostExists != write.PostExists || previous.PostMode != write.PostMode || (len(previous.Postimage) != 0 && !bytes.Equal(previous.Postimage, write.Postimage)) {
			return false
		}
	}
	return true
}

func journalRecoveryDescriptorProgress(identity recoveryIdentity, target string, data []byte) (string, *recoveryJournalProgress, error) {
	previousDescriptor, previousData, exists, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryJournal, target)
	if err != nil {
		return "", nil, err
	}
	if !exists || previousDescriptor.SHA256 == migrationDigest(data) {
		return "", nil, nil
	}
	previous, err := parsePrivateJournal(identity.Workflow, identity.RunID, target, previousData, os.FileMode(previousDescriptor.Mode))
	if err != nil {
		return "", nil, err
	}
	previousIntent, err := journalPreparationIntent(previous)
	if err != nil || !recoveryIdentityMatchesIntent(identity, previousIntent) {
		return "", nil, fmt.Errorf("workspace migration: predecessor journal differs from durable recovery identity")
	}
	candidate, err := parsePrivateJournal(identity.Workflow, identity.RunID, target, data, 0o600)
	if err != nil {
		return "", nil, err
	}
	progress, err := recoveryJournalProgressFrom(previous, candidate)
	if err != nil {
		return "", nil, err
	}
	replayed, err := replayRecoveryJournalProgress(previous, progress)
	if err != nil {
		return "", nil, err
	}
	replayedData, err := journalData(replayed)
	if err != nil || !bytes.Equal(replayedData, data) {
		return "", nil, fmt.Errorf("workspace migration: private journal successor cannot be replayed exactly")
	}
	return previousDescriptor.SHA256, &progress, nil
}

func recoveryJournalProgressFrom(previous, candidate privateJournal) (recoveryJournalProgress, error) {
	if !privateJournalTemporaryExtends(previous, candidate) {
		return recoveryJournalProgress{}, fmt.Errorf("workspace migration: private journal successor does not extend durable progress")
	}
	progress := recoveryJournalProgress{
		Phase:                 candidate.Phase,
		ExpectedMigrationTree: candidate.ExpectedMigrationTree,
		MigrationCommit:       candidate.MigrationCommit,
		ExpectedProofTree:     candidate.ExpectedProofTree,
		ProofCommit:           candidate.ProofCommit,
		RestoreParent:         candidate.RestoreParent,
		ExpectedRestoreTree:   candidate.ExpectedRestoreTree,
		RestoreCommit:         candidate.RestoreCommit,
	}
	previousWrites := make(map[string]privateWrite, len(previous.Writes))
	for _, write := range previous.Writes {
		previousWrites[journalWriteKey(write.Scope, write.Kind, write.Path)] = write
	}
	for _, write := range candidate.Writes {
		old := previousWrites[journalWriteKey(write.Scope, write.Kind, write.Path)]
		if len(old.Postimage) == 0 && len(write.Postimage) != 0 {
			switch write.Kind {
			case journalKindProof:
				expected, err := journalProofData(candidate)
				if err != nil || !bytes.Equal(write.Postimage, expected) {
					return recoveryJournalProgress{}, fmt.Errorf("workspace migration: private journal proof successor is not deterministic")
				}
				progress.ProofPrepared = true
			case journalKindCompletion:
				expected, err := completionWitnessData(candidate)
				if err != nil || !bytes.Equal(write.Postimage, expected) {
					return recoveryJournalProgress{}, fmt.Errorf("workspace migration: private journal completion successor is not deterministic")
				}
				progress.CompletionPrepared = true
			default:
				return recoveryJournalProgress{}, fmt.Errorf("workspace migration: private journal successor contains unsupported mutable data")
			}
		}
	}
	progress.MigrationIndexPost = previous.MigrationIndexPost == nil && candidate.MigrationIndexPost != nil
	progress.ProofIndexPost = previous.ProofIndexPost == nil && candidate.ProofIndexPost != nil
	progress.RestoreIndexPost = previous.RestoreIndexPost == nil && candidate.RestoreIndexPost != nil
	if err := progress.validate(); err != nil {
		return recoveryJournalProgress{}, err
	}
	return progress, nil
}

func replayRecoveryJournalProgress(previous privateJournal, progress recoveryJournalProgress) (privateJournal, error) {
	if err := progress.validate(); err != nil {
		return privateJournal{}, err
	}
	candidate := clonePrivateJournalForProgress(previous)
	candidate.Phase = progress.Phase
	candidate.ExpectedMigrationTree = progress.ExpectedMigrationTree
	candidate.MigrationCommit = progress.MigrationCommit
	candidate.ExpectedProofTree = progress.ExpectedProofTree
	candidate.ProofCommit = progress.ProofCommit
	candidate.RestoreParent = progress.RestoreParent
	candidate.ExpectedRestoreTree = progress.ExpectedRestoreTree
	candidate.RestoreCommit = progress.RestoreCommit
	if progress.ProofPrepared {
		data, err := journalProofData(candidate)
		if err != nil {
			return privateJournal{}, err
		}
		if err := setJournalProgressPostimage(&candidate, journalKindProof, data); err != nil {
			return privateJournal{}, err
		}
	}
	if progress.CompletionPrepared {
		data, err := completionWitnessData(candidate)
		if err != nil {
			return privateJournal{}, err
		}
		if err := setJournalProgressPostimage(&candidate, journalKindCompletion, data); err != nil {
			return privateJournal{}, err
		}
	}
	if progress.MigrationIndexPost {
		if candidate.MigrationIndexPost != nil {
			return privateJournal{}, fmt.Errorf("workspace migration: recovery journal migration index progress is invalid")
		}
		entries, err := derivedJournalIndex(candidate.Workflow, candidate, journalIndexMigration)
		if err != nil {
			return privateJournal{}, err
		}
		candidate.MigrationIndexPost = entries
	}
	if progress.ProofIndexPost {
		if candidate.ProofIndexPost != nil {
			return privateJournal{}, fmt.Errorf("workspace migration: recovery journal proof index progress is invalid")
		}
		entries, err := derivedJournalIndex(candidate.Workflow, candidate, journalIndexProof)
		if err != nil {
			return privateJournal{}, err
		}
		candidate.ProofIndexPost = entries
	}
	if progress.RestoreIndexPost {
		if candidate.RestoreIndexPost != nil {
			return privateJournal{}, fmt.Errorf("workspace migration: recovery journal restore index progress is invalid")
		}
		entries, err := derivedJournalIndex(candidate.Workflow, candidate, journalIndexRestore)
		if err != nil {
			return privateJournal{}, err
		}
		candidate.RestoreIndexPost = entries
	}
	if !privateJournalTemporaryExtends(previous, candidate) || candidate.validate() != nil {
		return privateJournal{}, fmt.Errorf("workspace migration: recovery journal progress does not extend durable authority")
	}
	return candidate, nil
}

func clonePrivateJournalForProgress(journal privateJournal) privateJournal {
	clone := journal
	clone.Writes = make([]privateWrite, len(journal.Writes))
	for i, write := range journal.Writes {
		clone.Writes[i] = write
		clone.Writes[i].Preimage = append([]byte(nil), write.Preimage...)
		clone.Writes[i].Postimage = append([]byte(nil), write.Postimage...)
	}
	clone.MigrationIndexPost = append([]recordsIndexEntry(nil), journal.MigrationIndexPost...)
	clone.ProofIndexPost = append([]recordsIndexEntry(nil), journal.ProofIndexPost...)
	clone.RestoreIndexPost = append([]recordsIndexEntry(nil), journal.RestoreIndexPost...)
	return clone
}

func setJournalProgressPostimage(journal *privateJournal, kind string, data []byte) error {
	for i := range journal.Writes {
		write := &journal.Writes[i]
		if write.Kind != kind {
			continue
		}
		if !write.PostExists || len(write.Postimage) != 0 {
			return fmt.Errorf("workspace migration: recovery journal progress write is invalid")
		}
		write.Postimage = append([]byte(nil), data...)
		return nil
	}
	return fmt.Errorf("workspace migration: recovery journal progress write is absent")
}

func replayPendingRecoveryJournalPayload(identity recoveryIdentity, descriptor recoveryPayloadDescriptor) ([]byte, error) {
	if descriptor.Purpose != journalKindRecoveryJournal || descriptor.JournalProgress == nil || !validDigest(descriptor.JournalPreviousSHA256) {
		return nil, fmt.Errorf("workspace migration: pending journal payload is not reconstructible")
	}
	previousDescriptor, previousData, err := loadRecoveryJournalPayloadByDigest(identity, descriptor.JournalPreviousSHA256)
	if err != nil {
		return nil, err
	}
	journal, err := parsePrivateJournal(identity.Workflow, identity.RunID, descriptor.Target, previousData, os.FileMode(previousDescriptor.Mode))
	if err != nil {
		return nil, err
	}
	candidate, err := replayRecoveryJournalProgress(journal, *descriptor.JournalProgress)
	if err != nil {
		return nil, err
	}
	intent, err := journalPreparationIntent(candidate)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		return nil, fmt.Errorf("workspace migration: pending journal payload differs from durable recovery identity")
	}
	data, err := journalData(candidate)
	if err != nil || int64(len(data)) != descriptor.Size || migrationDigest(data) != descriptor.SHA256 {
		return nil, fmt.Errorf("workspace migration: pending journal payload does not match its descriptor")
	}
	return data, nil
}

func loadRecoveryJournalPayloadByDigest(identity recoveryIdentity, digest string) (recoveryPayloadDescriptor, []byte, error) {
	target, err := journalPath(identity.Workflow, identity.RunID)
	if err != nil {
		return recoveryPayloadDescriptor{}, nil, err
	}
	active, data, exists, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryJournal, target)
	if err != nil {
		return recoveryPayloadDescriptor{}, nil, err
	}
	if exists && active.SHA256 == digest {
		return active, data, nil
	}
	dir, err := recoveryDescriptorDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return recoveryPayloadDescriptor{}, nil, err
	}
	entries, err := recoveryDirectoryEntries(identity.Workflow, dir, 0o700)
	if err != nil {
		return recoveryPayloadDescriptor{}, nil, err
	}
	var found recoveryPayloadDescriptor
	var foundData []byte
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), recoveryDescriptorHistoryPrefix) || entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		link, exists, err := readMigrationOpaqueSymlink(identity.Workflow, filepath.Join(dir, entry.Name()))
		if err != nil || !exists {
			return recoveryPayloadDescriptor{}, nil, fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
		}
		descriptor, err := parseRecoveryPayloadDescriptorLinkTarget(link)
		if err != nil || descriptor.validate(identity, journalKindRecoveryJournal, target) != nil {
			return recoveryPayloadDescriptor{}, nil, fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
		}
		name, err := recoveryDescriptorHistoryName(descriptor)
		if err != nil || name != entry.Name() {
			return recoveryPayloadDescriptor{}, nil, fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
		}
		if descriptor.SHA256 != digest {
			continue
		}
		if foundData != nil {
			return recoveryPayloadDescriptor{}, nil, fmt.Errorf("workspace migration: durable recovery journal predecessor is ambiguous")
		}
		data, err := loadRecoveryPayloadBlob(identity, descriptor)
		if err != nil {
			return recoveryPayloadDescriptor{}, nil, err
		}
		found, foundData = descriptor, data
	}
	if foundData == nil {
		return recoveryPayloadDescriptor{}, nil, fmt.Errorf("workspace migration: durable recovery journal predecessor is missing")
	}
	return found, foundData, nil
}

func recoveryPendingPayloadReplay(layout workspace.Layout, identity recoveryIdentity, intent preparationIntent, current *privateJournal) func(recoveryPayloadDescriptor) ([]byte, error) {
	var initial map[string][]byte
	initialPayload := func(purpose string) ([]byte, error) {
		if initial == nil {
			var err error
			initial, err = regeneratePreparationPayloads(layout, intent)
			if err != nil {
				return nil, err
			}
		}
		payload, ok := initial[purpose]
		if !ok {
			return nil, fmt.Errorf("workspace migration: pending recovery payload is not reconstructible")
		}
		return payload, nil
	}
	return func(descriptor recoveryPayloadDescriptor) ([]byte, error) {
		if err := descriptor.validate(identity, descriptor.Purpose, descriptor.Target); err != nil {
			return nil, err
		}
		switch descriptor.Purpose {
		case journalKindRecoveryStatus:
			return journalStatusData(intent)
		case journalKindRecoveryIntent:
			return preparationIntentData(intent)
		case journalKindRecoveryBundle:
			return initialPayload(journalKindRecoveryBundle)
		case journalKindRecoveryJournal:
			if descriptor.JournalProgress != nil {
				return replayPendingRecoveryJournalPayload(identity, descriptor)
			}
			if current != nil && !privateJournalInitialTemporary(*current) {
				return nil, fmt.Errorf("workspace migration: pending journal payload is not reconstructible")
			}
			return initialPayload(journalKindRecoveryJournal)
		case journalKindRecoveryCompletion:
			if current == nil {
				return nil, fmt.Errorf("workspace migration: pending completion payload is not reconstructible")
			}
			write, ok := journalWrite(*current, journalKindCompletion)
			if !ok || !write.PostExists || len(write.Postimage) == 0 {
				return nil, fmt.Errorf("workspace migration: pending completion payload is not reconstructible")
			}
			return append([]byte(nil), write.Postimage...), nil
		default:
			return nil, fmt.Errorf("workspace migration: pending recovery payload is not reconstructible")
		}
	}
}

func verifyPrivateJournalTemporaryProgress(layout workspace.Layout, journal privateJournal) error {
	if err := verifyJournalInputsLocked(layout, journal); err != nil {
		return err
	}
	if journal.MigrationCommit != "" {
		if err := verifyMigrationCommit(layout.WorkflowRoot, journal); err != nil {
			return err
		}
	} else if journal.ExpectedMigrationTree != "" {
		if err := verifyPrivateJournalStagedTree(layout.WorkflowRoot, journal.RecordsParent, journal.ExpectedMigrationTree); err != nil {
			return err
		}
	}
	if journal.ProofCommit != "" {
		if err := verifyProofCommit(layout.WorkflowRoot, journal); err != nil {
			return err
		}
	} else if journal.ExpectedProofTree != "" {
		if err := verifyPrivateJournalStagedTree(layout.WorkflowRoot, journal.MigrationCommit, journal.ExpectedProofTree); err != nil {
			return err
		}
	}
	if journal.RestoreCommit != "" {
		return verifyRestoreCommit(layout.WorkflowRoot, journal)
	}
	if journal.ExpectedRestoreTree != "" {
		return verifyPrivateJournalStagedTree(layout.WorkflowRoot, journal.RestoreParent, journal.ExpectedRestoreTree)
	}
	return nil
}

func verifyPrivateJournalStagedTree(root, parent, expected string) error {
	if !canonicalCommit.MatchString(parent) || !canonicalCommit.MatchString(expected) {
		return fmt.Errorf("private journal staged tree identity is invalid")
	}
	head, err := migrationGitText(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != parent {
		return fmt.Errorf("private journal staged tree parent differs from records HEAD")
	}
	tree, err := migrationGitText(root, "write-tree")
	if err != nil || tree != expected {
		return fmt.Errorf("private journal staged tree differs from the authenticated temporary")
	}
	return nil
}
