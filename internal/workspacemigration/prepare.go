package workspacemigration

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workspace"
)

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
	return prepareJournalWithBackup(l, plan, intent, nil)
}

// prepareJournalWithBackup replays initial journal construction from a
// revalidated plan when backup is supplied. The supplied backup must already
// have been generated and verified against the exact current records identity.
func prepareJournalWithBackup(l workspace.Layout, plan Plan, intent preparationIntent, backup *recordsGitBackup) (privateJournal, error) {
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
	bindings, err := migrationBindings(plan, intent.Owner)
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
	if backup == nil {
		journal.RecordsBackup, err = captureRecordsGitBackup(l.WorkflowRoot, intent, plan.RecordsGit)
		if err != nil {
			return privateJournal{}, err
		}
		if err := migrationAfterPreparation("backup"); err != nil {
			return privateJournal{}, err
		}
	} else {
		journal.RecordsBackup = *backup
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

func migrationBindings(plan Plan, owner string) ([]workspace.MigrationBinding, error) {
	if !validMigrationOwner(owner) {
		return nil, fmt.Errorf("workspace migration: invalid private owner token seed")
	}
	seed, err := hex.DecodeString(owner)
	if err != nil {
		return nil, err
	}
	bindings, err := plannedBindingTemplates(plan)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(bindings))
	for i := range bindings {
		token := migrationBindingOwnerToken(seed, bindings[i])
		if seen[token] {
			return nil, fmt.Errorf("workspace migration: private owner token collision")
		}
		seen[token] = true
		bindings[i].Ownership = token
	}
	return bindings, nil
}

func migrationBindingOwnerToken(seed []byte, binding workspace.MigrationBinding) string {
	mac := hmac.New(sha256.New, seed)
	for _, value := range []string{
		"homonto/workspace-migration/binding-owner/v1",
		binding.Workflow,
		binding.Change,
		binding.StateID,
		binding.Repo,
		binding.Path,
		binding.CommonDir,
		binding.GitDir,
		binding.Branch,
		binding.BaseRef,
		binding.BaseTarget,
		binding.BaseCommit,
	} {
		_, _ = mac.Write([]byte(value))
		_, _ = mac.Write([]byte{0})
	}
	return hex.EncodeToString(mac.Sum(nil)[:16])
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
