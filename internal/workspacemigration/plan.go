package workspacemigration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

var (
	canonicalCommit = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)
	stableID        = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// Build inventories the only supported legacy ownership shape. It never
// writes: no marker, registry, journal, Git initialization, index refresh, or
// state normalization is performed. A blocked result is returned with
// ErrBlocked so CLI callers can emit its JSON inventory before exiting nonzero.
func Build(configPath, manifestPath string) (Plan, error) {
	return build(configPath, manifestPath, nil)
}

// buildLocked repeats the read-only inventory while the migration's own
// process/lifecycle/registry locks exist. Only protocol-owned claims and
// guardian metadata are excluded; all other control files and pending
// operations remain blockers.
func buildLocked(configPath, manifestPath string) (Plan, error) {
	resolved, err := absolutePath(configPath)
	if err != nil {
		return buildLockedIgnoring(configPath, manifestPath, migrationIgnoredLocks())
	}
	l, err := workspace.LoadMigration(resolved)
	if err != nil {
		return buildLockedIgnoring(configPath, manifestPath, migrationIgnoredLocks())
	}
	return buildLockedIgnoring(configPath, manifestPath, migrationHeldLockArtifacts(l.ConfigRoot, l.WorkflowRoot))
}

func buildLockedIgnoring(configPath, manifestPath string, ignored map[string]bool) (Plan, error) {
	return build(configPath, manifestPath, ignored)
}

func build(configPath, manifestPath string, ignoredLocks map[string]bool) (Plan, error) {
	requireCleanRecords := true
	ignoredLocks = mergeMigrationIgnoredArtifacts(ignoredLocks)
	p := newPlan()
	configPath, err := absolutePath(configPath)
	if err != nil {
		p.block("config_path_invalid", "", "config path is not a safe absolute path")
		return p.finish()
	}
	p.Config.Path = configPath
	configBytes, err := readRealRegularFile(configPath)
	if err != nil {
		p.block("config_unreadable", configPath, "config must be a real regular file")
		return p.finish()
	}
	p.Config = fingerprint(configPath, "config", configBytes)

	l, err := workspace.LoadMigration(configPath)
	if err != nil {
		code := workspace.MigrationLayoutCode(err)
		p.block("legacy_layout_"+code, configPath, legacyLayoutDetail(code))
		return p.finish()
	}
	layout, err := inventoryLayout(l)
	if err != nil {
		p.block("source_identity_unavailable", l.ConfigRoot, "configured source repository identity cannot be read")
		return p.finish()
	}
	p.Layout = layout

	manifestPath, err = absolutePath(manifestPath)
	if err != nil {
		p.block("manifest_path_invalid", "", "manifest path is not a safe absolute path")
		return p.finish()
	}
	p.Manifest.Path = manifestPath
	if pathWithin(l.WorkflowRoot, manifestPath) {
		p.block("manifest_path_unsafe", manifestPath, "migration manifest must remain outside authoritative workflow records")
		return p.finish()
	}
	manifestBytes, err := readRealRegularFile(manifestPath)
	if err != nil {
		p.block("manifest_unreadable", manifestPath, "manifest must be a real regular file")
		return p.finish()
	}
	p.Manifest = fingerprint(manifestPath, "migration_manifest", manifestBytes)
	manifest, err := decodeManifest(manifestBytes)
	manifestRecords := map[string]ManifestRecord{}
	validManifest := false
	if err != nil {
		p.block("manifest_invalid", manifestPath, "manifest has an unsupported version, unknown field, duplicate field, or invalid attestation")
	} else {
		manifestRecords, validManifest = validateManifest(&p, manifest, manifestPath)
	}

	inspectControlFiles(&p, l, ignoredLocks)
	if records, ok := inspectRecordsGit(&p, l, requireCleanRecords, ignoredLocks); ok {
		p.RecordsGit = records
	} else {
		return p.finish()
	}
	files, err := snapshotRecordFiles(l.WorkflowRoot, ignoredLocks)
	if err != nil {
		var sensitive *sensitiveRecordFileError
		if errors.As(err, &sensitive) {
			p.block("sensitive_record_file", sensitive.path, "M1 will not fingerprint a potential secret-bearing record file")
			return p.finish()
		}
		p.block("records_tree_unsafe", l.WorkflowRoot, "records contain a symlink, nested Git directory, or unsupported filesystem object")
		return p.finish()
	}
	p.Files = files

	discovered := discoverRecords(&p, l.WorkflowRoot, ignoredLocks)
	if validManifest {
		bindManifest(&p, l, manifestRecords, discovered)
		prepareRecordWrites(&p, l.WorkflowRoot, discovered)
		if err := capturePlanLogicalRecordsIndex(&p, l.WorkflowRoot); err != nil {
			p.block("records_logical_index_unavailable", l.WorkflowRoot, "migration-owned records index entries cannot be captured without changing the index")
		}
	}
	return p.finish()
}

// migrationGuardianArtifacts names lock metadata owned by the migration
// protocol. Guardian files are intentionally opaque to planning: their inode
// identity is verified by applylock before a migration can proceed.
func migrationGuardianArtifacts() map[string]bool {
	suffix := applylock.GuardianSuffix
	return map[string]bool{
		".homonto/apply.lock" + suffix:     true,
		".homonto/worktrees.lock" + suffix: true,
		".homonto/lock-guardians/":         true,
		".change-names.lock" + suffix:      true,
		"changes/.onto.lock" + suffix:      true,
		"tasks/.to.lock" + suffix:          true,
	}
}

func mergeMigrationIgnoredArtifacts(extra map[string]bool) map[string]bool {
	merged := migrationGuardianArtifacts()
	for path := range extra {
		merged[path] = true
	}
	return merged
}

func preparationRunIDFromIgnoredArtifacts(ignored map[string]bool) string {
	const prefix = ".workflow/migrations/"
	const suffix = "/private/"
	runID := ""
	for path := range ignored {
		path = filepath.ToSlash(path)
		if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
			continue
		}
		candidate := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
		if !migrationrecord.SafeRunID(candidate) || runID != "" && runID != candidate {
			return ""
		}
		runID = candidate
	}
	return runID
}

// preparationJournalOnly is the narrow exception that lets recovery rebuild
// the reviewed plan after intent publication. Any additional migration artifact
// remains a blocker and cannot be silently reinterpreted as preparation state.
func preparationJournalOnly(root, runID string) bool {
	if !migrationrecord.SafeRunID(runID) {
		return false
	}
	base := filepath.Join(root, ".workflow", "migrations")
	entries, exists, err := realDirectoryEntries(base)
	if err != nil || !exists || len(entries) != 1 || entries[0].Name() != runID || !entries[0].IsDir() {
		return false
	}
	runEntries, exists, err := realDirectoryEntries(filepath.Join(base, runID))
	if err != nil || !exists || len(runEntries) != 1 || runEntries[0].Name() != "private" || !runEntries[0].IsDir() {
		return false
	}
	return preparationRecoveryStoreReady(root, runID)
}

func legacyLayoutDetail(code string) string {
	switch code {
	case "config_not_regular":
		return "config must be a real regular file"
	case "config_validation_failed":
		return "config failed schema-2 path or repository validation"
	case "schema_not_two":
		return "migration requires config schema_version 2"
	case "git_mode_not_existing":
		return "migration requires workflow.git existing"
	case "config_root_uninspectable":
		return "configuration root Git identity cannot be safely inspected"
	case "config_root_is_git":
		return "migration requires a non-Git configuration root"
	case "layout_marker_invalid":
		return "existing schema-2 layout marker is malformed or unsafe"
	case "layout_marker_present":
		return "existing schema-2 layout marker already owns the workspace"
	case "legacy_marker_invalid":
		return "legacy workflow-root marker is malformed or unsafe"
	case "legacy_marker_missing":
		return "legacy workflow-root marker is required"
	case "legacy_marker_mismatch":
		return "legacy workflow-root marker does not match workflow.root"
	case "legacy_alternative_root_uninspectable":
		return "legacy workflow-root alternatives cannot be safely inspected"
	case "legacy_alternative_state_present":
		return "legacy workflow state exists outside the selected workflow.root"
	case "precondition_failed":
		return "migration ownership preconditions are not met"
	default:
		return "migration ownership validation failed"
	}
}

