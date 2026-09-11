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
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workspace"
)

const migrationJournalVersion = migrationrecord.JournalVersion

const migrationIgnore = "# Local schema-2 migration status envelopes.\n*/private/\n"

const (
	migrationPreparationIntentVersion = 1
	migrationGuardianIdentity         = "process-guardian-v1"
)

const (
	journalScopeRecords      = "records"
	journalScopeConfig       = "config"
	journalScopeOwner        = "private_git"
	journalScopeRecovery     = "private_recovery"
	journalScopeRecordsIndex = "records_index"

	journalKindState                    = "state"
	journalKindIgnore                   = "migration_ignore"
	journalKindReceipt                  = "receipt"
	journalKindRegistry                 = "registry"
	journalKindOwner                    = "owner"
	journalKindMarker                   = "marker"
	journalKindProof                    = "proof"
	journalKindCompletion               = "completion"
	journalKindRecoveryRunDirectory     = "recovery_run_directory"
	journalKindRecoveryPrivateDirectory = "recovery_private_directory"
	journalKindRecoveryJournal          = "recovery_journal"
	journalKindRecoveryIntent           = "recovery_intent"
	journalKindRecoveryBundle           = "recovery_bundle"
	journalKindIndexRestore             = "restore_logical_index"
)

// ApplyResult is the content-free outcome of a successful migration apply.
type ApplyResult struct {
	RunID           string `json:"run_id"`
	PlanHash        string `json:"plan_hash"`
	Status          string `json:"status"`
	AlreadyComplete bool   `json:"already_complete,omitempty"`
}

// Verification is the content-free evidence emitted by verify. Raw state,
// config, manifest, ownership token, and journal bytes never leave the private
// journal through this type.
type Verification struct {
	RunID           string `json:"run_id"`
	PlanHash        string `json:"plan_hash"`
	Status          string `json:"status"`
	MigrationCommit string `json:"migration_commit"`
	ProofCommit     string `json:"proof_commit"`
	RecordWrites    int    `json:"record_writes"`
	RetiredRecords  int    `json:"retired_records"`
	Bindings        int    `json:"bindings"`
}

// RecoveryResult is the content-free outcome of resume or restore.
type RecoveryResult struct {
	RunID  string `json:"run_id"`
	Action string `json:"action"`
	Status string `json:"status"`
}

// privateJournal is deliberately stored only in a 0600, ignored recovery file.
// Snapshots and writes contain raw bytes so restore never has to infer a prior
// state from a Git tree, an external manifest, or a source checkout.
type privateJournal struct {
	Version    int    `json:"version"`
	RunID      string `json:"run_id"`
	Phase      string `json:"phase"`
	PlanHash   string `json:"plan_hash"`
	ConfigPath string `json:"config_path"`
	ConfigRoot string `json:"config_root"`
	Workflow   string `json:"workflow_root"`

	Plan          Plan                         `json:"plan"`
	Snapshots     []privateSnapshot            `json:"snapshots"`
	Writes        []privateWrite               `json:"writes"`
	Bindings      []workspace.MigrationBinding `json:"bindings"`
	RecordsBackup recordsGitBackup             `json:"records_git_backup"`
	IntentOwner   string                       `json:"intent_owner"`

	RecordsParent          string `json:"records_parent"`
	RecordsIndexSHA256     string `json:"records_index_sha256"`
	MigrationMessage       string `json:"migration_message"`
	MigrationMessageSHA256 string `json:"migration_message_sha256"`
	ExpectedMigrationTree  string `json:"expected_migration_tree,omitempty"`
	MigrationCommit        string `json:"migration_commit,omitempty"`
	ProofMessage           string `json:"proof_message"`
	ExpectedProofTree      string `json:"expected_proof_tree,omitempty"`
	ProofCommit            string `json:"proof_commit,omitempty"`
	RestoreMessage         string `json:"restore_message"`
	RestoreMessageSHA256   string `json:"restore_message_sha256"`
	RestoreParent          string `json:"restore_parent,omitempty"`
	ExpectedRestoreTree    string `json:"expected_restore_tree,omitempty"`
	RestoreCommit          string `json:"restore_commit,omitempty"`

	RecordsIndexPre    []recordsIndexEntry `json:"records_index_pre"`
	MigrationIndexPost []recordsIndexEntry `json:"migration_index_post,omitempty"`
	ProofIndexPost     []recordsIndexEntry `json:"proof_index_post,omitempty"`
	RestoreIndexPost   []recordsIndexEntry `json:"restore_index_post,omitempty"`
}

// preparationIntent is the minimal durable recovery identity created before
// any private backup or authoritative migration write. It deliberately holds
// no source bytes, state preimages, binding tokens, or Git backup material.
type preparationIntent struct {
	Version      int    `json:"version"`
	RunID        string `json:"run_id"`
	Phase        string `json:"phase"`
	PlanHash     string `json:"plan_hash"`
	ConfigPath   string `json:"config_path"`
	ConfigRoot   string `json:"config_root"`
	Workflow     string `json:"workflow_root"`
	ManifestPath string `json:"manifest_path"`
	Owner        string `json:"owner"`
	Guardian     string `json:"guardian"`
}

type privateSnapshot struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
	Data   []byte `json:"data"`
}

// recordsGitBackup is recovery-only evidence for the records repository before
// any authoritative migration write. The bundle and raw index stay under the
// private 0600 journal directory; neither reaches a receipt or CLI output.
type recordsGitBackup struct {
	BundlePath   string          `json:"bundle_path"`
	BundleSHA256 string          `json:"bundle_sha256"`
	IndexPath    string          `json:"index_path"`
	IndexSHA256  string          `json:"index_sha256"`
	IndexMode    uint32          `json:"index_mode"`
	IndexData    []byte          `json:"index_data"`
	Head         string          `json:"head"`
	Refs         []recordsGitRef `json:"refs"`
}

type recordsGitRef struct {
	Name   string `json:"name"`
	Object string `json:"object"`
}

// recordsIndexEntry is retained as the internal spelling for the public,
// plan-bound logical records index evidence.
type recordsIndexEntry = RecordsIndexEntry

