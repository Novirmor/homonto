package workspacemigration

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
)

func migrationDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func newRunID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return "migration-" + hex.EncodeToString(random[:]), nil
}

func newMigrationOwner() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(random[:]), nil
}

func validMigrationOwner(value string) bool {
	if len(value) != 32 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func journalPath(root, runID string) (string, error) {
	return migrationrecord.JournalPath(root, runID)
}

func journalRunDir(root, runID string) (string, error) {
	path, err := journalPath(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Dir(filepath.Dir(path)), nil
}

func journalStatusPath(root, runID string) (string, error) {
	return migrationrecord.JournalPath(root, runID)
}

func preparationIntentPath(root, runID string) (string, error) {
	runDir, err := journalRunDir(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(runDir, "private", "intent.json"), nil
}

func migrationGitDirectory(root string) (string, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("workspace migration: workflow root must be canonical and absolute")
	}
	path, err := migrationGitText(root, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", fmt.Errorf("workspace migration: resolve Git directory: %w", err)
	}
	path, err = absolutePath(path)
	if err != nil {
		return "", fmt.Errorf("workspace migration: Git directory is invalid")
	}
	return path, nil
}

func migrationRecoveryLockPath(root string) (string, error) {
	gitDir, err := migrationGitDirectory(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, "homonto-migration.lock"), nil
}

func recordsGitBackupPath(root, runID string) (string, error) {
	runDir, err := journalRunDir(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(runDir, "private", "records.git.bundle"), nil
}

func ensurePrivateJournalDirectory(root, runID string) (string, error) {
	path, err := journalPath(root, runID)
	if err != nil {
		return "", err
	}
	runDir, err := journalRunDir(root, runID)
	if err != nil {
		return "", err
	}
	migrations := filepath.Join(root, ".workflow", "migrations")
	if err := ensureMigrationDirectory(root, migrations, 0o755); err != nil {
		return "", err
	}
	if err := ensureMigrationDirectory(root, runDir, 0o700); err != nil {
		return "", err
	}
	privateDir := filepath.Dir(path)
	if err := ensureMigrationDirectory(root, privateDir, 0o700); err != nil {
		return "", err
	}
	if err := requirePrivateRecoveryDirectories(root, runID); err != nil {
		return "", err
	}
	return path, nil
}

func requirePrivateRecoveryDirectories(root, runID string) error {
	runDir, err := journalRunDir(root, runID)
	if err != nil {
		return err
	}
	path, err := journalPath(root, runID)
	if err != nil {
		return err
	}
	for _, dir := range []string{runDir, filepath.Dir(path)} {
		if err := requirePrivateRecoveryDirectory(root, dir); err != nil {
			return err
		}
	}
	return nil
}

func requirePrivateRecoveryDirectory(root, path string) error {
	parent, name, parentPath, err := openPinnedMigrationParent(root, path)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Lstat(name)
	if err != nil || !migrationRealDirectory(info) || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("workspace migration: private recovery directory is not a 0700 real directory")
	}
	return migrationPinnedParentStillCurrent(root, parentPath, parent)
}

func journalData(j privateJournal) ([]byte, error) {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func saveJournal(j privateJournal) error {
	if err := j.validate(); err != nil {
		return err
	}
	path, err := ensurePrivateJournalDirectory(j.Workflow, j.RunID)
	if err != nil {
		return fmt.Errorf("workspace migration: prepare private journal: %w", err)
	}
	data, err := journalData(j)
	if err != nil {
		return err
	}
	intent, err := journalPreparationIntent(j)
	if err != nil {
		return err
	}
	identity, err := loadRecoveryIdentity(j.Workflow, j.RunID)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		return fmt.Errorf("workspace migration: durable recovery identity is missing or differs from private journal")
	}
	if err := writePrivateRecoveryPayload(identity, journalKindRecoveryJournal, path, data); err != nil {
		return fmt.Errorf("workspace migration: write private journal: %w", err)
	}
	intentPath, err := preparationIntentPath(j.Workflow, j.RunID)
	if err != nil {
		return err
	}
	if err := removeMigrationOptionalRegular(j.Workflow, intentPath); err != nil {
		return fmt.Errorf("workspace migration: retire preparation intent: %w", err)
	}
	return nil
}

func loadJournal(root, runID string) (privateJournal, error) {
	path, err := journalPath(root, runID)
	if err != nil {
		return privateJournal{}, err
	}
	data, mode, exists, err := readMigrationOptionalRegular(root, path)
	if err != nil {
		return privateJournal{}, err
	}
	if !exists {
		return privateJournal{}, os.ErrNotExist
	}
	if err := requirePrivateRecoveryDirectories(root, runID); err != nil {
		return privateJournal{}, err
	}
	if status, err := migrationrecord.LoadJournalStatus(root, runID); err == nil && status.Phase == "preparing" {
		return loadInitialJournalRecoveryPayload(root, runID, path, data, mode)
	}
	identity, identityErr := loadRecoveryIdentity(root, runID)
	if identityErr == nil {
		authorized, err := recoveryPayloadAuthorizesData(identity, journalKindRecoveryJournal, path, data, mode)
		if err != nil || !authorized {
			return privateJournal{}, fmt.Errorf("workspace migration: private journal differs from durable recovery payload")
		}
	} else if !errors.Is(identityErr, os.ErrNotExist) {
		return privateJournal{}, identityErr
	}
	return parsePrivateJournal(root, runID, path, data, mode)
}

// loadInitialJournalRecoveryPayload recognizes the one interval where
// journal.json still contains the preparation status while an authenticated
// initial journal descriptor has already been promoted. The status and journal
// payloads must each validate against the same durable identity before the
// journal can become recovery authority.
func loadInitialJournalRecoveryPayload(root, runID, path string, statusData []byte, statusMode os.FileMode) (privateJournal, error) {
	intent, err := preparationIntentFromStatus(root, runID)
	if err != nil {
		return privateJournal{}, err
	}
	identity, err := loadRecoveryIdentity(root, runID)
	if errors.Is(err, os.ErrNotExist) {
		return privateJournal{}, os.ErrNotExist
	}
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		return privateJournal{}, fmt.Errorf("workspace migration: durable recovery identity is missing or differs from preparation status")
	}
	if err := validateRecoveryPayloadStore(identity, false); err != nil {
		return privateJournal{}, err
	}
	wantStatus, err := journalStatusData(intent)
	if err != nil || statusMode.Perm() != 0o600 || !bytes.Equal(statusData, wantStatus) {
		return privateJournal{}, fmt.Errorf("workspace migration: preparation status differs from durable recovery payload")
	}
	descriptor, journalData, exists, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryJournal, path)
	if err != nil {
		return privateJournal{}, err
	}
	if !exists {
		return privateJournal{}, os.ErrNotExist
	}
	journal, err := parsePrivateJournal(root, runID, path, journalData, os.FileMode(descriptor.Mode))
	if err != nil {
		return privateJournal{}, err
	}
	journalIntent, err := journalPreparationIntent(journal)
	if err != nil || journalIntent != intent || !privateJournalInitialTemporary(journal) {
		return privateJournal{}, fmt.Errorf("workspace migration: initial private journal differs from preparation state")
	}
	return journal, nil
}