func planHash(plan Plan) string {
	plan.PlanHash = ""
	data, err := json.Marshal(plan)
	if err != nil {
		panic(fmt.Sprintf("workspace migration plan cannot marshal: %v", err))
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func absolutePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("invalid path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func ignoredPath(root, path string, ignored map[string]bool) bool {
	if len(ignored) == 0 {
		return false
	}
	path = filepath.Clean(path)
	if ignored[path] {
		return true
	}
	for prefix := range ignored {
		if !filepath.IsAbs(prefix) || !strings.HasSuffix(prefix, string(filepath.Separator)) {
			continue
		}
		base := filepath.Clean(strings.TrimSuffix(prefix, string(filepath.Separator)))
		rel, err := filepath.Rel(base, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	if ignored[rel] {
		return true
	}
	for prefix := range ignored {
		if strings.HasSuffix(prefix, "/") && (rel == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(rel, prefix)) {
			return true
		}
	}
	return false
}

func readRealRegularFile(path string) ([]byte, error) {
	path, err := absolutePath(path)
	if err != nil {
		return nil, err
	}
	root := filepath.VolumeName(path) + string(os.PathSeparator)
	if err := fsutil.RequireRealParents(root, filepath.Dir(path)); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a real regular file")
	}
	return os.ReadFile(path)
}

func realDirectory(path string) (string, error) {
	path, err := absolutePath(path)
	if err != nil {
		return "", err
	}
	root := filepath.VolumeName(path) + string(os.PathSeparator)
	if err := fsutil.RequireRealParents(root, path); err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("not a real directory")
	}
	return path, nil
}

func readRegularWithin(root, path string) ([]byte, bool, error) {
	if err := fsutil.RequireRealParents(root, filepath.Dir(path)); err != nil {
		return nil, false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, true, fmt.Errorf("not a real regular file")
	}
	data, err := os.ReadFile(path)
	return data, true, err
}

func fingerprint(path, classification string, data []byte) FileFingerprint {
	sum := sha256.Sum256(data)
	return FileFingerprint{Path: filepath.Clean(path), SHA256: hex.EncodeToString(sum[:]), Classification: classification}
}

func inventoryLayout(l workspace.Layout) (Layout, error) {
	result := Layout{
		SchemaVersion: l.SchemaVersion,
		ConfigPath:    l.ConfigPath,
		ConfigRoot:    l.ConfigRoot,
		WorkflowRoot:  l.WorkflowRoot,
		GitMode:       l.GitMode,
		WorktreesDir:  l.WorktreesDir,
		Repos:         make([]Repository, 0, len(l.Repos)),
	}
	aliases := make([]string, 0, len(l.Repos))
	for alias := range l.Repos {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		path := l.Repos[alias]
		_, common, err := workspace.GitIdentity(path)
		if err != nil {
			return Layout{}, err
		}
		result.Repos = append(result.Repos, Repository{Alias: alias, Path: path, GitCommonDir: common})
	}
	return result, nil
}

func inspectControlFiles(p *Plan, l workspace.Layout, ignoredLocks map[string]bool) {
	controlRoot := filepath.Join(l.ConfigRoot, ".homonto")
	legacy := filepath.Join(controlRoot, "workflow-root")
	if data, exists, err := readRegularWithin(l.ConfigRoot, legacy); err != nil {
		p.block("legacy_marker_unsafe", legacy, "legacy workflow root marker must be a real regular file")
	} else if !exists {
		p.block("legacy_marker_missing", legacy, "legacy workflow root marker is required")
	} else {
		p.ControlFiles = append(p.ControlFiles, fingerprint(legacy, "legacy_workflow_root_marker", data))
	}
	for _, entry := range []struct {
		name string
		code string
	}{
		{"workflow-layout.json", "layout_marker_present"},
		{"worktrees.json", "worktree_registry_present"},
		{"worktrees.lock", "worktree_registry_pending"},
	} {
		path := filepath.Join(controlRoot, entry.name)
		if ignoredPath(l.ConfigRoot, path, ignoredLocks) {
			continue
		}
		data, exists, err := readRegularWithin(l.ConfigRoot, path)
		if err != nil {
			p.block("control_file_unsafe", path, "control file must be absent or a real regular file")
			continue
		}
		if !exists {
			continue
		}
		p.ControlFiles = append(p.ControlFiles, fingerprint(path, "control_file", data))
		p.block(entry.code, path, "M1 supports only legacy records without existing schema-2 control state")
	}
	inspectUnexpectedControlFiles(p, l.ConfigRoot, controlRoot, ignoredLocks)
}

func inspectUnexpectedControlFiles(p *Plan, configRoot, controlRoot string, ignoredLocks map[string]bool) {
	entries, exists, err := realDirectoryEntries(controlRoot)
	if err != nil || !exists {
		return // workflow-root inspection above reports a missing or unsafe root.
	}
	known := map[string]bool{
		"workflow-root":        true,
		"workflow-layout.json": true,
		"worktrees.json":       true,
		"worktrees.lock":       true,
	}
	for _, entry := range entries {
		if known[entry.Name()] {
			continue
		}
		path := filepath.Join(controlRoot, entry.Name())
		if ignoredPath(configRoot, path, ignoredLocks) {
			continue
		}
		if entry.Name() == "catalog" {
			inspectProjectionCatalog(p, path)
			continue
		}
		if classification, ok := projectionControlFileClassification(entry.Name()); ok {
			data, exists, err := readRegularWithin(configRoot, path)
			if err != nil || !exists {
				p.block("control_file_unsafe", path, "projection control file must be a real regular file")
				continue
			}
			p.ControlFiles = append(p.ControlFiles, fingerprint(path, classification, data))
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			p.block("control_file_unsafe", path, "unexpected control entry cannot be safely inspected")
			continue
		}
		p.block("other_control_owner_present", path, "M1 supports only the legacy workflow-root control marker")
	}
}

func inspectProjectionCatalog(p *Plan, path string) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		p.block("control_file_unsafe", path, "projection catalog must be a real directory")
	}
}

func projectionControlFileClassification(name string) (string, bool) {
	if name == "remote.lock.json" {
		return "projection_remote_lock", true
	}
	if name == "state.json" || projectionStatePartitionName(name) {
		return "projection_state", true
	}
	return "", false
}

func projectionStatePartitionName(name string) bool {
	if !strings.HasPrefix(name, "state.") || !strings.HasSuffix(name, ".json") {
		return false
	}
	alias := strings.TrimSuffix(strings.TrimPrefix(name, "state."), ".json")
	return validAlias(alias)
}

