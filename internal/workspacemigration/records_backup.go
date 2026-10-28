package workspacemigration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func plannedRecordsGitBackup(root, runID string, planned RecordsGit) (recordsGitBackup, error) {
	head, err := migrationGitText(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != planned.Head {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: records HEAD changed before private backup")
	}
	indexPath, err := migrationGitText(root, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil || !filepath.IsAbs(indexPath) || filepath.Clean(indexPath) != indexPath {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: records index is unavailable for private backup")
	}
	indexData, err := readRealRegularFile(indexPath)
	if err != nil || migrationDigest(indexData) != planned.IndexSHA256 {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: records index changed before private backup")
	}
	indexInfo, err := os.Lstat(indexPath)
	if err != nil || !indexInfo.Mode().IsRegular() || indexInfo.Mode()&os.ModeSymlink != 0 {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: records index is unsafe for private backup")
	}
	refs, err := recordsGitReferences(root)
	if err != nil {
		return recordsGitBackup{}, err
	}
	bundlePath, err := recordsGitBackupPath(root, runID)
	if err != nil {
		return recordsGitBackup{}, err
	}
	return recordsGitBackup{
		BundlePath:  bundlePath,
		IndexPath:   indexPath,
		IndexSHA256: migrationDigest(indexData),
		IndexMode:   uint32(indexInfo.Mode().Perm()),
		IndexData:   append([]byte(nil), indexData...),
		Head:        head,
		Refs:        refs,
	}, nil
}

// captureRecordsGitBackup creates the complete records-Git disaster-recovery
// bundle before any registry, state, receipt, proof, marker, or owner-token
// write. It deliberately does not modify the records index, refs, hooks, or
// checkout files.
func captureRecordsGitBackup(root string, intent preparationIntent, planned RecordsGit) (recordsGitBackup, error) {
	backup, err := plannedRecordsGitBackup(root, intent.RunID, planned)
	if err != nil {
		return recordsGitBackup{}, err
	}
	if _, err := ensurePrivateJournalDirectory(root, intent.RunID); err != nil {
		return recordsGitBackup{}, err
	}
	identity, err := loadRecoveryIdentity(root, intent.RunID)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: durable recovery identity is missing or differs from private backup")
	}
	bundle, bundleMode, exists, err := readMigrationOptionalRegular(root, backup.BundlePath)
	if err != nil {
		return recordsGitBackup{}, err
	}
	if exists {
		if bundleMode.Perm() != 0o600 {
			return recordsGitBackup{}, fmt.Errorf("workspace migration: private records Git backup is not a 0600 regular file")
		}
		descriptor, expected, found, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryBundle, backup.BundlePath)
		if err != nil || !found || descriptor.Target != backup.BundlePath || !bytes.Equal(bundle, expected) || bundleMode.Perm() != os.FileMode(descriptor.Mode).Perm() {
			return recordsGitBackup{}, fmt.Errorf("workspace migration: private records Git backup differs from durable recovery payload")
		}
		backup.BundleSHA256 = migrationDigest(bundle)
		if err := verifyRecordsGitBackup(root, backup); err != nil {
			return recordsGitBackup{}, err
		}
		return backup, nil
	}
	bundle, err = migrationGitOutput(root, "bundle", "create", "-", "HEAD", "--all")
	if err != nil {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: create records Git backup: %w", err)
	}
	if err := writePrivateRecoveryPayload(identity, journalKindRecoveryBundle, backup.BundlePath, bundle); err != nil {
		return recordsGitBackup{}, err
	}
	backup.BundleSHA256 = migrationDigest(bundle)
	if err := verifyRecordsGitBackup(root, backup); err != nil {
		return recordsGitBackup{}, err
	}
	return backup, nil
}

// regenerateRecordsGitBackup creates no files. It rechecks the exact planned
// records identity, then proves the generated bundle covers that identity
// before a staged blob prefix may be treated as owned recovery material.
func regenerateRecordsGitBackup(root string, intent preparationIntent, planned RecordsGit) (recordsGitBackup, []byte, error) {
	backup, err := plannedRecordsGitBackup(root, intent.RunID, planned)
	if err != nil {
		return recordsGitBackup{}, nil, err
	}
	identity, err := loadRecoveryIdentity(root, intent.RunID)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		return recordsGitBackup{}, nil, fmt.Errorf("workspace migration: durable recovery identity is missing or differs from private backup")
	}
	bundle, err := migrationGitOutput(root, "bundle", "create", "-", "HEAD", "--all")
	if err != nil {
		return recordsGitBackup{}, nil, fmt.Errorf("workspace migration: create records Git backup: %w", err)
	}
	backup.BundleSHA256 = migrationDigest(bundle)
	if err := verifyRecordsGitBackupData(root, backup, bundle); err != nil {
		return recordsGitBackup{}, nil, err
	}
	return backup, bundle, nil
}

