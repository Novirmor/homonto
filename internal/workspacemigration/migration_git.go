package workspacemigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

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
	data, err := journalProofData(*journal)
	if err != nil {
		return err
	}
	write, err := prepareWrite(journalScopeRecords, journalKindProof, journal.Workflow, proofPath, false, nil, true, data, 0o644)
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

func journalProofData(journal privateJournal) ([]byte, error) {
	proof := migrationrecord.CommitProof{Version: migrationrecord.ReceiptVersion, RunID: journal.RunID, MigrationCommit: journal.MigrationCommit, Parent: journal.RecordsParent, Tree: journal.ExpectedMigrationTree, MessageSHA256: journal.MigrationMessageSHA256}
	if err := proof.Validate(journal.RunID); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
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