func inspectRecordsGit(p *Plan, l workspace.Layout, requireClean bool, ignoredLocks map[string]bool) (RecordsGit, bool) {
	root, err := realDirectory(l.WorkflowRoot)
	if err != nil {
		p.block("records_root_invalid", l.WorkflowRoot, "records root must be a real directory")
		return RecordsGit{}, false
	}
	gitDir := filepath.Join(root, ".git")
	info, err := os.Lstat(gitDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		p.block("records_git_invalid", gitDir, "records must be an exact standalone existing Git repository")
		return RecordsGit{}, false
	}
	top, common, err := workspace.GitIdentity(root)
	if err != nil || top != root || common != gitDir {
		p.block("records_git_identity_mismatch", root, "records must retain an exact standalone Git identity")
		return RecordsGit{}, false
	}
	if err := requireSupportedGitIndexFlags(root, safeGitRelativePath); err != nil {
		p.block("records_index_flags_unsupported", root, "records Git uses assume-unchanged, skip-worktree, or another unsupported index flag")
		return RecordsGit{}, false
	}
	head, err := gitText(root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !canonicalCommit.MatchString(head) {
		p.block("records_head_invalid", root, "records Git HEAD must resolve to a canonical commit")
		return RecordsGit{}, false
	}
	indexPath := filepath.Join(gitDir, "index")
	index, err := readRealRegularFile(indexPath)
	if err != nil {
		p.block("records_index_invalid", indexPath, "records Git index must be a real regular file")
		return RecordsGit{}, false
	}
	dirt, err := gitDirtIgnoring(root, root, ignoredLocks)
	if err != nil {
		p.block("records_status_unavailable", root, "records Git dirt cannot be inspected without changing the index")
		return RecordsGit{}, false
	}
	remotes, err := gitLines(root, "remote")
	if err != nil {
		p.block("records_remote_unavailable", root, "records Git remotes cannot be inspected")
		return RecordsGit{}, false
	}
	if len(remotes) != 0 {
		p.block("records_remote_present", root, "M1 supports only the documented local records Git without remotes")
	}
	if requireClean && (dirt.Tracked != 0 || dirt.Untracked != 0 || dirt.Ignored != 0) {
		p.block("records_dirty", root, "migration apply requires a clean records Git worktree outside its own lifecycle locks")
	}
	for _, state := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "index.lock", "homonto-history/pending.json"} {
		path := filepath.Join(gitDir, state)
		if _, err := os.Lstat(path); err == nil {
			p.block("records_git_pending", path, "records Git has a pending operation or history journal")
		} else if !errors.Is(err, os.ErrNotExist) {
			p.block("records_git_pending_unreadable", path, "records Git pending-state path cannot be inspected")
		}
	}
	return RecordsGit{
		Path:         root,
		GitCommonDir: common,
		Head:         head,
		IndexSHA256:  fingerprint(indexPath, "records_git_index", index).SHA256,
		LogicalIndex: []RecordsIndexEntry{},
		Dirt:         dirt,
		RemoteNames:  remotes,
	}, true
}

func gitText(dir string, args ...string) (string, error) {
	data, err := workspace.ReadGit(dir, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func gitLines(dir string, args ...string) ([]string, error) {
	text, err := gitText(dir, args...)
	if err != nil {
		return nil, err
	}
	if text == "" {
		return []string{}, nil
	}
	lines := strings.Split(text, "\n")
	sort.Strings(lines)
	return lines, nil
}

// sourceReferenceSnapshot captures every ref name and object in a declared
// source without touching its index. HEAD's symbolic attachment is intentionally
// recorded separately because two branches can resolve to the same commit.
func sourceReferenceSnapshot(root string) ([]GitReference, error) {
	data, err := workspace.ReadGit(root, "for-each-ref", "--format=%(refname) %(objectname)", "refs")
	if err != nil {
		return nil, err
	}
	refs := []GitReference{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !safeBackupRefName(fields[0]) || !canonicalCommit.MatchString(fields[1]) || seen[fields[0]] {
			return nil, fmt.Errorf("source refs cannot be snapshotted safely")
		}
		seen[fields[0]] = true
		refs = append(refs, GitReference{Name: fields[0], Object: fields[1]})
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name < refs[j].Name })
	return refs, nil
}

func sourceHEADAttachment(root string) (string, bool, error) {
	ref, err := gitText(root, "rev-parse", "--symbolic-full-name", "HEAD")
	if err != nil {
		return "", false, err
	}
	if ref == "HEAD" {
		return "", false, nil
	}
	if !safeBackupRefName(ref) {
		return "", false, fmt.Errorf("source symbolic HEAD is unsafe")
	}
	return ref, true, nil
}

func gitDirt(dir string) (Dirt, error) {
	return gitDirtIgnoring(dir, "", nil)
}

func gitDirtIgnoring(dir, root string, ignored map[string]bool) (Dirt, error) {
	data, err := workspace.ReadGit(dir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored", "--ignore-submodules=none")
	if err != nil {
		return Dirt{}, err
	}
	entries := bytesSplitNUL(data)
	var dirt Dirt
	var normalized []string
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < 3 {
			continue
		}
		path := string(entry[3:])
		isRename := entry[0] == 'R' || entry[0] == 'C' || entry[1] == 'R' || entry[1] == 'C'
		ignoredEntry := (entry[0] == '?' || entry[0] == '!') && root != "" && ignoredGitPath(root, path, ignored)
		if isRename {
			i++ // porcelain -z follows a rename/copy record with its old path.
		}
		if ignoredEntry {
			continue
		}
		normalized = append(normalized, string(entry))
		switch entry[0] {
		case '?':
			dirt.Untracked++
		case '!':
			dirt.Ignored++
		default:
			dirt.Tracked++
		}
	}
	digest := sha256.Sum256([]byte(strings.Join(normalized, "\x00")))
	dirt.SHA256 = hex.EncodeToString(digest[:])
	return dirt, nil
}

// requireSupportedGitIndexFlags rejects index bits whose status output can hide
// worktree content from Git's ordinary dirt probes. The migration intentionally
// has no secret-byte fallback for a clean tracked sensitive path: unsupported
// flags block planning and every later source/index verification instead.
func requireSupportedGitIndexFlags(root string, safePath func(string) bool) error {
	data, err := workspace.ReadGit(root, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	for _, record := range bytesSplitNUL(data) {
		if len(record) < 3 || record[1] != ' ' {
			return fmt.Errorf("Git index flags are malformed")
		}
		path := string(record[2:])
		if !safePath(path) {
			return fmt.Errorf("Git index flag path is unsafe")
		}
		// `git ls-files -v` reports normal cached entries as H, skip-worktree
		// as S, and assume-unchanged entries with a lower-case tag. Refuse any
		// other tag too rather than relying on a Git-version-specific fallback.
		if record[0] != 'H' {
			return fmt.Errorf("Git index has an unsupported flag at %s", path)
		}
	}
	return nil
}

const (
	sourceStatusTrackedClean = "tracked_clean"
	sourceStatusTrackedDirty = "tracked_dirty"
	sourceStatusUntracked    = "untracked"
	sourceStatusIgnored      = "ignored"
)

type sourceIndexEntry struct {
	Blob string
}

type sensitiveSourceFileError struct{ path string }

func (e *sensitiveSourceFileError) Error() string {
	return "potentially secret-bearing source file cannot be content-preserved"
}

// snapshotSourceWorktree binds every source file that Git observes, including
// clean index entries and files below ignored/untracked directories. It never
// retains source content: regular files and link targets are reduced to hashes.
func snapshotSourceWorktree(root string) (WorktreePreservation, error) {
	index, err := sourceIndexEntries(root)
	if err != nil {
		return WorktreePreservation{}, err
	}
	statuses, err := sourceStatuses(root)
	if err != nil {
		return WorktreePreservation{}, err
	}
	files, err := sourceWorktreeFiles(root)
	if err != nil {
		return WorktreePreservation{}, err
	}
	changed, err := sourceUnstagedDiffPaths(root)
	if err != nil {
		return WorktreePreservation{}, err
	}

	type candidate struct {
		status string
		blob   string
	}
	candidates := make(map[string]candidate, len(index)+len(statuses)+len(files))
	for path, entry := range index {
		candidates[path] = candidate{status: sourceStatusTrackedClean, blob: entry.Blob}
	}
	for path, status := range statuses {
		if current, ok := candidates[path]; ok {
			current.status = status
			candidates[path] = current
			continue
		}
		candidates[path] = candidate{status: status}
	}
	for path := range files {
		if _, ok := candidates[path]; ok {
			continue
		}
		status, ok := sourceInheritedStatus(path, statuses)
		if !ok {
			return WorktreePreservation{}, fmt.Errorf("source file is not classified by Git: %s", path)
		}
		candidates[path] = candidate{status: status}
	}

	preservation := WorktreePreservation{Files: make([]PreservedFile, 0, len(candidates))}
	for path, candidate := range candidates {
		if _, exists := files[path]; !exists && candidate.blob == "" {
			info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path)))
			if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
				continue // Child files inherit this ignored/untracked directory status.
			}
			if err == nil {
				return WorktreePreservation{}, fmt.Errorf("source file was not discovered: %s", path)
			}
			if !errors.Is(err, os.ErrNotExist) {
				return WorktreePreservation{}, err
			}
		}
		file, err := fingerprintSourceWorktreePath(root, path, candidate.status, candidate.blob)
		if err != nil {
			return WorktreePreservation{}, err
		}
		if file.Status == sourceStatusTrackedClean && changed[path] {
			return WorktreePreservation{}, fmt.Errorf("clean tracked source path differs from its index: %s", path)
		}
		preservation.Files = append(preservation.Files, file)
	}
	sort.Slice(preservation.Files, func(i, j int) bool {
		return preservation.Files[i].Path < preservation.Files[j].Path
	})
	return preservation, nil
}