func recordsGitReferences(root string) ([]recordsGitRef, error) {
	data, err := workspace.ReadGit(root, "for-each-ref", "--format=%(refname) %(objectname)", "refs")
	if err != nil {
		return nil, err
	}
	refs := []recordsGitRef{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !safeBackupRefName(fields[0]) || !canonicalCommit.MatchString(fields[1]) || seen[fields[0]] {
			return nil, fmt.Errorf("workspace migration: records refs cannot be backed up safely")
		}
		seen[fields[0]] = true
		refs = append(refs, recordsGitRef{Name: fields[0], Object: fields[1]})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs, nil
}

func verifyRecordsGitBackup(root string, backup recordsGitBackup) error {
	data, mode, exists, err := readMigrationOptionalRegular(root, backup.BundlePath)
	if err != nil || !exists || mode.Perm() != 0o600 {
		return fmt.Errorf("workspace migration: private records Git backup is not a 0600 regular file")
	}
	runID := filepath.Base(filepath.Dir(filepath.Dir(backup.BundlePath)))
	if migrationrecord.SafeRunID(runID) {
		identity, identityErr := loadRecoveryIdentity(root, runID)
		if identityErr == nil {
			descriptor, expected, found, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryBundle, backup.BundlePath)
			if err != nil || !found || descriptor.Target != backup.BundlePath || mode.Perm() != os.FileMode(descriptor.Mode).Perm() || !bytes.Equal(data, expected) {
				return fmt.Errorf("workspace migration: private records Git backup differs from durable recovery payload")
			}
		} else if !errors.Is(identityErr, os.ErrNotExist) {
			return identityErr
		}
	}
	return verifyRecordsGitBackupData(root, backup, data)
}

func verifyRecordsGitBackupData(root string, backup recordsGitBackup, bundle []byte) error {
	if migrationDigest(bundle) != backup.BundleSHA256 || migrationDigest(backup.IndexData) != backup.IndexSHA256 {
		return fmt.Errorf("workspace migration: private records Git backup digest differs")
	}
	if _, err := migrationGitInput(root, bundle, "bundle", "verify", "-"); err != nil {
		return fmt.Errorf("workspace migration: private records Git backup verification failed: %w", err)
	}
	data, err := migrationGitInput(root, bundle, "bundle", "list-heads", "-")
	if err != nil {
		return fmt.Errorf("workspace migration: private records Git backup heads are unreadable: %w", err)
	}
	heads := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !canonicalCommit.MatchString(fields[0]) || (fields[1] != "HEAD" && !safeBackupRefName(fields[1])) {
			return fmt.Errorf("workspace migration: private records Git backup head list is invalid")
		}
		if _, exists := heads[fields[1]]; exists {
			return fmt.Errorf("workspace migration: private records Git backup head list is duplicated")
		}
		heads[fields[1]] = fields[0]
	}
	if heads["HEAD"] != backup.Head {
		return fmt.Errorf("workspace migration: private records Git backup does not cover HEAD")
	}
	for _, ref := range backup.Refs {
		if heads[ref.Name] != ref.Object {
			return fmt.Errorf("workspace migration: private records Git backup does not cover ref %s", ref.Name)
		}
	}
	return nil
}

func validateRecordsGitBackup(j privateJournal) error {
	backup := j.RecordsBackup
	wantPath, err := recordsGitBackupPath(j.Workflow, j.RunID)
	if err != nil || backup.BundlePath != wantPath || !validDigest(backup.BundleSHA256) || backup.IndexPath != filepath.Join(j.Plan.RecordsGit.GitCommonDir, "index") || !validDigest(backup.IndexSHA256) || backup.IndexSHA256 != j.RecordsIndexSHA256 || backup.IndexSHA256 != migrationDigest(backup.IndexData) || backup.IndexMode == 0 || backup.IndexMode&^0o777 != 0 || backup.Head != j.RecordsParent || !canonicalCommit.MatchString(backup.Head) || backup.Refs == nil {
		return fmt.Errorf("workspace migration: invalid records Git backup")
	}
	seen := map[string]bool{}
	for _, ref := range backup.Refs {
		if !safeBackupRefName(ref.Name) || !canonicalCommit.MatchString(ref.Object) || seen[ref.Name] {
			return fmt.Errorf("workspace migration: invalid records Git backup ref")
		}
		seen[ref.Name] = true
	}
	return nil
}
