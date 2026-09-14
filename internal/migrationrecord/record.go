// Package migrationrecord defines the public, content-free records emitted by
// the schema-2 workspace migration. It deliberately does not depend on the
// migration executor so normal workspace loading can validate adopted bindings
// without creating an import cycle.
package migrationrecord

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
)

const (
	// ReceiptVersion is the only public schema written by the migration.
	ReceiptVersion = 1
	// JournalVersion is the version of the small status envelope at the start
	// of the private migration journal. The executor owns the remaining fields.
	JournalVersion = 2
	// CompletionWitnessVersion is the independent authorization record normal
	// loaders require before a completed migration can unblock the workspace.
	CompletionWitnessVersion = 1
	// RecoveryRetiredLinkPrefix marks a preparation-only recovery directory
	// whose private evidence was deliberately retained after restore. The link
	// target is the digest of the sibling immutable identity link target; it
	// contains no payload bytes and can be checked without following either link.
	RecoveryRetiredLinkPrefix = "homonto-recovery-retired-v1:"
)

var runIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{7,127}$`)
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

// FileRef binds a public input or output to its path and digest without
// carrying its potentially sensitive bytes.
type FileRef struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

const LegacyConfigProvenance = "unverified_attested_control_only"

// LegacyConfig preserves the exact removed legacy scalar fields in the public
// receipt without making them selected-source provenance.
type LegacyConfig struct {
	BaseRef    string `json:"base_ref"`
	BaseBranch string `json:"base_branch"`
	Provenance string `json:"provenance"`
}

// RecordWrite describes one transformed or preserved state by digest only.
type RecordWrite struct {
	Path         string        `json:"path"`
	PreSHA256    string        `json:"pre_sha256"`
	PostSHA256   string        `json:"post_sha256"`
	Action       string        `json:"action"`
	Fields       []string      `json:"changed_fields"`
	LegacyConfig *LegacyConfig `json:"legacy_config,omitempty"`
}

// RetiredRecord is an immutable, path-and-ID-addressed abandoned state. Its
// historical change name is intentionally absent: a retired record must never
// be attached to an active record merely because their old names match.
type RetiredRecord struct {
	Path          string `json:"path"`
	ID            string `json:"id"`
	SchemaVersion int    `json:"schema_version"`
	SHA256        string `json:"sha256"`
}

// Binding is the public proof for one adopted legacy execution checkout. The
// ownership token itself stays in a private Git administrative file and private
// journal; only its digest is public.
type Binding struct {
	Workflow      string `json:"workflow"`
	Change        string `json:"change"`
	StateID       string `json:"state_id"`
	Repo          string `json:"repo"`
	Path          string `json:"path"`
	CommonDir     string `json:"common_dir"`
	GitDir        string `json:"git_dir"`
	Branch        string `json:"branch"`
	BaseRef       string `json:"base_ref"`
	BaseTarget    string `json:"base_target"`
	BaseCommit    string `json:"base_commit"`
	OwnershipHash string `json:"ownership_sha256"`
}

// Receipt is committed with the first migration records commit. It contains
// enough public evidence to validate the final layout and retained abandoned
// records, but never config, state, token, or backup bytes.
type Receipt struct {
	Version         int             `json:"version"`
	RunID           string          `json:"run_id"`
	PlanHash        string          `json:"plan_hash"`
	Config          FileRef         `json:"config"`
	Manifest        FileRef         `json:"manifest"`
	LayoutMarker    FileRef         `json:"layout_marker"`
	Registry        FileRef         `json:"registry"`
	RecordWrites    []RecordWrite   `json:"record_writes"`
	Retired         []RetiredRecord `json:"retired"`
	Bindings        []Binding       `json:"bindings"`
	CommitProofPath string          `json:"commit_proof_path"`
}

// CommitProof is committed separately after the receipt. Pointing backward to
// the first commit avoids placing a commit's own hash or tree in that commit's
// tree.
type CommitProof struct {
	Version         int    `json:"version"`
	RunID           string `json:"run_id"`
	MigrationCommit string `json:"migration_commit"`
	Parent          string `json:"parent"`
	Tree            string `json:"tree"`
	MessageSHA256   string `json:"message_sha256"`
}

// CompletionWitness is written only after the executor has validated the final
// migration invariant with the schema-2 marker active. It binds exact receipt
// and proof bytes to the approved plan and transaction without hashing itself
// or future source development.
type CompletionWitness struct {
	Version           int    `json:"version"`
	RunID             string `json:"run_id"`
	PlanHash          string `json:"plan_hash"`
	ReceiptSHA256     string `json:"receipt_sha256"`
	CommitProofSHA256 string `json:"commit_proof_sha256"`
	MigrationCommit   string `json:"migration_commit"`
	MigrationParent   string `json:"migration_parent"`
	MigrationTree     string `json:"migration_tree"`
	TransactionSHA256 string `json:"transaction_sha256"`
}

// JournalStatus is the minimal private-journal envelope normal loaders use to
// keep pending migration operations fail-closed. Its full private payload is
// validated by the executor before any recovery operation.
type JournalStatus struct {
	Version     int                `json:"version"`
	RunID       string             `json:"run_id"`
	Phase       string             `json:"phase"`
	Preparation *PreparationStatus `json:"preparation,omitempty"`
}

// PreparationStatus is the content-free identity that makes the private
// preparation staging slots recoverable before a full journal exists.
type PreparationStatus struct {
	PlanHash     string `json:"plan_hash"`
	ConfigPath   string `json:"config_path"`
	ConfigRoot   string `json:"config_root"`
	WorkflowRoot string `json:"workflow_root"`
	ManifestPath string `json:"manifest_path"`
	Owner        string `json:"owner"`
	Guardian     string `json:"guardian"`
}

func SafeRunID(runID string) bool { return runIDPattern.MatchString(runID) }

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func migrationRoot(root string) string {
	return filepath.Join(root, ".workflow", "migrations")
}

func ReceiptPath(root, runID string) (string, error) {
	if !SafeRunID(runID) {
		return "", fmt.Errorf("migration record: invalid run ID")
	}
	return filepath.Join(migrationRoot(root), runID, "receipt.json"), nil
}

func CommitProofPath(root, runID string) (string, error) {
	if !SafeRunID(runID) {
		return "", fmt.Errorf("migration record: invalid run ID")
	}
	return filepath.Join(migrationRoot(root), runID, "commit-proof.json"), nil
}

func JournalPath(root, runID string) (string, error) {
	if !SafeRunID(runID) {
		return "", fmt.Errorf("migration record: invalid run ID")
	}
	return filepath.Join(migrationRoot(root), runID, "private", "journal.json"), nil
}

// CompletionWitnessPath is separate from the journal status envelope so
// changing only a journal phase can never authorize an incomplete migration.
func CompletionWitnessPath(root, runID string) (string, error) {
	if !SafeRunID(runID) {
		return "", fmt.Errorf("migration record: invalid run ID")
	}
	return filepath.Join(migrationRoot(root), runID, "private", "completion.json"), nil
}

// LoadJournalStatus reads only the common, non-sensitive envelope of a private
// journal. Recovery validates the full journal separately.
func LoadJournalStatus(root, runID string) (JournalStatus, error) {
	path, err := JournalPath(root, runID)
	if err != nil {
		return JournalStatus{}, err
	}
	data, err := readRegularWithin(root, path)
	if err != nil {
		return JournalStatus{}, fmt.Errorf("migration record: reading journal status: %w", err)
	}
	return ParseJournalStatus(data, root, runID)
}

// ParseJournalStatus validates an in-memory private-journal envelope before
// recovery uses a status image that has not yet been published at journal.json.
func ParseJournalStatus(data []byte, root, runID string) (JournalStatus, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return JournalStatus{}, fmt.Errorf("migration record: journal status is malformed or unsupported")
	}
	var status JournalStatus
	if err := json.Unmarshal(data, &status); err != nil || status.Version != JournalVersion || status.RunID != runID || !validJournalPhase(status.Phase) || !validPreparationStatus(root, status) {
		return JournalStatus{}, fmt.Errorf("migration record: journal status is malformed or unsupported")
	}
	return status, nil
}

func validPreparationStatus(root string, status JournalStatus) bool {
	if status.Phase != "preparing" {
		return status.Preparation == nil
	}
	preparation := status.Preparation
	if preparation == nil || !digestPattern.MatchString(preparation.PlanHash) || !filepath.IsAbs(preparation.ConfigPath) || filepath.Clean(preparation.ConfigPath) != preparation.ConfigPath || !filepath.IsAbs(preparation.ConfigRoot) || filepath.Clean(preparation.ConfigRoot) != preparation.ConfigRoot || filepath.Dir(preparation.ConfigPath) != preparation.ConfigRoot || preparation.WorkflowRoot != root || !filepath.IsAbs(preparation.ManifestPath) || filepath.Clean(preparation.ManifestPath) != preparation.ManifestPath || len(preparation.Owner) != 32 || preparation.Owner != strings.ToLower(preparation.Owner) || strings.TrimSpace(preparation.Guardian) == "" {
		return false
	}
	_, err := hex.DecodeString(preparation.Owner)
	return err == nil
}

// IsPreparationOrphan recognizes only empty preparation directory scaffolding.
// Temporary artifacts have an executor-owned, run-bound authority and are
// handled there; a prefix-shaped file is never enough to authorize deletion.
func IsPreparationOrphan(root, runID string) (bool, error) {
	if !SafeRunID(runID) {
		return false, fmt.Errorf("migration record: invalid run ID")
	}
	runPath := filepath.Join(migrationRoot(root), runID)
	if err := fsutil.RequireRealParents(root, filepath.Dir(runPath)); err != nil {
		return false, err
	}
	info, err := os.Lstat(runPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	entries, err := os.ReadDir(runPath)
	if err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return true, nil
	}
	if len(entries) != 1 || entries[0].Name() != "private" || !entries[0].IsDir() {
		return false, nil
	}
	privatePath := filepath.Join(runPath, "private")
	privateInfo, err := os.Lstat(privatePath)
	if err != nil {
		return false, err
	}
	if !privateInfo.IsDir() || privateInfo.Mode()&os.ModeSymlink != 0 {
		return false, nil
	}
	privateEntries, err := os.ReadDir(privatePath)
	if err != nil {
		return false, err
	}
	return len(privateEntries) == 0, nil
}

// IsRetiredPreparation recognizes the narrowly shaped evidence left after a
// preparation-only restore. Unlike an orphan, it is intentionally retained so
// a later operator can inspect durable recovery material without making normal
// workspace loading fail closed forever.
func IsRetiredPreparation(root, runID string) (bool, error) {
	if !SafeRunID(runID) {
		return false, fmt.Errorf("migration record: invalid run ID")
	}
	runPath := filepath.Join(migrationRoot(root), runID)
	privatePath := filepath.Join(runPath, "private")
	if err := fsutil.RequireRealParents(root, filepath.Dir(runPath)); err != nil {
		return false, err
	}
	for _, path := range []string{runPath, privatePath} {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
			return false, nil
		}
	}
	entries, err := os.ReadDir(privatePath)
	if err != nil {
		return false, err
	}
	allowed := map[string]bool{"recovery-identity": true, "recovery-retired": true, "recovery-store": true}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !allowed[entry.Name()] || seen[entry.Name()] {
			return false, nil
		}
		seen[entry.Name()] = true
	}
	if !seen["recovery-identity"] || !seen["recovery-retired"] || !seen["recovery-store"] {
		return false, nil
	}
	identityPath := filepath.Join(privatePath, "recovery-identity")
	retiredPath := filepath.Join(privatePath, "recovery-retired")
	identityInfo, err := os.Lstat(identityPath)
	if err != nil || identityInfo.Mode()&os.ModeSymlink == 0 {
		return false, err
	}
	retiredInfo, err := os.Lstat(retiredPath)
	if err != nil || retiredInfo.Mode()&os.ModeSymlink == 0 {
		return false, err
	}
	identityTarget, err := os.Readlink(identityPath)
	if err != nil {
		return false, err
	}
	if !strings.HasPrefix(identityTarget, "homonto-recovery-identity-v1:") || len(identityTarget) > 960 {
		return false, nil
	}
	retiredTarget, err := os.Readlink(retiredPath)
	if err != nil || retiredTarget != RecoveryRetiredLinkPrefix+digest([]byte(identityTarget)) {
		return false, nil
	}
	storePath := filepath.Join(privatePath, "recovery-store")
	storeInfo, err := os.Lstat(storePath)
	if err != nil || !storeInfo.IsDir() || storeInfo.Mode()&os.ModeSymlink != 0 || storeInfo.Mode().Perm() != 0o700 {
		return false, err
	}
	storeEntries, err := os.ReadDir(storePath)
	if err != nil {
		return false, err
	}
	if len(storeEntries) != 2 {
		return false, nil
	}
	for _, entry := range storeEntries {
		if entry.Name() != "blobs" && entry.Name() != "descriptors" {
			return false, nil
		}
		path := filepath.Join(storePath, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
			return false, err
		}
	}
	descriptorEntries, err := os.ReadDir(filepath.Join(storePath, "descriptors"))
	if err != nil {
		return false, err
	}
	for _, entry := range descriptorEntries {
		name := entry.Name()
		valid := name == "status" || name == "intent" || name == "bundle" || name == "journal" || name == "completion" ||
			(strings.HasPrefix(name, "history-") && digestPattern.MatchString(strings.TrimPrefix(name, "history-")))
		info, err := os.Lstat(filepath.Join(storePath, "descriptors", name))
		if !valid || err != nil || info.Mode()&os.ModeSymlink == 0 {
			return false, nil
		}
		target, err := os.Readlink(filepath.Join(storePath, "descriptors", name))
		if err != nil || !strings.HasPrefix(target, "homonto-recovery-descriptor-v1:") || len(target) > 960 {
			return false, nil
		}
	}
	blobEntries, err := os.ReadDir(filepath.Join(storePath, "blobs"))
	if err != nil {
		return false, err
	}
	for _, entry := range blobEntries {
		info, err := os.Lstat(filepath.Join(storePath, "blobs", entry.Name()))
		if err != nil || !digestPattern.MatchString(entry.Name()) || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o400 {
			return false, nil
		}
	}
	return true, nil
}

// LoadReceipt reads only a real, regular public receipt and validates its
// shape. A malformed receipt is never treated as a usable ownership proof.
func LoadReceipt(root, runID string) (Receipt, error) {
	path, err := ReceiptPath(root, runID)
	if err != nil {
		return Receipt{}, err
	}
	data, err := readRegularWithin(root, path)
	if err != nil {
		return Receipt{}, fmt.Errorf("migration record: reading receipt: %w", err)
	}
	return ParseReceipt(data)
}

// ParseReceipt validates an in-memory public receipt with the same strict JSON
// rules as LoadReceipt. Migration recovery uses it before a receipt reaches the
// records worktree.
func ParseReceipt(data []byte) (Receipt, error) {
	var receipt Receipt
	if err := decodeStrict(data, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("migration record: invalid receipt: %w", err)
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (r Receipt) Validate() error {
	if r.Version != ReceiptVersion || !SafeRunID(r.RunID) || !digestPattern.MatchString(r.PlanHash) {
		return fmt.Errorf("migration record: invalid receipt identity")
	}
	for _, ref := range []FileRef{r.Config, r.Manifest, r.LayoutMarker, r.Registry} {
		if !filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path || !digestPattern.MatchString(ref.SHA256) {
			return fmt.Errorf("migration record: invalid receipt file reference")
		}
	}
	if r.RecordWrites == nil || r.Retired == nil || r.Bindings == nil || r.CommitProofPath == "" {
		return fmt.Errorf("migration record: incomplete receipt")
	}
	if !safeRelative(r.CommitProofPath) || !strings.HasPrefix(r.CommitProofPath, ".workflow/migrations/"+r.RunID+"/") {
		return fmt.Errorf("migration record: invalid commit proof path")
	}
	seenPaths := map[string]bool{}
	for _, write := range r.RecordWrites {
		if !filepath.IsAbs(write.Path) || filepath.Clean(write.Path) != write.Path || !digestPattern.MatchString(write.PreSHA256) || !digestPattern.MatchString(write.PostSHA256) || write.Action == "" || seenPaths[write.Path] {
			return fmt.Errorf("migration record: invalid record write")
		}
		if write.LegacyConfig != nil && (write.Action != "transform_active" || write.LegacyConfig.Provenance != LegacyConfigProvenance) {
			return fmt.Errorf("migration record: invalid legacy config receipt")
		}
		seenPaths[write.Path] = true
	}
	seenRetired := map[string]bool{}
	seenRetiredPaths := map[string]bool{}
	for _, retired := range r.Retired {
		if !safeRelative(retired.Path) || retired.ID == "" || retired.SchemaVersion < 1 || !digestPattern.MatchString(retired.SHA256) || seenRetiredPaths[retired.Path] {
			return fmt.Errorf("migration record: invalid retired record")
		}
		key := retired.Path + "\x00" + retired.ID
		if seenRetired[key] {
			return fmt.Errorf("migration record: duplicate retired record")
		}
		seenRetired[key] = true
		seenRetiredPaths[retired.Path] = true
	}
	seenBindings := map[string]bool{}
	for _, binding := range r.Bindings {
		if err := binding.Validate(); err != nil {
			return err
		}
		key := binding.Workflow + "\x00" + binding.Change + "\x00" + binding.Repo
		if seenBindings[key] {
			return fmt.Errorf("migration record: duplicate binding")
		}
		seenBindings[key] = true
	}
	return nil
}

func (b Binding) Validate() error {
	if b.Workflow != "onto" || b.Change == "" || b.StateID == "" || b.Repo == "" || b.Branch == "" ||
		!filepath.IsAbs(b.Path) || filepath.Clean(b.Path) != b.Path ||
		!filepath.IsAbs(b.CommonDir) || filepath.Clean(b.CommonDir) != b.CommonDir ||
		!filepath.IsAbs(b.GitDir) || filepath.Clean(b.GitDir) != b.GitDir ||
		!commitPattern.MatchString(b.BaseRef) || !commitPattern.MatchString(b.BaseCommit) ||
		!strings.HasPrefix(b.BaseTarget, "refs/heads/") || !digestPattern.MatchString(b.OwnershipHash) {
		return fmt.Errorf("migration record: invalid binding")
	}
	return nil
}

// ValidateRetiredCorrespondence proves that every retired receipt entry has
// exactly the preserve_retired state-file set it claims. It cannot infer an ID
// from raw YAML (that belongs to ontostate), so callers with the parsed state
// must additionally compare the path-and-ID pair before filtering it.
func (r Receipt) ValidateRetiredCorrespondence(root string) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fmt.Errorf("migration record: retired correspondence root is invalid")
	}
	type expectedRetired struct {
		sha256 string
	}
	byPath := map[string][]RecordWrite{}
	for _, write := range r.RecordWrites {
		if write.Action != "preserve_retired" {
			continue
		}
		if write.PreSHA256 != write.PostSHA256 || len(write.Fields) != 0 || write.LegacyConfig != nil || !retiredStateFile(filepath.Base(write.Path)) {
			return fmt.Errorf("migration record: invalid preserved retired state write")
		}
		dir := filepath.Clean(filepath.Dir(write.Path))
		rel, err := filepath.Rel(root, dir)
		if err != nil || !safeRelative(filepath.ToSlash(rel)) {
			return fmt.Errorf("migration record: preserved retired state is outside workflow root")
		}
		byPath[filepath.ToSlash(rel)] = append(byPath[filepath.ToSlash(rel)], write)
	}
	expected := make(map[string]expectedRetired, len(byPath))
	for path, writes := range byPath {
		sort.Slice(writes, func(i, j int) bool { return writes[i].Path < writes[j].Path })
		seenNames := map[string]bool{}
		for _, write := range writes {
			name := filepath.Base(write.Path)
			if seenNames[name] {
				return fmt.Errorf("migration record: duplicate preserved retired state write")
			}
			seenNames[name] = true
		}
		expected[path] = expectedRetired{sha256: writes[0].PreSHA256}
	}
	actual := make(map[string]RetiredRecord, len(r.Retired))
	for _, retired := range r.Retired {
		if _, exists := actual[retired.Path]; exists {
			return fmt.Errorf("migration record: duplicate retired receipt path")
		}
		actual[retired.Path] = retired
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("migration record: retired receipt coverage differs from preserved state writes")
	}
	for path, wanted := range expected {
		retired, ok := actual[path]
		if !ok || retired.SHA256 != wanted.sha256 {
			return fmt.Errorf("migration record: retired receipt entry lacks its preserved state")
		}
	}
	return nil
}

func (p CommitProof) Validate(runID string) error {
	if p.Version != ReceiptVersion || p.RunID != runID || !commitPattern.MatchString(p.MigrationCommit) || !commitPattern.MatchString(p.Parent) || !commitPattern.MatchString(p.Tree) || !digestPattern.MatchString(p.MessageSHA256) {
		return fmt.Errorf("migration record: invalid commit proof")
	}
	return nil
}

func LoadCommitProof(root, runID string) (CommitProof, error) {
	path, err := CommitProofPath(root, runID)
	if err != nil {
		return CommitProof{}, err
	}
	data, err := readRegularWithin(root, path)
	if err != nil {
		return CommitProof{}, fmt.Errorf("migration record: reading commit proof: %w", err)
	}
	return ParseCommitProof(data, runID)
}

// ParseCommitProof validates an in-memory public proof before recovery writes
// it or stages it in records Git.
func ParseCommitProof(data []byte, runID string) (CommitProof, error) {
	var proof CommitProof
	if err := decodeStrict(data, &proof); err != nil {
		return CommitProof{}, fmt.Errorf("migration record: invalid commit proof: %w", err)
	}
	if err := proof.Validate(runID); err != nil {
		return CommitProof{}, err
	}
	return proof, nil
}

// NewCompletionWitness derives a non-self-referential authorization record
// from the exact receipt and commit-proof bytes already accepted by the
// migration. It never retains those bytes in the witness.
func NewCompletionWitness(runID string, receiptData, proofData []byte) (CompletionWitness, error) {
	receipt, err := ParseReceipt(receiptData)
	if err != nil {
		return CompletionWitness{}, err
	}
	if receipt.RunID != runID {
		return CompletionWitness{}, fmt.Errorf("migration record: receipt run ID differs from completion witness")
	}
	proof, err := ParseCommitProof(proofData, runID)
	if err != nil {
		return CompletionWitness{}, err
	}
	witness := CompletionWitness{
		Version:           CompletionWitnessVersion,
		RunID:             runID,
		PlanHash:          receipt.PlanHash,
		ReceiptSHA256:     digest(receiptData),
		CommitProofSHA256: digest(proofData),
		MigrationCommit:   proof.MigrationCommit,
		MigrationParent:   proof.Parent,
		MigrationTree:     proof.Tree,
	}
	witness.TransactionSHA256 = completionTransactionDigest(witness)
	if err := witness.Validate(); err != nil {
		return CompletionWitness{}, err
	}
	return witness, nil
}

func (w CompletionWitness) Validate() error {
	if w.Version != CompletionWitnessVersion || !SafeRunID(w.RunID) || !digestPattern.MatchString(w.PlanHash) ||
		!digestPattern.MatchString(w.ReceiptSHA256) || !digestPattern.MatchString(w.CommitProofSHA256) ||
		!commitPattern.MatchString(w.MigrationCommit) || !commitPattern.MatchString(w.MigrationParent) ||
		!commitPattern.MatchString(w.MigrationTree) || !digestPattern.MatchString(w.TransactionSHA256) ||
		w.TransactionSHA256 != completionTransactionDigest(w) {
		return fmt.Errorf("migration record: invalid completion witness")
	}
	return nil
}

// ParseCompletionWitness applies the same strict JSON rules used for a
// persisted witness when recovery validates its private journal authority.
func ParseCompletionWitness(data []byte) (CompletionWitness, error) {
	var witness CompletionWitness
	if err := decodeStrict(data, &witness); err != nil {
		return CompletionWitness{}, fmt.Errorf("migration record: invalid completion witness: %w", err)
	}
	if err := witness.Validate(); err != nil {
		return CompletionWitness{}, err
	}
	return witness, nil
}

func completionTransactionDigest(w CompletionWitness) string {
	data := strings.Join([]string{
		"schema-2-completion-v1",
		w.RunID,
		w.PlanHash,
		w.ReceiptSHA256,
		w.CommitProofSHA256,
		w.MigrationCommit,
		w.MigrationParent,
		w.MigrationTree,
	}, "\x00")
	return digest([]byte(data))
}

// LoadCompletionWitness reads the independently persisted completion record.
// It is private recovery material, so it must remain a 0600 regular file.
func LoadCompletionWitness(root, runID string) (CompletionWitness, error) {
	path, err := CompletionWitnessPath(root, runID)
	if err != nil {
		return CompletionWitness{}, err
	}
	if err := fsutil.RequireRealParents(root, filepath.Dir(path)); err != nil {
		return CompletionWitness{}, err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		return CompletionWitness{}, fmt.Errorf("migration record: completion witness is not a 0600 regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return CompletionWitness{}, fmt.Errorf("migration record: reading completion witness: %w", err)
	}
	return ParseCompletionWitness(data)
}

// ValidateCompletionWitness compares the persisted authorization against the
// exact current receipt and proof bytes. It deliberately does not inspect
// sources, which may legitimately evolve after migration completion.
func ValidateCompletionWitness(root, runID string) error {
	receiptPath, err := ReceiptPath(root, runID)
	if err != nil {
		return err
	}
	proofPath, err := CommitProofPath(root, runID)
	if err != nil {
		return err
	}
	receiptData, err := readRegularWithin(root, receiptPath)
	if err != nil {
		return fmt.Errorf("migration record: reading completion receipt: %w", err)
	}
	proofData, err := readRegularWithin(root, proofPath)
	if err != nil {
		return fmt.Errorf("migration record: reading completion proof: %w", err)
	}
	expected, err := NewCompletionWitness(runID, receiptData, proofData)
	if err != nil {
		return err
	}
	actual, err := LoadCompletionWitness(root, runID)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("migration record: completion witness does not match the approved migration records")
	}
	return nil
}

// ValidateBarrier rejects every incomplete or malformed migration operation.
// Complete journals are retained for recovery evidence and are allowed only if
// their matching public receipt remains valid.
func ValidateBarrier(root string) error {
	rootInfo, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("workspace migration barrier: inspect workflow root: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace migration barrier: workflow root is not a real directory")
	}
	base := migrationRoot(root)
	if err := fsutil.RequireRealParents(root, filepath.Dir(base)); err != nil {
		return fmt.Errorf("workspace migration barrier: %w", err)
	}
	info, err := os.Lstat(base)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("workspace migration barrier: inspect migrations: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace migration barrier: migrations path is not a real directory")
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return fmt.Errorf("workspace migration barrier: read migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() == ".gitignore" {
			if err := ensureRegular(filepath.Join(base, entry.Name())); err != nil {
				return fmt.Errorf("workspace migration barrier: invalid migration ignore file")
			}
			continue
		}
		if !entry.IsDir() || !SafeRunID(entry.Name()) {
			return fmt.Errorf("workspace migration barrier: unknown migration entry")
		}
		runID := entry.Name()
		status, err := LoadJournalStatus(root, runID)
		if err != nil {
			retired, retiredErr := IsRetiredPreparation(root, runID)
			if retiredErr != nil {
				return fmt.Errorf("workspace migration barrier: inspect retired preparation artifact: %w", retiredErr)
			}
			if retired {
				continue
			}
			orphan, orphanErr := IsPreparationOrphan(root, runID)
			if orphanErr != nil {
				return fmt.Errorf("workspace migration barrier: inspect preparation artifact: %w", orphanErr)
			}
			if orphan {
				return fmt.Errorf("workspace migration pending (preparing); run homonto workspace migrate recover --run-id %s --action resume --yes or restore before ordinary commands", runID)
			}
			return fmt.Errorf("workspace migration barrier: migration journal is missing, malformed, or unsupported")
		}
		if status.Phase != "complete" {
			return fmt.Errorf("workspace migration pending (%s); run homonto workspace migrate recover --run-id %s --action resume --yes or restore before ordinary commands", status.Phase, runID)
		}
		receipt, err := LoadReceipt(root, runID)
		if err != nil {
			return fmt.Errorf("workspace migration barrier: completed migration receipt is invalid: %w", err)
		}
		if _, err := LoadCommitProof(root, runID); err != nil {
			return fmt.Errorf("workspace migration barrier: completed migration proof is invalid: %w", err)
		}
		if err := receipt.ValidateRetiredCorrespondence(root); err != nil {
			return fmt.Errorf("workspace migration barrier: completed migration retired records are invalid: %w", err)
		}
		if err := ValidateCompletionWitness(root, runID); err != nil {
			return fmt.Errorf("workspace migration barrier: completed migration authorization is invalid: %w", err)
		}
	}
	return nil
}

func validJournalPhase(phase string) bool {
	switch phase {
	case "preparing", "prepared", "applying", "pending-finalization", "restoring", "restored", "complete":
		return true
	default:
		return false
	}
}

// IsRetired reports whether a state-bearing directory is explicitly preserved
// as retired by a completed public receipt. The retained state must still match
// the receipt digest exactly. It is intentionally path+ID based; names are not
// used as identity.
func IsRetired(root, changeDir, stateID string, schemaVersions ...int) (bool, error) {
	if stateID == "" {
		return false, nil
	}
	if len(schemaVersions) > 1 || len(schemaVersions) == 1 && schemaVersions[0] < 1 {
		return false, fmt.Errorf("migration record: retired state schema is invalid")
	}
	stateSchema := 0
	if len(schemaVersions) == 1 {
		stateSchema = schemaVersions[0]
	}
	rel, err := filepath.Rel(root, changeDir)
	if err != nil || !safeRelative(filepath.ToSlash(rel)) {
		return false, fmt.Errorf("migration record: retired path is outside workflow root")
	}
	rel = filepath.ToSlash(rel)
	base := migrationRoot(root)
	entries, err := os.ReadDir(base)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !SafeRunID(entry.Name()) {
			continue
		}
		status, err := LoadJournalStatus(root, entry.Name())
		if err != nil || status.Phase != "complete" {
			return false, fmt.Errorf("migration record: retired-record receipt belongs to an incomplete or invalid migration")
		}
		receipt, err := LoadReceipt(root, entry.Name())
		if err != nil {
			return false, err
		}
		if _, err := LoadCommitProof(root, entry.Name()); err != nil {
			return false, err
		}
		if err := receipt.ValidateRetiredCorrespondence(root); err != nil {
			return false, err
		}
		if err := ValidateCompletionWitness(root, entry.Name()); err != nil {
			return false, err
		}
		for _, retired := range receipt.Retired {
			if retired.Path != rel {
				continue
			}
			if retired.ID != stateID {
				return false, fmt.Errorf("migration record: retired receipt ID differs from state identity")
			}
			if stateSchema != 0 && retired.SchemaVersion != stateSchema {
				return false, fmt.Errorf("migration record: retired receipt schema differs from state identity")
			}
			matches, err := retiredStateMatches(root, changeDir, receipt.RecordWrites, retired.SHA256)
			if err != nil {
				return false, err
			}
			if !matches {
				return false, fmt.Errorf("migration record: retired state does not match its receipt")
			}
			return true, nil
		}
	}
	return false, nil
}

func retiredStateMatches(root, changeDir string, recordWrites []RecordWrite, expected string) (bool, error) {
	changeDir = filepath.Clean(changeDir)
	writes := make(map[string]RecordWrite)
	hasExpected := false
	for _, write := range recordWrites {
		if write.Action != "preserve_retired" || filepath.Dir(write.Path) != changeDir {
			continue
		}
		name := filepath.Base(write.Path)
		if !retiredStateFile(name) || write.PreSHA256 != write.PostSHA256 {
			return false, fmt.Errorf("migration record: invalid preserved retired state write")
		}
		if _, exists := writes[name]; exists {
			return false, fmt.Errorf("migration record: duplicate preserved retired state write")
		}
		writes[name] = write
		if write.PostSHA256 == expected {
			hasExpected = true
		}
	}
	if len(writes) == 0 || !hasExpected {
		return false, fmt.Errorf("migration record: retired receipt has no matching preserved state write")
	}
	for _, name := range []string{"onto-state.yaml", "to-state.yaml", "state.yaml"} {
		data, err := readRegularWithin(root, filepath.Join(changeDir, name))
		if errors.Is(err, os.ErrNotExist) {
			if _, expected := writes[name]; expected {
				return false, nil
			}
			continue
		}
		if err != nil {
			return false, fmt.Errorf("migration record: reading retired state: %w", err)
		}
		write, expected := writes[name]
		if !expected {
			return false, fmt.Errorf("migration record: retired state is not listed in its receipt")
		}
		actual := fmt.Sprintf("%x", sha256.Sum256(data))
		if actual != write.PostSHA256 {
			return false, nil
		}
		delete(writes, name)
	}
	return len(writes) == 0, nil
}

func retiredStateFile(name string) bool {
	switch name {
	case "onto-state.yaml", "to-state.yaml", "state.yaml":
		return true
	default:
		return false
	}
}

// BindingFor returns the exact receipt-listed legacy binding. Callers compare
// every identity field before allowing a registry entry to bypass the normal
// allocated-worktree path rule.
func (r Receipt) BindingFor(workflow, change, repo, path string) (Binding, bool) {
	for _, binding := range r.Bindings {
		if binding.Workflow == workflow && binding.Change == change && binding.Repo == repo && binding.Path == path {
			return binding, true
		}
	}
	return Binding{}, false
}

func readRegularWithin(root, path string) ([]byte, error) {
	if err := fsutil.RequireRealParents(root, filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := ensureRegular(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func ensureRegular(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("not a real regular file")
	}
	return nil
}

func decodeStrict(data []byte, value any) error {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

// rejectDuplicateJSONKeys prevents last-wins decoding for public receipts and
// the journal status envelope. It validates only JSON object structure.
func rejectDuplicateJSONKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := inspectJSONValue(dec); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func inspectJSONValue(dec *json.Decoder) error {
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid JSON object field")
			}
			seen[name] = true
			if err := inspectJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			if err := inspectJSONValue(dec); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	default:
		return fmt.Errorf("invalid JSON delimiter")
	}
}

func safeRelative(path string) bool {
	return path != "" && !filepath.IsAbs(path) && !strings.ContainsAny(path, "\\\x00") && filepath.ToSlash(filepath.Clean(path)) == path && path != ".." && !strings.HasPrefix(path, "../")
}

// SortedBindings returns a copy in the stable public ordering used by receipts.
func SortedBindings(bindings []Binding) []Binding {
	out := make([]Binding, len(bindings))
	copy(out, bindings)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Workflow+"/"+out[i].Change+"/"+out[i].Repo < out[j].Workflow+"/"+out[j].Change+"/"+out[j].Repo
	})
	return out
}