func sourceIndexEntries(root string) (map[string]sourceIndexEntry, error) {
	if err := requireSupportedGitIndexFlags(root, safeSourceRelativePath); err != nil {
		return nil, err
	}
	data, err := workspace.ReadGit(root, "ls-files", "-s", "-z")
	if err != nil {
		return nil, err
	}
	entries := make(map[string]sourceIndexEntry)
	for _, record := range bytesSplitNUL(data) {
		before, path, ok := bytes.Cut(record, []byte{'\t'})
		if !ok || !safeSourceRelativePath(string(path)) {
			return nil, fmt.Errorf("invalid source index entry")
		}
		parts := strings.Fields(string(before))
		if len(parts) != 3 || parts[2] != "0" || !canonicalCommit.MatchString(parts[1]) {
			return nil, fmt.Errorf("source index has an unmerged or invalid entry")
		}
		if _, err := strconv.ParseUint(parts[0], 8, 32); err != nil {
			return nil, fmt.Errorf("source index has an invalid file mode")
		}
		name := string(path)
		if _, exists := entries[name]; exists {
			return nil, fmt.Errorf("source index has a duplicate path")
		}
		entries[name] = sourceIndexEntry{Blob: parts[1]}
	}
	return entries, nil
}

func sourceIndexFlags(root string) error {
	return requireSupportedGitIndexFlags(root, safeSourceRelativePath)
}

func sourceStatuses(root string) (map[string]string, error) {
	data, err := workspace.ReadGit(root, "status", "--porcelain=v1", "-z", "--no-renames", "--untracked-files=all", "--ignored", "--ignore-submodules=none")
	if err != nil {
		return nil, err
	}
	statuses := map[string]string{}
	for _, entry := range bytesSplitNUL(data) {
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, fmt.Errorf("source Git status is malformed")
		}
		path := strings.TrimSuffix(string(entry[3:]), "/")
		if !safeSourceRelativePath(path) {
			return nil, fmt.Errorf("source Git status has an unsafe path")
		}
		status := sourceStatusTrackedDirty
		switch entry[0] {
		case '?':
			status = sourceStatusUntracked
		case '!':
			status = sourceStatusIgnored
		}
		if existing, exists := statuses[path]; exists && existing != status {
			return nil, fmt.Errorf("source Git status has a duplicate path")
		}
		statuses[path] = status
	}
	return statuses, nil
}

func sourceWorktreeFiles(root string) (map[string]bool, error) {
	files := map[string]bool{}
	var walk func(string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == ".git" {
				continue
			}
			if !safeSourceRelativePath(rel) {
				return fmt.Errorf("source worktree has an unsafe path")
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 || info.Mode().IsRegular() {
				files[rel] = true
				continue
			}
			if !info.IsDir() {
				return fmt.Errorf("source worktree has an unsupported filesystem object: %s", rel)
			}
			if err := walk(path); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return files, nil
}

func sourceUnstagedDiffPaths(root string) (map[string]bool, error) {
	data, err := workspace.ReadGit(root, "diff", "--no-ext-diff", "--no-textconv", "--name-only", "-z", "--")
	if err != nil {
		return nil, err
	}
	paths := map[string]bool{}
	for _, raw := range bytesSplitNUL(data) {
		path := string(raw)
		if !safeSourceRelativePath(path) {
			return nil, fmt.Errorf("source Git diff has an unsafe path")
		}
		paths[path] = true
	}
	return paths, nil
}

func sourceInheritedStatus(path string, statuses map[string]string) (string, bool) {
	for parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(path))); parent != "." && parent != "/"; parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(parent))) {
		if status, ok := statuses[parent]; ok && (status == sourceStatusUntracked || status == sourceStatusIgnored) {
			return status, true
		}
	}
	return "", false
}

func safeSourceRelativePath(path string) bool {
	return path != "" && !filepath.IsAbs(path) && !strings.ContainsAny(path, "\\\x00") && filepath.ToSlash(filepath.Clean(path)) == path && path != "." && path != ".." && !strings.HasPrefix(path, "../")
}

func fingerprintSourceWorktreePath(root, rel, status, blob string) (PreservedFile, error) {
	if !safeSourceRelativePath(rel) {
		return PreservedFile{}, fmt.Errorf("unsafe source path")
	}
	path := filepath.Join(root, filepath.FromSlash(rel))
	if !pathWithin(root, path) || fsutil.RequireRealParents(root, filepath.Dir(path)) != nil {
		return PreservedFile{}, fmt.Errorf("unsafe source path")
	}
	sensitive := sensitiveSourcePath(rel)
	if sensitive && status != sourceStatusTrackedClean {
		return PreservedFile{}, &sensitiveSourceFileError{path: path}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if sensitive {
			return PreservedFile{}, &sensitiveSourceFileError{path: path}
		}
		return PreservedFile{Path: rel, Status: status, Type: "missing", GitBlob: blob}, nil
	}
	if err != nil {
		return PreservedFile{}, err
	}
	result := PreservedFile{Path: rel, Status: status, Mode: uint32(info.Mode().Perm()), GitBlob: blob}
	switch {
	case info.Mode().IsRegular():
		result.Type = "regular"
		if sensitive {
			if blob == "" {
				return PreservedFile{}, &sensitiveSourceFileError{path: path}
			}
			return result, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return PreservedFile{}, err
		}
		result.SHA256 = migrationDigest(data)
		return result, nil
	case info.Mode()&os.ModeSymlink != 0:
		result.Type = "symlink"
		if sensitive {
			if blob == "" {
				return PreservedFile{}, &sensitiveSourceFileError{path: path}
			}
		}
		target, err := os.Readlink(path)
		if err != nil {
			return PreservedFile{}, err
		}
		result.SHA256 = migrationDigest([]byte(target))
		return result, nil
	default:
		return PreservedFile{}, fmt.Errorf("source path has an unsupported filesystem object: %s", rel)
	}
}

func sensitiveSourcePath(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	name := strings.ToLower(parts[len(parts)-1])
	if sensitiveRecordFile(name) || name == "key" || strings.Contains(name, "kubeconfig") {
		return true
	}
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], ".kube") && strings.EqualFold(parts[i+1], "config") {
			return true
		}
	}
	return false
}

func ignoredGitPath(root, path string, ignored map[string]bool) bool {
	if len(ignored) == 0 || filepath.IsAbs(path) || strings.ContainsAny(path, "\\\x00") {
		return false
	}
	rel := filepath.ToSlash(filepath.Clean(path))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	return ignoredPath(root, filepath.Join(root, filepath.FromSlash(rel)), ignored)
}

