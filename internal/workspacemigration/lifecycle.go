package workspacemigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
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
	journalKindRecoveryIdentity         = "recovery_identity"
	journalKindRecoveryStoreDirectory   = "recovery_store_directory"
	journalKindRecoveryBlobDirectory    = "recovery_blob_directory"
	journalKindRecoveryDescriptorDir    = "recovery_descriptor_directory"
	journalKindRecoveryDescriptor       = "recovery_payload_descriptor"
	journalKindRecoveryBlob             = "recovery_payload_blob"
	journalKindRecoveryRetired          = "recovery_retired"
	journalKindRecoveryStatus           = "recovery_status"
	journalKindRecoveryJournal          = "recovery_journal"
	journalKindRecoveryIntent           = "recovery_intent"
	journalKindRecoveryBundle           = "recovery_bundle"
	journalKindRecoveryCompletion       = "recovery_completion"
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

// recoveryJournalProgress is the bounded, private delta that lets recovery
// reconstruct a successor journal before it accepts a staged blob prefix. It
// deliberately excludes the immutable plan, snapshots, write preimages,
// bindings, and private owner seed carried by the predecessor journal.
type recoveryJournalProgress struct {
	Phase                 string `json:"p"`
	ExpectedMigrationTree string `json:"m,omitempty"`
	MigrationCommit       string `json:"c,omitempty"`
	ExpectedProofTree     string `json:"q,omitempty"`
	ProofCommit           string `json:"d,omitempty"`
	RestoreParent         string `json:"r,omitempty"`
	ExpectedRestoreTree   string `json:"t,omitempty"`
	RestoreCommit         string `json:"x,omitempty"`
	ProofPrepared         bool   `json:"w,omitempty"`
	CompletionPrepared    bool   `json:"n,omitempty"`
	MigrationIndexPost    bool   `json:"i,omitempty"`
	ProofIndexPost        bool   `json:"j,omitempty"`
	RestoreIndexPost      bool   `json:"k,omitempty"`
}