type privateWrite struct {
	Scope      string `json:"scope"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	PreExists  bool   `json:"pre_exists"`
	PreMode    uint32 `json:"pre_mode,omitempty"`
	Preimage   []byte `json:"preimage,omitempty"`
	PostExists bool   `json:"post_exists"`
	PostMode   uint32 `json:"post_mode,omitempty"`
	Postimage  []byte `json:"postimage,omitempty"`
}

// migrationAfterWrite is a test seam. A failure after the durable file write
// models interruption at every file boundary; recovery classifies pre/post
// bytes rather than assuming a failed call made no progress.
var migrationAfterWrite = func(string) error { return nil }

// migrationAfterMarker is a test seam for the crash window after schema-2
// ownership becomes visible but before the private journal can be completed.
var migrationAfterMarker = func() error { return nil }

// migrationAfterPreparation models a crash-safe failure boundary after the
// durable intent and at the completion of private backup preparation.
var migrationAfterPreparation = func(string) error { return nil }

// These seams model the two crash windows around persisting a staged logical
// index. Production leaves them as no-ops.
var migrationBeforePostIndexSave = func(string) error { return nil }
var migrationAfterPostIndexSave = func(string) error { return nil }

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

// captureRecordsGitBackup creates the complete records-Git disaster-recovery
// bundle before any registry, state, receipt, proof, marker, or owner-token
// write. It deliberately does not modify the records index, refs, hooks, or
// checkout files.
func captureRecordsGitBackup(root, runID string, planned RecordsGit) (recordsGitBackup, error) {
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
	if _, err := ensurePrivateJournalDirectory(root, runID); err != nil {
		return recordsGitBackup{}, err
	}
	bundle, bundleMode, exists, err := readMigrationOptionalRegular(root, bundlePath)
	if err != nil {
		return recordsGitBackup{}, err
	}
	backup := recordsGitBackup{
		BundlePath:   bundlePath,
		BundleSHA256: migrationDigest(bundle),
		IndexPath:    indexPath,
		IndexSHA256:  migrationDigest(indexData),
		IndexMode:    uint32(indexInfo.Mode().Perm()),
		IndexData:    append([]byte(nil), indexData...),
		Head:         head,
		Refs:         refs,
	}
	if exists {
		if bundleMode.Perm() != 0o600 {
			return recordsGitBackup{}, fmt.Errorf("workspace migration: private records Git backup is not a 0600 regular file")
		}
		if err := verifyRecordsGitBackup(root, backup); err != nil {
			return recordsGitBackup{}, err
		}
		return backup, nil
	}
	bundle, err = migrationGitOutput(root, "bundle", "create", "-", "HEAD", "--all")
	if err != nil {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: create records Git backup: %w", err)
	}
	if err := writeMigrationRegular(root, bundlePath, bundle, 0o600); err != nil {
		return recordsGitBackup{}, err
	}
	backup.BundleSHA256 = migrationDigest(bundle)
	if err := verifyRecordsGitBackup(root, backup); err != nil {
		return recordsGitBackup{}, err
	}
	return backup, nil
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
	if migrationDigest(data) != backup.BundleSHA256 || migrationDigest(backup.IndexData) != backup.IndexSHA256 {
		return fmt.Errorf("workspace migration: private records Git backup digest differs")
	}
	if _, err := migrationGitInput(root, data, "bundle", "verify", "-"); err != nil {
		return fmt.Errorf("workspace migration: private records Git backup verification failed: %w", err)
	}
	data, err = migrationGitInput(root, data, "bundle", "list-heads", "-")
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

func saveJournal(j privateJournal) error {
	if err := j.validate(); err != nil {
		return err
	}
	path, err := ensurePrivateJournalDirectory(j.Workflow, j.RunID)
	if err != nil {
		return fmt.Errorf("workspace migration: prepare private journal: %w", err)
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	if err := writeMigrationRegular(j.Workflow, path, append(data, '\n'), 0o600); err != nil {
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
		return privateJournal{}, os.ErrNotExist
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

func saveJournalStatus(root, runID, phase string) error {
	if !migrationrecord.SafeRunID(runID) || phase == "" {
		return fmt.Errorf("workspace migration: invalid journal status identity")
	}
	if _, err := ensurePrivateJournalDirectory(root, runID); err != nil {
		return err
	}
	status := migrationrecord.JournalStatus{Version: migrationJournalVersion, RunID: runID, Phase: phase}
	data, err := json.Marshal(status)
	if err != nil {
		return err
	}
	path, err := journalStatusPath(root, runID)
	if err != nil {
		return err
	}
	return writeMigrationRegular(root, path, append(data, '\n'), 0o600)
}

func newPreparationIntent(layout workspace.Layout, plan Plan) (preparationIntent, error) {
	if err := validateReadyPlan(plan); err != nil {
		return preparationIntent{}, err
	}
	runID, err := newRunID()
	if err != nil {
		return preparationIntent{}, err
	}
	owner, err := newMigrationOwner()
	if err != nil {
		return preparationIntent{}, err
	}
	intent := preparationIntent{
		Version:      migrationPreparationIntentVersion,
		RunID:        runID,
		Phase:        "preparing",
		PlanHash:     plan.PlanHash,
		ConfigPath:   layout.ConfigPath,
		ConfigRoot:   layout.ConfigRoot,
		Workflow:     layout.WorkflowRoot,
		ManifestPath: plan.Manifest.Path,
		Owner:        owner,
		Guardian:     migrationGuardianIdentity,
	}
	if err := intent.validate(); err != nil {
		return preparationIntent{}, err
	}
	return intent, nil
}

func (intent preparationIntent) validate() error {
	if intent.Version != migrationPreparationIntentVersion || intent.Phase != "preparing" || !migrationrecord.SafeRunID(intent.RunID) || !validDigest(intent.PlanHash) || !validMigrationOwner(intent.Owner) || intent.Guardian != migrationGuardianIdentity {
		return fmt.Errorf("workspace migration: invalid preparation intent identity")
	}
	for _, path := range []string{intent.ConfigPath, intent.ConfigRoot, intent.Workflow, intent.ManifestPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("workspace migration: invalid preparation intent path")
		}
	}
	if filepath.Dir(intent.ConfigPath) != intent.ConfigRoot {
		return fmt.Errorf("workspace migration: invalid preparation intent layout")
	}
	return nil
}

func validatePreparationIntentLayout(intent preparationIntent, layout workspace.Layout) error {
	if err := intent.validate(); err != nil {
		return err
	}
	if intent.ConfigPath != layout.ConfigPath || intent.ConfigRoot != layout.ConfigRoot || intent.Workflow != layout.WorkflowRoot {
		return fmt.Errorf("workspace migration: preparation intent does not belong to this workspace layout")
	}
	return nil
}

func savePreparationIntent(intent preparationIntent) error {
	if err := intent.validate(); err != nil {
		return err
	}
	// Publish the content-free pending status first so an interrupted intent is
	// discoverable by ordinary loaders before any backup work can begin.
	if err := saveJournalStatus(intent.Workflow, intent.RunID, intent.Phase); err != nil {
		return err
	}
	path, err := ensurePrivateJournalDirectory(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	intentPath, err := preparationIntentPath(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	if filepath.Dir(path) != filepath.Dir(intentPath) {
		return fmt.Errorf("workspace migration: preparation intent directory differs from journal directory")
	}
	data, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return err
	}
	return writeMigrationRegular(intent.Workflow, intentPath, append(data, '\n'), 0o600)
}

func loadPreparationIntent(root, runID string) (preparationIntent, error) {
	path, err := preparationIntentPath(root, runID)
	if err != nil {
		return preparationIntent{}, err
	}
	data, mode, exists, err := readMigrationOptionalRegular(root, path)
	if err != nil {
		return preparationIntent{}, err
	}
	if !exists {
		return preparationIntent{}, os.ErrNotExist
	}
	if err := requirePrivateRecoveryDirectories(root, runID); err != nil {
		return preparationIntent{}, err
	}
	if mode.Perm()&0o077 != 0 {
		return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent is not a 0600 regular file")
	}
	var intent preparationIntent
	if err := decodePrivateJSON(data, &intent); err != nil {
		return preparationIntent{}, fmt.Errorf("workspace migration: invalid preparation intent: %w", err)
	}
	if err := intent.validate(); err != nil {
		return preparationIntent{}, err
	}
	if intent.RunID != runID || intent.Workflow != root {
		return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent does not match its requested recovery directory")
	}
	if expected, err := preparationIntentPath(intent.Workflow, intent.RunID); err != nil || expected != path {
		return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent path does not match its identity")
	}
	status, err := migrationrecord.LoadJournalStatus(root, runID)
	if err != nil || status.RunID != runID || status.Phase != "preparing" {
		return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent status is missing or invalid")
	}
	return intent, nil
}

func (j privateJournal) validate() error {
	if j.Version != migrationJournalVersion || !migrationrecord.SafeRunID(j.RunID) || !validJournalPhase(j.Phase) || !validDigest(j.PlanHash) {
		return fmt.Errorf("workspace migration: invalid private journal identity")
	}
	for _, path := range []string{j.ConfigPath, j.ConfigRoot, j.Workflow} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("workspace migration: invalid private journal path")
		}
	}
	if filepath.Dir(j.ConfigPath) != j.ConfigRoot || j.Plan.PlanHash != j.PlanHash {
		return fmt.Errorf("workspace migration: invalid private journal plan binding")
	}
	if err := validateReadyPlan(j.Plan); err != nil {
		return err
	}
	if !canonicalCommit.MatchString(j.RecordsParent) || !validDigest(j.RecordsIndexSHA256) || (j.IntentOwner != "" && !validMigrationOwner(j.IntentOwner)) || strings.TrimSpace(j.MigrationMessage) == "" || !validDigest(j.MigrationMessageSHA256) || strings.TrimSpace(j.ProofMessage) == "" || strings.TrimSpace(j.RestoreMessage) == "" || !validDigest(j.RestoreMessageSHA256) {
		return fmt.Errorf("workspace migration: invalid private journal records identity")
	}
	if j.MigrationMessageSHA256 != migrationDigest([]byte(j.MigrationMessage+"\n")) {
		return fmt.Errorf("workspace migration: invalid private journal migration message")
	}
	if j.RestoreMessageSHA256 != migrationDigest([]byte(j.RestoreMessage+"\n")) {
		return fmt.Errorf("workspace migration: invalid private journal restore message")
	}
	for _, tree := range []string{j.ExpectedMigrationTree, j.ExpectedProofTree, j.ExpectedRestoreTree} {
		if tree != "" && !canonicalCommit.MatchString(tree) {
			return fmt.Errorf("workspace migration: invalid private journal Git tree")
		}
	}
	for _, commit := range []string{j.MigrationCommit, j.ProofCommit, j.RestoreParent, j.RestoreCommit} {
		if commit != "" && !canonicalCommit.MatchString(commit) {
			return fmt.Errorf("workspace migration: invalid private journal Git commit")
		}
	}
	if (j.ExpectedMigrationTree != "" && j.MigrationCommit == "" && j.Phase != "prepared" && j.Phase != "applying" && j.Phase != "restoring" && j.Phase != "restored") || (j.ProofCommit != "" && (j.MigrationCommit == "" || j.ExpectedProofTree == "")) || (j.ExpectedRestoreTree != "" && j.RestoreParent == "") || (j.RestoreCommit != "" && (j.RestoreParent == "" || j.ExpectedRestoreTree == "")) {
		return fmt.Errorf("workspace migration: incomplete private journal commit state")
	}
	seenWrites := map[string]bool{}
	for _, write := range j.Writes {
		if err := write.validate(j); err != nil {
			return err
		}
		key := write.Scope + "\x00" + write.Path
		if seenWrites[key] {
			return fmt.Errorf("workspace migration: duplicate private journal write")
		}
		seenWrites[key] = true
	}
	seenSnapshots := map[string]bool{}
	for _, snapshot := range j.Snapshots {
		if !filepath.IsAbs(snapshot.Path) || filepath.Clean(snapshot.Path) != snapshot.Path || !validDigest(snapshot.SHA256) || snapshot.SHA256 != migrationDigest(snapshot.Data) || snapshot.Mode == 0 || seenSnapshots[snapshot.Path] {
			return fmt.Errorf("workspace migration: invalid private backup snapshot")
		}
		seenSnapshots[snapshot.Path] = true
	}
	if err := validateRecordsGitBackup(j); err != nil {
		return err
	}
	if err := validateJournalIndexAuthority(j); err != nil {
		return err
	}
	seenBindings := map[string]bool{}
	for _, binding := range j.Bindings {
		key := binding.Workflow + "\x00" + binding.Change + "\x00" + binding.Repo
		if key == "\x00\x00" || seenBindings[key] || binding.Ownership == "" {
			return fmt.Errorf("workspace migration: invalid private binding")
		}
		seenBindings[key] = true
	}
	return validateJournalAuthority(j)
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

func journalIndexPaths(journal privateJournal) ([]string, error) {
	paths, err := plannedJournalIndexPaths(journal.Plan)
	if err != nil {
		return nil, err
	}
	for i, path := range paths {
		paths[i] = strings.ReplaceAll(path, runIDPlaceholder, journal.RunID)
		if !safeGitRelativePath(paths[i]) {
			return nil, fmt.Errorf("workspace migration: unsafe reviewed records index path")
		}
	}
	return paths, nil
}

func captureRecordsIndex(root string, paths []string) ([]recordsIndexEntry, error) {
	if err := requireSupportedGitIndexFlags(root, safeGitRelativePath); err != nil {
		return nil, fmt.Errorf("workspace migration: records index flags are unsupported: %w", err)
	}
	wanted := make(map[string]bool, len(paths))
	for _, path := range paths {
		if !safeGitRelativePath(path) || wanted[path] {
			return nil, fmt.Errorf("workspace migration: invalid records index path")
		}
		wanted[path] = true
	}
	if len(wanted) == 0 {
		return []recordsIndexEntry{}, nil
	}
	args := append([]string{"ls-files", "--stage", "-z", "--"}, paths...)
	data, err := migrationGit(root, args...)
	if err != nil {
		return nil, err
	}
	entries := make([]recordsIndexEntry, 0, len(paths))
	seen := map[string]bool{}
	for _, raw := range bytesSplitNUL(data) {
		before, rawPath, ok := bytes.Cut(raw, []byte{'\t'})
		path := string(rawPath)
		fields := strings.Fields(string(before))
		if !ok || len(fields) != 3 || !wanted[path] || !safeGitRelativePath(path) || seen[path] || !canonicalCommit.MatchString(fields[1]) {
			return nil, fmt.Errorf("workspace migration: records index has an invalid entry")
		}
		mode, err := strconv.ParseUint(fields[0], 8, 32)
		if err != nil || !validRecordsIndexMode(uint32(mode)) {
			return nil, fmt.Errorf("workspace migration: records index has an unsupported file mode")
		}
		stage, err := strconv.Atoi(fields[2])
		if err != nil || stage != 0 {
			return nil, fmt.Errorf("workspace migration: records index has an unmerged entry")
		}
		seen[path] = true
		entries = append(entries, recordsIndexEntry{Path: path, Mode: uint32(mode), Object: fields[1], Stage: stage})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

func validRecordsIndexMode(mode uint32) bool {
	return mode == 0o100644 || mode == 0o100755
}

func logicalRecordsIndexDigest(entries []recordsIndexEntry) string {
	data, err := json.Marshal(entries)
	if err != nil {
		panic(fmt.Sprintf("workspace migration: logical records index cannot marshal: %v", err))
	}
	return migrationDigest(data)
}

func capturePlanLogicalRecordsIndex(plan *Plan, root string) error {
	paths, err := plannedLogicalRecordsIndexPaths(*plan)
	if err != nil {
		return err
	}
	entries, err := captureRecordsIndex(root, paths)
	if err != nil {
		return err
	}
	plan.RecordsGit.LogicalIndex = append([]recordsIndexEntry(nil), entries...)
	plan.RecordsGit.LogicalIndexSHA256 = logicalRecordsIndexDigest(entries)
	return nil
}

func verifyPlanLogicalRecordsIndex(root string, plan Plan) error {
	if err := validatePlanLogicalIndex(plan); err != nil {
		return err
	}
	paths, err := plannedLogicalRecordsIndexPaths(plan)
	if err != nil {
		return err
	}
	actual, err := captureRecordsIndex(root, paths)
	if err != nil || !sameRecordsIndex(actual, plan.RecordsGit.LogicalIndex) || logicalRecordsIndexDigest(actual) != plan.RecordsGit.LogicalIndexSHA256 {
		return fmt.Errorf("workspace migration: migration-owned records index changed after planning")
	}
	return nil
}

func validateJournalIndexSnapshot(paths []string, entries []recordsIndexEntry, required bool) error {
	if required && entries == nil {
		return fmt.Errorf("workspace migration: private journal lacks records index authority")
	}
	allowed := make(map[string]bool, len(paths))
	for _, path := range paths {
		if !safeGitRelativePath(path) || allowed[path] {
			return fmt.Errorf("workspace migration: invalid reviewed records index path")
		}
		allowed[path] = true
	}
	seen := map[string]bool{}
	last := ""
	for _, entry := range entries {
		if !allowed[entry.Path] || entry.Path <= last || seen[entry.Path] || !validRecordsIndexMode(entry.Mode) || !canonicalCommit.MatchString(entry.Object) || entry.Stage != 0 {
			return fmt.Errorf("workspace migration: invalid private records index authority")
		}
		seen[entry.Path] = true
		last = entry.Path
	}
	return nil
}

func validateJournalIndexAuthority(journal privateJournal) error {
	paths, err := journalIndexPaths(journal)
	if err != nil {
		return err
	}
	if err := validateJournalIndexSnapshot(paths, journal.RecordsIndexPre, true); err != nil {
		return err
	}
	if !sameRecordsIndex(journal.RecordsIndexPre, journal.Plan.RecordsGit.LogicalIndex) || logicalRecordsIndexDigest(journal.RecordsIndexPre) != journal.Plan.RecordsGit.LogicalIndexSHA256 {
		return fmt.Errorf("workspace migration: private journal records preindex differs from the reviewed logical index")
	}
	for _, write := range journal.Writes {
		if write.Scope != journalScopeRecords || write.Kind != journalKindState || !write.PreExists {
			continue
		}
		rel, err := filepath.Rel(journal.Workflow, write.Path)
		if err != nil || !indexContainsPath(journal.RecordsIndexPre, filepath.ToSlash(rel)) {
			return fmt.Errorf("workspace migration: private journal index omits a planned state preimage")
		}
	}
	for _, snapshot := range [][]recordsIndexEntry{journal.MigrationIndexPost, journal.ProofIndexPost, journal.RestoreIndexPost} {
		if snapshot == nil {
			continue
		}
		if err := validateJournalIndexSnapshot(paths, snapshot, false); err != nil {
			return err
		}
	}
	return validateJournalIndexRestoreOperation(journal)
}

func validateJournalIndexRestoreOperation(journal privateJournal) error {
	paths, err := plannedJournalIndexPaths(journal.Plan)
	if err != nil {
		return err
	}
	var operation *ProspectiveOperation
	for i := range journal.Plan.Prospective.Operations {
		candidate := &journal.Plan.Prospective.Operations[i]
		if candidate.Scope != journalScopeRecordsIndex || candidate.Kind != journalKindIndexRestore {
			continue
		}
		if operation != nil {
			return fmt.Errorf("workspace migration: duplicate reviewed logical index recovery operation")
		}
		operation = candidate
	}
	if operation == nil || operation.Path != filepath.Join(journal.Plan.RecordsGit.GitCommonDir, "index") || !sameStringSet(operation.Paths, paths) || operation.Intent != "restore_authenticated_original_logical_index" || !operation.PreExists || !operation.PostExists || operation.PreSHA256 != journal.Plan.RecordsGit.LogicalIndexSHA256 || operation.PostSHA256 != journal.Plan.RecordsGit.LogicalIndexSHA256 || operation.DataClass != "records_logical_index" || operation.Mutation != "restore_selected_preimage_if_uncommitted" || operation.DynamicRule != dynamicIndexRestoreRule {
		return fmt.Errorf("workspace migration: reviewed logical index recovery operation differs from the plan")
	}
	return nil
}

func indexContainsPath(entries []recordsIndexEntry, path string) bool {
	for _, entry := range entries {
		if entry.Path == path {
			return true
		}
	}
	return false
}

func sameRecordsIndex(actual, expected []recordsIndexEntry) bool {
	return reflect.DeepEqual(actual, expected)
}

const (
	journalIndexPre       = "pre"
	journalIndexMigration = "migration"
	journalIndexProof     = "proof"
	journalIndexRestore   = "restore"
)

// derivedJournalIndex calculates every accepted records-index postimage from
// the plan-bound preimage and authenticated journal writes. Persisted postindex
// arrays are recovery diagnostics only: they are checked against this result,
// never used to authorize a staged index.
func derivedJournalIndex(root string, journal privateJournal, phase string) ([]recordsIndexEntry, error) {
	entries := append([]recordsIndexEntry{}, journal.Plan.RecordsGit.LogicalIndex...)
	switch phase {
	case journalIndexPre:
		return entries, nil
	case journalIndexMigration:
		return applyJournalIndexWrites(root, journal, entries, true, journalKindState, journalKindIgnore, journalKindReceipt)
	case journalIndexProof:
		entries, err := applyJournalIndexWrites(root, journal, entries, true, journalKindState, journalKindIgnore, journalKindReceipt)
		if err != nil {
			return nil, err
		}
		return applyJournalIndexWrites(root, journal, entries, true, journalKindProof)
	case journalIndexRestore:
		return applyJournalIndexWrites(root, journal, entries, false, journalKindState, journalKindIgnore, journalKindReceipt, journalKindProof)
	default:
		return nil, fmt.Errorf("workspace migration: unknown logical records index phase %q", phase)
	}
}

func applyJournalIndexWrites(root string, journal privateJournal, entries []recordsIndexEntry, post bool, kinds ...string) ([]recordsIndexEntry, error) {
	allowedPaths, err := journalIndexPaths(journal)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(allowedPaths))
	for _, path := range allowedPaths {
		allowed[path] = true
	}
	selected := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		selected[kind] = true
	}
	byPath := make(map[string]recordsIndexEntry, len(entries))
	for _, entry := range entries {
		if !allowed[entry.Path] {
			return nil, fmt.Errorf("workspace migration: logical records index includes an unreviewed path")
		}
		byPath[entry.Path] = entry
	}
	seen := map[string]bool{}
	for _, write := range journal.Writes {
		if write.Scope != journalScopeRecords || !selected[write.Kind] {
			continue
		}
		rel, err := filepath.Rel(journal.Workflow, write.Path)
		rel = filepath.ToSlash(rel)
		if err != nil || !safeGitRelativePath(rel) || !allowed[rel] || seen[rel] {
			return nil, fmt.Errorf("workspace migration: logical records index write is outside reviewed authority")
		}
		seen[rel] = true
		exists, data, mode := write.PreExists, write.Preimage, write.PreMode
		if post {
			exists, data, mode = write.PostExists, write.Postimage, write.PostMode
		}
		if !exists {
			delete(byPath, rel)
			continue
		}
		if write.Kind == journalKindProof && post && len(data) == 0 {
			return nil, fmt.Errorf("workspace migration: logical commit-proof index postimage is not prepared")
		}
		object, err := migrationGitInput(root, data, "hash-object", "--path="+rel, "--stdin")
		if err != nil {
			return nil, fmt.Errorf("workspace migration: derive logical records index object: %w", err)
		}
		objectID := strings.TrimSpace(string(object))
		if !canonicalCommit.MatchString(objectID) {
			return nil, fmt.Errorf("workspace migration: derived logical records index object is invalid")
		}
		indexMode := uint32(0o100644)
		if mode&0o111 != 0 {
			indexMode = 0o100755
		}
		byPath[rel] = recordsIndexEntry{Path: rel, Mode: indexMode, Object: objectID, Stage: 0}
	}
	result := make([]recordsIndexEntry, 0, len(byPath))
	for _, entry := range byPath {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, nil
}

func journalProofIndexPrepared(journal privateJournal) bool {
	proof, ok := journalWrite(journal, journalKindProof)
	return ok && proof.PostExists && len(proof.Postimage) != 0
}

func validateJournalIndexCaches(root string, journal privateJournal) error {
	for _, candidate := range []struct {
		phase    string
		snapshot []recordsIndexEntry
	}{
		{journalIndexMigration, journal.MigrationIndexPost},
		{journalIndexProof, journal.ProofIndexPost},
		{journalIndexRestore, journal.RestoreIndexPost},
	} {
		if candidate.snapshot == nil {
			continue
		}
		expected, err := derivedJournalIndex(root, journal, candidate.phase)
		if err != nil || !sameRecordsIndex(candidate.snapshot, expected) {
			return fmt.Errorf("workspace migration: persisted logical records index postimage differs from trusted writes")
		}
	}
	return nil
}

// validateRecordsIndexAgainst refuses a staged-only or unmerged conflict before
// the migration can invoke git add. Every accepted entry is one exact snapshot
// captured by this journal; arbitrary index filters and third blobs are never
// normalized away.
func validateRecordsIndexAgainst(root string, paths []string, allowed ...[]recordsIndexEntry) ([]recordsIndexEntry, error) {
	actual, err := captureRecordsIndex(root, paths)
	if err != nil {
		return nil, err
	}
	for _, expected := range allowed {
		if expected != nil && sameRecordsIndex(actual, expected) {
			return actual, nil
		}
	}
	return nil, fmt.Errorf("workspace migration: records index contains a conflicting staged blob, mode, or path")
}

func verifyJournalRecordsIndex(root string, journal privateJournal) error {
	paths, err := journalIndexPaths(journal)
	if err != nil {
		return err
	}
	pre, err := derivedJournalIndex(root, journal, journalIndexPre)
	if err != nil {
		return err
	}
	migration, err := derivedJournalIndex(root, journal, journalIndexMigration)
	if err != nil {
		return err
	}
	allowed := [][]recordsIndexEntry{pre}
	switch journal.Phase {
	case "prepared":
		// The durable journal exists before any migration index mutation.
	case "applying":
		allowed = append(allowed, migration)
		if journalProofIndexPrepared(journal) {
			proof, err := derivedJournalIndex(root, journal, journalIndexProof)
			if err != nil {
				return err
			}
			allowed = append(allowed, proof)
		}
	case "pending-finalization", "complete":
		proof, err := derivedJournalIndex(root, journal, journalIndexProof)
		if err != nil {
			return err
		}
		allowed = [][]recordsIndexEntry{proof}
	case "restoring":
		allowed = append(allowed, migration)
		if journalProofIndexPrepared(journal) {
			proof, err := derivedJournalIndex(root, journal, journalIndexProof)
			if err != nil {
				return err
			}
			allowed = append(allowed, proof)
		}
		restored, err := derivedJournalIndex(root, journal, journalIndexRestore)
		if err != nil {
			return err
		}
		allowed = append(allowed, restored)
	case "restored":
		if journal.RestoreCommit != "" {
			restored, err := derivedJournalIndex(root, journal, journalIndexRestore)
			if err != nil {
				return err
			}
			allowed = [][]recordsIndexEntry{restored}
		}
	}
	_, err = validateRecordsIndexAgainst(root, paths, allowed...)
	return err
}

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
	want := map[string]string{
		journalKindRecoveryRunDirectory:     runDir,
		journalKindRecoveryPrivateDirectory: filepath.Dir(journalPath),
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
		if err := writeMigrationRegularExpected(root, write.Path, wantData, wantMode, expected); err != nil {
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

func removeOptionalRegular(root, path string) error {
	return removeMigrationOptionalRegular(root, path)
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

func migrationGitCommand(dir string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.fsmonitor=false", "-c", "user.useConfigOnly=true", "-c", "maintenance.autoDetach=false", "-c", "gc.autoDetach=false"}, args...)...)
	blocked := map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true, "GIT_INDEX_FILE": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_PREFIX": true,
		"GIT_NAMESPACE": true, "GIT_LITERAL_PATHSPECS": true, "GIT_GLOB_PATHSPECS": true,
		"GIT_NOGLOB_PATHSPECS": true, "GIT_ICASE_PATHSPECS": true, "GIT_OPTIONAL_LOCKS": true,
	}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if !blocked[key] {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_LITERAL_PATHSPECS=1", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	return cmd
}

func migrationGit(dir string, args ...string) ([]byte, error) {
	out, err := migrationGitCommand(dir, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("workspace migration: git %s: %s: %w", args[0], strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

func migrationGitOutput(dir string, args ...string) ([]byte, error) {
	cmd := migrationGitCommand(dir, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("workspace migration: git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return out, nil
}

func migrationGitInput(dir string, input []byte, args ...string) ([]byte, error) {
	cmd := migrationGitCommand(dir, args...)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("workspace migration: git %s: %s: %w", args[0], strings.TrimSpace(stderr.String()), err)
	}
	return out, nil
}

func withInitialMigrationLocks(configPath string, fn func(workspace.Layout) error) error {
	configPath, err := absolutePath(configPath)
	if err != nil {
		return err
	}
	// Resolve once only to identify the records lock. The same layout is loaded
	// again after the config lock and then inventory is rebuilt under every lock.
	l, err := workspace.LoadMigration(configPath)
	if err != nil {
		return err
	}
	configLock, err := applylock.Acquire(filepath.Join(l.ConfigRoot, ".homonto"))
	if err != nil {
		return fmt.Errorf("workspace migration: %w", err)
	}
	defer configLock.Release()
	l, err = workspace.LoadMigration(configPath)
	if err != nil {
		return err
	}
	recoveryLockPath, err := migrationRecoveryLockPath(l.WorkflowRoot)
	if err != nil {
		return err
	}
	recordsLock, err := applylock.AcquirePath(recoveryLockPath, filepath.Dir(recoveryLockPath))
	if err != nil {
		return fmt.Errorf("workspace migration: records lock: %w", err)
	}
	defer recordsLock.Release()
	releaseBindings, err := workspace.LockMigrationBindings(l)
	if err != nil {
		return fmt.Errorf("workspace migration: lifecycle locks: %w", err)
	}
	defer releaseBindings()
	return fn(l)
}

type recoveryMaterial struct {
	journal *privateJournal
	intent  *preparationIntent
	orphan  bool
}

func loadRecoveryMaterial(root, runID string) (recoveryMaterial, error) {
	path, err := journalPath(root, runID)
	if err != nil {
		return recoveryMaterial{}, err
	}
	journal, journalErr := loadJournal(root, runID)
	if journalErr == nil {
		if err := removeMigrationTemporaryFiles(root, filepath.Dir(path)); err != nil {
			return recoveryMaterial{}, err
		}
		return recoveryMaterial{journal: &journal}, nil
	}
	intent, intentErr := loadPreparationIntent(root, runID)
	if intentErr == nil {
		if err := removeMigrationTemporaryFiles(root, filepath.Dir(path)); err != nil {
			return recoveryMaterial{}, err
		}
		return recoveryMaterial{intent: &intent}, nil
	}
	if errors.Is(journalErr, os.ErrNotExist) && errors.Is(intentErr, os.ErrNotExist) {
		orphan, orphanErr := migrationrecord.IsPreparationOrphan(root, runID)
		if orphanErr != nil {
			return recoveryMaterial{}, fmt.Errorf("workspace migration: inspect preparation artifact: %w", orphanErr)
		}
		if orphan {
			if err := removeMigrationTemporaryFiles(root, filepath.Dir(path)); err != nil {
				return recoveryMaterial{}, err
			}
			return recoveryMaterial{orphan: true}, nil
		}
		if status, statusErr := migrationrecord.LoadJournalStatus(root, runID); statusErr == nil && status.Phase == "preparing" {
			return recoveryMaterial{}, fmt.Errorf("workspace migration: preparation artifacts are not recognized; refusing automatic cleanup")
		}
	}
	if !errors.Is(journalErr, os.ErrNotExist) {
		return recoveryMaterial{}, journalErr
	}
	return recoveryMaterial{}, intentErr
}

func withRecoveryMigrationLocks(configPath, runID, expectedPlanHash string, fn func(workspace.Layout, recoveryMaterial) error) error {
	configPath, err := absolutePath(configPath)
	if err != nil {
		return err
	}
	if !migrationrecord.SafeRunID(runID) {
		return fmt.Errorf("workspace migration: invalid run ID")
	}
	if !validDigest(expectedPlanHash) {
		return fmt.Errorf("workspace migration: recovery requires the reviewed --plan-hash")
	}
	configRoot := filepath.Dir(configPath)
	configLock, err := applylock.Acquire(filepath.Join(configRoot, ".homonto"))
	if err != nil {
		return fmt.Errorf("workspace migration: %w", err)
	}
	defer configLock.Release()
	// This recovery-specific loader repeats normal schema/path/repository
	// validation while permitting only a matching legacy or pending marker.
	// It never converts a generic workspace.Load failure into permission to act.
	l, err := workspace.LoadMigrationRecovery(configPath)
	if err != nil {
		return fmt.Errorf("workspace migration: recovery layout validation failed: %w", err)
	}
	if l.ConfigPath != configPath || l.ConfigRoot != configRoot {
		return fmt.Errorf("workspace migration: recovery layout identity changed")
	}
	recoveryLockPath, err := migrationRecoveryLockPath(l.WorkflowRoot)
	if err != nil {
		return err
	}
	recordsLock, err := applylock.AcquirePath(recoveryLockPath, filepath.Dir(recoveryLockPath))
	if err != nil {
		return fmt.Errorf("workspace migration: records lock: %w", err)
	}
	defer recordsLock.Release()
	releaseBindings, err := workspace.LockMigrationBindings(l)
	if err != nil {
		return fmt.Errorf("workspace migration: lifecycle locks: %w", err)
	}
	defer releaseBindings()
	material, err := loadRecoveryMaterial(l.WorkflowRoot, runID)
	if err != nil {
		return err
	}
	if material.journal != nil {
		if err := validateJournalLayout(*material.journal, l); err != nil {
			return err
		}
		if material.journal.PlanHash != expectedPlanHash {
			return fmt.Errorf("workspace migration: recovery journal does not match the reviewed plan hash")
		}
	}
	if material.intent != nil {
		if err := validatePreparationIntentLayout(*material.intent, l); err != nil {
			return err
		}
		if material.intent.PlanHash != expectedPlanHash {
			return fmt.Errorf("workspace migration: recovery preparation intent does not match the reviewed plan hash")
		}
	}
	return fn(l, material)
}

func validateJournalLayout(j privateJournal, l workspace.Layout) error {
	actual, err := inventoryLayout(l)
	if err != nil {
		return fmt.Errorf("workspace migration: current repository identity is unavailable: %w", err)
	}
	if j.ConfigPath != l.ConfigPath || j.ConfigRoot != l.ConfigRoot || j.Workflow != l.WorkflowRoot || !reflect.DeepEqual(j.Plan.Layout, actual) {
		return fmt.Errorf("workspace migration: journal does not belong to this workspace layout")
	}
	top, common, err := workspace.GitIdentity(l.WorkflowRoot)
	if err != nil || top != l.WorkflowRoot || common != j.Plan.RecordsGit.GitCommonDir {
		return fmt.Errorf("workspace migration: records Git identity differs from reviewed plan")
	}
	return validateJournalConcreteAuthority(l, j)
}

func prepareJournal(l workspace.Layout, plan Plan, intent preparationIntent) (privateJournal, error) {
	if err := validateReadyPlan(plan); err != nil || plan.Layout.ConfigPath != l.ConfigPath || plan.Layout.ConfigRoot != l.ConfigRoot || plan.Layout.WorkflowRoot != l.WorkflowRoot {
		return privateJournal{}, fmt.Errorf("workspace migration: invalid ready plan")
	}
	if err := validatePreparationIntentLayout(intent, l); err != nil || intent.PlanHash != plan.PlanHash || intent.ManifestPath != plan.Manifest.Path {
		return privateJournal{}, fmt.Errorf("workspace migration: preparation intent does not match the reviewed plan")
	}
	if err := verifyPlanInputsWithIgnored(l, plan, true, migrationPreparationIgnored(l, intent.RunID)); err != nil {
		return privateJournal{}, err
	}
	runID := intent.RunID
	if _, err := loadJournal(l.WorkflowRoot, runID); err == nil {
		return privateJournal{}, fmt.Errorf("workspace migration: private journal already exists for preparation intent")
	} else if !errors.Is(err, os.ErrNotExist) {
		return privateJournal{}, err
	}
	bindings, err := migrationBindings(plan)
	if err != nil {
		return privateJournal{}, err
	}
	adoptions, err := workspace.PlanMigrationAdoptions(l, runID, bindings)
	if err != nil {
		return privateJournal{}, err
	}
	markerData, err := plannedMarkerData(plan.Layout)
	if err != nil {
		return privateJournal{}, err
	}
	markerPath := filepath.Join(l.ConfigRoot, ".homonto", workflowroot.LayoutMarkerFile)

	journal := privateJournal{
		Version:            migrationJournalVersion,
		RunID:              runID,
		Phase:              "prepared",
		PlanHash:           plan.PlanHash,
		ConfigPath:         l.ConfigPath,
		ConfigRoot:         l.ConfigRoot,
		Workflow:           l.WorkflowRoot,
		Plan:               plan,
		Bindings:           bindings,
		IntentOwner:        intent.Owner,
		RecordsParent:      plan.RecordsGit.Head,
		RecordsIndexSHA256: plan.RecordsGit.IndexSHA256,
		MigrationMessage:   "Migrate legacy workflow records to schema-2 ownership (" + runID + ")",
		ProofMessage:       "Record schema-2 migration commit proof (" + runID + ")",
		RestoreMessage:     "Restore interrupted schema-2 migration (" + runID + ")",
	}
	journal.MigrationMessageSHA256 = migrationDigest([]byte(journal.MigrationMessage + "\n"))
	journal.RestoreMessageSHA256 = migrationDigest([]byte(journal.RestoreMessage + "\n"))
	prepared := append([]preparedRecordWrite(nil), plan.preparedRecordWrites...)
	for _, write := range prepared {
		item, err := prepareWrite(journalScopeRecords, journalKindState, l.WorkflowRoot, write.summary.Path, true, write.preimage, true, write.postimage, 0o644)
		if err != nil {
			return privateJournal{}, err
		}
		journal.Writes = append(journal.Writes, item)
	}
	ignorePath := filepath.Join(l.WorkflowRoot, ".workflow", "migrations", ".gitignore")
	ignore, err := prepareWrite(journalScopeRecords, journalKindIgnore, l.WorkflowRoot, ignorePath, false, nil, true, []byte(migrationIgnore), 0o644)
	if err != nil {
		return privateJournal{}, err
	}
	journal.Writes = append(journal.Writes, ignore)

	receipt, err := buildReceipt(plan, runID, markerPath, markerData, adoptions)
	if err != nil {
		return privateJournal{}, err
	}
	receiptData, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return privateJournal{}, err
	}
	receiptData = append(receiptData, '\n')
	receiptPath, err := migrationrecord.ReceiptPath(l.WorkflowRoot, runID)
	if err != nil {
		return privateJournal{}, err
	}
	receiptWrite, err := prepareWrite(journalScopeRecords, journalKindReceipt, l.WorkflowRoot, receiptPath, false, nil, true, receiptData, 0o644)
	if err != nil {
		return privateJournal{}, err
	}
	journal.Writes = append(journal.Writes, receiptWrite)
	proofPath, err := migrationrecord.CommitProofPath(l.WorkflowRoot, runID)
	if err != nil {
		return privateJournal{}, err
	}
	// The proof bytes depend on the first records commit, but its one permitted
	// destination is reserved before any backup or authoritative write.
	proofWrite, err := prepareWrite(journalScopeRecords, journalKindProof, l.WorkflowRoot, proofPath, false, nil, true, nil, 0o644)
	if err != nil {
		return privateJournal{}, err
	}
	journal.Writes = append(journal.Writes, proofWrite)
	completionPath, err := migrationrecord.CompletionWitnessPath(l.WorkflowRoot, runID)
	if err != nil {
		return privateJournal{}, err
	}
	completionWrite, err := prepareWrite(journalScopeRecords, journalKindCompletion, l.WorkflowRoot, completionPath, false, nil, true, nil, 0o600)
	if err != nil {
		return privateJournal{}, err
	}
	journal.Writes = append(journal.Writes, completionWrite)
	registry, err := prepareWrite(journalScopeConfig, journalKindRegistry, l.ConfigRoot, adoptions.RegistryPath, false, nil, true, adoptions.RegistryData, 0o600)
	if err != nil {
		return privateJournal{}, err
	}
	journal.Writes = append(journal.Writes, registry)
	for _, binding := range bindings {
		ownerPath := filepath.Join(binding.GitDir, "homonto-owner")
		owner, err := prepareWrite(journalScopeOwner, journalKindOwner, binding.CommonDir, ownerPath, false, nil, true, []byte(binding.Ownership), 0o600)
		if err != nil {
			return privateJournal{}, err
		}
		journal.Writes = append(journal.Writes, owner)
	}
	markerWrite, err := prepareWrite(journalScopeConfig, journalKindMarker, l.ConfigRoot, markerPath, false, nil, true, markerData, 0o644)
	if err != nil {
		return privateJournal{}, err
	}
	journal.Writes = append(journal.Writes, markerWrite)
	journal.Writes = sortedJournalWrites(journal.Writes)
	indexPaths, err := journalIndexPaths(journal)
	if err != nil {
		return privateJournal{}, err
	}
	journal.RecordsIndexPre, err = captureRecordsIndex(l.WorkflowRoot, indexPaths)
	if err != nil {
		return privateJournal{}, err
	}
	journal.Snapshots, err = privateSnapshots(plan)
	if err != nil {
		return privateJournal{}, err
	}
	// Recheck the complete plan immediately before creating the durable records
	// backup. The backup is private recovery material, not authority to accept a
	// source or records change that raced the initial preflight.
	if err := verifyPlanInputsWithIgnored(l, plan, true, migrationPreparationIgnored(l, intent.RunID)); err != nil {
		return privateJournal{}, err
	}
	journal.RecordsBackup, err = captureRecordsGitBackup(l.WorkflowRoot, runID, plan.RecordsGit)
	if err != nil {
		return privateJournal{}, err
	}
	if err := migrationAfterPreparation("backup"); err != nil {
		return privateJournal{}, err
	}
	if err := validateJournalConcreteAuthority(l, journal); err != nil {
		return privateJournal{}, err
	}
	if err := journal.validate(); err != nil {
		return privateJournal{}, err
	}
	return journal, nil
}

func prepareWrite(scope, kind, root, path string, preExists bool, preimage []byte, postExists bool, postimage []byte, newMode os.FileMode) (privateWrite, error) {
	actual, mode, exists, err := readOptionalRegular(root, path)
	if err != nil {
		return privateWrite{}, err
	}
	if exists != preExists || (exists && !bytes.Equal(actual, preimage)) {
		return privateWrite{}, fmt.Errorf("workspace migration: stale or conflicting path %s", path)
	}
	preMode := uint32(0)
	if exists {
		preMode = uint32(mode)
	}
	postMode := uint32(newMode)
	if postExists && exists {
		postMode = uint32(mode)
	}
	return privateWrite{Scope: scope, Kind: kind, Path: path, PreExists: preExists, PreMode: preMode, Preimage: append([]byte(nil), preimage...), PostExists: postExists, PostMode: postMode, Postimage: append([]byte(nil), postimage...)}, nil
}

func privateSnapshots(plan Plan) ([]privateSnapshot, error) {
	refs := append([]FileFingerprint{plan.Config, plan.Manifest}, plan.ControlFiles...)
	refs = append(refs, plan.Files...)
	seen := map[string]bool{}
	out := make([]privateSnapshot, 0, len(refs))
	for _, ref := range refs {
		if seen[ref.Path] {
			continue
		}
		seen[ref.Path] = true
		data, err := readRealRegularFile(ref.Path)
		if err != nil || migrationDigest(data) != ref.SHA256 {
			return nil, fmt.Errorf("workspace migration: input changed before backup at %s", ref.Path)
		}
		info, err := os.Lstat(ref.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, privateSnapshot{Path: ref.Path, SHA256: ref.SHA256, Mode: uint32(info.Mode().Perm()), Data: append([]byte(nil), data...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func buildReceipt(plan Plan, runID, markerPath string, markerData []byte, adoptions workspace.MigrationAdoptions) (migrationrecord.Receipt, error) {
	proofPath, err := migrationrecord.CommitProofPath(plan.Layout.WorkflowRoot, runID)
	if err != nil {
		return migrationrecord.Receipt{}, err
	}
	proofRel, err := filepath.Rel(plan.Layout.WorkflowRoot, proofPath)
	if err != nil {
		return migrationrecord.Receipt{}, err
	}
	receipt := migrationrecord.Receipt{
		Version:         migrationrecord.ReceiptVersion,
		RunID:           runID,
		PlanHash:        plan.PlanHash,
		Config:          migrationrecord.FileRef{Path: plan.Config.Path, SHA256: plan.Config.SHA256},
		Manifest:        migrationrecord.FileRef{Path: plan.Manifest.Path, SHA256: plan.Manifest.SHA256},
		LayoutMarker:    migrationrecord.FileRef{Path: markerPath, SHA256: migrationDigest(markerData)},
		Registry:        migrationrecord.FileRef{Path: adoptions.RegistryPath, SHA256: migrationDigest(adoptions.RegistryData)},
		RecordWrites:    make([]migrationrecord.RecordWrite, 0, len(plan.RecordWrites)),
		Retired:         make([]migrationrecord.RetiredRecord, 0),
		Bindings:        make([]migrationrecord.Binding, 0, len(adoptions.Worktrees)),
		CommitProofPath: filepath.ToSlash(proofRel),
	}
	for _, write := range plan.RecordWrites {
		var legacy *migrationrecord.LegacyConfig
		if write.LegacyConfig != nil {
			legacy = &migrationrecord.LegacyConfig{BaseRef: write.LegacyConfig.BaseRef, BaseBranch: write.LegacyConfig.BaseBranch, Provenance: write.LegacyConfig.Provenance}
		}
		receipt.RecordWrites = append(receipt.RecordWrites, migrationrecord.RecordWrite{Path: write.Path, PreSHA256: write.PreSHA256, PostSHA256: write.PostSHA256, Action: write.Action, Fields: append([]string{}, write.ChangedFields...), LegacyConfig: legacy})
	}
	retired, err := expectedRetiredRecords(plan)
	if err != nil {
		return migrationrecord.Receipt{}, err
	}
	receipt.Retired = retired
	for _, worktree := range adoptions.Worktrees {
		tokenHash := migrationDigest([]byte(worktree.Ownership))
		receipt.Bindings = append(receipt.Bindings, migrationrecord.Binding{Workflow: worktree.Workflow, Change: worktree.Change, StateID: worktree.StateID, Repo: worktree.Repo, Path: worktree.Path, CommonDir: worktree.CommonDir, GitDir: worktree.GitDir, Branch: worktree.Branch, BaseRef: worktree.BaseRef, BaseTarget: worktree.BaseTarget, BaseCommit: worktree.BaseCommit, OwnershipHash: tokenHash})
	}
	receipt.Bindings = migrationrecord.SortedBindings(receipt.Bindings)
	sort.Slice(receipt.Retired, func(i, j int) bool {
		return receipt.Retired[i].Path+"\x00"+receipt.Retired[i].ID < receipt.Retired[j].Path+"\x00"+receipt.Retired[j].ID
	})
	if err := receipt.Validate(); err != nil {
		return migrationrecord.Receipt{}, err
	}
	return receipt, nil
}

// expectedRetiredRecords derives the public tombstone set from the exact
// reviewed plan, not from whatever receipt bytes happen to be present in a
// private journal. One path-and-ID entry represents each retired record; every
// co-resident preserved state file remains covered by its RecordWrite entries.
func expectedRetiredRecords(plan Plan) ([]migrationrecord.RetiredRecord, error) {
	byPath := make(map[string][]RecordWrite)
	for _, write := range plan.RecordWrites {
		if write.Action != RecordWritePreserveRetired {
			continue
		}
		if !validDigest(write.PreSHA256) || write.PreSHA256 != write.PostSHA256 || len(write.ChangedFields) != 0 || write.LegacyConfig != nil || !retiredStateFilename(filepath.Base(write.Path)) {
			return nil, fmt.Errorf("workspace migration: invalid preserved retired write at %s", write.Path)
		}
		byPath[filepath.Clean(filepath.Dir(write.Path))] = append(byPath[filepath.Clean(filepath.Dir(write.Path))], write)
	}
	expected := make([]migrationrecord.RetiredRecord, 0)
	consumed := map[string]bool{}
	for _, record := range plan.Records {
		if record.Lifecycle != "retired" {
			continue
		}
		if record.ID == "" || !pathWithin(plan.Layout.WorkflowRoot, record.Path) {
			return nil, fmt.Errorf("workspace migration: retired record has an invalid identity")
		}
		writes := byPath[record.Path]
		if len(writes) == 0 {
			return nil, fmt.Errorf("workspace migration: retired record has no preserved state write: %s", record.Path)
		}
		sort.Slice(writes, func(i, j int) bool { return writes[i].Path < writes[j].Path })
		seenFiles := map[string]bool{}
		for _, write := range writes {
			name := filepath.Base(write.Path)
			if seenFiles[name] {
				return nil, fmt.Errorf("workspace migration: retired record has duplicate preserved state writes: %s", record.Path)
			}
			seenFiles[name] = true
		}
		rel, err := filepath.Rel(plan.Layout.WorkflowRoot, record.Path)
		if err != nil || !safeGitRelativePath(filepath.ToSlash(rel)) {
			return nil, fmt.Errorf("workspace migration: retired record path is invalid")
		}
		if record.SchemaVersion < 1 {
			return nil, fmt.Errorf("workspace migration: retired record has an invalid schema")
		}
		expected = append(expected, migrationrecord.RetiredRecord{Path: filepath.ToSlash(rel), ID: record.ID, SchemaVersion: record.SchemaVersion, SHA256: writes[0].PreSHA256})
		consumed[record.Path] = true
	}
	for path := range byPath {
		if !consumed[path] {
			return nil, fmt.Errorf("workspace migration: preserved retired write has no retired record: %s", path)
		}
	}
	sort.Slice(expected, func(i, j int) bool {
		return expected[i].Path+"\x00"+expected[i].ID < expected[j].Path+"\x00"+expected[j].ID
	})
	return expected, nil
}

func retiredStateFilename(name string) bool {
	switch name {
	case "onto-state.yaml", "state.yaml", "to-state.yaml":
		return true
	default:
		return false
	}
}

func migrationBindings(plan Plan) ([]workspace.MigrationBinding, error) {
	bindings, err := plannedBindingTemplates(plan)
	if err != nil {
		return nil, err
	}
	for i := range bindings {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return nil, err
		}
		bindings[i].Ownership = hex.EncodeToString(token[:])
	}
	return bindings, nil
}

func migrationGitText(dir string, args ...string) (string, error) {
	data, err := workspace.ReadGit(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func migrationIgnoredLocks() map[string]bool {
	return migrationGuardianArtifacts()
}

func verifyPlanInputs(l workspace.Layout, plan Plan, requireCleanRecords bool) error {
	return verifyPlanInputsWithIgnored(l, plan, requireCleanRecords, migrationIgnoredLocks())
}

func migrationHeldLockArtifacts(configRoot, workflowRoot string) map[string]bool {
	ignored := migrationIgnoredLocks()
	controlRoot := filepath.Join(configRoot, ".homonto")
	for _, path := range []string{
		filepath.Join(controlRoot, "apply.lock"),
		filepath.Join(controlRoot, "worktrees.lock"),
		filepath.Join(workflowRoot, ".change-names.lock"),
		filepath.Join(workflowRoot, "changes", ".onto.lock"),
		filepath.Join(workflowRoot, "tasks", ".to.lock"),
	} {
		ignored[path] = true
	}
	ignored[filepath.Join(controlRoot, "lock-guardians")+string(filepath.Separator)] = true
	ignored[filepath.Join(workflowRoot, ".homonto", "lock-guardians")+string(filepath.Separator)] = true
	return ignored
}

func migrationPreparationIgnored(l workspace.Layout, runID string) map[string]bool {
	ignored := migrationHeldLockArtifacts(l.ConfigRoot, l.WorkflowRoot)
	if migrationrecord.SafeRunID(runID) {
		ignored[filepath.ToSlash(filepath.Join(".workflow", "migrations", runID, "private"))+"/"] = true
	}
	return ignored
}

func verifyPlanInputsWithIgnored(l workspace.Layout, plan Plan, requireCleanRecords bool, ignored map[string]bool) error {
	if plan.Layout.ConfigPath != l.ConfigPath || plan.Layout.ConfigRoot != l.ConfigRoot || plan.Layout.WorkflowRoot != l.WorkflowRoot || plan.Layout.GitMode != l.GitMode || plan.Layout.SchemaVersion != l.SchemaVersion {
		return fmt.Errorf("workspace migration: layout changed after planning")
	}
	for _, ref := range []FileFingerprint{plan.Config, plan.Manifest} {
		data, err := readRealRegularFile(ref.Path)
		if err != nil || migrationDigest(data) != ref.SHA256 {
			return fmt.Errorf("workspace migration: planned input changed at %s", ref.Path)
		}
	}
	for _, ref := range append(append([]FileFingerprint{}, plan.ControlFiles...), plan.Files...) {
		data, err := readRealRegularFile(ref.Path)
		if err != nil || migrationDigest(data) != ref.SHA256 {
			return fmt.Errorf("workspace migration: planned record changed at %s", ref.Path)
		}
	}
	top, common, err := workspace.GitIdentity(l.WorkflowRoot)
	if err != nil || top != l.WorkflowRoot || common != plan.RecordsGit.GitCommonDir {
		return fmt.Errorf("workspace migration: records Git identity changed after planning")
	}
	head, err := migrationGitText(l.WorkflowRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != plan.RecordsGit.Head {
		return fmt.Errorf("workspace migration: records HEAD changed after planning")
	}
	index, err := gitIndexSHA256(l.WorkflowRoot)
	if err != nil || index != plan.RecordsGit.IndexSHA256 {
		return fmt.Errorf("workspace migration: records index changed after planning")
	}
	dirt, err := gitDirtIgnoring(l.WorkflowRoot, l.WorkflowRoot, ignored)
	if err != nil || dirt != plan.RecordsGit.Dirt {
		return fmt.Errorf("workspace migration: records dirt changed after planning")
	}
	if requireCleanRecords && (dirt.Tracked != 0 || dirt.Untracked != 0 || dirt.Ignored != 0) {
		return fmt.Errorf("workspace migration: records Git must be clean before apply")
	}
	seenSources := map[string]bool{}
	for _, record := range plan.Records {
		for _, source := range record.Sources {
			key := record.Path + "\x00" + source.Alias
			if seenSources[key] {
				continue
			}
			seenSources[key] = true
			if err := verifySource(source); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifySource(source Source) error {
	top, common, err := workspace.GitIdentity(source.Path)
	if err != nil || top != source.Path || common != source.GitCommonDir {
		return fmt.Errorf("workspace migration: source identity changed at %s", source.Path)
	}
	head, err := migrationGitText(source.Path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != source.Head {
		return fmt.Errorf("workspace migration: source HEAD changed at %s", source.Path)
	}
	headRef, headAttached, err := sourceHEADAttachment(source.Path)
	if err != nil || headRef != source.HeadRef || headAttached != source.HeadAttached {
		return fmt.Errorf("workspace migration: source symbolic HEAD changed at %s", source.Path)
	}
	refs, err := sourceReferenceSnapshot(source.Path)
	if err != nil || !reflect.DeepEqual(refs, source.Refs) {
		return fmt.Errorf("workspace migration: source refs changed at %s", source.Path)
	}
	branchHead, err := migrationGitText(source.Path, "rev-parse", "--verify", "--end-of-options", "refs/heads/"+source.BaseBranch+"^{commit}")
	if err != nil || branchHead != source.BaseBranchHead {
		return fmt.Errorf("workspace migration: source base branch changed at %s", source.Path)
	}
	index, err := gitIndexSHA256(source.Path)
	if err != nil || index != source.IndexSHA256 {
		return fmt.Errorf("workspace migration: source index changed at %s", source.Path)
	}
	dirt, err := gitDirt(source.Path)
	if err != nil || dirt != source.Dirt {
		return fmt.Errorf("workspace migration: source dirt changed at %s", source.Path)
	}
	if err := verifySourcePreservation(source.Path, source.Preservation); err != nil {
		return err
	}
	if source.Execution == nil {
		return nil
	}
	execution := source.Execution
	top, common, err = workspace.GitIdentity(execution.Path)
	if err != nil || top != execution.Path || common != execution.GitCommonDir || common != source.GitCommonDir {
		return fmt.Errorf("workspace migration: execution checkout identity changed at %s", execution.Path)
	}
	head, err = migrationGitText(execution.Path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head != execution.Head {
		return fmt.Errorf("workspace migration: execution checkout HEAD changed at %s", execution.Path)
	}
	headRef, headAttached, err = sourceHEADAttachment(execution.Path)
	if err != nil || headRef != execution.HeadRef || headAttached != execution.HeadAttached || !headAttached {
		return fmt.Errorf("workspace migration: execution checkout symbolic HEAD changed at %s", execution.Path)
	}
	refs, err = sourceReferenceSnapshot(execution.Path)
	if err != nil || !reflect.DeepEqual(refs, execution.Refs) {
		return fmt.Errorf("workspace migration: execution checkout refs changed at %s", execution.Path)
	}
	branch, err := migrationGitText(execution.Path, "branch", "--show-current")
	if err != nil || branch != execution.Branch || branch == "" {
		return fmt.Errorf("workspace migration: execution checkout branch changed at %s", execution.Path)
	}
	gitDir, err := migrationGitText(execution.Path, "rev-parse", "--absolute-git-dir")
	if err != nil || gitDir != execution.GitDir {
		return fmt.Errorf("workspace migration: execution checkout Git directory changed at %s", execution.Path)
	}
	index, err = gitIndexSHA256(execution.Path)
	if err != nil || index != execution.IndexSHA256 {
		return fmt.Errorf("workspace migration: execution checkout index changed at %s", execution.Path)
	}
	dirt, err = gitDirt(execution.Path)
	if err != nil || dirt != execution.Dirt {
		return fmt.Errorf("workspace migration: execution checkout dirt changed at %s", execution.Path)
	}
	if err := verifySourcePreservation(execution.Path, execution.Preservation); err != nil {
		return err
	}
	return nil
}

func verifySourcePreservation(root string, expected WorktreePreservation) error {
	actual, err := snapshotSourceWorktree(root)
	if err != nil {
		return fmt.Errorf("workspace migration: source preservation cannot be verified at %s", root)
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("workspace migration: source worktree content changed at %s", root)
	}
	return nil
}

// Apply performs the reviewed schema-2 ownership transition. The caller must
// supply the hash emitted by a fresh read-only plan; the executor repeats that
// inventory while holding every migration lock before it writes a journal.
func Apply(configPath, manifestPath, expectedPlanHash string) (ApplyResult, error) {
	if !validDigest(expectedPlanHash) {
		return ApplyResult{}, fmt.Errorf("workspace migration: --plan-hash must be a lowercase SHA-256 digest")
	}
	if err := requireMigrationFilesystemSupport(); err != nil {
		return ApplyResult{}, err
	}
	configPath, err := absolutePath(configPath)
	if err != nil {
		return ApplyResult{}, err
	}
	// A completed marker makes the legacy planner intentionally unavailable.
	// Treat an exact, verified receipt as an idempotent no-op rather than trying
	// to reinterpret already-owned records.
	if layout, loadErr := workspace.Load(configPath); loadErr == nil {
		if result, found, err := completedApply(layout, manifestPath, expectedPlanHash); err != nil {
			return ApplyResult{}, err
		} else if found {
			return result, nil
		}
		return ApplyResult{}, fmt.Errorf("workspace migration: workspace is already schema-2 owned; no matching completed migration receipt was found")
	}

	var result ApplyResult
	err = withInitialMigrationLocks(configPath, func(layout workspace.Layout) error {
		plan, err := buildLocked(configPath, manifestPath)
		if err != nil || plan.Status != "ready" {
			return fmt.Errorf("workspace migration: apply preflight blocked; run workspace migrate plan --json and review its blockers")
		}
		if plan.PlanHash != expectedPlanHash {
			return fmt.Errorf("workspace migration: stale plan hash; planned inputs changed or the supplied hash is not the locked inventory")
		}
		intent, err := newPreparationIntent(layout, plan)
		if err != nil {
			return err
		}
		if err := savePreparationIntent(intent); err != nil {
			return err
		}
		result = ApplyResult{RunID: intent.RunID, PlanHash: intent.PlanHash, Status: intent.Phase}
		if err := migrationAfterPreparation("intent"); err != nil {
			return fmt.Errorf("workspace migration: run %s is preparing; recover with `homonto workspace migrate recover --run-id %s --action resume --plan-hash %s --yes` or restore: %w", intent.RunID, intent.RunID, intent.PlanHash, err)
		}
		journal, err := prepareJournal(layout, plan, intent)
		if err != nil {
			return fmt.Errorf("workspace migration: run %s is preparing; recover with `homonto workspace migrate recover --run-id %s --action resume --plan-hash %s --yes` or restore: %w", intent.RunID, intent.RunID, intent.PlanHash, err)
		}
		if err := saveJournal(journal); err != nil {
			return fmt.Errorf("workspace migration: run %s is preparing; recover with `homonto workspace migrate recover --run-id %s --action resume --plan-hash %s --yes` or restore: %w", intent.RunID, intent.RunID, intent.PlanHash, err)
		}
		// Read the durable copy before the first authoritative write. This proves
		// a killed process has a complete private preimage rather than only a
		// best-effort in-memory backup.
		journal, err = loadJournal(layout.WorkflowRoot, journal.RunID)
		if err != nil {
			return fmt.Errorf("workspace migration: private backup verification failed: %w", err)
		}
		result = ApplyResult{RunID: journal.RunID, PlanHash: journal.PlanHash, Status: journal.Phase}
		if err := resumeJournalLocked(layout, &journal); err != nil {
			result.Status = journal.Phase
			return fmt.Errorf("workspace migration: run %s is %s; recover with `homonto workspace migrate recover --run-id %s --action resume --plan-hash %s --yes` or restore: %w", journal.RunID, journal.Phase, journal.RunID, journal.PlanHash, err)
		}
		result = ApplyResult{RunID: journal.RunID, PlanHash: journal.PlanHash, Status: journal.Phase}
		return nil
	})
	return result, err
}

func completedApply(layout workspace.Layout, manifestPath, planHash string) (ApplyResult, bool, error) {
	if err := migrationrecord.ValidateBarrier(layout.WorkflowRoot); err != nil {
		return ApplyResult{}, false, err
	}
	manifestPath, err := absolutePath(manifestPath)
	if err != nil {
		return ApplyResult{}, false, err
	}
	manifest, err := readRealRegularFile(manifestPath)
	if err != nil {
		return ApplyResult{}, false, err
	}
	config, err := readRealRegularFile(layout.ConfigPath)
	if err != nil {
		return ApplyResult{}, false, err
	}
	base := filepath.Join(layout.WorkflowRoot, ".workflow", "migrations")
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return ApplyResult{}, false, nil
	}
	if err != nil {
		return ApplyResult{}, false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !migrationrecord.SafeRunID(entry.Name()) {
			continue
		}
		receipt, err := migrationrecord.LoadReceipt(layout.WorkflowRoot, entry.Name())
		if err != nil {
			return ApplyResult{}, false, err
		}
		if receipt.PlanHash != planHash || receipt.Config.Path != layout.ConfigPath || receipt.Config.SHA256 != migrationDigest(config) || receipt.Manifest.Path != manifestPath || receipt.Manifest.SHA256 != migrationDigest(manifest) {
			continue
		}
		if _, err := Verify(layout.ConfigPath, receipt.RunID); err != nil {
			return ApplyResult{}, false, err
		}
		return ApplyResult{RunID: receipt.RunID, PlanHash: receipt.PlanHash, Status: "complete", AlreadyComplete: true}, true, nil
	}
	return ApplyResult{}, false, nil
}

func resumeJournalLocked(layout workspace.Layout, journal *privateJournal) error {
	if err := validateJournalLayout(*journal, layout); err != nil {
		return err
	}
	if err := verifyRecordsGitBackup(layout.WorkflowRoot, journal.RecordsBackup); err != nil {
		return err
	}
	if journal.Phase == "restored" {
		return fmt.Errorf("workspace migration: run %s was restored and cannot be resumed", journal.RunID)
	}
	if journal.Phase == "restoring" {
		return fmt.Errorf("workspace migration: run %s is restoring; use recover --action restore", journal.RunID)
	}
	if journal.Phase == "complete" {
		_, err := verifyJournalInvariant(layout, journal, true, true, false, migrationStatusIgnoredHeld(*journal))
		return err
	}
	if err := verifyJournalInputsLocked(layout, *journal); err != nil {
		return err
	}
	if journal.Phase == "prepared" || journal.Phase == "applying" {
		journal.Phase = "applying"
		if err := saveJournal(*journal); err != nil {
			return err
		}
		if err := applyForwardWrites(*journal, false); err != nil {
			return err
		}
		if err := ensureMigrationCommit(layout, journal); err != nil {
			return err
		}
		if err := ensureProofCommit(layout, journal); err != nil {
			return err
		}
		journal.Phase = "pending-finalization"
		if err := saveJournal(*journal); err != nil {
			return err
		}
	}
	if journal.Phase != "pending-finalization" {
		return fmt.Errorf("workspace migration: unsupported resume phase %q", journal.Phase)
	}
	if _, err := verifyJournalInvariant(layout, journal, false, false, false, migrationStatusIgnoredHeld(*journal)); err != nil {
		return err
	}
	if err := finalizeMarker(layout, *journal); err != nil {
		return err
	}
	if _, err := verifyJournalInvariant(layout, journal, false, true, true, migrationStatusIgnoredHeld(*journal)); err != nil {
		return err
	}
	if err := ensureCompletionWitness(journal); err != nil {
		return err
	}
	if _, err := verifyJournalInvariant(layout, journal, false, true, false, migrationStatusIgnoredHeld(*journal)); err != nil {
		return err
	}
	journal.Phase = "complete"
	return saveJournal(*journal)
}

func resumePreparationLocked(layout workspace.Layout, intent preparationIntent) (*privateJournal, error) {
	if err := validatePreparationIntentLayout(intent, layout); err != nil {
		return nil, err
	}
	plan, err := buildLockedIgnoring(layout.ConfigPath, intent.ManifestPath, migrationPreparationIgnored(layout, intent.RunID))
	if err != nil {
		return nil, fmt.Errorf("workspace migration: preparation intent cannot be replanned: %w", err)
	}
	if plan.Status != "ready" {
		return nil, fmt.Errorf("workspace migration: preparation intent no longer matches a ready plan")
	}
	if plan.PlanHash != intent.PlanHash {
		return nil, fmt.Errorf("workspace migration: preparation intent no longer matches the reviewed plan hash")
	}
	journal, err := prepareJournal(layout, plan, intent)
	if err != nil {
		return nil, err
	}
	if err := saveJournal(journal); err != nil {
		return nil, err
	}
	journal, err = loadJournal(layout.WorkflowRoot, journal.RunID)
	if err != nil {
		return nil, fmt.Errorf("workspace migration: private backup verification failed: %w", err)
	}
	return &journal, nil
}

func discardPreparationLocked(layout workspace.Layout, runID string) error {
	if !migrationrecord.SafeRunID(runID) {
		return fmt.Errorf("workspace migration: invalid preparation run ID")
	}
	for _, pathFn := range []func(string, string) (string, error){preparationIntentPath, recordsGitBackupPath} {
		path, err := pathFn(layout.WorkflowRoot, runID)
		if err != nil {
			return err
		}
		if err := removeMigrationOptionalRegular(layout.WorkflowRoot, path); err != nil {
			return err
		}
	}
	statusPath, err := journalStatusPath(layout.WorkflowRoot, runID)
	if err != nil {
		return err
	}
	if err := removeMigrationOptionalRegular(layout.WorkflowRoot, statusPath); err != nil {
		return err
	}
	privateDir := filepath.Dir(statusPath)
	if err := removeMigrationEmptyDirectory(layout.WorkflowRoot, privateDir); err != nil {
		return err
	}
	if err := removeMigrationEmptyDirectory(layout.WorkflowRoot, filepath.Dir(privateDir)); err != nil {
		return err
	}
	return removeMigrationEmptyDirectory(layout.WorkflowRoot, filepath.Dir(filepath.Dir(privateDir)))
}

func verifyJournalInputs(layout workspace.Layout, journal privateJournal) error {
	return verifyJournalInputsWithIgnored(layout, journal, migrationStatusIgnored(journal))
}

func verifyJournalInputsLocked(layout workspace.Layout, journal privateJournal) error {
	return verifyJournalInputsWithIgnored(layout, journal, migrationStatusIgnoredHeld(journal))
}

func verifyJournalInputsWithIgnored(layout workspace.Layout, journal privateJournal, ignored map[string]bool) error {
	if err := validateJournalLayout(journal, layout); err != nil {
		return err
	}
	if err := verifyJournalRecordsWorkspace(layout, journal, false, ignored); err != nil {
		return err
	}
	if journal.Phase == "prepared" {
		head, err := migrationGitText(layout.WorkflowRoot, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil || head != journal.RecordsParent {
			return fmt.Errorf("workspace migration: records HEAD changed after journal creation")
		}
		index, err := gitIndexSHA256(layout.WorkflowRoot)
		if err != nil || index != journal.RecordsIndexSHA256 {
			return fmt.Errorf("workspace migration: records index changed after journal creation")
		}
		dirt, err := gitDirtIgnoring(layout.WorkflowRoot, layout.WorkflowRoot, ignored)
		if err != nil || dirt != journal.Plan.RecordsGit.Dirt {
			return fmt.Errorf("workspace migration: records dirt changed after journal creation")
		}
	}
	for _, snapshot := range journal.Snapshots {
		// The external manifest is an initial-planning input only. Once the
		// durable journal has captured and authenticated its bytes, completed
		// verification and recovery must not depend on its continued presence.
		if snapshot.Path == journal.Plan.Manifest.Path {
			continue
		}
		data, err := readRealRegularFile(snapshot.Path)
		if err != nil {
			return fmt.Errorf("workspace migration: backup input disappeared at %s", snapshot.Path)
		}
		info, err := os.Lstat(snapshot.Path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace migration: backup input changed type at %s", snapshot.Path)
		}
		if migrationDigest(data) == snapshot.SHA256 && uint32(info.Mode().Perm()) == snapshot.Mode {
			continue
		}
		allowed := false
		for _, write := range journal.Writes {
			if write.Path == snapshot.Path && writeMatches(write, data, info.Mode().Perm(), true, true) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("workspace migration: input changed after journal creation at %s", snapshot.Path)
		}
	}
	for _, record := range journal.Plan.Records {
		for _, source := range record.Sources {
			if err := verifySource(source); err != nil {
				return err
			}
		}
	}
	return nil
}

func verifyJournalRecordsWorkspace(layout workspace.Layout, journal privateJournal, requireClean bool, ignored map[string]bool) error {
	paths, err := journalRecordPaths(journal, journalKindState, journalKindIgnore, journalKindReceipt, journalKindProof)
	if err != nil {
		return err
	}
	if err := ensureOnlyMigrationChanges(layout.WorkflowRoot, paths, ignored); err != nil {
		return err
	}
	if err := verifyJournalRecordsIndex(layout.WorkflowRoot, journal); err != nil {
		return err
	}
	if !requireClean {
		return nil
	}
	dirt, err := gitDirtIgnoring(layout.WorkflowRoot, layout.WorkflowRoot, ignored)
	if err != nil {
		return err
	}
	if dirt.Tracked != 0 || dirt.Untracked != 0 || dirt.Ignored != 0 {
		return fmt.Errorf("workspace migration: records Git is dirty after migration commits")
	}
	return nil
}

func applyForwardWrites(journal privateJournal, includeProof bool) error {
	for _, write := range sortedJournalWrites(journal.Writes) {
		if write.Kind == journalKindMarker || write.Kind == journalKindCompletion || (!includeProof && write.Kind == journalKindProof) {
			continue
		}
		if _, err := applyJournalWrite(journal, write, true); err != nil {
			return err
		}
	}
	return nil
}

func journalWrite(journal privateJournal, kind string) (privateWrite, bool) {
	for _, write := range journal.Writes {
		if write.Kind == kind {
			return write, true
		}
	}
	return privateWrite{}, false
}

func finalizeMarker(layout workspace.Layout, journal privateJournal) error {
	if layout.ConfigPath != journal.ConfigPath || layout.ConfigRoot != journal.ConfigRoot || layout.WorkflowRoot != journal.Workflow {
		return fmt.Errorf("workspace migration: marker finalization layout differs from private journal")
	}
	marker, ok := journalWrite(journal, journalKindMarker)
	if !ok {
		return fmt.Errorf("workspace migration: marker write is absent from journal")
	}
	changed, err := applyJournalWrite(journal, marker, true)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	return migrationAfterMarker()
}

// ensureCompletionWitness is deliberately called only after the marker-active
// final invariant has passed. The journal is persisted with the exact witness
// bytes first, then the separate private file is written; a crash between those
// steps remains safely resumable because neither phase advancement nor normal
// barrier authorization occurs first.
func ensureCompletionWitness(journal *privateJournal) error {
	witness, err := completionWitnessForJournal(*journal)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	for i := range journal.Writes {
		write := &journal.Writes[i]
		if write.Kind != journalKindCompletion {
			continue
		}
		if len(write.Postimage) == 0 {
			write.Postimage = append([]byte(nil), data...)
			if err := saveJournal(*journal); err != nil {
				return err
			}
		} else if !bytes.Equal(write.Postimage, data) {
			return fmt.Errorf("workspace migration: completion witness bytes differ from final authorization")
		}
		if _, err := applyJournalWrite(*journal, *write, true); err != nil {
			return err
		}
		if err := migrationrecord.ValidateCompletionWitness(journal.Workflow, journal.RunID); err != nil {
			return fmt.Errorf("workspace migration: completion authorization did not persist: %w", err)
		}
		return nil
	}
	return fmt.Errorf("workspace migration: reviewed completion witness operation is absent")
}

func migrationStatusIgnored(journal privateJournal) map[string]bool {
	ignored := migrationIgnoredLocks()
	path, err := migrationrecord.JournalPath(journal.Workflow, journal.RunID)
	if err == nil {
		if rel, relErr := filepath.Rel(journal.Workflow, filepath.Dir(path)); relErr == nil {
			ignored[filepath.ToSlash(rel)+"/"] = true
		}
	}
	return ignored
}

func migrationStatusIgnoredHeld(journal privateJournal) map[string]bool {
	ignored := migrationStatusIgnored(journal)
	for path := range migrationHeldLockArtifacts(journal.ConfigRoot, journal.Workflow) {
		ignored[path] = true
	}
	return ignored
}

func journalRecordPaths(journal privateJournal, kinds ...string) ([]string, error) {
	selected := map[string]bool{}
	for _, kind := range kinds {
		selected[kind] = true
	}
	paths := []string{}
	for _, write := range journal.Writes {
		if write.Scope != journalScopeRecords || !selected[write.Kind] {
			continue
		}
		if write.PreExists == write.PostExists && bytes.Equal(write.Preimage, write.Postimage) {
			continue
		}
		rel, err := filepath.Rel(journal.Workflow, write.Path)
		if err != nil || !safeGitRelativePath(filepath.ToSlash(rel)) {
			return nil, fmt.Errorf("workspace migration: unsafe records commit path")
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	return paths, nil
}

func safeGitRelativePath(path string) bool {
	return path != "" && !strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "\\\x00") && filepath.ToSlash(filepath.Clean(path)) == path && path != ".." && !strings.HasPrefix(path, "../")
}

func ensureMigrationCommit(layout workspace.Layout, journal *privateJournal) error {
	if journal.MigrationCommit != "" {
		return verifyMigrationCommit(layout.WorkflowRoot, *journal)
	}
	if err := migrationGitIdentity(layout.WorkflowRoot); err != nil {
		return err
	}
	head, err := migrationGitText(layout.WorkflowRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if head != journal.RecordsParent {
		if journal.ExpectedMigrationTree == "" {
			return fmt.Errorf("workspace migration: records HEAD moved before migration commit")
		}
		candidate := *journal
		candidate.MigrationCommit = head
		if err := verifyMigrationCommit(layout.WorkflowRoot, candidate); err != nil {
			return fmt.Errorf("workspace migration: records HEAD does not match the prepared migration commit: %w", err)
		}
		journal.MigrationCommit = head
		return saveJournal(*journal)
	}
	paths, err := journalRecordPaths(*journal, journalKindState, journalKindIgnore, journalKindReceipt)
	if err != nil {
		return err
	}
	tree, err := prepareRecordsCommit(layout.WorkflowRoot, journal, paths, "migration")
	if err != nil {
		return err
	}
	if journal.ExpectedMigrationTree != "" && journal.ExpectedMigrationTree != tree {
		return fmt.Errorf("workspace migration: staged migration tree changed after preparation")
	}
	if journal.ExpectedMigrationTree == "" {
		journal.ExpectedMigrationTree = tree
		if err := saveJournal(*journal); err != nil {
			return err
		}
	}
	if _, err := migrationGit(layout.WorkflowRoot, "commit", "-m", journal.MigrationMessage); err != nil {
		return fmt.Errorf("workspace migration: records migration commit remains pending: %w", err)
	}
	head, err = migrationGitText(layout.WorkflowRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	journal.MigrationCommit = head
	if err := verifyMigrationCommit(layout.WorkflowRoot, *journal); err != nil {
		return err
	}
	return saveJournal(*journal)
}

func ensureProofCommit(layout workspace.Layout, journal *privateJournal) error {
	if journal.MigrationCommit == "" {
		return fmt.Errorf("workspace migration: migration commit is required before commit proof")
	}
	if err := verifyMigrationCommit(layout.WorkflowRoot, *journal); err != nil {
		return err
	}
	if journal.ProofCommit != "" {
		return verifyProofCommit(layout.WorkflowRoot, *journal)
	}
	proof, ok := journalWrite(*journal, journalKindProof)
	if !ok {
		return fmt.Errorf("workspace migration: reviewed proof operation is absent from private journal")
	}
	if len(proof.Postimage) == 0 {
		if err := addProofWrite(journal); err != nil {
			return err
		}
		if err := saveJournal(*journal); err != nil {
			return err
		}
	}
	proof, _ = journalWrite(*journal, journalKindProof)
	if _, err := applyJournalWrite(*journal, proof, true); err != nil {
		return err
	}
	if err := migrationGitIdentity(layout.WorkflowRoot); err != nil {
		return err
	}
	head, err := migrationGitText(layout.WorkflowRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	if head != journal.MigrationCommit {
		if journal.ExpectedProofTree == "" {
			return fmt.Errorf("workspace migration: records HEAD moved before commit proof")
		}
		candidate := *journal
		candidate.ProofCommit = head
		if err := verifyProofCommit(layout.WorkflowRoot, candidate); err != nil {
			return fmt.Errorf("workspace migration: records HEAD does not match the prepared proof commit: %w", err)
		}
		journal.ProofCommit = head
		return saveJournal(*journal)
	}
	paths, err := journalRecordPaths(*journal, journalKindProof)
	if err != nil {
		return err
	}
	tree, err := prepareRecordsCommit(layout.WorkflowRoot, journal, paths, "proof")
	if err != nil {
		return err
	}
	if journal.ExpectedProofTree != "" && journal.ExpectedProofTree != tree {
		return fmt.Errorf("workspace migration: staged commit-proof tree changed after preparation")
	}
	if journal.ExpectedProofTree == "" {
		journal.ExpectedProofTree = tree
		if err := saveJournal(*journal); err != nil {
			return err
		}
	}
	if _, err := migrationGit(layout.WorkflowRoot, "commit", "-m", journal.ProofMessage); err != nil {
		return fmt.Errorf("workspace migration: records commit proof remains pending: %w", err)
	}
	head, err = migrationGitText(layout.WorkflowRoot, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return err
	}
	journal.ProofCommit = head
	if err := verifyProofCommit(layout.WorkflowRoot, *journal); err != nil {
		return err
	}
	return saveJournal(*journal)
}

func addProofWrite(journal *privateJournal) error {
	proofPath, err := migrationrecord.CommitProofPath(journal.Workflow, journal.RunID)
	if err != nil {
		return err
	}
	proof := migrationrecord.CommitProof{Version: migrationrecord.ReceiptVersion, RunID: journal.RunID, MigrationCommit: journal.MigrationCommit, Parent: journal.RecordsParent, Tree: journal.ExpectedMigrationTree, MessageSHA256: journal.MigrationMessageSHA256}
	if err := proof.Validate(journal.RunID); err != nil {
		return err
	}
	data, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return err
	}
	write, err := prepareWrite(journalScopeRecords, journalKindProof, journal.Workflow, proofPath, false, nil, true, append(data, '\n'), 0o644)
	if err != nil {
		return err
	}
	for i, existing := range journal.Writes {
		if existing.Scope == journalScopeRecords && existing.Kind == journalKindProof && existing.Path == proofPath {
			journal.Writes[i] = write
			return nil
		}
	}
	return fmt.Errorf("workspace migration: reviewed proof operation is absent from private journal")
}

func migrationGitIdentity(root string) error {
	for _, name := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if _, err := migrationGit(root, "var", name); err != nil {
			return fmt.Errorf("workspace migration: records Git author and committer identity are required: %w", err)
		}
	}
	return nil
}

func prepareRecordsCommit(root string, journal *privateJournal, paths []string, kind string) (string, error) {
	if len(paths) == 0 {
		return "", fmt.Errorf("workspace migration: records commit unexpectedly has no paths")
	}
	var beforePhase, afterPhase string
	var recorded *[]recordsIndexEntry
	switch kind {
	case "migration":
		beforePhase, afterPhase = journalIndexPre, journalIndexMigration
		recorded = &journal.MigrationIndexPost
	case "proof":
		beforePhase, afterPhase = journalIndexMigration, journalIndexProof
		recorded = &journal.ProofIndexPost
	default:
		return "", fmt.Errorf("workspace migration: unknown records commit kind")
	}
	baseline, err := derivedJournalIndex(root, *journal, beforePhase)
	if err != nil {
		return "", err
	}
	expected, err := derivedJournalIndex(root, *journal, afterPhase)
	if err != nil {
		return "", err
	}
	return stageRecordsPaths(root, journal, paths, paths, baseline, expected, recorded, kind, true, true)
}

// stageRecordsPaths is the sole migration staging path. It verifies the exact
// old index before git add, then persists and checks the exact post-index. A
// process killed after git add is recognized only when every owned path already
// matches the journaled worktree image; an operator's staged third blob is never
// silently overwritten.
func stageRecordsPaths(root string, journal *privateJournal, paths, expectedStaged []string, baseline, expected []recordsIndexEntry, recorded *[]recordsIndexEntry, stage string, post, requireExactStaged bool) (string, error) {
	if err := ensureOnlyMigrationChanges(root, paths, migrationStatusIgnoredHeld(*journal)); err != nil {
		return "", err
	}
	indexPaths, err := journalIndexPaths(*journal)
	if err != nil {
		return "", err
	}
	if err := verifyJournalRecordPaths(*journal, paths, post); err != nil {
		return "", err
	}
	if recorded != nil && *recorded != nil && !sameRecordsIndex(*recorded, expected) {
		return "", fmt.Errorf("workspace migration: persisted logical records index postimage differs from trusted writes")
	}
	actual, err := captureRecordsIndex(root, indexPaths)
	if err != nil {
		return "", err
	}
	if !sameRecordsIndex(actual, baseline) && !sameRecordsIndex(actual, expected) {
		return "", fmt.Errorf("workspace migration: records index contains a conflicting staged blob, mode, or path")
	}
	if sameRecordsIndex(actual, baseline) {
		args := append([]string{"add", "--all", "--"}, paths...)
		if _, err := migrationGit(root, args...); err != nil {
			return "", err
		}
		if err := verifyJournalRecordPaths(*journal, paths, post); err != nil {
			return "", err
		}
		actual, err = captureRecordsIndex(root, indexPaths)
		if err != nil {
			return "", err
		}
		if !sameRecordsIndex(actual, expected) {
			return "", fmt.Errorf("workspace migration: records index postimage differs from trusted staged writes")
		}
	}
	if recorded != nil {
		if *recorded == nil {
			if err := migrationBeforePostIndexSave(stage); err != nil {
				return "", err
			}
			*recorded = append([]recordsIndexEntry(nil), expected...)
			if err := saveJournal(*journal); err != nil {
				return "", err
			}
			if err := migrationAfterPostIndexSave(stage); err != nil {
				return "", err
			}
		}
	}
	staged, err := stagedPaths(root)
	if err != nil {
		return "", err
	}
	if !stagedPathSetAllowed(staged, expectedStaged, requireExactStaged) {
		return "", fmt.Errorf("workspace migration: records index contains paths outside the prepared commit")
	}
	tree, err := migrationGitText(root, "write-tree")
	if err != nil || !canonicalCommit.MatchString(tree) {
		return "", fmt.Errorf("workspace migration: cannot prepare records Git tree")
	}
	return tree, nil
}

func verifyJournalRecordPaths(journal privateJournal, paths []string, post bool) error {
	wanted := map[string]bool{}
	for _, path := range paths {
		if !safeGitRelativePath(path) {
			return fmt.Errorf("workspace migration: unsafe staged records path")
		}
		wanted[path] = true
	}
	for _, write := range journal.Writes {
		if write.Scope != journalScopeRecords {
			continue
		}
		rel, err := filepath.Rel(journal.Workflow, write.Path)
		if err != nil || !wanted[filepath.ToSlash(rel)] {
			continue
		}
		data, mode, exists, err := readOptionalRegular(journal.Workflow, write.Path)
		if err != nil || !writeMatches(write, data, mode, exists, post) {
			return fmt.Errorf("workspace migration: records path differs from its journaled %simage", map[bool]string{true: "post", false: "pre"}[post])
		}
	}
	return nil
}

func recordsIndexMatchesWorktree(root string, actual, baseline []recordsIndexEntry, stagedPaths []string) (bool, error) {
	staged := map[string]bool{}
	for _, path := range stagedPaths {
		if !safeGitRelativePath(path) || staged[path] {
			return false, fmt.Errorf("workspace migration: invalid staged records path")
		}
		staged[path] = true
	}
	actualByPath := map[string]recordsIndexEntry{}
	for _, entry := range actual {
		actualByPath[entry.Path] = entry
	}
	baselineByPath := map[string]recordsIndexEntry{}
	for _, entry := range baseline {
		baselineByPath[entry.Path] = entry
	}
	for path, expected := range baselineByPath {
		if staged[path] {
			continue
		}
		if actualByPath[path] != expected {
			return false, nil
		}
	}
	for path, entry := range actualByPath {
		if !staged[path] {
			if expected, ok := baselineByPath[path]; !ok || entry != expected {
				return false, nil
			}
		}
	}
	for path := range staged {
		object, mode, exists, err := recordsWorktreeIndexObject(root, path)
		if err != nil {
			return false, err
		}
		entry, indexed := actualByPath[path]
		if exists != indexed {
			return false, nil
		}
		if exists && (entry.Object != object || entry.Mode != mode || entry.Stage != 0) {
			return false, nil
		}
	}
	return true, nil
}

func recordsWorktreeIndexObject(root, path string) (string, uint32, bool, error) {
	full := filepath.Join(root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", 0, false, fmt.Errorf("workspace migration: staged records path is not a real regular file")
	}
	object, err := migrationGitText(root, "hash-object", "--path="+path, "--", full)
	if err != nil || !canonicalCommit.MatchString(object) {
		return "", 0, false, fmt.Errorf("workspace migration: cannot hash staged records path")
	}
	mode := uint32(0o100644)
	if info.Mode()&0o111 != 0 {
		mode = 0o100755
	}
	return object, mode, true, nil
}

func stagedPathSetAllowed(actual, expected []string, requireExact bool) bool {
	allowed := map[string]bool{}
	for _, path := range expected {
		allowed[path] = true
	}
	for _, path := range actual {
		if !allowed[path] {
			return false
		}
	}
	return !requireExact || sameStringSet(actual, expected)
}

func ensureOnlyMigrationChanges(root string, allowed []string, ignored map[string]bool) error {
	allowedSet := map[string]bool{}
	for _, path := range allowed {
		allowedSet[path] = true
	}
	data, err := migrationGit(root, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all", "--ignored", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	for _, entry := range bytesSplitNUL(data) {
		if len(entry) < 4 || entry[2] != ' ' {
			return fmt.Errorf("workspace migration: malformed records Git status")
		}
		path := string(entry[3:])
		if ignoredGitPath(root, path, ignored) {
			continue
		}
		if !allowedSet[path] {
			return fmt.Errorf("workspace migration: records Git has unrelated changed path %q", path)
		}
	}
	return nil
}

func stagedPaths(root string) ([]string, error) {
	data, err := migrationGit(root, "diff", "--cached", "--name-only", "--no-renames", "-z")
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, path := range strings.Split(string(data), "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func sameStringSet(actual, expected []string) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

func rawCommitMessage(root, commit string) ([]byte, error) {
	data, err := migrationGit(root, "cat-file", "commit", commit)
	if err != nil {
		return nil, err
	}
	_, message, found := bytes.Cut(data, []byte("\n\n"))
	if !found {
		return nil, fmt.Errorf("workspace migration: malformed Git commit")
	}
	return message, nil
}

func verifyMigrationCommit(root string, journal privateJournal) error {
	if journal.MigrationCommit == "" || journal.ExpectedMigrationTree == "" {
		return fmt.Errorf("workspace migration: migration commit is not recorded")
	}
	parents, err := migrationGitText(root, "rev-list", "--parents", "-n", "1", journal.MigrationCommit)
	if err != nil {
		return err
	}
	fields := strings.Fields(parents)
	if len(fields) != 2 || fields[0] != journal.MigrationCommit || fields[1] != journal.RecordsParent {
		return fmt.Errorf("workspace migration: migration commit parent differs from prepared records HEAD")
	}
	tree, err := migrationGitText(root, "rev-parse", "--verify", journal.MigrationCommit+"^{tree}")
	if err != nil || tree != journal.ExpectedMigrationTree {
		return fmt.Errorf("workspace migration: migration commit tree differs from prepared tree")
	}
	message, err := rawCommitMessage(root, journal.MigrationCommit)
	if err != nil || migrationDigest(message) != journal.MigrationMessageSHA256 {
		return fmt.Errorf("workspace migration: migration commit message differs from prepared message")
	}
	expected, err := journalRecordPaths(journal, journalKindState, journalKindIgnore, journalKindReceipt)
	if err != nil {
		return err
	}
	actual, err := commitChangedPaths(root, journal.RecordsParent, journal.MigrationCommit)
	if err != nil || !sameStringSet(actual, expected) {
		return fmt.Errorf("workspace migration: migration commit paths differ from prepared write set")
	}
	return nil
}

func verifyProofCommit(root string, journal privateJournal) error {
	if journal.ProofCommit == "" || journal.ExpectedProofTree == "" {
		return fmt.Errorf("workspace migration: commit proof is not recorded")
	}
	parents, err := migrationGitText(root, "rev-list", "--parents", "-n", "1", journal.ProofCommit)
	if err != nil {
		return err
	}
	fields := strings.Fields(parents)
	if len(fields) != 2 || fields[0] != journal.ProofCommit || fields[1] != journal.MigrationCommit {
		return fmt.Errorf("workspace migration: proof commit parent differs from migration commit")
	}
	tree, err := migrationGitText(root, "rev-parse", "--verify", journal.ProofCommit+"^{tree}")
	if err != nil || tree != journal.ExpectedProofTree {
		return fmt.Errorf("workspace migration: proof commit tree differs from prepared tree")
	}
	message, err := rawCommitMessage(root, journal.ProofCommit)
	if err != nil || !bytes.Equal(message, []byte(journal.ProofMessage+"\n")) {
		return fmt.Errorf("workspace migration: proof commit message differs from prepared message")
	}
	expected, err := journalRecordPaths(journal, journalKindProof)
	if err != nil {
		return err
	}
	actual, err := commitChangedPaths(root, journal.MigrationCommit, journal.ProofCommit)
	if err != nil || !sameStringSet(actual, expected) {
		return fmt.Errorf("workspace migration: proof commit paths differ from prepared write set")
	}
	return nil
}

func verifyRestoreCommit(root string, journal privateJournal) error {
	if journal.RestoreCommit == "" || journal.RestoreParent == "" || journal.ExpectedRestoreTree == "" {
		return fmt.Errorf("workspace migration: restoration commit is not recorded")
	}
	wantParent := journal.MigrationCommit
	if journal.ProofCommit != "" {
		wantParent = journal.ProofCommit
	}
	if journal.RestoreParent != wantParent {
		return fmt.Errorf("workspace migration: restoration parent differs from completed migration history")
	}
	parents, err := migrationGitText(root, "rev-list", "--parents", "-n", "1", journal.RestoreCommit)
	if err != nil {
		return err
	}
	fields := strings.Fields(parents)
	if len(fields) != 2 || fields[0] != journal.RestoreCommit || fields[1] != journal.RestoreParent {
		return fmt.Errorf("workspace migration: restoration commit parent differs from prepared history")
	}
	tree, err := migrationGitText(root, "rev-parse", "--verify", journal.RestoreCommit+"^{tree}")
	if err != nil || tree != journal.ExpectedRestoreTree {
		return fmt.Errorf("workspace migration: restoration commit tree differs from prepared tree")
	}
	message, err := rawCommitMessage(root, journal.RestoreCommit)
	if err != nil || !bytes.Equal(message, []byte(journal.RestoreMessage+"\n")) {
		return fmt.Errorf("workspace migration: restoration commit message differs from prepared message")
	}
	kinds := []string{journalKindState, journalKindIgnore, journalKindReceipt}
	if journal.RestoreParent == journal.ProofCommit && journal.ProofCommit != "" {
		kinds = append(kinds, journalKindProof)
	}
	expected, err := journalRecordPaths(journal, kinds...)
	if err != nil {
		return err
	}
	actual, err := commitChangedPaths(root, journal.RestoreParent, journal.RestoreCommit)
	if err != nil || !sameStringSet(actual, expected) {
		return fmt.Errorf("workspace migration: restoration commit paths differ from prepared write set")
	}
	return nil
}

func commitChangedPaths(root, parent, commit string) ([]string, error) {
	data, err := migrationGit(root, "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z", parent, commit)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, path := range strings.Split(string(data), "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths, nil
}

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
		if material.orphan {
			if err := discardPreparationLocked(layout, runID); err != nil {
				return err
			}
			result.Status = "preparation-cleared"
			return nil
		}
		if material.intent != nil {
			if action == "restore" {
				if err := discardPreparationLocked(layout, material.intent.RunID); err != nil {
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