// gitIndexSHA256 binds the exact index selected by this worktree without
// refreshing it. Linked execution worktrees have their own private index, so
// using the common directory's index would miss a concurrent writer.
func gitIndexSHA256(dir string) (string, error) {
	path, err := gitText(dir, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("cannot identify Git index")
	}
	data, err := readRealRegularFile(path)
	if err != nil {
		return "", err
	}
	return fingerprint(path, "git_index", data).SHA256, nil
}

func bytesSplitNUL(data []byte) [][]byte {
	parts := make([][]byte, 0)
	for len(data) != 0 {
		i := 0
		for i < len(data) && data[i] != 0 {
			i++
		}
		if i != 0 {
			parts = append(parts, data[:i])
		}
		if i == len(data) {
			break
		}
		data = data[i+1:]
	}
	return parts
}

func snapshotRecordFiles(root string, ignoredLocks map[string]bool) ([]FileFingerprint, error) {
	root, err := realDirectory(root)
	if err != nil {
		return nil, err
	}
	var files []FileFingerprint
	if err := walkRecordDirectory(root, root, &files, ignoredLocks); err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

type sensitiveRecordFileError struct{ path string }

func (e *sensitiveRecordFileError) Error() string { return "potentially secret-bearing record file" }

func walkRecordDirectory(root, dir string, files *[]FileFingerprint, ignoredLocks map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if ignoredPath(root, path, ignoredLocks) {
			continue
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("records Git metadata is unsafe")
			}
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink at %s", path)
		}
		if info.IsDir() {
			if entry.Name() == ".git" {
				return fmt.Errorf("nested Git directory at %s", path)
			}
			if err := walkRecordDirectory(root, path, files, ignoredLocks); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported filesystem object at %s", path)
		}
		if sensitiveRecordFile(entry.Name()) {
			return &sensitiveRecordFileError{path: path}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		*files = append(*files, fingerprint(path, recordFileClassification(rel), data))
	}
	return nil
}

func sensitiveRecordFile(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case ".env", "credentials", "credentials.json", ".netrc", "id_rsa", "id_ed25519":
		return true
	}
	return strings.HasPrefix(name, ".env.") || strings.HasSuffix(name, ".pem") || strings.HasSuffix(name, ".key") || strings.HasSuffix(name, ".p12") || strings.HasSuffix(name, ".pfx")
}

func recordFileClassification(rel string) string {
	parts := strings.Split(rel, "/")
	if len(parts) != 0 && (parts[0] == "changes" || parts[0] == "tasks") {
		for _, part := range parts[1:] {
			if part == ".onto" {
				return "onto_evidence"
			}
		}
	}
	if len(parts) != 0 && parts[0] == ".workflow" {
		return "workflow_metadata"
	}
	if len(parts) != 0 && (parts[0] == "changes" || parts[0] == "tasks") {
		return "workflow_record"
	}
	return "workflow_file"
}

type discoveredRecord struct {
	index     int
	relative  string
	state     *ontostate.RawInspection
	supported bool
}

func discoverRecords(p *Plan, root string, ignoredLocks map[string]bool) []discoveredRecord {
	var discovered []discoveredRecord
	changes := filepath.Join(root, "changes")
	if entries, exists, err := realDirectoryEntries(changes); err != nil {
		p.block("changes_tree_unsafe", changes, "changes must be a real directory")
	} else if exists {
		for _, entry := range entries {
			path := filepath.Join(changes, entry.Name())
			if ignoredPath(root, path, ignoredLocks) {
				continue
			}
			if entry.Name() == "archive" {
				discovered = append(discovered, discoverArchiveRecords(p, root, path)...)
				continue
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				p.block("changes_entry_unsafe", path, "changes entry cannot be safely inspected")
				continue
			}
			if info.Mode().IsRegular() {
				if !changeDocument(entry.Name()) {
					p.block("changes_entry_invalid", path, "changes permits record directories and direct Markdown documentation only")
				}
				continue
			}
			if !info.IsDir() {
				p.block("changes_entry_unsafe", path, "changes entry must be a real directory or regular document")
				continue
			}
			candidate, marker, err := ontoRecordCandidate(root, path)
			if err != nil {
				p.block("changes_record_marker_unsafe", marker, "change record marker must be a real regular file")
			}
			if !candidate {
				continue
			}
			discovered = append(discovered, discoverOntoRecord(p, root, path, "active"))
		}
	}

	tasks := filepath.Join(root, "tasks")
	if entries, exists, err := realDirectoryEntries(tasks); err != nil {
		p.block("tasks_tree_unsafe", tasks, "tasks must be a real directory")
	} else if exists {
		nonLockEntries := 0
		for _, entry := range entries {
			path := filepath.Join(tasks, entry.Name())
			if ignoredPath(root, path, ignoredLocks) {
				continue
			}
			nonLockEntries++
			if entry.Name() == "archive" {
				discovered = append(discovered, discoverUnsupportedTaskArchive(p, root, path)...)
				continue
			}
			if !entry.IsDir() {
				p.block("tasks_entry_invalid", path, "tasks entries must be directories")
				continue
			}
			discovered = append(discovered, discoverUnsupportedTask(p, root, path))
		}
		if nonLockEntries != 0 {
			p.block("tasks_unsupported", tasks, "M1 does not support nonempty to task records")
		}
	}

	for _, entry := range []struct {
		path string
		code string
	}{
		{filepath.Join(root, ".to-promote"), "conversion_recovery_present"},
		{filepath.Join(root, ".onto-demote"), "conversion_recovery_present"},
		{filepath.Join(root, ".workflow", "migrations"), "migration_journal_present"},
		{filepath.Join(root, ".workflow", "events"), "conversion_history_unsupported"},
		{filepath.Join(root, ".workflow", "snapshots"), "conversion_recovery_present"},
		{filepath.Join(root, ".workflow", "lineage.json"), "conversion_history_unsupported"},
		{filepath.Join(root, ".change-names.lock"), "workflow_lifecycle_pending"},
		{filepath.Join(root, "changes", ".onto.lock"), "workflow_lifecycle_pending"},
		{filepath.Join(root, "tasks", ".to.lock"), "workflow_lifecycle_pending"},
		{filepath.Join(root, ".homonto-workflow.json"), "managed_history_owner_present"},
	} {
		if entry.code == "migration_journal_present" && preparationJournalOnly(root, preparationRunIDFromIgnoredArtifacts(ignoredLocks)) {
			continue
		}
		if ignoredPath(root, entry.path, ignoredLocks) {
			continue
		}
		if exists, err := pathExists(root, entry.path); err != nil {
			p.block("records_control_unsafe", entry.path, "records control path cannot be safely inspected")
		} else if exists {
			p.block(entry.code, entry.path, "M1 does not support existing conversion, pending, or managed ownership state")
		}
	}
	validateDiscoveredIdentities(p, discovered)
	return discovered
}

func changeDocument(name string) bool {
	name = strings.ToLower(name)
	return name == "readme" || name == "readme.md" || strings.HasSuffix(name, ".md")
}

func ontoRecordCandidate(root, dir string) (bool, string, error) {
	candidate := false
	for _, name := range []string{"proposal.md", "onto-state.yaml", "state.yaml"} {
		path := filepath.Join(dir, name)
		if err := fsutil.RequireRealParents(root, filepath.Dir(path)); err != nil {
			return false, path, err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, path, err
		}
		if !info.Mode().IsRegular() {
			return true, path, fmt.Errorf("not a real regular record marker")
		}
		candidate = true
	}
	return candidate, "", nil
}

func realDirectoryEntries(path string) ([]os.DirEntry, bool, error) {
	path, err := absolutePath(path)
	if err != nil {
		return nil, false, err
	}
	root := filepath.VolumeName(path) + string(os.PathSeparator)
	if err := fsutil.RequireRealParents(root, path); err != nil {
		return nil, false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, true, fmt.Errorf("not a real directory")
	}
	entries, err := os.ReadDir(path)
	return entries, true, err
}

