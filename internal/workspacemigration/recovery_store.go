package workspacemigration

import (
	"bytes"
	"compress/flate"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

const (
	recoveryIdentityVersion          = 1
	recoveryPayloadDescriptorVersion = 1
	recoveryIdentityName             = "recovery-identity"
	recoveryRetiredName              = "recovery-retired"
	recoveryStoreName                = "recovery-store"
	recoveryBlobDirectoryName        = "blobs"
	recoveryDescriptorDirectoryName  = "descriptors"
	recoveryDescriptorStagePrefix    = ".homonto-recovery-descriptor-stage-"
	recoveryDescriptorHistoryPrefix  = "history-"

	recoveryIdentityLinkPrefix   = "homonto-recovery-identity-v1:"
	recoveryDescriptorLinkPrefix = "homonto-recovery-descriptor-v1:"
	// macOS limits symlink targets to 1 KiB. Keep the opaque metadata below that
	// boundary so an overlong layout is rejected before the run creates any
	// payload or recovery directory contents.
	recoveryLinkTargetLimit  = 960
	recoveryLinkPayloadLimit = 4096

	recoveryBlobMode = 0o400
)

// recoveryIdentity is the first non-directory artifact of a migration run. It
// is encoded in an opaque, atomically-created symlink rather than a regular
// file, so creating the identity has no partial-file write window.
type recoveryIdentity struct {
	Version        int    `json:"version"`
	RunID          string `json:"run_id"`
	Owner          string `json:"owner"`
	PlanHash       string `json:"plan_hash"`
	ConfigPath     string `json:"config_path"`
	ConfigRoot     string `json:"config_root"`
	ConfigSHA256   string `json:"config_sha256"`
	Workflow       string `json:"workflow_root"`
	ManifestPath   string `json:"manifest_path"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

type recoveryIdentityWire struct {
	Version        int    `json:"v"`
	RunID          string `json:"r"`
	Owner          string `json:"o"`
	PlanHash       string `json:"p"`
	ConfigPath     string `json:"c"`
	ConfigSHA256   string `json:"h"`
	Workflow       string `json:"w"`
	ManifestPath   string `json:"m"`
	ManifestSHA256 string `json:"H"`
}

// recoveryPayloadDescriptor is published atomically before a private target
// replacement starts. Its immutable blob holds the exact bytes that authorize
// an interrupted target temporary.
type recoveryPayloadDescriptor struct {
	Version               int                      `json:"version"`
	RunID                 string                   `json:"run_id"`
	Owner                 string                   `json:"owner"`
	IdentitySHA256        string                   `json:"identity_sha256"`
	Purpose               string                   `json:"purpose"`
	Target                string                   `json:"target"`
	Mode                  uint32                   `json:"mode"`
	Size                  int64                    `json:"size"`
	SHA256                string                   `json:"sha256"`
	JournalPreviousSHA256 string                   `json:"journal_previous_sha256,omitempty"`
	JournalProgress       *recoveryJournalProgress `json:"journal_progress,omitempty"`
}

type recoveryPayloadDescriptorWire struct {
	Version               int                      `json:"v"`
	RunID                 string                   `json:"r"`
	Owner                 string                   `json:"o"`
	IdentitySHA256        string                   `json:"i"`
	Purpose               string                   `json:"p"`
	Target                string                   `json:"t"`
	Mode                  uint32                   `json:"m"`
	Size                  int64                    `json:"s"`
	SHA256                string                   `json:"h"`
	JournalPreviousSHA256 string                   `json:"q,omitempty"`
	JournalProgress       *recoveryJournalProgress `json:"j,omitempty"`
}

func newRecoveryIdentity(intent preparationIntent, plan Plan) (recoveryIdentity, error) {
	identity := recoveryIdentity{
		Version:        recoveryIdentityVersion,
		RunID:          intent.RunID,
		Owner:          intent.Owner,
		PlanHash:       intent.PlanHash,
		ConfigPath:     intent.ConfigPath,
		ConfigRoot:     intent.ConfigRoot,
		ConfigSHA256:   plan.Config.SHA256,
		Workflow:       intent.Workflow,
		ManifestPath:   intent.ManifestPath,
		ManifestSHA256: plan.Manifest.SHA256,
	}
	if err := identity.validate(); err != nil {
		return recoveryIdentity{}, err
	}
	if plan.PlanHash != intent.PlanHash || plan.Config.Path != intent.ConfigPath || plan.Manifest.Path != intent.ManifestPath {
		return recoveryIdentity{}, fmt.Errorf("workspace migration: preparation identity does not match the reviewed plan")
	}
	if _, err := recoveryIdentityLinkTarget(identity); err != nil {
		return recoveryIdentity{}, err
	}
	return identity, nil
}

func (identity recoveryIdentity) validate() error {
	if identity.Version != recoveryIdentityVersion || !migrationrecord.SafeRunID(identity.RunID) || !validMigrationOwner(identity.Owner) || !validDigest(identity.PlanHash) || !validDigest(identity.ConfigSHA256) || !validDigest(identity.ManifestSHA256) {
		return fmt.Errorf("workspace migration: invalid durable recovery identity")
	}
	for _, path := range []string{identity.ConfigPath, identity.ConfigRoot, identity.Workflow, identity.ManifestPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("workspace migration: invalid durable recovery identity path")
		}
	}
	if filepath.Dir(identity.ConfigPath) != identity.ConfigRoot {
		return fmt.Errorf("workspace migration: invalid durable recovery identity layout")
	}
	return nil
}

func (identity recoveryIdentity) preparationIntent() (preparationIntent, error) {
	intent := preparationIntent{
		Version:      migrationPreparationIntentVersion,
		RunID:        identity.RunID,
		Phase:        "preparing",
		PlanHash:     identity.PlanHash,
		ConfigPath:   identity.ConfigPath,
		ConfigRoot:   identity.ConfigRoot,
		Workflow:     identity.Workflow,
		ManifestPath: identity.ManifestPath,
		Owner:        identity.Owner,
		Guardian:     migrationGuardianIdentity,
	}
	if err := intent.validate(); err != nil {
		return preparationIntent{}, err
	}
	return intent, nil
}

func recoveryIdentityMatchesIntent(identity recoveryIdentity, intent preparationIntent) bool {
	return identity.RunID == intent.RunID && identity.Owner == intent.Owner && identity.PlanHash == intent.PlanHash && identity.ConfigPath == intent.ConfigPath && identity.ConfigRoot == intent.ConfigRoot && identity.Workflow == intent.Workflow && identity.ManifestPath == intent.ManifestPath
}

func validateRecoveryIdentityLayout(identity recoveryIdentity, layout workspace.Layout) error {
	if err := identity.validate(); err != nil {
		return err
	}
	if identity.ConfigPath != layout.ConfigPath || identity.ConfigRoot != layout.ConfigRoot || identity.Workflow != layout.WorkflowRoot {
		return fmt.Errorf("workspace migration: durable recovery identity does not belong to this workspace layout")
	}
	return nil
}

func validateRecoveryIdentityInputs(identity recoveryIdentity) error {
	if err := identity.validate(); err != nil {
		return err
	}
	config, err := readRealRegularFile(identity.ConfigPath)
	if err != nil || migrationDigest(config) != identity.ConfigSHA256 {
		return fmt.Errorf("workspace migration: durable recovery identity configuration input changed")
	}
	manifest, err := readRealRegularFile(identity.ManifestPath)
	if err != nil || migrationDigest(manifest) != identity.ManifestSHA256 {
		return fmt.Errorf("workspace migration: durable recovery identity manifest input changed")
	}
	return nil
}

func recoveryIdentityData(identity recoveryIdentity) ([]byte, error) {
	if err := identity.validate(); err != nil {
		return nil, err
	}
	return json.Marshal(recoveryIdentityWire{
		Version:        identity.Version,
		RunID:          identity.RunID,
		Owner:          identity.Owner,
		PlanHash:       identity.PlanHash,
		ConfigPath:     identity.ConfigPath,
		ConfigSHA256:   identity.ConfigSHA256,
		Workflow:       identity.Workflow,
		ManifestPath:   identity.ManifestPath,
		ManifestSHA256: identity.ManifestSHA256,
	})
}

func recoveryIdentityDigest(identity recoveryIdentity) (string, error) {
	data, err := recoveryIdentityData(identity)
	if err != nil {
		return "", err
	}
	return migrationDigest(data), nil
}

func recoveryIdentityLinkTarget(identity recoveryIdentity) (string, error) {
	data, err := recoveryIdentityData(identity)
	if err != nil {
		return "", err
	}
	return recoveryOpaqueLinkTarget(recoveryIdentityLinkPrefix, data)
}

func parseRecoveryIdentityLinkTarget(target string) (recoveryIdentity, error) {
	data, err := parseRecoveryOpaqueLinkTarget(recoveryIdentityLinkPrefix, target)
	if err != nil {
		return recoveryIdentity{}, err
	}
	var wire recoveryIdentityWire
	if err := decodePrivateJSON(data, &wire); err != nil {
		return recoveryIdentity{}, err
	}
	identity := recoveryIdentity{
		Version:        wire.Version,
		RunID:          wire.RunID,
		Owner:          wire.Owner,
		PlanHash:       wire.PlanHash,
		ConfigPath:     wire.ConfigPath,
		ConfigRoot:     filepath.Dir(wire.ConfigPath),
		ConfigSHA256:   wire.ConfigSHA256,
		Workflow:       wire.Workflow,
		ManifestPath:   wire.ManifestPath,
		ManifestSHA256: wire.ManifestSHA256,
	}
	canonical, err := recoveryIdentityLinkTarget(identity)
	if err != nil || canonical != target {
		return recoveryIdentity{}, fmt.Errorf("workspace migration: durable recovery identity is not canonical")
	}
	if err := identity.validate(); err != nil {
		return recoveryIdentity{}, err
	}
	return identity, nil
}

func recoveryOpaqueLinkTarget(prefix string, data []byte) (string, error) {
	if len(data) == 0 || len(data) > recoveryLinkPayloadLimit {
		return "", fmt.Errorf("workspace migration: durable recovery metadata exceeds the supported atomic link size")
	}
	var compressed bytes.Buffer
	writer, err := flate.NewWriter(&compressed, flate.BestCompression)
	if err != nil {
		return "", err
	}
	if _, err := writer.Write(data); err != nil {
		_ = writer.Close()
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	target := prefix + base64.RawURLEncoding.EncodeToString(compressed.Bytes())
	if len(target) == len(prefix) || len(target) > recoveryLinkTargetLimit {
		return "", fmt.Errorf("workspace migration: durable recovery metadata exceeds the supported atomic link size")
	}
	return target, nil
}

func parseRecoveryOpaqueLinkTarget(prefix, target string) ([]byte, error) {
	if !strings.HasPrefix(target, prefix) || len(target) > recoveryLinkTargetLimit {
		return nil, fmt.Errorf("workspace migration: durable recovery metadata is invalid")
	}
	compressed, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(target, prefix))
	if err != nil || len(compressed) == 0 {
		return nil, fmt.Errorf("workspace migration: durable recovery metadata is invalid")
	}
	reader := flate.NewReader(bytes.NewReader(compressed))
	data, readErr := io.ReadAll(io.LimitReader(reader, recoveryLinkPayloadLimit+1))
	closeErr := reader.Close()
	if err := errors.Join(readErr, closeErr); err != nil || len(data) == 0 || len(data) > recoveryLinkPayloadLimit {
		return nil, fmt.Errorf("workspace migration: durable recovery metadata is invalid")
	}
	return data, nil
}

func recoveryPrivateDirectory(root, runID string) (string, error) {
	path, err := journalPath(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Dir(path), nil
}

func recoveryIdentityPath(root, runID string) (string, error) {
	private, err := recoveryPrivateDirectory(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(private, recoveryIdentityName), nil
}

func recoveryRetiredPath(root, runID string) (string, error) {
	private, err := recoveryPrivateDirectory(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(private, recoveryRetiredName), nil
}

func recoveryStorePath(root, runID string) (string, error) {
	private, err := recoveryPrivateDirectory(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(private, recoveryStoreName), nil
}

func recoveryBlobDirectory(root, runID string) (string, error) {
	store, err := recoveryStorePath(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(store, recoveryBlobDirectoryName), nil
}

func recoveryDescriptorDirectory(root, runID string) (string, error) {
	store, err := recoveryStorePath(root, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(store, recoveryDescriptorDirectoryName), nil
}

func publishRecoveryIdentity(identity recoveryIdentity) error {
	if err := identity.validate(); err != nil {
		return err
	}
	if _, err := ensurePrivateJournalDirectory(identity.Workflow, identity.RunID); err != nil {
		return err
	}
	path, err := recoveryIdentityPath(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	target, err := recoveryIdentityLinkTarget(identity)
	if err != nil {
		return err
	}
	return publishMigrationOpaqueSymlink(identity.Workflow, path, target, "recovery-identity")
}

func loadRecoveryIdentity(root, runID string) (recoveryIdentity, error) {
	path, err := recoveryIdentityPath(root, runID)
	if err != nil {
		return recoveryIdentity{}, err
	}
	target, exists, err := readMigrationOpaqueSymlink(root, path)
	if err != nil {
		return recoveryIdentity{}, err
	}
	if !exists {
		return recoveryIdentity{}, os.ErrNotExist
	}
	identity, err := parseRecoveryIdentityLinkTarget(target)
	if err != nil || identity.RunID != runID || identity.Workflow != root {
		return recoveryIdentity{}, fmt.Errorf("workspace migration: durable recovery identity does not match its run directory")
	}
	return identity, nil
}

func recoveryRetiredLinkTarget(identity recoveryIdentity) (string, error) {
	identityTarget, err := recoveryIdentityLinkTarget(identity)
	if err != nil {
		return "", err
	}
	return migrationrecord.RecoveryRetiredLinkPrefix + migrationDigest([]byte(identityTarget)), nil
}

func publishRecoveryRetired(identity recoveryIdentity) error {
	if err := identity.validate(); err != nil {
		return err
	}
	stored, err := loadRecoveryIdentity(identity.Workflow, identity.RunID)
	if err != nil || stored != identity {
		return fmt.Errorf("workspace migration: durable recovery identity is missing or changed before preparation retirement")
	}
	path, err := recoveryRetiredPath(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	target, err := recoveryRetiredLinkTarget(identity)
	if err != nil {
		return err
	}
	return publishMigrationOpaqueSymlink(identity.Workflow, path, target, "recovery-retired")
}

func recoveryPreparationRetired(root, runID string) (bool, error) {
	identity, err := loadRecoveryIdentity(root, runID)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	path, err := recoveryRetiredPath(root, runID)
	if err != nil {
		return false, err
	}
	target, exists, err := readMigrationOpaqueSymlink(root, path)
	if err != nil || !exists {
		return false, err
	}
	want, err := recoveryRetiredLinkTarget(identity)
	if err != nil || target != want {
		return false, fmt.Errorf("workspace migration: preparation retirement marker is invalid")
	}
	if err := validateRecoveryPayloadStore(identity, false); err != nil {
		return false, err
	}
	return true, nil
}

func ensureRecoveryPayloadStore(identity recoveryIdentity) error {
	if err := identity.validate(); err != nil {
		return err
	}
	stored, err := loadRecoveryIdentity(identity.Workflow, identity.RunID)
	if err != nil || stored != identity {
		return fmt.Errorf("workspace migration: durable recovery identity is missing or changed before payload preparation")
	}
	for _, path := range []func(string, string) (string, error){recoveryStorePath, recoveryBlobDirectory, recoveryDescriptorDirectory} {
		dir, err := path(identity.Workflow, identity.RunID)
		if err != nil {
			return err
		}
		if err := ensureMigrationDirectory(identity.Workflow, dir, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func validRecoveryPayloadPurpose(purpose string) bool {
	switch purpose {
	case journalKindRecoveryStatus, journalKindRecoveryIntent, journalKindRecoveryBundle, journalKindRecoveryJournal, journalKindRecoveryCompletion:
		return true
	default:
		return false
	}
}

func recoveryDescriptorName(purpose string) (string, error) {
	switch purpose {
	case journalKindRecoveryStatus:
		return "status", nil
	case journalKindRecoveryIntent:
		return "intent", nil
	case journalKindRecoveryBundle:
		return "bundle", nil
	case journalKindRecoveryJournal:
		return "journal", nil
	case journalKindRecoveryCompletion:
		return "completion", nil
	default:
		return "", fmt.Errorf("workspace migration: unknown private recovery payload purpose")
	}
}

func recoveryPayloadDescriptorPath(identity recoveryIdentity, purpose string) (string, error) {
	name, err := recoveryDescriptorName(purpose)
	if err != nil {
		return "", err
	}
	dir, err := recoveryDescriptorDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func recoveryPayloadBlobPath(identity recoveryIdentity, digest string) (string, error) {
	if !validDigest(digest) {
		return "", fmt.Errorf("workspace migration: invalid private recovery payload digest")
	}
	dir, err := recoveryBlobDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, digest), nil
}

func expectedPrivateRecoveryTarget(identity recoveryIdentity, purpose string) (string, error) {
	switch purpose {
	case journalKindRecoveryStatus, journalKindRecoveryJournal:
		return journalPath(identity.Workflow, identity.RunID)
	case journalKindRecoveryIntent:
		return preparationIntentPath(identity.Workflow, identity.RunID)
	case journalKindRecoveryBundle:
		return recordsGitBackupPath(identity.Workflow, identity.RunID)
	case journalKindRecoveryCompletion:
		return migrationrecord.CompletionWitnessPath(identity.Workflow, identity.RunID)
	default:
		return "", fmt.Errorf("workspace migration: unknown private recovery payload purpose")
	}
}

func newRecoveryPayloadDescriptor(identity recoveryIdentity, purpose, target string, data []byte, mode os.FileMode) (recoveryPayloadDescriptor, error) {
	if err := identity.validate(); err != nil {
		return recoveryPayloadDescriptor{}, err
	}
	expectedTarget, err := expectedPrivateRecoveryTarget(identity, purpose)
	if err != nil || target != expectedTarget || mode.Perm() != 0o600 {
		return recoveryPayloadDescriptor{}, fmt.Errorf("workspace migration: private recovery payload target is outside reviewed authority")
	}
	identityDigest, err := recoveryIdentityDigest(identity)
	if err != nil {
		return recoveryPayloadDescriptor{}, err
	}
	descriptor := recoveryPayloadDescriptor{
		Version:        recoveryPayloadDescriptorVersion,
		RunID:          identity.RunID,
		Owner:          identity.Owner,
		IdentitySHA256: identityDigest,
		Purpose:        purpose,
		Target:         target,
		Mode:           uint32(mode.Perm()),
		Size:           int64(len(data)),
		SHA256:         migrationDigest(data),
	}
	if purpose == journalKindRecoveryJournal {
		previous, progress, err := journalRecoveryDescriptorProgress(identity, target, data)
		if err != nil {
			return recoveryPayloadDescriptor{}, err
		}
		descriptor.JournalPreviousSHA256 = previous
		descriptor.JournalProgress = progress
	}
	if err := descriptor.validate(identity, purpose, target); err != nil {
		return recoveryPayloadDescriptor{}, err
	}
	if _, err := recoveryPayloadDescriptorLinkTarget(descriptor); err != nil {
		return recoveryPayloadDescriptor{}, err
	}
	return descriptor, nil
}

func (descriptor recoveryPayloadDescriptor) validate(identity recoveryIdentity, purpose, target string) error {
	identityDigest, err := recoveryIdentityDigest(identity)
	if err != nil {
		return err
	}
	if descriptor.Version != recoveryPayloadDescriptorVersion || descriptor.RunID != identity.RunID || descriptor.Owner != identity.Owner || descriptor.IdentitySHA256 != identityDigest || descriptor.Purpose != purpose || descriptor.Target != target || descriptor.Mode != 0o600 || descriptor.Size < 0 || !validDigest(descriptor.SHA256) {
		return fmt.Errorf("workspace migration: private recovery payload descriptor is invalid")
	}
	expectedTarget, err := expectedPrivateRecoveryTarget(identity, purpose)
	if err != nil || target != expectedTarget || !pathWithin(identity.Workflow, target) {
		return fmt.Errorf("workspace migration: private recovery payload descriptor target is invalid")
	}
	if purpose != journalKindRecoveryJournal {
		if descriptor.JournalPreviousSHA256 != "" || descriptor.JournalProgress != nil {
			return fmt.Errorf("workspace migration: private recovery payload descriptor progress is invalid")
		}
		return nil
	}
	if (descriptor.JournalPreviousSHA256 == "") != (descriptor.JournalProgress == nil) {
		return fmt.Errorf("workspace migration: private recovery journal descriptor progress is invalid")
	}
	if descriptor.JournalProgress != nil && (!validDigest(descriptor.JournalPreviousSHA256) || descriptor.JournalPreviousSHA256 == descriptor.SHA256 || descriptor.JournalProgress.validate() != nil) {
		return fmt.Errorf("workspace migration: private recovery journal descriptor progress is invalid")
	}
	return nil
}

func recoveryPayloadDescriptorData(descriptor recoveryPayloadDescriptor) ([]byte, error) {
	data, err := json.Marshal(recoveryPayloadDescriptorWire{
		Version:               descriptor.Version,
		RunID:                 descriptor.RunID,
		Owner:                 descriptor.Owner,
		IdentitySHA256:        descriptor.IdentitySHA256,
		Purpose:               descriptor.Purpose,
		Target:                descriptor.Target,
		Mode:                  descriptor.Mode,
		Size:                  descriptor.Size,
		SHA256:                descriptor.SHA256,
		JournalPreviousSHA256: descriptor.JournalPreviousSHA256,
		JournalProgress:       descriptor.JournalProgress,
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

func recoveryPayloadDescriptorLinkTarget(descriptor recoveryPayloadDescriptor) (string, error) {
	data, err := recoveryPayloadDescriptorData(descriptor)
	if err != nil {
		return "", err
	}
	return recoveryOpaqueLinkTarget(recoveryDescriptorLinkPrefix, data)
}

func parseRecoveryPayloadDescriptorLinkTarget(target string) (recoveryPayloadDescriptor, error) {
	data, err := parseRecoveryOpaqueLinkTarget(recoveryDescriptorLinkPrefix, target)
	if err != nil {
		return recoveryPayloadDescriptor{}, err
	}
	var wire recoveryPayloadDescriptorWire
	if err := decodePrivateJSON(data, &wire); err != nil {
		return recoveryPayloadDescriptor{}, err
	}
	descriptor := recoveryPayloadDescriptor{
		Version:               wire.Version,
		RunID:                 wire.RunID,
		Owner:                 wire.Owner,
		IdentitySHA256:        wire.IdentitySHA256,
		Purpose:               wire.Purpose,
		Target:                wire.Target,
		Mode:                  wire.Mode,
		Size:                  wire.Size,
		SHA256:                wire.SHA256,
		JournalPreviousSHA256: wire.JournalPreviousSHA256,
		JournalProgress:       wire.JournalProgress,
	}
	canonical, err := recoveryPayloadDescriptorLinkTarget(descriptor)
	if err != nil || canonical != target {
		return recoveryPayloadDescriptor{}, fmt.Errorf("workspace migration: private recovery payload descriptor is not canonical")
	}
	return descriptor, nil
}

func recoveryPayloadDescriptorID(descriptor recoveryPayloadDescriptor) (string, error) {
	target, err := recoveryPayloadDescriptorLinkTarget(descriptor)
	if err != nil {
		return "", err
	}
	return migrationDigest([]byte(target)), nil
}

func recoveryDescriptorStageName(descriptor recoveryPayloadDescriptor) (string, error) {
	id, err := recoveryPayloadDescriptorID(descriptor)
	if err != nil {
		return "", err
	}
	return recoveryDescriptorStagePrefix + id, nil
}

func recoveryDescriptorStagePath(identity recoveryIdentity, descriptor recoveryPayloadDescriptor) (string, error) {
	dir, err := recoveryDescriptorDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return "", err
	}
	name, err := recoveryDescriptorStageName(descriptor)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

func recoveryDescriptorHistoryName(descriptor recoveryPayloadDescriptor) (string, error) {
	id, err := recoveryPayloadDescriptorID(descriptor)
	if err != nil {
		return "", err
	}
	return recoveryDescriptorHistoryPrefix + id, nil
}

func loadRecoveryPayloadDescriptor(identity recoveryIdentity, purpose, target string) (recoveryPayloadDescriptor, bool, error) {
	path, err := recoveryPayloadDescriptorPath(identity, purpose)
	if err != nil {
		return recoveryPayloadDescriptor{}, false, err
	}
	link, exists, err := readMigrationOpaqueSymlink(identity.Workflow, path)
	if err != nil || !exists {
		return recoveryPayloadDescriptor{}, exists, err
	}
	descriptor, err := parseRecoveryPayloadDescriptorLinkTarget(link)
	if err != nil {
		return recoveryPayloadDescriptor{}, false, err
	}
	if err := descriptor.validate(identity, purpose, target); err != nil {
		return recoveryPayloadDescriptor{}, false, err
	}
	return descriptor, true, nil
}

func stageRecoveryPayloadDescriptor(identity recoveryIdentity, descriptor recoveryPayloadDescriptor) error {
	if err := descriptor.validate(identity, descriptor.Purpose, descriptor.Target); err != nil {
		return err
	}
	path, err := recoveryDescriptorStagePath(identity, descriptor)
	if err != nil {
		return err
	}
	link, err := recoveryPayloadDescriptorLinkTarget(descriptor)
	if err != nil {
		return err
	}
	return publishMigrationOpaqueSymlink(identity.Workflow, path, link, "recovery-descriptor")
}

func promoteRecoveryPayloadDescriptor(identity recoveryIdentity, descriptor recoveryPayloadDescriptor) error {
	if err := descriptor.validate(identity, descriptor.Purpose, descriptor.Target); err != nil {
		return err
	}
	stagePath, err := recoveryDescriptorStagePath(identity, descriptor)
	if err != nil {
		return err
	}
	path, err := recoveryPayloadDescriptorPath(identity, descriptor.Purpose)
	if err != nil {
		return err
	}
	stageTarget, exists, err := readMigrationOpaqueSymlink(identity.Workflow, stagePath)
	if err != nil || !exists {
		if err != nil {
			return err
		}
		return fmt.Errorf("workspace migration: durable recovery descriptor staging link is missing")
	}
	wantTarget, err := recoveryPayloadDescriptorLinkTarget(descriptor)
	if err != nil || stageTarget != wantTarget {
		return fmt.Errorf("workspace migration: durable recovery descriptor staging link is invalid")
	}
	parent, stageName, parentPath, err := openPinnedMigrationParent(identity.Workflow, stagePath)
	if err != nil {
		return err
	}
	defer parent.Close()
	if filepath.Dir(path) != parentPath {
		return fmt.Errorf("workspace migration: durable recovery descriptor parent differs")
	}
	activeName := filepath.Base(path)
	activeInfo, activeErr := parent.Lstat(activeName)
	if activeErr != nil && !errors.Is(activeErr, os.ErrNotExist) {
		return activeErr
	}
	if activeErr == nil {
		if activeInfo.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("workspace migration: opaque recovery symlink path is occupied")
		}
		activeTarget, err := parent.Readlink(activeName)
		if err != nil || !strings.HasPrefix(activeTarget, recoveryDescriptorLinkPrefix) {
			return fmt.Errorf("workspace migration: durable recovery descriptor is invalid")
		}
		if activeTarget == wantTarget {
			if err := migrationFS.remove(parent, stageName); err != nil {
				return err
			}
			return syncPinnedMigrationDirectoryKind(parent, parentPath, "recovery-descriptor")
		}
		current, err := parseRecoveryPayloadDescriptorLinkTarget(activeTarget)
		if err != nil || current.validate(identity, descriptor.Purpose, descriptor.Target) != nil {
			return fmt.Errorf("workspace migration: durable recovery descriptor is invalid")
		}
		currentData, err := loadRecoveryPayloadBlob(identity, current)
		if err != nil {
			return err
		}
		temporary, err := recoveryPayloadTemporaryPath(identity, current, currentData)
		if err != nil {
			return err
		}
		if _, _, present, err := readMigrationOptionalRegular(identity.Workflow, temporary); err != nil || present {
			if err != nil {
				return err
			}
			return fmt.Errorf("workspace migration: private recovery descriptor has an unresolved temporary")
		}
		historyName, err := recoveryDescriptorHistoryName(current)
		if err != nil {
			return err
		}
		historyInfo, historyErr := parent.Lstat(historyName)
		if historyErr != nil && !errors.Is(historyErr, os.ErrNotExist) {
			return historyErr
		}
		if historyErr == nil {
			if historyInfo.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
			}
			historyTarget, err := parent.Readlink(historyName)
			if err != nil || historyTarget != activeTarget {
				return fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
			}
		} else {
			if err := migrationPinnedParentStillCurrent(identity.Workflow, parentPath, parent); err != nil {
				return err
			}
			if err := migrationFS.rename(parent, activeName, historyName); err != nil {
				return err
			}
			if err := syncPinnedMigrationDirectoryKind(parent, parentPath, "recovery-descriptor"); err != nil {
				return err
			}
		}
	}
	if err := migrationPinnedParentStillCurrent(identity.Workflow, parentPath, parent); err != nil {
		return err
	}
	if err := migrationFS.rename(parent, stageName, activeName); err != nil {
		return err
	}
	return syncPinnedMigrationDirectoryKind(parent, parentPath, "recovery-descriptor")
}

type pendingRecoveryDescriptorStage struct {
	descriptor recoveryPayloadDescriptor
	expected   []byte
	promote    bool
	repair     bool
}

// recoverPendingRecoveryDescriptorStages first authenticates every durable
// staged payload before it promotes a descriptor or repairs a blob. A later
// journal can supply a compact predecessor-bound replay delta; initial payload
// stages remain for their ordinary resume/restore paths after their prefix has
// been verified.
func recoverPendingRecoveryDescriptorStages(identity recoveryIdentity, replay func(recoveryPayloadDescriptor) ([]byte, error)) error {
	dir, err := recoveryDescriptorDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	entries, err := recoveryDirectoryEntries(identity.Workflow, dir, 0o700)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	stages := make([]pendingRecoveryDescriptorStage, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), recoveryDescriptorStagePrefix) {
			continue
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is unsafe")
		}
		path := filepath.Join(dir, entry.Name())
		link, exists, err := readMigrationOpaqueSymlink(identity.Workflow, path)
		if err != nil || !exists {
			return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is invalid")
		}
		descriptor, err := parseRecoveryPayloadDescriptorLinkTarget(link)
		if err != nil || descriptor.validate(identity, descriptor.Purpose, descriptor.Target) != nil {
			return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is invalid")
		}
		name, err := recoveryDescriptorStageName(descriptor)
		if err != nil || name != entry.Name() {
			return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is invalid")
		}
		stage := pendingRecoveryDescriptorStage{descriptor: descriptor}
		if _, err := loadRecoveryPayloadBlob(identity, descriptor); err != nil {
			blobPath, pathErr := recoveryPayloadBlobPath(identity, descriptor.SHA256)
			if pathErr != nil {
				return pathErr
			}
			_, _, blobExists, readErr := readMigrationOptionalRegular(identity.Workflow, blobPath)
			if readErr != nil || blobExists {
				return err
			}
			if replay == nil {
				return fmt.Errorf("workspace migration: durable recovery blob temporary cannot be authenticated")
			}
			expected, replayErr := replay(descriptor)
			if replayErr != nil || int64(len(expected)) != descriptor.Size || migrationDigest(expected) != descriptor.SHA256 {
				return fmt.Errorf("workspace migration: durable recovery blob temporary is not an exact immutable payload")
			}
			authority, authorityErr := recoveryPayloadBlobTempAuthority(identity, descriptor, expected)
			if authorityErr != nil {
				return authorityErr
			}
			temporaryName, nameErr := authority.name()
			if nameErr != nil {
				return nameErr
			}
			temporaryPath := filepath.Join(filepath.Dir(blobPath), temporaryName)
			temporary, mode, temporaryExists, readErr := readMigrationOptionalRegular(identity.Workflow, temporaryPath)
			if readErr != nil {
				return readErr
			}
			if temporaryExists && !authority.matchesPartial(temporary, mode) {
				return fmt.Errorf("workspace migration: durable recovery blob temporary is not an exact immutable payload")
			}
			stage.expected = expected
			stage.repair = descriptor.Purpose == journalKindRecoveryJournal && descriptor.JournalProgress != nil
		} else {
			stage.promote = true
		}
		stages = append(stages, stage)
	}
	for _, stage := range stages {
		if stage.repair {
			if err := ensureRecoveryPayloadBlob(identity, stage.descriptor, stage.expected); err != nil {
				return err
			}
			stage.promote = true
		}
		if stage.promote {
			if err := promoteRecoveryPayloadDescriptor(identity, stage.descriptor); err != nil {
				return err
			}
		}
	}
	return nil
}

// discardPendingRecoveryDescriptorStages removes only the exact staging links
// and blob temporaries that a preparation restore can prove belong to this run.
// A partial blob requires exact payload replay from the revalidated plan;
// altered or unreconstructible bytes remain a recovery conflict.
func discardPendingRecoveryDescriptorStages(identity recoveryIdentity, replay func(recoveryPayloadDescriptor) ([]byte, error)) error {
	dir, err := recoveryDescriptorDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	entries, err := recoveryDirectoryEntries(identity.Workflow, dir, 0o700)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), recoveryDescriptorStagePrefix) {
			continue
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is unsafe")
		}
		path := filepath.Join(dir, entry.Name())
		link, exists, err := readMigrationOpaqueSymlink(identity.Workflow, path)
		if err != nil || !exists {
			return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is invalid")
		}
		descriptor, err := parseRecoveryPayloadDescriptorLinkTarget(link)
		if err != nil || descriptor.validate(identity, descriptor.Purpose, descriptor.Target) != nil {
			return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is invalid")
		}
		name, err := recoveryDescriptorStageName(descriptor)
		if err != nil || name != entry.Name() {
			return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is invalid")
		}
		if _, err := loadRecoveryPayloadBlob(identity, descriptor); err == nil {
			if err := promoteRecoveryPayloadDescriptor(identity, descriptor); err != nil {
				return err
			}
			continue
		}
		blobPath, err := recoveryPayloadBlobPath(identity, descriptor.SHA256)
		if err != nil {
			return err
		}
		_, _, blobExists, readErr := readMigrationOptionalRegular(identity.Workflow, blobPath)
		if readErr != nil || blobExists {
			if readErr != nil {
				return readErr
			}
			return fmt.Errorf("workspace migration: immutable private recovery payload blob differs from its descriptor")
		}
		authority, err := recoveryPayloadBlobTemporaryAuthority(identity, descriptor)
		if err != nil {
			return err
		}
		temporaryName, err := authority.name()
		if err != nil {
			return err
		}
		temporaryPath := filepath.Join(filepath.Dir(blobPath), temporaryName)
		temporary, mode, temporaryExists, err := readMigrationOptionalRegular(identity.Workflow, temporaryPath)
		if err != nil {
			return err
		}
		if temporaryExists {
			exact := mode.Perm() == recoveryBlobMode && int64(len(temporary)) == descriptor.Size && migrationDigest(temporary) == descriptor.SHA256
			if !exact {
				if replay == nil {
					return fmt.Errorf("workspace migration: durable recovery blob temporary is not an exact immutable payload")
				}
				expected, err := replay(descriptor)
				if err != nil || int64(len(expected)) != descriptor.Size || migrationDigest(expected) != descriptor.SHA256 {
					return fmt.Errorf("workspace migration: durable recovery blob temporary is not an exact immutable payload")
				}
				authority, err := recoveryPayloadBlobTempAuthority(identity, descriptor, expected)
				if err != nil || !authority.matchesPartial(temporary, mode) {
					return fmt.Errorf("workspace migration: durable recovery blob temporary is not an exact immutable payload")
				}
			}
			if err := removeMigrationOptionalRegularExpected(identity.Workflow, temporaryPath, &migrationRegularExpectation{exists: true, data: temporary, mode: mode}); err != nil {
				return err
			}
		}
		if err := removeRecoveryPayloadDescriptorStage(identity, descriptor); err != nil {
			return err
		}
	}
	return nil
}

func removeRecoveryPayloadDescriptorStage(identity recoveryIdentity, descriptor recoveryPayloadDescriptor) error {
	path, err := recoveryDescriptorStagePath(identity, descriptor)
	if err != nil {
		return err
	}
	wantTarget, err := recoveryPayloadDescriptorLinkTarget(descriptor)
	if err != nil {
		return err
	}
	parent, name, parentPath, err := openPinnedMigrationParent(identity.Workflow, path)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Lstat(name)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is invalid")
	}
	target, err := parent.Readlink(name)
	if err != nil || target != wantTarget {
		return fmt.Errorf("workspace migration: durable recovery descriptor staging entry is invalid")
	}
	if err := migrationPinnedParentStillCurrent(identity.Workflow, parentPath, parent); err != nil {
		return err
	}
	if err := migrationFS.remove(parent, name); err != nil {
		return err
	}
	return syncPinnedMigrationDirectoryKind(parent, parentPath, "recovery-descriptor")
}

func loadRecoveryPayloadBlob(identity recoveryIdentity, descriptor recoveryPayloadDescriptor) ([]byte, error) {
	path, err := recoveryPayloadBlobPath(identity, descriptor.SHA256)
	if err != nil {
		return nil, err
	}
	data, mode, exists, err := readMigrationOptionalRegular(identity.Workflow, path)
	if err != nil || !exists || mode.Perm() != recoveryBlobMode || int64(len(data)) != descriptor.Size || migrationDigest(data) != descriptor.SHA256 {
		return nil, fmt.Errorf("workspace migration: immutable private recovery payload blob differs from its descriptor")
	}
	return data, nil
}

func ensureRecoveryPayloadBlob(identity recoveryIdentity, descriptor recoveryPayloadDescriptor, data []byte) error {
	if int64(len(data)) != descriptor.Size || migrationDigest(data) != descriptor.SHA256 {
		return fmt.Errorf("workspace migration: private recovery payload does not match its descriptor")
	}
	path, err := recoveryPayloadBlobPath(identity, descriptor.SHA256)
	if err != nil {
		return err
	}
	existing, mode, exists, err := readMigrationOptionalRegular(identity.Workflow, path)
	if err != nil {
		return err
	}
	if exists {
		if mode.Perm() != recoveryBlobMode || !bytes.Equal(existing, data) {
			return fmt.Errorf("workspace migration: immutable private recovery payload blob conflicts with existing contents")
		}
		return nil
	}
	authority, err := recoveryPayloadBlobTempAuthority(identity, descriptor, data)
	if err != nil {
		return err
	}
	if err := writeMigrationRegularAuthorized(identity.Workflow, path, data, recoveryBlobMode, &migrationRegularExpectation{}, authority); err != nil {
		return fmt.Errorf("workspace migration: persist immutable private recovery payload: %w", err)
	}
	_, err = loadRecoveryPayloadBlob(identity, descriptor)
	return err
}

func recoveryPayloadBlobTempAuthority(identity recoveryIdentity, descriptor recoveryPayloadDescriptor, data []byte) (migrationTempAuthority, error) {
	if err := descriptor.validate(identity, descriptor.Purpose, descriptor.Target); err != nil {
		return migrationTempAuthority{}, err
	}
	if int64(len(data)) != descriptor.Size || migrationDigest(data) != descriptor.SHA256 {
		return migrationTempAuthority{}, fmt.Errorf("workspace migration: immutable private recovery payload differs from its descriptor")
	}
	authority, err := recoveryPayloadBlobTemporaryAuthority(identity, descriptor)
	if err != nil {
		return migrationTempAuthority{}, err
	}
	authority.data = append([]byte(nil), data...)
	return authority, nil
}

func recoveryPayloadBlobTemporaryAuthority(identity recoveryIdentity, descriptor recoveryPayloadDescriptor) (migrationTempAuthority, error) {
	if err := descriptor.validate(identity, descriptor.Purpose, descriptor.Target); err != nil {
		return migrationTempAuthority{}, err
	}
	target, err := recoveryPayloadBlobPath(identity, descriptor.SHA256)
	if err != nil {
		return migrationTempAuthority{}, err
	}
	descriptorID, err := recoveryPayloadDescriptorID(descriptor)
	if err != nil {
		return migrationTempAuthority{}, err
	}
	return migrationTempAuthority{
		root:               identity.Workflow,
		runID:              identity.RunID,
		owner:              identity.Owner,
		scope:              journalScopeRecovery,
		kind:               "recovery_blob",
		target:             target,
		direction:          "store",
		mode:               recoveryBlobMode,
		privateSlot:        true,
		recoveryDescriptor: descriptorID,
		syncKind:           "recovery-blob",
	}, nil
}

func privateRecoveryPayloadTempAuthority(identity recoveryIdentity, descriptor recoveryPayloadDescriptor, data []byte) (migrationTempAuthority, error) {
	if err := descriptor.validate(identity, descriptor.Purpose, descriptor.Target); err != nil {
		return migrationTempAuthority{}, err
	}
	if int64(len(data)) != descriptor.Size || migrationDigest(data) != descriptor.SHA256 {
		return migrationTempAuthority{}, fmt.Errorf("workspace migration: private recovery payload differs from its descriptor")
	}
	descriptorID, err := recoveryPayloadDescriptorID(descriptor)
	if err != nil {
		return migrationTempAuthority{}, err
	}
	return migrationTempAuthority{
		root:               identity.Workflow,
		runID:              identity.RunID,
		owner:              identity.Owner,
		scope:              journalScopeRecovery,
		kind:               descriptor.Purpose,
		target:             descriptor.Target,
		direction:          "publish",
		data:               append([]byte(nil), data...),
		mode:               os.FileMode(descriptor.Mode),
		privateSlot:        true,
		recoveryDescriptor: descriptorID,
	}, nil
}

func recoveryPayloadTemporaryPath(identity recoveryIdentity, descriptor recoveryPayloadDescriptor, data []byte) (string, error) {
	authority, err := privateRecoveryPayloadTempAuthority(identity, descriptor, data)
	if err != nil {
		return "", err
	}
	name, err := authority.name()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(descriptor.Target), name), nil
}

func publishRecoveryPayloadDescriptor(identity recoveryIdentity, descriptor recoveryPayloadDescriptor, data []byte) error {
	if err := descriptor.validate(identity, descriptor.Purpose, descriptor.Target); err != nil {
		return err
	}
	if _, err := loadRecoveryPayloadBlob(identity, descriptor); err != nil {
		return err
	}
	if err := stageRecoveryPayloadDescriptor(identity, descriptor); err != nil {
		return err
	}
	return promoteRecoveryPayloadDescriptor(identity, descriptor)
}

func writePrivateRecoveryPayload(identity recoveryIdentity, purpose, target string, data []byte) error {
	return writePrivateRecoveryPayloadExpected(identity, purpose, target, data, nil)
}

func writePrivateRecoveryPayloadExpected(identity recoveryIdentity, purpose, target string, data []byte, expected *migrationRegularExpectation) error {
	descriptor, err := newRecoveryPayloadDescriptor(identity, purpose, target, data, 0o600)
	if err != nil {
		return err
	}
	if err := ensureRecoveryPayloadStore(identity); err != nil {
		return err
	}
	if err := stageRecoveryPayloadDescriptor(identity, descriptor); err != nil {
		return err
	}
	if err := ensureRecoveryPayloadBlob(identity, descriptor, data); err != nil {
		return err
	}
	if err := promoteRecoveryPayloadDescriptor(identity, descriptor); err != nil {
		return err
	}
	authority, err := privateRecoveryPayloadTempAuthority(identity, descriptor, data)
	if err != nil {
		return err
	}
	return writeMigrationRegularAuthorized(identity.Workflow, target, data, 0o600, expected, authority)
}

func loadPrivateRecoveryPayload(identity recoveryIdentity, purpose, target string) (recoveryPayloadDescriptor, []byte, bool, error) {
	descriptor, exists, err := loadRecoveryPayloadDescriptor(identity, purpose, target)
	if err != nil || !exists {
		return recoveryPayloadDescriptor{}, nil, exists, err
	}
	data, err := loadRecoveryPayloadBlob(identity, descriptor)
	if err != nil {
		return recoveryPayloadDescriptor{}, nil, false, err
	}
	return descriptor, data, true, nil
}

// recoveryPayloadAuthorizesData accepts a target image only when an active or
// retained descriptor in this run's private store names those exact bytes. A
// retained descriptor is needed when a newer journal descriptor was promoted
// before its target replacement: the older target remains the only safe
// preimage until recovery promotes the newer payload.
func recoveryPayloadAuthorizesData(identity recoveryIdentity, purpose, target string, data []byte, mode os.FileMode) (bool, error) {
	descriptor, expected, exists, err := loadPrivateRecoveryPayload(identity, purpose, target)
	if err != nil {
		return false, err
	}
	if exists && mode.Perm() == os.FileMode(descriptor.Mode).Perm() && bytes.Equal(data, expected) {
		return true, nil
	}
	dir, err := recoveryDescriptorDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return false, err
	}
	entries, err := recoveryDirectoryEntries(identity.Workflow, dir, 0o700)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), recoveryDescriptorHistoryPrefix) {
			continue
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return false, fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
		}
		link, exists, err := readMigrationOpaqueSymlink(identity.Workflow, filepath.Join(dir, entry.Name()))
		if err != nil || !exists {
			return false, fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
		}
		historical, err := parseRecoveryPayloadDescriptorLinkTarget(link)
		if err != nil || historical.validate(identity, purpose, target) != nil {
			return false, fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
		}
		name, err := recoveryDescriptorHistoryName(historical)
		if err != nil || name != entry.Name() {
			return false, fmt.Errorf("workspace migration: durable recovery descriptor history is invalid")
		}
		historicalData, err := loadRecoveryPayloadBlob(identity, historical)
		if err != nil {
			return false, err
		}
		if mode.Perm() == os.FileMode(historical.Mode).Perm() && bytes.Equal(data, historicalData) {
			return true, nil
		}
	}
	return false, nil
}

func readMigrationOpaqueSymlink(root, path string) (string, bool, error) {
	parent, name, parentPath, err := openPinnedMigrationParent(root, path)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	defer parent.Close()
	before, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil || before.Mode()&os.ModeSymlink == 0 {
		return "", false, fmt.Errorf("workspace migration: expected an opaque recovery symlink")
	}
	target, err := parent.Readlink(name)
	if err != nil {
		return "", false, err
	}
	after, err := parent.Lstat(name)
	if err != nil || after.Mode()&os.ModeSymlink == 0 || !os.SameFile(before, after) {
		return "", false, fmt.Errorf("workspace migration: opaque recovery symlink changed while being read")
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return "", false, err
	}
	return target, true, nil
}

func publishMigrationOpaqueSymlink(root, path, target, syncKind string) error {
	if len(target) == 0 || len(target) > recoveryLinkTargetLimit {
		return fmt.Errorf("workspace migration: opaque recovery symlink target is invalid")
	}
	parent, name, parentPath, err := openPinnedMigrationParent(root, path)
	if err != nil {
		return err
	}
	defer parent.Close()
	info, err := parent.Lstat(name)
	if err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("workspace migration: opaque recovery symlink path is occupied")
		}
		actual, err := parent.Readlink(name)
		if err != nil || actual != target {
			return fmt.Errorf("workspace migration: opaque recovery symlink differs from durable identity")
		}
		return migrationPinnedParentStillCurrent(root, parentPath, parent)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return err
	}
	if err := migrationFS.symlink(parent, target, name); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		return publishMigrationOpaqueSymlink(root, path, target, syncKind)
	}
	return syncPinnedMigrationDirectoryKind(parent, parentPath, syncKind)
}

func replaceMigrationOpaqueSymlink(root, path, target, syncKind string) error {
	if len(target) == 0 || len(target) > recoveryLinkTargetLimit {
		return fmt.Errorf("workspace migration: opaque recovery symlink target is invalid")
	}
	parent, name, parentPath, err := openPinnedMigrationParent(root, path)
	if err != nil {
		return err
	}
	defer parent.Close()
	existing, err := parent.Lstat(name)
	exists := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if exists && existing.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("workspace migration: opaque recovery symlink path is occupied")
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return err
	}
	var temporary string
	for range 32 {
		var token [16]byte
		if _, err := rand.Read(token[:]); err != nil {
			return err
		}
		candidate := migrationTempPrefix + hex.EncodeToString(token[:])
		if err := migrationFS.symlink(parent, target, candidate); errors.Is(err, os.ErrExist) {
			continue
		} else if err != nil {
			return err
		}
		temporary = candidate
		break
	}
	if temporary == "" {
		return fmt.Errorf("workspace migration: cannot allocate an opaque recovery descriptor temporary")
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = migrationFS.remove(parent, temporary)
		}
	}()
	if err := syncPinnedMigrationDirectoryKind(parent, parentPath, syncKind); err != nil {
		return err
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return err
	}
	if exists {
		current, err := parent.Lstat(name)
		if err != nil || current.Mode()&os.ModeSymlink == 0 || !os.SameFile(existing, current) {
			return fmt.Errorf("workspace migration: opaque recovery symlink changed after classification")
		}
	} else if _, err := parent.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		if err != nil {
			return err
		}
		return fmt.Errorf("workspace migration: opaque recovery symlink appeared after classification")
	}
	if err := migrationFS.rename(parent, temporary, name); err != nil {
		return err
	}
	renamed = true
	return syncPinnedMigrationDirectoryKind(parent, parentPath, syncKind)
}

func privateRecoveryTemporaryAuthorities(identity recoveryIdentity) ([]migrationTempAuthority, error) {
	authorities := make([]migrationTempAuthority, 0, 4)
	for _, purpose := range []string{journalKindRecoveryStatus, journalKindRecoveryIntent, journalKindRecoveryBundle, journalKindRecoveryJournal, journalKindRecoveryCompletion} {
		target, err := expectedPrivateRecoveryTarget(identity, purpose)
		if err != nil {
			return nil, err
		}
		descriptor, data, exists, err := loadPrivateRecoveryPayload(identity, purpose, target)
		if err != nil {
			return nil, err
		}
		if !exists {
			continue
		}
		authority, err := privateRecoveryPayloadTempAuthority(identity, descriptor, data)
		if err != nil {
			return nil, err
		}
		authorities = append(authorities, authority)
	}
	return authorities, nil
}

func ensureNoUnexpectedPrivateRecoveryTemporary(identity recoveryIdentity) error {
	authorities, err := privateRecoveryTemporaryAuthorities(identity)
	if err != nil {
		return err
	}
	allowed := make(map[string]bool, len(authorities))
	for _, authority := range authorities {
		name, err := authority.name()
		if err != nil {
			return err
		}
		allowed[name] = true
	}
	private, err := recoveryPrivateDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	parent, _, parentPath, err := openPinnedMigrationParent(identity.Workflow, filepath.Join(private, ".scan"))
	if err != nil {
		return err
	}
	defer parent.Close()
	dir, err := parent.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(-1)
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return err
	}
	if err := migrationPinnedParentStillCurrent(identity.Workflow, parentPath, parent); err != nil {
		return err
	}
	for _, entry := range entries {
		if migrationTemporaryFileName(entry.Name()) && !allowed[entry.Name()] {
			return fmt.Errorf("workspace migration: recovery conflict: unrecognized private temporary %q", entry.Name())
		}
	}
	return nil
}

func recoveryDirectoryEntries(root, path string, mode os.FileMode) ([]os.DirEntry, error) {
	parent, name, parentPath, err := openPinnedMigrationParent(root, path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	info, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err != nil || !migrationRealDirectory(info) || info.Mode().Perm() != mode.Perm() {
		return nil, fmt.Errorf("workspace migration: durable recovery directory is not a real directory with the required mode")
	}
	dir, err := openPinnedMigrationChild(parent, name, info)
	if err != nil {
		return nil, err
	}
	file, err := dir.Open(".")
	if err != nil {
		_ = dir.Close()
		return nil, err
	}
	entries, readErr := file.ReadDir(-1)
	closeFileErr := file.Close()
	closeErr := dir.Close()
	if err := errors.Join(readErr, closeFileErr, closeErr); err != nil {
		return nil, err
	}
	if err := migrationPinnedParentStillCurrent(root, parentPath, parent); err != nil {
		return nil, err
	}
	return entries, nil
}

func validatePrivateRecoveryPayloadTarget(identity recoveryIdentity, descriptor recoveryPayloadDescriptor, data []byte) (bool, error) {
	actual, mode, exists, err := readMigrationOptionalRegular(identity.Workflow, descriptor.Target)
	if err != nil || !exists {
		return exists, err
	}
	if mode.Perm() != os.FileMode(descriptor.Mode).Perm() || !bytes.Equal(actual, data) {
		return true, privateRecoveryPayloadConflict(descriptor.Purpose)
	}
	return true, nil
}

// validateRecoveryPayloadStore makes the identity-owned recovery store an
// allowlist rather than a directory convention. A recovery run never treats an
// extra descriptor or blob as ours merely because it lives below private/.
func validateRecoveryPayloadStore(identity recoveryIdentity, allowAbsent bool) error {
	private, err := recoveryPrivateDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	privateEntries, err := recoveryDirectoryEntries(identity.Workflow, private, 0o700)
	if err != nil {
		return err
	}
	seenPrivate := map[string]os.DirEntry{}
	allowedPrivate := map[string]bool{
		recoveryIdentityName: true,
		recoveryRetiredName:  true,
		recoveryStoreName:    true,
		"journal.json":       true,
		"intent.json":        true,
		"records.git.bundle": true,
		"completion.json":    true,
	}
	for _, entry := range privateEntries {
		if _, duplicate := seenPrivate[entry.Name()]; duplicate {
			return fmt.Errorf("workspace migration: duplicate private recovery entry")
		}
		if !allowedPrivate[entry.Name()] && !migrationTemporaryFileName(entry.Name()) {
			return fmt.Errorf("workspace migration: durable recovery directory has an unexpected entry")
		}
		seenPrivate[entry.Name()] = entry
	}
	identityEntry, ok := seenPrivate[recoveryIdentityName]
	if !ok || identityEntry.Type()&os.ModeSymlink == 0 {
		return fmt.Errorf("workspace migration: durable recovery identity is missing")
	}
	storeEntry, storeExists := seenPrivate[recoveryStoreName]
	if !storeExists {
		if allowAbsent {
			return ensureNoUnexpectedPrivateRecoveryTemporary(identity)
		}
		return fmt.Errorf("workspace migration: durable recovery payload store is missing")
	}
	if !storeEntry.IsDir() || storeEntry.Type()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace migration: durable recovery payload store is unsafe")
	}
	store, err := recoveryStorePath(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	storeEntries, err := recoveryDirectoryEntries(identity.Workflow, store, 0o700)
	if err != nil {
		return err
	}
	storeNames := map[string]os.DirEntry{}
	storeChildren := map[string][]os.DirEntry{}
	for _, entry := range storeEntries {
		if entry.Name() != recoveryBlobDirectoryName && entry.Name() != recoveryDescriptorDirectoryName {
			return fmt.Errorf("workspace migration: durable recovery payload store has an unexpected entry")
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace migration: durable recovery payload store is unsafe")
		}
		children, err := recoveryDirectoryEntries(identity.Workflow, filepath.Join(store, entry.Name()), 0o700)
		if err != nil {
			return err
		}
		storeNames[entry.Name()] = entry
		storeChildren[entry.Name()] = children
	}
	_, blobsPresent := storeNames[recoveryBlobDirectoryName]
	_, descriptorsPresent := storeNames[recoveryDescriptorDirectoryName]
	if descriptorsPresent && !blobsPresent {
		return fmt.Errorf("workspace migration: durable recovery payload store is unsafe")
	}
	if !blobsPresent || !descriptorsPresent {
		if !allowAbsent {
			return fmt.Errorf("workspace migration: durable recovery payload store has an unexpected entry")
		}
		if blobsPresent && len(storeChildren[recoveryBlobDirectoryName]) != 0 {
			return fmt.Errorf("workspace migration: durable recovery payload store has an unexpected entry")
		}
		return ensureNoUnexpectedPrivateRecoveryTemporary(identity)
	}

	descriptorDir, err := recoveryDescriptorDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	descriptorEntries, err := recoveryDirectoryEntries(identity.Workflow, descriptorDir, 0o700)
	if err != nil {
		return err
	}
	activeDescriptors := map[string]recoveryPayloadDescriptor{}
	for _, purpose := range []string{journalKindRecoveryStatus, journalKindRecoveryIntent, journalKindRecoveryBundle, journalKindRecoveryJournal, journalKindRecoveryCompletion} {
		target, err := expectedPrivateRecoveryTarget(identity, purpose)
		if err != nil {
			return err
		}
		descriptor, exists, err := loadRecoveryPayloadDescriptor(identity, purpose, target)
		if err != nil {
			return err
		}
		if exists {
			name, err := recoveryDescriptorName(purpose)
			if err != nil {
				return err
			}
			activeDescriptors[name] = descriptor
		}
	}
	descriptors := map[string]recoveryPayloadDescriptor{}
	pendingBlobTemps := map[string]bool{}
	for _, entry := range descriptorEntries {
		if entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("workspace migration: durable recovery descriptor set has an unexpected entry")
		}
		link, exists, err := readMigrationOpaqueSymlink(identity.Workflow, filepath.Join(descriptorDir, entry.Name()))
		if err != nil || !exists {
			return fmt.Errorf("workspace migration: durable recovery descriptor set has an unexpected entry")
		}
		descriptor, err := parseRecoveryPayloadDescriptorLinkTarget(link)
		if err != nil || descriptor.validate(identity, descriptor.Purpose, descriptor.Target) != nil {
			return fmt.Errorf("workspace migration: durable recovery descriptor set has an unexpected entry")
		}
		if strings.HasPrefix(entry.Name(), recoveryDescriptorStagePrefix) {
			stageName, err := recoveryDescriptorStageName(descriptor)
			if err != nil || stageName != entry.Name() {
				return fmt.Errorf("workspace migration: durable recovery descriptor set has an unexpected entry")
			}
			authority, err := recoveryPayloadBlobTemporaryAuthority(identity, descriptor)
			if err != nil {
				return err
			}
			tempName, err := authority.name()
			if err != nil {
				return err
			}
			pendingBlobTemps[tempName] = true
			continue
		}
		if active, ok := activeDescriptors[entry.Name()]; ok {
			activeLink, err := recoveryPayloadDescriptorLinkTarget(active)
			if err != nil || activeLink != link {
				return fmt.Errorf("workspace migration: durable recovery descriptor set has an unexpected entry")
			}
		} else {
			historyName, err := recoveryDescriptorHistoryName(descriptor)
			if err != nil || historyName != entry.Name() {
				return fmt.Errorf("workspace migration: durable recovery descriptor set has an unexpected entry")
			}
		}
		if _, duplicate := descriptors[entry.Name()]; duplicate {
			return fmt.Errorf("workspace migration: durable recovery descriptor set has an unexpected entry")
		}
		descriptors[entry.Name()] = descriptor
	}

	blobDir, err := recoveryBlobDirectory(identity.Workflow, identity.RunID)
	if err != nil {
		return err
	}
	blobEntries, err := recoveryDirectoryEntries(identity.Workflow, blobDir, 0o700)
	if err != nil {
		return err
	}
	expectedBlobs := map[string]bool{}
	for _, descriptor := range descriptors {
		expectedBlobs[descriptor.SHA256] = true
		if _, err := loadRecoveryPayloadBlob(identity, descriptor); err != nil {
			return err
		}
	}
	for _, entry := range blobEntries {
		if !entry.Type().IsRegular() || entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace migration: durable recovery blob set has an unexpected entry")
		}
		if expectedBlobs[entry.Name()] {
			continue
		}
		if !pendingBlobTemps[entry.Name()] {
			return fmt.Errorf("workspace migration: durable recovery blob set has an unexpected entry")
		}
		info, err := os.Lstat(filepath.Join(blobDir, entry.Name()))
		if err != nil || (info.Mode().Perm() != 0o600 && info.Mode().Perm() != recoveryBlobMode) {
			return fmt.Errorf("workspace migration: durable recovery blob set has an unexpected entry")
		}
	}
	return ensureNoUnexpectedPrivateRecoveryTemporary(identity)
}

func preparationRecoveryStoreReady(root, runID string) bool {
	intent, err := preparationIntentFromStatus(root, runID)
	if err != nil {
		return false
	}
	identity, err := loadRecoveryIdentity(root, runID)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) || validateRecoveryPayloadStore(identity, false) != nil {
		return false
	}
	for _, purpose := range []string{journalKindRecoveryStatus, journalKindRecoveryIntent} {
		target, err := expectedPrivateRecoveryTarget(identity, purpose)
		if err != nil {
			return false
		}
		descriptor, data, exists, err := loadPrivateRecoveryPayload(identity, purpose, target)
		if err != nil || !exists {
			return false
		}
		if _, err := validatePrivateRecoveryPayloadTarget(identity, descriptor, data); err != nil {
			return false
		}
	}
	journalPath, err := journalPath(root, runID)
	if err != nil {
		return false
	}
	if _, exists, err := loadRecoveryPayloadDescriptor(identity, journalKindRecoveryJournal, journalPath); err != nil || exists {
		return false
	}
	return true
}