func (progress recoveryJournalProgress) validate() error {
	if !validJournalPhase(progress.Phase) {
		return fmt.Errorf("invalid journal progress phase")
	}
	for _, tree := range []string{progress.ExpectedMigrationTree, progress.ExpectedProofTree, progress.ExpectedRestoreTree} {
		if tree != "" && !canonicalCommit.MatchString(tree) {
			return fmt.Errorf("invalid journal progress tree")
		}
	}
	for _, commit := range []string{progress.MigrationCommit, progress.ProofCommit, progress.RestoreParent, progress.RestoreCommit} {
		if commit != "" && !canonicalCommit.MatchString(commit) {
			return fmt.Errorf("invalid journal progress commit")
		}
	}
	return nil
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
		identity, err := newRecoveryIdentity(intent, plan)
		if err != nil {
			return err
		}
		if err := publishRecoveryIdentity(identity); err != nil {
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

func preparationRecordsGitBackup(layout workspace.Layout, intent preparationIntent) (recordsGitBackup, error) {
	plan, err := buildLockedIgnoring(layout.ConfigPath, intent.ManifestPath, migrationPreparationIgnored(layout, intent.RunID))
	if err != nil {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: preparation intent cannot be replanned for cleanup: %w", err)
	}
	if plan.Status != "ready" || plan.PlanHash != intent.PlanHash {
		return recordsGitBackup{}, fmt.Errorf("workspace migration: preparation intent no longer matches the reviewed plan hash")
	}
	return plannedRecordsGitBackup(layout.WorkflowRoot, intent.RunID, plan.RecordsGit)
}

// regeneratePreparationPayloads recomputes only the initial immutable payloads
// that can be authenticated from the still-durable preparation identity. It
// does not write a bundle or journal: callers use the bytes solely to classify
// a staged blob prefix before removing it.
func regeneratePreparationPayloads(layout workspace.Layout, intent preparationIntent) (map[string][]byte, error) {
	if err := validatePreparationIntentLayout(intent, layout); err != nil {
		return nil, err
	}
	plan, err := buildLockedIgnoring(layout.ConfigPath, intent.ManifestPath, migrationPreparationIgnored(layout, intent.RunID))
	if err != nil {
		return nil, fmt.Errorf("workspace migration: preparation intent cannot be replanned for payload recovery: %w", err)
	}
	if plan.Status != "ready" || plan.PlanHash != intent.PlanHash {
		return nil, fmt.Errorf("workspace migration: preparation intent no longer matches the reviewed plan hash")
	}
	backup, bundle, err := regenerateRecordsGitBackup(layout.WorkflowRoot, intent, plan.RecordsGit)
	if err != nil {
		return nil, err
	}
	journal, err := prepareJournalWithBackup(layout, plan, intent, &backup)
	if err != nil {
		return nil, err
	}
	journalPayload, err := journalData(journal)
	if err != nil {
		return nil, err
	}
	return map[string][]byte{
		journalKindRecoveryBundle:  bundle,
		journalKindRecoveryJournal: journalPayload,
	}, nil
}

func validatePreparationBundleData(layout workspace.Layout, intent preparationIntent, data []byte) error {
	backup, err := preparationRecordsGitBackup(layout, intent)
	if err != nil {
		return err
	}
	backup.BundleSHA256 = migrationDigest(data)
	return verifyRecordsGitBackupData(layout.WorkflowRoot, backup, data)
}

func preparationBundleExpectation(layout workspace.Layout, intent preparationIntent) (*migrationRegularExpectation, error) {
	backup, err := preparationRecordsGitBackup(layout, intent)
	if err != nil {
		return nil, err
	}
	data, mode, exists, err := readMigrationOptionalRegular(layout.WorkflowRoot, backup.BundlePath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return &migrationRegularExpectation{}, nil
	}
	if mode.Perm() != 0o600 {
		return nil, fmt.Errorf("workspace migration: private records Git backup is not a 0600 regular file")
	}
	backup.BundleSHA256 = migrationDigest(data)
	if err := verifyRecordsGitBackup(layout.WorkflowRoot, backup); err != nil {
		return nil, err
	}
	return &migrationRegularExpectation{exists: true, data: data, mode: mode}, nil
}

func discardPreparationLocked(layout workspace.Layout, runID string, intent *preparationIntent) error {
	if !migrationrecord.SafeRunID(runID) {
		return fmt.Errorf("workspace migration: invalid preparation run ID")
	}
	bundlePath, err := recordsGitBackupPath(layout.WorkflowRoot, runID)
	if err != nil {
		return err
	}
	var identity recoveryIdentity
	if intent != nil {
		if intent.RunID != runID {
			return fmt.Errorf("workspace migration: preparation intent does not match the requested cleanup run")
		}
		identity, err = loadRecoveryIdentity(layout.WorkflowRoot, runID)
		if err != nil || !recoveryIdentityMatchesIntent(identity, *intent) || validateRecoveryIdentityLayout(identity, layout) != nil {
			return fmt.Errorf("workspace migration: durable recovery identity is missing or differs from preparation cleanup")
		}
		expectedBundle, err := preparationBundleExpectation(layout, *intent)
		if err != nil {
			return err
		}
		var replayed map[string][]byte
		replay := func(descriptor recoveryPayloadDescriptor) ([]byte, error) {
			switch descriptor.Purpose {
			case journalKindRecoveryBundle, journalKindRecoveryJournal:
			default:
				return nil, fmt.Errorf("workspace migration: durable recovery blob temporary is not an exact immutable payload")
			}
			if replayed == nil {
				var err error
				replayed, err = regeneratePreparationPayloads(layout, *intent)
				if err != nil {
					return nil, err
				}
			}
			payload, ok := replayed[descriptor.Purpose]
			if !ok {
				return nil, fmt.Errorf("workspace migration: durable recovery payload cannot be replayed")
			}
			return payload, nil
		}
		if err := discardPendingRecoveryDescriptorStages(identity, replay); err != nil {
			return err
		}
		// The intent remains durable until its dependent backup is gone. A failed
		// unlink can therefore be retried without losing the only preparation
		// identity that authorizes the backup's removal.
		if err := removeMigrationOptionalRegularExpected(layout.WorkflowRoot, bundlePath, expectedBundle); err != nil {
			return err
		}
		intentPath, err := preparationIntentPath(layout.WorkflowRoot, runID)
		if err != nil {
			return err
		}
		intentData, err := preparationIntentData(*intent)
		if err != nil {
			return err
		}
		actualIntent, actualMode, actualExists, err := readMigrationOptionalRegular(layout.WorkflowRoot, intentPath)
		if err != nil {
			return err
		}
		if actualExists && (!bytes.Equal(actualIntent, intentData) || actualMode.Perm() != 0o600) {
			return fmt.Errorf("workspace migration: preparation intent differs from durable status identity")
		}
		if err := removeMigrationOptionalRegularExpected(layout.WorkflowRoot, intentPath, &migrationRegularExpectation{exists: actualExists, data: actualIntent, mode: actualMode}); err != nil {
			return err
		}
	} else {
		// IsPreparationOrphan has already established that an orphan has no
		// backup. Do not reinterpret a newly appeared bundle as ours.
		if _, _, exists, err := readMigrationOptionalRegular(layout.WorkflowRoot, bundlePath); err != nil || exists {
			if err != nil {
				return err
			}
			return fmt.Errorf("workspace migration: preparation backup is not recognized; refusing automatic cleanup")
		}
	}
	statusPath, err := journalStatusPath(layout.WorkflowRoot, runID)
	if err != nil {
		return err
	}
	if intent == nil {
		privateDir := filepath.Dir(statusPath)
		// IsPreparationOrphan established that this run contains only empty
		// scaffolding. Empty-directory removal rechecks that claim through pinned
		// parents, so a raced status or temporary file remains in place.
		if err := removeMigrationEmptyDirectory(layout.WorkflowRoot, privateDir); err != nil {
			return err
		}
		if err := removeMigrationEmptyDirectory(layout.WorkflowRoot, filepath.Dir(privateDir)); err != nil {
			return err
		}
		return removeMigrationEmptyDirectory(layout.WorkflowRoot, filepath.Dir(filepath.Dir(privateDir)))
	}
	statusData, err := journalStatusData(*intent)
	if err != nil {
		return err
	}
	if err := removeMigrationOptionalRegularExpected(layout.WorkflowRoot, statusPath, &migrationRegularExpectation{exists: true, data: statusData, mode: 0o600}); err != nil {
		return err
	}
	// The identity, immutable payload blobs, and descriptors remain as durable
	// evidence. A small atomic retirement marker is published only after every
	// preparation target is gone, so a kill before it remains safely resumable.
	return publishRecoveryRetired(identity)
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
	data, err := completionWitnessData(*journal)
	if err != nil {
		return err
	}
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

func completionWitnessData(journal privateJournal) ([]byte, error) {
	witness, err := completionWitnessForJournal(journal)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(witness, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
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