func pathExists(root, path string) (bool, error) {
	if err := fsutil.RequireRealParents(root, filepath.Dir(path)); err != nil {
		return false, err
	}
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func discoverArchiveRecords(p *Plan, root, archive string) []discoveredRecord {
	entries, exists, err := realDirectoryEntries(archive)
	if err != nil {
		p.block("archive_tree_unsafe", archive, "archive must be a real directory")
		return nil
	}
	if !exists || len(entries) == 0 {
		return nil
	}
	p.block("archives_unsupported", archive, "M1 does not support nonempty archived onto records")
	var discovered []discoveredRecord
	for _, entry := range entries {
		path := filepath.Join(archive, entry.Name())
		if !entry.IsDir() {
			p.block("archive_entry_invalid", path, "archive entries must be directories")
			continue
		}
		discovered = append(discovered, discoverOntoRecord(p, root, path, "archive"))
	}
	return discovered
}

func discoverOntoRecord(p *Plan, root, dir, scope string) discoveredRecord {
	rel, _ := filepath.Rel(root, dir)
	rel = filepath.ToSlash(rel)
	record := Record{
		Path:          filepath.Clean(dir),
		RelativePath:  rel,
		Framework:     "onto",
		Lifecycle:     "malformed",
		StateFiles:    []string{},
		SourceAliases: []string{},
		Sources:       []Source{},
		UnknownFields: []string{},
	}
	var states []ontostate.RawInspection
	for _, name := range []string{"onto-state.yaml", "state.yaml"} {
		path := filepath.Join(dir, name)
		data, exists, err := readRegularWithin(root, path)
		if err != nil {
			p.block("state_file_unsafe", path, "state file must be a real regular file")
			continue
		}
		if !exists {
			continue
		}
		record.StateFiles = append(record.StateFiles, path)
		state, err := ontostate.InspectRaw(data, path)
		if err != nil {
			p.block("state_malformed", path, "onto state must use a supported versioned schema")
			continue
		}
		states = append(states, state)
	}
	if len(states) == 0 {
		p.block("state_missing", dir, "onto record has no supported state file")
		return appendDiscovered(p, record, rel, nil, scope == "active")
	}
	state := states[0]
	for _, other := range states[1:] {
		if !coResidentStatesEqual(state, other) {
			p.block("state_identity_conflict", dir, "co-resident state files disagree on identity, terminal status, or source scope")
		}
	}
	record.ID = state.ID
	record.Name = state.Change
	record.Workflow = state.Workflow
	record.Phase = state.Phase
	record.SchemaVersion = state.SchemaVersion
	record.RepoMode = state.RepoMode
	record.ScalarBaseRef = state.BaseRef
	record.ScalarBaseBranch = state.BaseBranch
	record.SourceAliases = sortedStrings(state.Repos)
	// An empty repo_mode is the original implicit configuration-repository
	// scope; "legacy" is its explicitly carried-forward representation. Neither
	// may be silently treated as an ordinary selected source alias.
	record.LegacyConfigSource = state.RepoMode == "" || state.RepoMode == "legacy"
	for _, inspected := range states {
		record.UnknownFields = append(record.UnknownFields, inspected.UnknownFields...)
	}
	record.UnknownFields = sortedStrings(record.UnknownFields)
	if !stableID.MatchString(record.ID) {
		p.block("state_id_invalid", dir, "onto state must carry a stable plain ID")
	}
	if strings.TrimSpace(record.Name) == "" || strings.ContainsAny(record.Name, "/\\") || strings.IndexFunc(record.Name, unicode.IsControl) >= 0 {
		p.block("state_name_invalid", dir, "onto state change name is unsafe")
	}
	switch {
	case scope == "archive":
		record.Lifecycle = "unsupported_archive"
	case state.Abandoned:
		record.Lifecycle = "retired"
	case state.Archived:
		record.Lifecycle = "unsupported_archived_state"
		p.block("active_archived_state", dir, "archived onto state must not remain in active changes")
	default:
		record.Lifecycle = "active"
	}
	return appendDiscovered(p, record, rel, &state, scope == "active")
}

func coResidentStatesEqual(a, b ontostate.RawInspection) bool {
	return a.SchemaVersion == b.SchemaVersion &&
		a.ID == b.ID &&
		a.Change == b.Change &&
		a.Workflow == b.Workflow &&
		a.Phase == b.Phase &&
		a.Archived == b.Archived &&
		a.Abandoned == b.Abandoned &&
		a.RepoMode == b.RepoMode &&
		a.BaseRef == b.BaseRef &&
		a.BaseBranch == b.BaseBranch &&
		sameCoResidentRepos(a.Repos, b.Repos) &&
		sameCoResidentRepoBases(a.RepoBases, b.RepoBases)
}

func sameCoResidentRepos(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameCoResidentRepoBases(a, b map[string]ontostate.RepoBase) bool {
	if len(a) != len(b) {
		return false
	}
	for alias, base := range a {
		other, ok := b[alias]
		if !ok || base != other {
			return false
		}
	}
	return true
}

func discoverUnsupportedTaskArchive(p *Plan, root, archive string) []discoveredRecord {
	entries, exists, err := realDirectoryEntries(archive)
	if err != nil {
		p.block("tasks_archive_unsafe", archive, "task archive must be a real directory")
		return nil
	}
	if !exists {
		return nil
	}
	var discovered []discoveredRecord
	for _, entry := range entries {
		path := filepath.Join(archive, entry.Name())
		if !entry.IsDir() {
			p.block("tasks_archive_entry_invalid", path, "task archive entries must be directories")
			continue
		}
		discovered = append(discovered, discoverUnsupportedTask(p, root, path))
	}
	return discovered
}

func discoverUnsupportedTask(p *Plan, root, dir string) discoveredRecord {
	rel, _ := filepath.Rel(root, dir)
	rel = filepath.ToSlash(rel)
	record := Record{
		Path:          filepath.Clean(dir),
		RelativePath:  rel,
		Framework:     "to",
		Lifecycle:     "unsupported_to",
		StateFiles:    []string{},
		SourceAliases: []string{},
		Sources:       []Source{},
		UnknownFields: []string{},
	}
	for _, name := range []string{"to-state.yaml", "state.yaml"} {
		path := filepath.Join(dir, name)
		if _, exists, err := readRegularWithin(root, path); err != nil {
			p.block("task_state_file_unsafe", path, "task state file must be a real regular file")
		} else if exists {
			record.StateFiles = append(record.StateFiles, path)
		}
	}
	if len(record.StateFiles) == 0 {
		p.block("task_state_missing", dir, "task record has no state file")
	}
	return appendDiscovered(p, record, rel, nil, false)
}

func appendDiscovered(p *Plan, record Record, relative string, state *ontostate.RawInspection, supported bool) discoveredRecord {
	p.Records = append(p.Records, record)
	return discoveredRecord{index: len(p.Records) - 1, relative: relative, state: state, supported: supported}
}

func validateDiscoveredIdentities(p *Plan, discovered []discoveredRecord) {
	ids := map[string]string{}
	activeNames := map[string]string{}
	for _, record := range discovered {
		if record.state == nil {
			continue
		}
		current := &p.Records[record.index]
		if current.ID != "" {
			if previous, ok := ids[current.ID]; ok {
				p.block("duplicate_state_id", current.Path, "state ID is already used by "+previous)
			} else {
				ids[current.ID] = current.Path
			}
		}
		if current.Lifecycle != "active" || current.Name == "" {
			continue
		}
		if previous, ok := activeNames[current.Name]; ok {
			p.block("duplicate_active_state_name", current.Path, "active state name is already used by "+previous)
		} else {
			activeNames[current.Name] = current.Path
		}
	}
}

func sortedStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validateManifest(p *Plan, manifest Manifest, manifestPath string) (map[string]ManifestRecord, bool) {
	valid := true
	records := make(map[string]ManifestRecord, len(manifest.Records))
	ids := make(map[string]string, len(manifest.Records))
	for _, record := range manifest.Records {
		path, err := manifestRecordPath(record.Path)
		if err != nil {
			p.block("manifest_record_path_invalid", manifestPath, "record path must be an exact active changes/<directory> path")
			valid = false
			continue
		}
		if !stableID.MatchString(record.ID) {
			p.block("manifest_record_id_invalid", manifestPath, "record id must be a stable plain identifier")
			valid = false
		}
		if previous, exists := records[path]; exists {
			p.block("manifest_record_path_duplicate", manifestPath, "record path is declared more than once")
			valid = false
			_ = previous
		} else {
			record.Path = path
			records[path] = record
		}
		if previous, exists := ids[record.ID]; exists {
			p.block("manifest_record_id_duplicate", manifestPath, "record id is declared more than once")
			valid = false
			_ = previous
		} else {
			ids[record.ID] = path
		}
		if record.Sources == nil {
			p.block("manifest_sources_missing", manifestPath, "every record must explicitly declare sources, including an empty array")
			valid = false
			continue
		}
		seenAliases := map[string]bool{}
		for _, source := range record.Sources {
			if !validAlias(source.Alias) || seenAliases[source.Alias] {
				p.block("manifest_source_alias_invalid", manifestPath, "source aliases must be unique declared plain names")
				valid = false
			}
			seenAliases[source.Alias] = true
			if source.BaseRef == "" || source.BaseBranch == "" || source.GitCommonDir == "" {
				p.block("manifest_source_anchor_missing", manifestPath, "each source requires base_ref, base_branch, and git_common_dir")
				valid = false
			}
		}
	}
	return records, valid
}

func manifestRecordPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) || strings.ContainsAny(path, "\\\x00") || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("unsafe record path")
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean != path {
		return "", fmt.Errorf("record path is not canonical")
	}
	parts := strings.Split(clean, "/")
	if len(parts) != 2 || parts[0] != "changes" || !stableID.MatchString(parts[1]) {
		return "", fmt.Errorf("record path is outside active changes")
	}
	return clean, nil
}

