package workspacemigration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

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