func parsePrivateJournal(root, runID, path string, data []byte, mode os.FileMode) (privateJournal, error) {
	if err := requirePrivateRecoveryDirectories(root, runID); err != nil {
		return privateJournal{}, err
	}
	if mode.Perm()&0o077 != 0 {
		return privateJournal{}, fmt.Errorf("workspace migration: private journal is not a 0600 regular file")
	}
	var journal privateJournal
	if err := decodePrivateJSON(data, &journal); err != nil {
		return privateJournal{}, fmt.Errorf("workspace migration: invalid private journal: %w", err)
	}
	if err := journal.validate(); err != nil {
		return privateJournal{}, err
	}
	if journal.RunID != runID || journal.Workflow != root {
		return privateJournal{}, fmt.Errorf("workspace migration: private journal does not match its requested recovery directory")
	}
	if expected, err := journalPath(journal.Workflow, journal.RunID); err != nil || expected != path {
		return privateJournal{}, fmt.Errorf("workspace migration: private journal path does not match its identity")
	}
	if err := validateJournalIndexCaches(journal.Workflow, journal); err != nil {
		return privateJournal{}, err
	}
	if err := verifyRecordsGitBackup(journal.Workflow, journal.RecordsBackup); err != nil {
		return privateJournal{}, err
	}
	return journal, nil
}

func decodePrivateJSON(data []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing data")
		}
		return err
	}
	return nil
}

func preparationStatus(intent preparationIntent) *migrationrecord.PreparationStatus {
	return &migrationrecord.PreparationStatus{
		PlanHash:     intent.PlanHash,
		ConfigPath:   intent.ConfigPath,
		ConfigRoot:   intent.ConfigRoot,
		WorkflowRoot: intent.Workflow,
		ManifestPath: intent.ManifestPath,
		Owner:        intent.Owner,
		Guardian:     intent.Guardian,
	}
}

func journalStatusData(intent preparationIntent) ([]byte, error) {
	status := migrationrecord.JournalStatus{Version: migrationJournalVersion, RunID: intent.RunID, Phase: intent.Phase, Preparation: preparationStatus(intent)}
	data, err := json.Marshal(status)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func saveJournalStatus(intent preparationIntent) error {
	if err := intent.validate(); err != nil {
		return fmt.Errorf("workspace migration: invalid journal status identity")
	}
	data, err := journalStatusData(intent)
	if err != nil {
		return err
	}
	path, err := journalStatusPath(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	identity, err := loadRecoveryIdentity(intent.Workflow, intent.RunID)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		return fmt.Errorf("workspace migration: durable recovery identity is missing or differs from preparation status")
	}
	return writePrivateRecoveryPayload(identity, journalKindRecoveryStatus, path, data)
}