func validAlias(alias string) bool {
	return alias != "" && alias != "." && alias != ".." && !strings.ContainsAny(alias, "/\\") && strings.IndexFunc(alias, unicode.IsControl) < 0
}

func bindManifest(p *Plan, l workspace.Layout, manifest map[string]ManifestRecord, discovered []discoveredRecord) {
	byPath := make(map[string]discoveredRecord, len(discovered))
	for _, record := range discovered {
		if record.supported {
			byPath[record.relative] = record
		}
	}
	for path, input := range manifest {
		discovered, exists := byPath[path]
		if !exists {
			p.block("manifest_record_not_found", path, "manifest record path does not identify a supported active onto record")
			continue
		}
		if p.Records[discovered.index].ID != input.ID {
			p.block("manifest_state_id_mismatch", p.Records[discovered.index].Path, "manifest record ID does not match the state at its explicit path")
		}
	}
	for _, discovered := range byPath {
		input, exists := manifest[discovered.relative]
		if !exists {
			p.block("manifest_record_missing", p.Records[discovered.index].Path, "every active or retired onto record requires an explicit path-and-ID manifest entry")
			continue
		}
		bindRecordSources(p, l, discovered, input)
	}
}

func bindRecordSources(p *Plan, l workspace.Layout, discovered discoveredRecord, input ManifestRecord) {
	record := &p.Records[discovered.index]
	if discovered.state == nil {
		return
	}
	expected := sortedStrings(discovered.state.Repos)
	provided := make([]string, 0, len(input.Sources))
	for _, source := range input.Sources {
		provided = append(provided, source.Alias)
	}
	provided = sortedStrings(provided)
	if !sameStrings(expected, provided) {
		p.block("manifest_source_alias_mismatch", record.Path, "manifest sources must exactly match the state-selected repository aliases")
	}
	for _, source := range input.Sources {
		planned, ok := validateSource(p, l, record.Path, discovered.state, source)
		if ok {
			record.Sources = append(record.Sources, planned)
		}
	}
}

func prepareRecordWrites(p *Plan, root string, discovered []discoveredRecord) {
	for _, found := range discovered {
		if found.state == nil {
			continue
		}
		record := &p.Records[found.index]
		switch record.Lifecycle {
		case "retired":
			for _, path := range record.StateFiles {
				preparePreservedRecordWrite(p, root, path, RecordWritePreserveRetired)
			}
		case "active":
			if len(record.StateFiles) != 1 {
				p.block("co_resident_state_transform_unsupported", record.Path, "M2 refuses to transform co-resident state files without an atomic dual-state conversion")
				continue
			}
			anchors, complete := recordTransformAnchors(record, found.state)
			if !complete {
				// bindManifest has already recorded the concrete missing, extra, or
				// invalid source-anchor blocker. Do not invent a partial postimage.
				continue
			}
			path := record.StateFiles[0]
			raw, exists, err := readRegularWithin(root, path)
			if err != nil || !exists {
				p.block("state_transform_unreadable", path, "active state must remain a real regular file while planning its transform")
				continue
			}
			transformed, err := ontostate.TransformActiveForExplicitRepos(raw, record.ID, anchors)
			if err != nil {
				p.block("state_transform_unsupported", path, "active state cannot be transformed without losing semantics or guessing provenance")
				continue
			}
			action := RecordWritePreserveActive
			if len(transformed.ChangedFields) != 0 {
				action = RecordWriteTransformActive
			}
			p.addPreparedRecordWrite(path, action, raw, transformed.Bytes, transformed.ChangedFields, transformed.LegacyConfig)
		}
	}
}

func preparePreservedRecordWrite(p *Plan, root, path, action string) {
	raw, exists, err := readRegularWithin(root, path)
	if err != nil || !exists {
		p.block("state_transform_unreadable", path, "state must remain a real regular file while planning its preservation")
		return
	}
	p.addPreparedRecordWrite(path, action, raw, raw, nil, nil)
}

func recordTransformAnchors(record *Record, state *ontostate.RawInspection) ([]ontostate.ValidatedRepoAnchor, bool) {
	if len(record.Sources) != len(state.Repos) {
		return nil, false
	}
	provided := make([]string, 0, len(record.Sources))
	anchors := make([]ontostate.ValidatedRepoAnchor, 0, len(record.Sources))
	for _, source := range record.Sources {
		provided = append(provided, source.Alias)
		anchors = append(anchors, ontostate.ValidatedRepoAnchor{
			Alias:        source.Alias,
			BaseRef:      source.BaseRef,
			BaseBranch:   source.BaseBranch,
			GitCommonDir: source.GitCommonDir,
		})
	}
	if !sameStrings(sortedStrings(state.Repos), sortedStrings(provided)) {
		return nil, false
	}
	return anchors, true
}

