package workspacemigration

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func safeBackupRefName(name string) bool {
	return strings.HasPrefix(name, "refs/") && !strings.ContainsAny(name, " ~^:?*[\\\x00") && !strings.Contains(name, "..") && !strings.HasSuffix(name, ".") && !strings.HasSuffix(name, "/") && strings.IndexFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

func validJournalPhase(phase string) bool {
	switch phase {
	case "prepared", "applying", "pending-finalization", "restoring", "restored", "complete":
		return true
	default:
		return false
	}
}

func (w privateWrite) validate(j privateJournal) error {
	if !filepath.IsAbs(w.Path) || filepath.Clean(w.Path) != w.Path || !w.PreExists && len(w.Preimage) != 0 || !w.PostExists && len(w.Postimage) != 0 || w.PreMode&^0o777 != 0 || w.PostMode&^0o777 != 0 {
		return fmt.Errorf("workspace migration: invalid private journal write")
	}
	switch w.Scope {
	case journalScopeRecords:
		if !pathWithin(j.Workflow, w.Path) || pathWithin(filepath.Join(j.Workflow, ".git"), w.Path) {
			return fmt.Errorf("workspace migration: invalid records journal write")
		}
	case journalScopeConfig:
		if !pathWithin(j.ConfigRoot, w.Path) {
			return fmt.Errorf("workspace migration: invalid config journal write")
		}
	case journalScopeOwner:
		validOwner := false
		for _, binding := range j.Bindings {
			if w.Path == filepath.Join(binding.GitDir, "homonto-owner") {
				validOwner = true
			}
		}
		if !validOwner {
			return fmt.Errorf("workspace migration: invalid ownership-token journal write")
		}
	default:
		return fmt.Errorf("workspace migration: unknown private journal write scope")
	}
	switch w.Kind {
	case journalKindState, journalKindIgnore, journalKindReceipt, journalKindRegistry, journalKindOwner, journalKindMarker, journalKindProof, journalKindCompletion:
	default:
		return fmt.Errorf("workspace migration: unknown private journal write kind")
	}
	return nil
}

// validateJournalAuthority proves that the private journal is only a concrete
// realization of the reviewed public plan. Recovery never treats a path or
// digest merely because an interrupted journal claims it.
func validateJournalAuthority(j privateJournal) error {
	if err := validateJournalBindings(j); err != nil {
		return err
	}
	if err := validateJournalSnapshots(j); err != nil {
		return err
	}

	writes := make(map[string]privateWrite, len(j.Writes))
	for _, write := range j.Writes {
		key := journalWriteKey(write.Scope, write.Kind, write.Path)
		if _, exists := writes[key]; exists {
			return fmt.Errorf("workspace migration: duplicate concrete journal operation")
		}
		writes[key] = write
	}
	expectedWrites := 0
	for _, operation := range j.Plan.Prospective.Operations {
		if operation.Scope == journalScopeRecovery || operation.Scope == journalScopeRecordsIndex {
			continue
		}
		path, err := resolvePlannedOperationPath(operation.Path, j.RunID)
		if err != nil {
			return err
		}
		expectedWrites++
		key := journalWriteKey(operation.Scope, operation.Kind, path)
		write, ok := writes[key]
		if !ok {
			return fmt.Errorf("workspace migration: private journal omits reviewed operation %s", path)
		}
		if write.PreExists != operation.PreExists || write.PreMode != operation.PreMode || write.PostExists != operation.PostExists || write.PostMode != operation.PostMode {
			return fmt.Errorf("workspace migration: private journal operation metadata differs at %s", path)
		}
		if operation.DynamicRule == "" {
			if (operation.PreExists && migrationDigest(write.Preimage) != operation.PreSHA256) || (operation.PostExists && migrationDigest(write.Postimage) != operation.PostSHA256) {
				return fmt.Errorf("workspace migration: private journal operation digest differs at %s", path)
			}
			continue
		}
		if err := validateDynamicJournalWrite(j, operation, write); err != nil {
			return err
		}
	}
	if len(writes) != expectedWrites {
		return fmt.Errorf("workspace migration: private journal contains an operation outside the reviewed authority")
	}
	return validatePrivateRecoveryOperationAuthority(j)
}

func validatePrivateRecoveryOperationAuthority(journal privateJournal) error {
	expected := plannedPrivateRecoveryOperations(journal.Plan)
	sort.Slice(expected, func(i, j int) bool {
		if expected[i].Scope != expected[j].Scope {
			return expected[i].Scope < expected[j].Scope
		}
		if expected[i].Kind != expected[j].Kind {
			return expected[i].Kind < expected[j].Kind
		}
		return expected[i].Path < expected[j].Path
	})
	actual := make([]ProspectiveOperation, 0, len(expected))
	for _, operation := range journal.Plan.Prospective.Operations {
		if operation.Scope == journalScopeRecovery {
			actual = append(actual, operation)
		}
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("workspace migration: private recovery operation authority differs from the reviewed plan")
	}
	runDir, err := journalRunDir(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	journalPath, err := journalPath(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	intentPath, err := preparationIntentPath(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	bundlePath, err := recordsGitBackupPath(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	identityPath, err := recoveryIdentityPath(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	storePath, err := recoveryStorePath(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	blobPath, err := recoveryBlobDirectory(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	descriptorPath, err := recoveryDescriptorDirectory(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	retiredPath, err := recoveryRetiredPath(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	completionPath, err := migrationrecord.CompletionWitnessPath(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	want := map[string]string{
		journalKindRecoveryRunDirectory:     runDir,
		journalKindRecoveryPrivateDirectory: filepath.Dir(journalPath),
		journalKindRecoveryIdentity:         identityPath,
		journalKindRecoveryStoreDirectory:   storePath,
		journalKindRecoveryBlobDirectory:    blobPath,
		journalKindRecoveryDescriptorDir:    descriptorPath,
		journalKindRecoveryDescriptor:       descriptorPath,
		journalKindRecoveryBlob:             blobPath,
		journalKindRecoveryRetired:          retiredPath,
		journalKindRecoveryCompletion:       completionPath,
		journalKindRecoveryStatus:           journalPath,
		journalKindRecoveryJournal:          journalPath,
		journalKindRecoveryIntent:           intentPath,
		journalKindRecoveryBundle:           bundlePath,
	}
	for _, operation := range actual {
		path, err := resolvePlannedOperationPath(operation.Path, journal.RunID)
		if err != nil || want[operation.Kind] != path || !pathWithin(journal.Workflow, path) {
			return fmt.Errorf("workspace migration: private recovery operation resolves outside its reviewed destination")
		}
	}
	if journal.RecordsBackup.BundlePath != bundlePath {
		return fmt.Errorf("workspace migration: records Git backup is outside its reviewed private destination")
	}
	return nil
}

func journalWriteKey(scope, kind, path string) string {
	return scope + "\x00" + kind + "\x00" + path
}

func validateJournalBindings(j privateJournal) error {
	expected, err := plannedBindingTemplates(j.Plan)
	if err != nil {
		return err
	}
	if len(j.Bindings) != len(expected) {
		return fmt.Errorf("workspace migration: private journal binding set differs from reviewed plan")
	}
	actual := make(map[string]workspace.MigrationBinding, len(j.Bindings))
	for _, binding := range j.Bindings {
		if len(binding.Ownership) != 32 || binding.Ownership != strings.ToLower(binding.Ownership) {
			return fmt.Errorf("workspace migration: private journal binding token is invalid")
		}
		if _, err := hex.DecodeString(binding.Ownership); err != nil {
			return fmt.Errorf("workspace migration: private journal binding token is invalid")
		}
		key := binding.Workflow + "\x00" + binding.Change + "\x00" + binding.Repo
		if _, exists := actual[key]; exists {
			return fmt.Errorf("workspace migration: duplicate private journal binding")
		}
		actual[key] = binding
	}
	for _, binding := range expected {
		key := binding.Workflow + "\x00" + binding.Change + "\x00" + binding.Repo
		actualBinding, ok := actual[key]
		if !ok || actualBinding.Workflow != binding.Workflow || actualBinding.Change != binding.Change || actualBinding.StateID != binding.StateID || actualBinding.Repo != binding.Repo || actualBinding.Path != binding.Path || actualBinding.CommonDir != binding.CommonDir || actualBinding.GitDir != binding.GitDir || actualBinding.Branch != binding.Branch || actualBinding.BaseRef != binding.BaseRef || actualBinding.BaseTarget != binding.BaseTarget || actualBinding.BaseCommit != binding.BaseCommit {
			return fmt.Errorf("workspace migration: private journal binding differs from reviewed plan")
		}
	}
	return nil
}

func validateJournalSnapshots(j privateJournal) error {
	expected := plannedSnapshotRefs(j.Plan)
	if len(j.Snapshots) != len(expected) {
		return fmt.Errorf("workspace migration: private snapshot set differs from reviewed plan")
	}
	actual := make(map[string]privateSnapshot, len(j.Snapshots))
	for _, snapshot := range j.Snapshots {
		if _, exists := actual[snapshot.Path]; exists {
			return fmt.Errorf("workspace migration: duplicate private backup snapshot")
		}
		actual[snapshot.Path] = snapshot
	}
	for _, ref := range expected {
		snapshot, ok := actual[ref.Path]
		if !ok || snapshot.SHA256 != ref.SHA256 || migrationDigest(snapshot.Data) != ref.SHA256 {
			return fmt.Errorf("workspace migration: private snapshot differs from reviewed input at %s", ref.Path)
		}
	}
	manifest, ok := actual[j.Plan.Manifest.Path]
	if !ok || manifest.SHA256 != j.Plan.Manifest.SHA256 || migrationDigest(manifest.Data) != j.Plan.Manifest.SHA256 {
		return fmt.Errorf("workspace migration: private manifest snapshot differs from reviewed plan")
	}
	return nil
}

func validateDynamicJournalWrite(j privateJournal, operation ProspectiveOperation, write privateWrite) error {
	if !write.PostExists {
		return fmt.Errorf("workspace migration: dynamic operation has no postimage at %s", write.Path)
	}
	switch operation.DynamicRule {
	case dynamicReceiptRule:
		receipt, err := migrationrecord.ParseReceipt(write.Postimage)
		if err != nil || receipt.RunID != j.RunID || receipt.PlanHash != j.PlanHash {
			return fmt.Errorf("workspace migration: private journal receipt differs from reviewed authority")
		}
	case dynamicRegistryRule:
		if len(write.Postimage) == 0 {
			return fmt.Errorf("workspace migration: private journal registry is empty")
		}
	case dynamicOwnerRule:
		for _, binding := range j.Bindings {
			if write.Path == filepath.Join(binding.GitDir, "homonto-owner") && bytes.Equal(write.Postimage, []byte(binding.Ownership)) {
				return nil
			}
		}
		return fmt.Errorf("workspace migration: private journal owner token differs from reviewed binding")
	case dynamicProofRule:
		// A prepared journal reserves the exact proof path before its migration
		// commit exists. Once materialized, the proof must be strict and tied to
		// that commit; an empty reserved postimage is never writable directly.
		if len(write.Postimage) == 0 && j.ProofCommit == "" && j.ExpectedProofTree == "" {
			return nil
		}
		proof, err := migrationrecord.ParseCommitProof(write.Postimage, j.RunID)
		if err != nil {
			return fmt.Errorf("workspace migration: private journal proof is invalid: %w", err)
		}
		if proof.MigrationCommit != j.MigrationCommit || proof.Parent != j.RecordsParent || proof.Tree != j.ExpectedMigrationTree || proof.MessageSHA256 != j.MigrationMessageSHA256 {
			return fmt.Errorf("workspace migration: private journal proof differs from reviewed authority")
		}
	case dynamicCompletionRule:
		if len(write.Postimage) == 0 && j.Phase != "complete" {
			return nil
		}
		expected, err := completionWitnessForJournal(j)
		if err != nil {
			return err
		}
		actual, err := migrationrecord.ParseCompletionWitness(write.Postimage)
		if err != nil || actual != expected {
			return fmt.Errorf("workspace migration: private journal completion witness differs from final authorization")
		}
	default:
		return fmt.Errorf("workspace migration: unknown dynamic journal operation")
	}
	return nil
}

func completionWitnessForJournal(journal privateJournal) (migrationrecord.CompletionWitness, error) {
	receipt, ok := journalWrite(journal, journalKindReceipt)
	if !ok || len(receipt.Postimage) == 0 {
		return migrationrecord.CompletionWitness{}, fmt.Errorf("workspace migration: completion authorization lacks its reviewed receipt")
	}
	proof, ok := journalWrite(journal, journalKindProof)
	if !ok || len(proof.Postimage) == 0 {
		return migrationrecord.CompletionWitness{}, fmt.Errorf("workspace migration: completion authorization lacks its reviewed commit proof")
	}
	witness, err := migrationrecord.NewCompletionWitness(journal.RunID, receipt.Postimage, proof.Postimage)
	if err != nil {
		return migrationrecord.CompletionWitness{}, err
	}
	if witness.PlanHash != journal.PlanHash || witness.MigrationCommit != journal.MigrationCommit || witness.MigrationParent != journal.RecordsParent || witness.MigrationTree != journal.ExpectedMigrationTree {
		return migrationrecord.CompletionWitness{}, fmt.Errorf("workspace migration: completion authorization differs from the reviewed transaction")
	}
	return witness, nil
}

func validateJournalConcreteAuthority(layout workspace.Layout, journal privateJournal) error {
	if err := validateJournalAuthority(journal); err != nil {
		return err
	}
	registry, ok := journalWrite(journal, journalKindRegistry)
	if !ok {
		return fmt.Errorf("workspace migration: reviewed registry operation is absent from private journal")
	}
	if err := workspace.ValidateMigrationRegistryData(layout, journal.RunID, journal.Bindings, registry.Postimage); err != nil {
		return fmt.Errorf("workspace migration: private journal registry differs from reviewed bindings: %w", err)
	}
	receiptWrite, ok := journalWrite(journal, journalKindReceipt)
	if !ok {
		return fmt.Errorf("workspace migration: reviewed receipt operation is absent from private journal")
	}
	receipt, err := migrationrecord.ParseReceipt(receiptWrite.Postimage)
	if err != nil {
		return err
	}
	if err := validateReceiptAgainstJournal(journal, receipt); err != nil {
		return err
	}
	return validateJournalCommitAuthority(journal)
}

func validateJournalCommitAuthority(journal privateJournal) error {
	if len(journal.Plan.Prospective.Commits) != 3 {
		return fmt.Errorf("workspace migration: reviewed commit authority is incomplete")
	}
	migration := journal.Plan.Prospective.Commits[0]
	proof := journal.Plan.Prospective.Commits[1]
	restore := journal.Plan.Prospective.Commits[2]
	if migration.Kind != "migration" || migration.Parent != journal.RecordsParent || migration.ParentRule != "exact_plan_records_head" || migration.MessageTemplate != "Migrate legacy workflow records to schema-2 ownership ({run_id})" || migration.TreeRule != "git_write_tree_after_exact_paths" || proof.Kind != "proof" || proof.Parent != "{migration_commit}" || proof.ParentRule != "migration_commit" || proof.MessageTemplate != "Record schema-2 migration commit proof ({run_id})" || proof.TreeRule != "git_write_tree_after_exact_paths" || restore.Kind != "restore" || restore.Parent != "{latest_records_migration_commit}" || restore.ParentRule != "migration_or_proof_commit" || restore.MessageTemplate != "Restore interrupted schema-2 migration ({run_id})" || restore.TreeRule != "git_write_tree_after_exact_paths" {
		return fmt.Errorf("workspace migration: private journal commit authority differs from reviewed plan")
	}
	if journal.MigrationMessage != strings.ReplaceAll(migration.MessageTemplate, runIDPlaceholder, journal.RunID) || journal.ProofMessage != strings.ReplaceAll(proof.MessageTemplate, runIDPlaceholder, journal.RunID) || journal.RestoreMessage != strings.ReplaceAll(restore.MessageTemplate, runIDPlaceholder, journal.RunID) {
		return fmt.Errorf("workspace migration: private journal messages differ from reviewed commit authority")
	}
	actualMigration, err := journalRecordPaths(journal, journalKindState, journalKindIgnore, journalKindReceipt)
	if err != nil {
		return err
	}
	actualProof, err := journalRecordPaths(journal, journalKindProof)
	if err != nil {
		return err
	}
	actualRestore := append(append([]string{}, actualMigration...), actualProof...)
	sort.Strings(actualRestore)
	if !sameStringSet(actualMigration, resolveCommitPaths(migration.Paths, journal.RunID)) || !sameStringSet(actualProof, resolveCommitPaths(proof.Paths, journal.RunID)) || !sameStringSet(actualRestore, resolveCommitPaths(restore.Paths, journal.RunID)) {
		return fmt.Errorf("workspace migration: private journal commit paths differ from reviewed authority")
	}
	return nil
}

func resolveCommitPaths(paths []string, runID string) []string {
	resolved := make([]string, len(paths))
	for i, path := range paths {
		resolved[i] = strings.ReplaceAll(path, runIDPlaceholder, runID)
	}
	sort.Strings(resolved)
	return resolved
}

func validateReceiptAgainstJournal(journal privateJournal, receipt migrationrecord.Receipt) error {
	if receipt.RunID != journal.RunID || receipt.PlanHash != journal.PlanHash || receipt.Config.Path != journal.Plan.Config.Path || receipt.Config.SHA256 != journal.Plan.Config.SHA256 || receipt.Manifest.Path != journal.Plan.Manifest.Path || receipt.Manifest.SHA256 != journal.Plan.Manifest.SHA256 {
		return fmt.Errorf("workspace migration: receipt identity differs from private journal")
	}
	marker, ok := journalWrite(journal, journalKindMarker)
	if !ok || receipt.LayoutMarker.Path != marker.Path || receipt.LayoutMarker.SHA256 != migrationDigest(marker.Postimage) {
		return fmt.Errorf("workspace migration: receipt layout marker differs from private journal")
	}
	registry, ok := journalWrite(journal, journalKindRegistry)
	if !ok || receipt.Registry.Path != registry.Path || receipt.Registry.SHA256 != migrationDigest(registry.Postimage) {
		return fmt.Errorf("workspace migration: receipt registry differs from private journal")
	}
	proof, ok := journalWrite(journal, journalKindProof)
	if !ok {
		return fmt.Errorf("workspace migration: receipt proof path differs from private journal")
	}
	rel, err := filepath.Rel(journal.Workflow, proof.Path)
	if err != nil || receipt.CommitProofPath != filepath.ToSlash(rel) {
		return fmt.Errorf("workspace migration: receipt proof path differs from private journal")
	}
	if len(receipt.RecordWrites) != len(journal.Plan.RecordWrites) {
		return fmt.Errorf("workspace migration: receipt record-write set differs from reviewed plan")
	}
	actual := make(map[string]migrationrecord.RecordWrite, len(receipt.RecordWrites))
	for _, write := range receipt.RecordWrites {
		if _, exists := actual[write.Path]; exists {
			return fmt.Errorf("workspace migration: receipt contains duplicate record write")
		}
		actual[write.Path] = write
	}
	for _, write := range journal.Plan.RecordWrites {
		public, ok := actual[write.Path]
		if !ok || public.PreSHA256 != write.PreSHA256 || public.PostSHA256 != write.PostSHA256 || public.Action != write.Action || !reflect.DeepEqual(public.Fields, write.ChangedFields) || !sameLegacyConfig(public.LegacyConfig, write.LegacyConfig) {
			return fmt.Errorf("workspace migration: receipt write differs from reviewed plan at %s", write.Path)
		}
	}
	expectedRetired, err := expectedRetiredRecords(journal.Plan)
	if err != nil {
		return err
	}
	actualRetired := make([]migrationrecord.RetiredRecord, len(receipt.Retired))
	copy(actualRetired, receipt.Retired)
	sort.Slice(actualRetired, func(i, j int) bool {
		return actualRetired[i].Path+"\x00"+actualRetired[i].ID < actualRetired[j].Path+"\x00"+actualRetired[j].ID
	})
	if !reflect.DeepEqual(actualRetired, expectedRetired) {
		return fmt.Errorf("workspace migration: receipt retired-record set differs from reviewed plan")
	}
	if err := receipt.ValidateRetiredCorrespondence(journal.Workflow); err != nil {
		return fmt.Errorf("workspace migration: receipt retired-record correspondence is invalid: %w", err)
	}
	if len(receipt.Bindings) != len(journal.Bindings) {
		return fmt.Errorf("workspace migration: receipt binding set differs from private journal")
	}
	bindings := make(map[string]migrationrecord.Binding, len(receipt.Bindings))
	for _, binding := range receipt.Bindings {
		bindings[binding.Workflow+"\x00"+binding.Change+"\x00"+binding.Repo] = binding
	}
	for _, binding := range journal.Bindings {
		public, ok := bindings[binding.Workflow+"\x00"+binding.Change+"\x00"+binding.Repo]
		if !ok || public.StateID != binding.StateID || public.Path != binding.Path || public.CommonDir != binding.CommonDir || public.GitDir != binding.GitDir || public.Branch != binding.Branch || public.BaseRef != binding.BaseRef || public.BaseTarget != binding.BaseTarget || public.BaseCommit != binding.BaseCommit || public.OwnershipHash != migrationDigest([]byte(binding.Ownership)) {
			return fmt.Errorf("workspace migration: receipt binding differs from private journal")
		}
	}
	return nil
}

func sameLegacyConfig(public *migrationrecord.LegacyConfig, planned *ontostate.LegacyConfig) bool {
	if planned == nil {
		return public == nil
	}
	if public == nil {
		return false
	}
	return public.BaseRef == planned.BaseRef && public.BaseBranch == planned.BaseBranch && public.Provenance == planned.Provenance
}

func journalWriteRoot(j privateJournal, write privateWrite) (string, error) {
	switch write.Scope {
	case journalScopeRecords:
		return j.Workflow, nil
	case journalScopeConfig:
		return j.ConfigRoot, nil
	case journalScopeOwner:
		for _, binding := range j.Bindings {
			if write.Path == filepath.Join(binding.GitDir, "homonto-owner") {
				return binding.CommonDir, nil
			}
		}
	}
	return "", fmt.Errorf("workspace migration: unknown journal write root")
}

func journalWriteTempAuthority(j privateJournal, write privateWrite, root string, data []byte, mode os.FileMode, post bool) (migrationTempAuthority, error) {
	var operation *ProspectiveOperation
	for index := range j.Plan.Prospective.Operations {
		candidate := &j.Plan.Prospective.Operations[index]
		path, err := resolvePlannedOperationPath(candidate.Path, j.RunID)
		if err != nil {
			return migrationTempAuthority{}, err
		}
		if candidate.Scope == write.Scope && candidate.Kind == write.Kind && path == write.Path {
			operation = candidate
			break
		}
	}
	if operation == nil || operation.TempNameRule != dynamicTempNameRule {
		return migrationTempAuthority{}, fmt.Errorf("workspace migration: journal write lacks planned temporary-file authority at %s", write.Path)
	}
	tempParent, err := resolvePlannedOperationPath(operation.TempParent, j.RunID)
	if err != nil || tempParent != filepath.Dir(write.Path) {
		return migrationTempAuthority{}, fmt.Errorf("workspace migration: journal write temporary parent differs at %s", write.Path)
	}
	if operation.DynamicRule == "" && (operation.PreExists != write.PreExists || operation.PreExists && operation.PreSHA256 != migrationDigest(write.Preimage) || operation.PreMode != write.PreMode || operation.PostExists != write.PostExists || operation.PostExists && operation.PostSHA256 != migrationDigest(write.Postimage) || operation.PostMode != write.PostMode) {
		return migrationTempAuthority{}, fmt.Errorf("workspace migration: journal write differs from planned temporary-file authority at %s", write.Path)
	}
	direction := "apply-post"
	if !post {
		direction = "restore-pre"
	}
	return migrationTempAuthority{
		root:       root,
		runID:      j.RunID,
		owner:      j.IntentOwner,
		scope:      write.Scope,
		kind:       write.Kind,
		target:     write.Path,
		direction:  direction,
		preExists:  write.PreExists,
		preimage:   append([]byte(nil), write.Preimage...),
		preMode:    os.FileMode(write.PreMode),
		postExists: write.PostExists,
		postimage:  append([]byte(nil), write.Postimage...),
		postMode:   os.FileMode(write.PostMode),
		data:       append([]byte(nil), data...),
		mode:       mode,
	}, nil
}

func cleanupJournalTemporaryArtifacts(j privateJournal) error {
	seen := map[string]bool{}
	for _, write := range j.Writes {
		root, err := journalWriteRoot(j, write)
		if err != nil {
			return err
		}
		for _, post := range []bool{true, false} {
			exists, data, mode := write.PostExists, write.Postimage, os.FileMode(write.PostMode)
			if !post {
				exists, data, mode = write.PreExists, write.Preimage, os.FileMode(write.PreMode)
			}
			if !exists {
				continue
			}
			authority, err := journalWriteTempAuthority(j, write, root, data, mode, post)
			if err != nil {
				return err
			}
			name, err := authority.name()
			if err != nil {
				return err
			}
			key := root + "\x00" + filepath.Dir(write.Path) + "\x00" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			if err := removeMigrationAuthorizedTemp(authority); err != nil {
				return err
			}
		}
	}
	return nil
}

func readOptionalRegular(root, path string) ([]byte, os.FileMode, bool, error) {
	return readMigrationOptionalRegular(root, path)
}

func writeMatches(write privateWrite, data []byte, mode os.FileMode, exists bool, post bool) bool {
	wantExists, wantData, wantMode := write.PreExists, write.Preimage, os.FileMode(write.PreMode)
	if post {
		wantExists, wantData, wantMode = write.PostExists, write.Postimage, os.FileMode(write.PostMode)
	}
	return exists == wantExists && (!exists || (bytes.Equal(data, wantData) && mode.Perm() == wantMode.Perm()))
}

func applyJournalWrite(j privateJournal, write privateWrite, post bool) (bool, error) {
	root, err := journalWriteRoot(j, write)
	if err != nil {
		return false, err
	}
	data, mode, exists, err := readOptionalRegular(root, write.Path)
	if err != nil {
		return false, err
	}
	if writeMatches(write, data, mode, exists, post) {
		return false, nil
	}
	if !writeMatches(write, data, mode, exists, !post) {
		return false, fmt.Errorf("workspace migration: recovery conflict at %s", write.Path)
	}
	expected := &migrationRegularExpectation{
		exists: exists,
		data:   append([]byte(nil), data...),
		mode:   mode,
	}
	wantExists, wantData, wantMode := write.PostExists, write.Postimage, os.FileMode(write.PostMode)
	if !post {
		wantExists, wantData, wantMode = write.PreExists, write.Preimage, os.FileMode(write.PreMode)
	}
	if wantExists {
		if post && write.Scope == journalScopeRecords && write.Kind == journalKindCompletion {
			identity, err := loadRecoveryIdentity(j.Workflow, j.RunID)
			intent, intentErr := journalPreparationIntent(j)
			if err != nil || intentErr != nil || !recoveryIdentityMatchesIntent(identity, intent) {
				return false, fmt.Errorf("workspace migration: completion witness lacks durable recovery identity")
			}
			if err := writePrivateRecoveryPayloadExpected(identity, journalKindRecoveryCompletion, write.Path, wantData, expected); err != nil {
				return false, err
			}
			if err := migrationAfterWrite(write.Path); err != nil {
				return true, err
			}
			return true, nil
		}
		authority, err := journalWriteTempAuthority(j, write, root, wantData, wantMode, post)
		if err != nil {
			return false, err
		}
		if err := writeMigrationRegularAuthorized(root, write.Path, wantData, wantMode, expected, authority); err != nil {
			return false, err
		}
	} else {
		if err := removeMigrationOptionalRegularExpected(root, write.Path, expected); err != nil {
			return false, err
		}
	}
	if err := migrationAfterWrite(write.Path); err != nil {
		return true, err
	}
	return true, nil
}

func sortedJournalWrites(writes []privateWrite) []privateWrite {
	out := append([]privateWrite(nil), writes...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		return out[i].Path < out[j].Path
	})
	return out
}