func (p *Plan) addPreparedRecordWrite(path, action string, preimage, postimage []byte, changedFields []string, legacyConfig *ontostate.LegacyConfig) {
	pre := fingerprint(path, "state_preimage", preimage).SHA256
	post := fingerprint(path, "state_postimage", postimage).SHA256
	summary := RecordWrite{
		Path:          filepath.Clean(path),
		Action:        action,
		PreSHA256:     pre,
		PostSHA256:    post,
		ChangedFields: append([]string{}, changedFields...),
		LegacyConfig:  legacyConfig,
	}
	p.RecordWrites = append(p.RecordWrites, summary)
	p.preparedRecordWrites = append(p.preparedRecordWrites, preparedRecordWrite{
		summary:   summary,
		preimage:  append([]byte(nil), preimage...),
		postimage: append([]byte(nil), postimage...),
	})
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validateSource(p *Plan, l workspace.Layout, recordPath string, state *ontostate.RawInspection, input ManifestSource) (Source, bool) {
	planned := Source{Alias: input.Alias, BaseRef: input.BaseRef, BaseBranch: input.BaseBranch}
	repo, declared := l.Repos[input.Alias]
	if !declared {
		p.block("manifest_source_unknown", recordPath, "manifest source alias is not declared by the schema-2 configuration")
		return planned, false
	}
	planned.Path = repo
	_, common, err := workspace.GitIdentity(repo)
	if err != nil {
		p.block("source_identity_unavailable", repo, "configured source Git identity cannot be read")
		return planned, false
	}
	planned.GitCommonDir = common
	manifestCommon, err := canonicalManifestDirectory(input.GitCommonDir)
	if err != nil || manifestCommon != common {
		p.block("source_common_dir_mismatch", repo, "manifest git_common_dir must be the live canonical source Git common directory")
		return planned, false
	}
	if !canonicalCommit.MatchString(input.BaseRef) {
		p.block("source_base_invalid", repo, "manifest base_ref must be a canonical commit ID")
		return planned, false
	}
	base, err := gitText(repo, "rev-parse", "--verify", "--end-of-options", input.BaseRef+"^{commit}")
	if err != nil || base != input.BaseRef {
		p.block("source_base_missing", repo, "manifest base_ref is not an existing commit in the declared source")
		return planned, false
	}
	branch, err := gitText(repo, "check-ref-format", "--branch", input.BaseBranch)
	if err != nil || branch != input.BaseBranch {
		p.block("source_branch_invalid", repo, "manifest base_branch is not a valid literal local branch name")
		return planned, false
	}
	branchHead, err := gitText(repo, "rev-parse", "--verify", "--end-of-options", "refs/heads/"+input.BaseBranch+"^{commit}")
	if err != nil || !canonicalCommit.MatchString(branchHead) {
		p.block("source_branch_missing", repo, "manifest base_branch is not an existing local branch")
		return planned, false
	}
	if _, err := workspace.ReadGit(repo, "merge-base", "--is-ancestor", input.BaseRef, "refs/heads/"+input.BaseBranch); err != nil {
		p.block("source_base_not_ancestor", repo, "manifest base_ref is not an ancestor of the literal local base_branch")
		return planned, false
	}
	head, err := gitText(repo, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !canonicalCommit.MatchString(head) {
		p.block("source_head_invalid", repo, "declared source HEAD cannot be read as a canonical commit")
		return planned, false
	}
	headRef, headAttached, err := sourceHEADAttachment(repo)
	if err != nil {
		p.block("source_head_attachment_unavailable", repo, "declared source symbolic HEAD cannot be inspected without changing it")
		return planned, false
	}
	refs, err := sourceReferenceSnapshot(repo)
	if err != nil {
		p.block("source_refs_unavailable", repo, "declared source refs cannot be snapshotted without changing them")
		return planned, false
	}
	if err := sourceIndexFlags(repo); err != nil {
		p.block("source_index_flags_unsupported", repo, "declared source uses assume-unchanged, skip-worktree, or another unsupported index flag")
		return planned, false
	}
	indexSHA256, err := gitIndexSHA256(repo)
	if err != nil {
		p.block("source_index_unavailable", repo, "declared source Git index cannot be fingerprinted without changing it")
		return planned, false
	}
	dirt, err := gitDirt(repo)
	if err != nil {
		p.block("source_dirt_unavailable", repo, "declared source Git dirt cannot be inspected without changing the index")
		return planned, false
	}
	preservation, err := snapshotSourceWorktree(repo)
	if err != nil {
		var sensitive *sensitiveSourceFileError
		if errors.As(err, &sensitive) {
			p.block("sensitive_source_file", sensitive.path, "migration will not read a dirty, untracked, or ignored potential source secret")
		} else {
			p.block("source_preservation_unavailable", repo, "declared source files cannot be content-preserved without changing the worktree")
		}
		return planned, false
	}
	if recorded, ok := state.RepoBases[input.Alias]; ok {
		if (recorded.BaseRef != "" && recorded.BaseRef != input.BaseRef) ||
			(recorded.BaseBranch != "" && recorded.BaseBranch != input.BaseBranch) ||
			(recorded.GitCommonDir != "" && recorded.GitCommonDir != common) {
			p.block("state_source_anchor_mismatch", recordPath, "manifest source anchor disagrees with already recorded source provenance")
			return planned, false
		}
	}
	planned.BaseBranchHead = branchHead
	planned.Head = head
	planned.HeadRef = headRef
	planned.HeadAttached = headAttached
	planned.Refs = refs
	planned.IndexSHA256 = indexSHA256
	planned.Dirt = dirt
	planned.Preservation = preservation
	if input.ExecutionPath != "" {
		execution, ok := inspectExecution(p, input.ExecutionPath, common)
		if !ok {
			return planned, false
		}
		planned.Execution = execution
	}
	return planned, true
}

func canonicalManifestDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("not an absolute canonical path")
	}
	actual, err := realDirectory(path)
	if err != nil || actual != path {
		return "", fmt.Errorf("not a real canonical directory")
	}
	return actual, nil
}

func inspectExecution(p *Plan, path, expectedCommon string) (*Execution, bool) {
	path, err := canonicalManifestDirectory(path)
	if err != nil {
		p.block("execution_path_invalid", path, "execution_path must be a real canonical Git worktree root")
		return nil, false
	}
	top, common, err := workspace.GitIdentity(path)
	if err != nil || top != path || common != expectedCommon {
		p.block("execution_identity_mismatch", path, "execution_path must retain the declared source Git common directory")
		return nil, false
	}
	head, err := gitText(path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !canonicalCommit.MatchString(head) {
		p.block("execution_head_invalid", path, "execution checkout HEAD cannot be read as a canonical commit")
		return nil, false
	}
	headRef, headAttached, err := sourceHEADAttachment(path)
	if err != nil || !headAttached {
		p.block("execution_head_attachment_unavailable", path, "execution checkout must retain an inspectable attached symbolic HEAD")
		return nil, false
	}
	refs, err := sourceReferenceSnapshot(path)
	if err != nil {
		p.block("execution_refs_unavailable", path, "execution checkout refs cannot be snapshotted without changing them")
		return nil, false
	}
	if err := sourceIndexFlags(path); err != nil {
		p.block("execution_index_flags_unsupported", path, "execution checkout uses assume-unchanged, skip-worktree, or another unsupported index flag")
		return nil, false
	}
	branch, err := gitText(path, "branch", "--show-current")
	if err != nil {
		p.block("execution_branch_unavailable", path, "execution checkout branch cannot be inspected")
		return nil, false
	}
	if branch == "" {
		p.block("execution_branch_unavailable", path, "execution checkout must retain an attached branch")
		return nil, false
	}
	gitDir, err := gitText(path, "rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDir) || filepath.Clean(gitDir) != gitDir {
		p.block("execution_git_dir_unavailable", path, "execution checkout Git directory cannot be inspected as a canonical absolute path")
		return nil, false
	}
	indexSHA256, err := gitIndexSHA256(path)
	if err != nil {
		p.block("execution_index_unavailable", path, "execution checkout index cannot be fingerprinted without changing it")
		return nil, false
	}
	dirt, err := gitDirt(path)
	if err != nil {
		p.block("execution_dirt_unavailable", path, "execution checkout dirt cannot be inspected without changing the index")
		return nil, false
	}
	preservation, err := snapshotSourceWorktree(path)
	if err != nil {
		var sensitive *sensitiveSourceFileError
		if errors.As(err, &sensitive) {
			p.block("sensitive_source_file", sensitive.path, "migration will not read a dirty, untracked, or ignored potential source secret")
		} else {
			p.block("execution_preservation_unavailable", path, "execution checkout files cannot be content-preserved without changing the worktree")
		}
		return nil, false
	}
	return &Execution{Path: path, GitCommonDir: common, GitDir: gitDir, Head: head, HeadRef: headRef, HeadAttached: headAttached, Refs: refs, Branch: branch, IndexSHA256: indexSHA256, Dirt: dirt, Preservation: preservation}, true
}
