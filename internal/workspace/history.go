package workspace

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/workflowroot"
)

const ownershipFile = ".homonto-workflow.json"
const historyIgnore = "# Transient workflow locks and staging, not durable records.\n*.lock\n*.tmp\n.onto-*\n.to-*\n.homonto-*\n!/.homonto-workflow.json\n.staging/\n.staging-*/\n.stage-*/\n"

var ownedTrees = []string{"changes", "tasks", "specs", "adr", "guides", ".workflow"}

type historyOwner struct {
	Version      int    `json:"version"`
	ConfigPath   string `json:"config_path"`
	ConfigRoot   string `json:"config_root"`
	WorkflowRoot string `json:"workflow_root"`
}

// Version 2 separates raw working fingerprints (mode:SHA-256) from normalized
// Git entries (mode:object ID). Empty values represent deletions in both maps.
// Raw fingerprints detect even edits that a clean filter would erase.
type historyJournal struct {
	Version  int               `json:"version"`
	Base     string            `json:"base_head"`
	Message  string            `json:"message"`
	Paths    map[string]string `json:"paths"`
	GitPaths map[string]string `json:"git_paths"`
	Scope    *MutationScope    `json:"scope,omitempty"`
	Before   map[string]string `json:"before,omitempty"`
}

// MutationScope is the operation's write set, relative to WorkflowRoot. Paths
// are exact files; Trees are reserved for creation, moves, and generated packs.
// It is not a sandbox: cooperating writers must take the history lock BEFORE
// lifecycle/registry/state locks. Fingerprints cannot attribute arbitrary editor
// writes to the same file while the callback runs.
type MutationScope struct {
	Paths   []string     `json:"paths,omitempty"`
	Trees   []string     `json:"trees,omitempty"`
	Archive *ArchivePlan `json:"archive,omitempty"`
}

func (s MutationScope) validate() error {
	for _, p := range append(append([]string{}, s.Paths...), s.Trees...) {
		if !ownedHistoryPath(p) || !strings.Contains(p, "/") || p == "changes/archive" || p == "tasks/archive" {
			return fmt.Errorf("workspace history: mutation scope %q must select specific workflow records, not a whole owned root", p)
		}
	}
	if p := s.Archive; p != nil {
		parts := strings.Split(p.Source, "/")
		if len(parts) != 2 || (parts[0] != "changes" && parts[0] != "tasks") || !safeHistoryPath(p.Source) || !safeHistoryPath(p.Destination) || !strings.HasPrefix(p.Destination, parts[0]+"/archive/"+p.Date+"-"+parts[1]) {
			return fmt.Errorf("workspace history: invalid prepared archive intent")
		}
		if _, err := time.Parse("2006-01-02", p.Date); err != nil {
			return fmt.Errorf("workspace history: invalid prepared archive date: %w", err)
		}
		base := parts[0] + "/archive/" + p.Date + "-" + parts[1]
		if p.Destination != base {
			suffix := strings.TrimPrefix(p.Destination, base+"-")
			n, err := strconv.Atoi(suffix)
			if err != nil || n < 2 || strconv.Itoa(n) != suffix {
				return fmt.Errorf("workspace history: invalid prepared archive generation")
			}
		}
		source, destination := false, false
		for _, tree := range s.Trees {
			source = source || tree == p.Source
			destination = destination || tree == p.Destination
		}
		if !source || !destination {
			return fmt.Errorf("workspace history: archive endpoints absent from write scope")
		}
	}
	return nil
}

func (s MutationScope) contains(p string) bool {
	for _, path := range s.Paths {
		if p == path {
			return true
		}
	}
	for _, tree := range s.Trees {
		if p == tree || strings.HasPrefix(p, tree+"/") {
			return true
		}
	}
	return false
}

func (s MutationScope) snapshot(l Layout) (map[string]string, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	files, err := historySnapshotTrees(l, s.Trees)
	if err != nil {
		return nil, err
	}
	for _, p := range s.Paths {
		hash, err := historyFileHash(l, p)
		if err != nil {
			return nil, err
		}
		if hash != "" {
			files[p] = hash
		}
	}
	return files, nil
}

// HistoryInspection is a read-only view; inspecting never acquires a lock or
// initializes a repository, even when the records directory does not exist.
type HistoryInspection struct {
	GitMode     string   `json:"git_mode"`
	GitRoot     string   `json:"git_root,omitempty"`
	Initialized bool     `json:"initialized"`
	Pending     bool     `json:"pending"`
	Head        string   `json:"head,omitempty"`
	OwnedPaths  []string `json:"owned_paths"`
}

func InspectHistory(l Layout) (HistoryInspection, error) {
	s := HistoryInspection{GitMode: l.GitMode, OwnedPaths: append([]string(nil), ownedTrees...)}
	if l.GitMode != "managed" {
		if l.GitMode == "existing" {
			root, err := existingGitOwner(l)
			s.GitRoot, s.Initialized = root, err == nil
			return s, err
		}
		return s, nil
	}
	if err := historyTopology(l); err != nil {
		return s, err
	}
	if _, err := os.Lstat(filepath.Join(l.WorkflowRoot, ".git")); errors.Is(err, fs.ErrNotExist) {
		return s, nil
	} else if err != nil {
		return s, err
	}
	if err := validateHistoryOwner(l); err != nil {
		return s, err
	}
	s.Initialized, s.GitRoot = true, l.WorkflowRoot
	head, err := historyHead(l)
	if err != nil {
		return s, err
	}
	s.Head = head
	_, err = os.Lstat(journalPath(l))
	s.Pending = err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return s, err
	}
	return s, nil
}

// InitManaged initializes only a new, empty, explicitly managed records root.
// Existing-Git mode validates its owner without setting up or committing files.
func InitManaged(l Layout) error {
	if l.GitMode == "existing" {
		_, err := existingGitOwner(l)
		return err
	}
	if l.GitMode != "managed" {
		return fmt.Errorf("workspace init requires workflow Git mode managed or existing")
	}
	if err := historyTopology(l); err != nil {
		return err
	}
	if err := historyLayoutMarker(l, false); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(l.WorkflowRoot, ".git")); err == nil {
		if err := validateHistoryOwner(l); err != nil {
			return err
		}
		return noPendingHistory(l)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	entries, err := os.ReadDir(l.WorkflowRoot)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("workspace init: managed records root must be empty; refusing adoption of %s", l.WorkflowRoot)
	}
	// Explicit --git-dir prevents accidentally borrowing identity from the
	// enclosing configuration/source repository before this repository exists.
	if err := historyIdentity(l); err != nil {
		return err
	}
	if err := historyLayoutMarker(l, true); err != nil {
		return err
	}
	if err := os.MkdirAll(l.WorkflowRoot, 0o755); err != nil {
		return err
	}
	// Claim the Git directory exclusively, rather than racing a second init
	// into reinitializing and rebinding its newly created repository.
	if err := os.Mkdir(filepath.Join(l.WorkflowRoot, ".git"), 0o755); err != nil {
		return fmt.Errorf("workspace init: could not claim empty managed repository: %w", err)
	}
	if _, err := historyGit(l, "init", "--", l.WorkflowRoot); err != nil {
		return err
	}
	lock, err := historyLock(l)
	if err != nil {
		return err
	}
	defer lock.Release()
	entries, err = os.ReadDir(l.WorkflowRoot)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Name() != ".git" {
		return fmt.Errorf("workspace init: records root was populated during initialization; refusing adoption")
	}
	owner, err := json.MarshalIndent(expectedHistoryOwner(l), "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(l.WorkflowRoot, ownershipFile), append(owner, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(l.WorkflowRoot, ".gitignore"), []byte(historyIgnore), 0o644); err != nil {
		return err
	}
	paths := make(map[string]string)
	for _, p := range []string{ownershipFile, ".gitignore"} {
		paths[p], err = historyFileHash(l, p)
		if err != nil {
			return err
		}
	}
	return recordHistory(l, paths, "Initialize managed workflow history")
}

// RunMutation is the shared outer boundary for onto and to mutations. A failed
// callback may have completed durable multi-hop progress, which is still saved.
func RunMutation(configRoot, label string, mutate func() error, scopes ...MutationScope) error {
	return runMutation(configRoot, label, func(Layout) (MutationScope, error) {
		if len(scopes) != 1 {
			return MutationScope{}, fmt.Errorf("workspace history: automatic mutation requires one explicit write scope")
		}
		return scopes[0], scopes[0].validate()
	}, mutate)
}

func runMutation(configRoot, label string, resolve func(Layout) (MutationScope, error), mutate func() error) error {
	if _, err := os.Lstat(filepath.Join(configRoot, "homonto.toml")); errors.Is(err, fs.ErrNotExist) {
		return mutate()
	}
	l, err := LoadRoot(configRoot)
	if err != nil {
		return err
	}
	if l.GitMode != "managed" {
		return mutate()
	}
	if strings.TrimSpace(label) == "" {
		return fmt.Errorf("workspace history: mutation label must not be empty")
	}
	return withHistory(l, func() error {
		scope, err := resolve(l)
		if err != nil {
			return err
		}
		before, err := scope.snapshot(l)
		if err != nil {
			return err
		}
		base, err := historyHead(l)
		if err != nil {
			return err
		}
		// Persist intent before entering any handler. A killed process leaves an
		// explicitly interrupted operation, even if it never reached Git staging.
		intent := historyJournal{Version: 3, Base: base, Message: label, Scope: &scope, Before: before}
		if err := writeHistoryJournal(l, intent); err != nil {
			return err
		}
		opErr := mutate()
		if errors.Is(opErr, errArchiveChanged) {
			return fmt.Errorf("workspace history intent pending; run homonto workspace recover after inspecting the archive conflict: %w", opErr)
		}
		after, err := scope.snapshot(l)
		if err != nil {
			return errors.Join(opErr, fmt.Errorf("workspace history intent pending; run homonto workspace recover: %w", err))
		}
		historyErr := recordHistory(l, changedHistoryPaths(before, after), label, &intent)
		if historyErr != nil {
			historyErr = fmt.Errorf("workspace history pending; run homonto workspace recover: %w", historyErr)
		}
		return errors.Join(opErr, historyErr)
	})
}

// Checkpoint records manual edits in selected workflow-relative paths. No
// selection means the explicit owned set, never the entire Git working tree.
func Checkpoint(l Layout, paths []string, message string) error {
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("workspace checkpoint: --message must not be empty")
	}
	selected := make([]string, 0, len(paths))
	for _, p := range paths {
		p = filepath.ToSlash(p)
		if !safeHistoryPath(p) || !ownedHistoryPath(p) {
			return fmt.Errorf("workspace checkpoint: path %q is not an owned workflow record", p)
		}
		selected = append(selected, p)
	}
	return withHistory(l, func() error {
		before, err := historyTree(l, "HEAD")
		if err != nil {
			return err
		}
		after, err := historySnapshot(l)
		if err != nil {
			return err
		}
		// Working fingerprints and Git object IDs are different representations.
		// Select candidates here; recordHistory compares normalized entries to HEAD.
		changed := after
		for p := range before {
			if _, ok := changed[p]; !ok {
				changed[p] = ""
			}
		}
		for p := range changed {
			keep := ownedHistoryPath(p) && len(selected) == 0
			for _, s := range selected {
				keep = keep || p == s || strings.HasPrefix(p, s+"/")
			}
			if !keep || !ownedHistoryPath(p) {
				delete(changed, p)
			}
		}
		return recordHistory(l, changed, message)
	})
}

// RecoverHistory retries an exact prepared checkpoint, refusing subsequent edits.
// An interrupted pre-write intent instead records the current scoped delta with
// an explicit recovery label; it never asserts successful operation completion.
func RecoverHistory(l Layout) error {
	if err := validateHistoryOwner(l); err != nil {
		return err
	}
	lock, err := historyLock(l)
	if err != nil {
		return err
	}
	defer lock.Release()
	j, err := readHistoryJournal(l)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("workspace recover: no pending history checkpoint")
	}
	if err != nil {
		return err
	}
	if j.Version == 3 {
		head, err := historyHead(l)
		if err != nil {
			return err
		}
		if head != j.Base {
			return fmt.Errorf("workspace history: HEAD moved from interrupted mutation base")
		}
		if err := historyIndex(l, nil); err != nil {
			return err
		}
		if err := historyIdentity(l); err != nil {
			return err
		}
		after, err := j.Scope.snapshot(l)
		if err != nil {
			return err
		}
		// Recovery accepts only the explicit scope. It cannot prove whether an
		// editor touched that scope after the crash, nor that the operation finished.
		return recordHistory(l, changedHistoryPaths(j.Before, after), "Recover interrupted operation: "+j.Message, &j)
	}
	if err := verifyHistoryFiles(l, j.Paths); err != nil {
		return err
	}
	head, err := historyHead(l)
	if err != nil {
		return err
	}
	if head != j.Base {
		// A crash after a successful commit but before journal removal is safe
		// to acknowledge only when that exact single commit can be proved.
		if err := verifyHistoryCommit(l, j); err != nil {
			return err
		}
		if err := historyIndex(l, nil); err != nil {
			return err
		}
		return os.Remove(journalPath(l))
	}
	if err := historyIndex(l, j.GitPaths); err != nil {
		return err
	}
	if err := historyIdentity(l); err != nil {
		return err
	}
	return finishHistory(l, j)
}

func withHistory(l Layout, fn func() error) error {
	if err := validateHistoryOwner(l); err != nil {
		return err
	}
	lock, err := historyLock(l)
	if err != nil {
		return err
	}
	defer lock.Release()
	if err := noPendingHistory(l); err != nil {
		return err
	}
	if err := historyIndex(l, nil); err != nil {
		return err
	}
	if err := historyIdentity(l); err != nil {
		return err
	}
	return fn()
}

func expectedHistoryOwner(l Layout) historyOwner {
	return historyOwner{1, filepath.Clean(l.ConfigPath), filepath.Clean(l.ConfigRoot), filepath.Clean(l.WorkflowRoot)}
}

func historyLayoutMarker(l Layout, write bool) error {
	marker := workflowroot.LayoutMarker{SchemaVersion: l.SchemaVersion, ConfigPath: l.ConfigPath, WorkflowRoot: l.WorkflowRoot, GitMode: l.GitMode}
	path := filepath.Join(l.ConfigRoot, ".homonto", workflowroot.LayoutMarkerFile)
	if err := fsutil.RequireRealParents(l.ConfigRoot, filepath.Dir(path)); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("workspace history: unsafe layout marker %s", path)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(path)
	if err == nil {
		var old workflowroot.LayoutMarker
		if json.Unmarshal(data, &old) != nil || old != marker {
			return fmt.Errorf("workspace history: layout ownership mismatch; refusing configuration rebind")
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if !write {
		return workflowroot.ValidateLayout(l.ConfigPath, l.WorkflowRoot, l.GitMode, l.SchemaVersion)
	}
	data, err = json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteControlPlaneWithin(l.ConfigRoot, path, append(data, '\n'), 0o600)
}

func historyTopology(l Layout) error {
	if l.GitMode != "managed" || !l.ExplicitRepos() {
		return fmt.Errorf("workflow history requires managed Git mode")
	}
	if !filepath.IsAbs(l.ConfigPath) || !filepath.IsAbs(l.ConfigRoot) || !filepath.IsAbs(l.WorkflowRoot) || filepath.Dir(l.ConfigPath) != filepath.Clean(l.ConfigRoot) {
		return fmt.Errorf("workspace history: invalid configuration identity")
	}
	if within(l.WorkflowRoot, l.ConfigRoot) {
		return fmt.Errorf("workspace history: managed workflow root overlaps configuration root")
	}
	for _, reserved := range []string{".git", ".homonto", ".opencode", ".claude", "homonto.toml", "opencode.json", "opencode.jsonc"} {
		if overlaps(l.WorkflowRoot, filepath.Join(l.ConfigRoot, reserved)) {
			return fmt.Errorf("workspace history: managed workflow root overlaps configuration control plane")
		}
	}
	for name, repo := range l.Repos {
		if overlaps(l.WorkflowRoot, repo) {
			return fmt.Errorf("workspace history: managed workflow root overlaps source repos.%s", name)
		}
	}
	if l.WorktreesDir != "" && overlaps(l.WorkflowRoot, l.WorktreesDir) {
		return fmt.Errorf("workspace history: managed workflow root overlaps worktrees directory")
	}
	// Reject symlinked ancestors as well as the root itself: ownership must
	// never silently follow a replacement directory to a different repository.
	for p := filepath.Clean(l.WorkflowRoot); ; p = filepath.Dir(p) {
		info, err := os.Lstat(p)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
			return fmt.Errorf("workspace history: unsafe directory %s", p)
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	parent := filepath.Dir(l.WorkflowRoot)
	for {
		if _, err := os.Stat(parent); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(parent) == parent {
			return err
		}
		parent = filepath.Dir(parent)
	}
	if top, _, err := gitRoots(parent); err == nil {
		return fmt.Errorf("workspace history: managed workflow root is nested in Git repository %s", top)
	}
	return nil
}

func existingGitOwner(l Layout) (string, error) {
	// Git may own an as-yet nonexistent records directory through an ancestor.
	p := l.WorkflowRoot
	for {
		if _, err := os.Stat(p); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) || filepath.Dir(p) == p {
			return "", err
		}
		p = filepath.Dir(p)
	}
	out, err := runHistoryGit(p, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("workspace existing Git owner: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func validateHistoryOwner(l Layout) error {
	if err := historyTopology(l); err != nil {
		return err
	}
	gitDir := filepath.Join(l.WorkflowRoot, ".git")
	info, err := os.Lstat(gitDir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("workspace history: managed repository is not initialized at %s; run homonto workspace init --yes", l.WorkflowRoot)
	}
	top, common, err := gitRoots(l.WorkflowRoot)
	if err != nil || top != l.WorkflowRoot || common != gitDir {
		return fmt.Errorf("workspace history: managed repository must be an exact standalone Git root")
	}
	for _, p := range []string{"homonto-history", "homonto-history/pending.json"} {
		if info, err := os.Lstat(filepath.Join(gitDir, p)); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("workspace history: refusing symlink %s", p)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	if _, err := historyFileHash(l, ownershipFile); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(l.WorkflowRoot, ownershipFile))
	var owner historyOwner
	if err != nil || json.Unmarshal(data, &owner) != nil || owner != expectedHistoryOwner(l) {
		return fmt.Errorf("workspace history: ownership mismatch at %s; refusing adoption or configuration rebind", l.WorkflowRoot)
	}
	head, err := historyHead(l)
	if err != nil {
		return err
	}
	if head == "" {
		j, err := readHistoryJournal(l)
		if err != nil || j.Base != "" || j.Paths[ownershipFile] == "" {
			return fmt.Errorf("workspace history: uncommitted ownership without an initialization journal; refusing adoption")
		}
		return verifyHistoryFiles(l, map[string]string{ownershipFile: j.Paths[ownershipFile]})
	}
	tracked, err := historyGit(l, "show", "HEAD:"+ownershipFile)
	if err != nil || string(tracked) != string(data) {
		return fmt.Errorf("workspace history: ownership metadata differs from committed identity")
	}
	return nil
}

func historyLock(l Layout) (*applylock.Lock, error) {
	return applylock.AcquireProcess(filepath.Join(l.WorkflowRoot, ".git", "homonto-history"))
}

func journalPath(l Layout) string {
	return filepath.Join(l.WorkflowRoot, ".git", "homonto-history", "pending.json")
}

func noPendingHistory(l Layout) error {
	if _, err := os.Lstat(journalPath(l)); err == nil {
		return fmt.Errorf("workspace history checkpoint pending; run homonto workspace recover before another mutation")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func readHistoryJournal(l Layout) (historyJournal, error) {
	var j historyJournal
	data, err := os.ReadFile(journalPath(l))
	if err != nil {
		return j, err
	}
	if err := json.Unmarshal(data, &j); err != nil {
		return j, fmt.Errorf("workspace history: invalid pending journal: %w", err)
	}
	if j.Version == 3 {
		if j.Scope == nil || j.Base == "" || strings.TrimSpace(j.Message) == "" || len(j.Paths) != 0 || len(j.GitPaths) != 0 {
			return j, fmt.Errorf("workspace history: invalid mutation intent")
		}
		if err := j.Scope.validate(); err != nil {
			return j, err
		}
		for p, hash := range j.Before {
			parts := strings.Split(hash, ":")
			if !ownedHistoryPath(p) || !j.Scope.contains(p) || len(parts) != 2 || (parts[0] != "100644" && parts[0] != "100755") || len(parts[1]) != 64 {
				return j, fmt.Errorf("workspace history: invalid mutation intent fingerprint %q", p)
			}
			if _, err := hex.DecodeString(parts[1]); err != nil {
				return j, err
			}
		}
		return j, nil
	}
	if j.Version != 2 {
		return j, fmt.Errorf("workspace history: unsupported pending journal version %d (expected 2)", j.Version)
	}
	if len(j.Paths) == 0 || len(j.Paths) != len(j.GitPaths) || strings.TrimSpace(j.Message) == "" {
		return j, fmt.Errorf("workspace history: invalid pending journal")
	}
	for p, hash := range j.Paths {
		initial := j.Base == "" && (p == ownershipFile || p == ".gitignore")
		if !safeHistoryPath(p) || (!initial && !ownedHistoryPath(p)) {
			return j, fmt.Errorf("workspace history: unsafe pending path %q", p)
		}
		gitHash, ok := j.GitPaths[p]
		if !ok || (hash == "") != (gitHash == "") {
			return j, fmt.Errorf("workspace history: inconsistent pending Git entry for %q", p)
		}
		for i, value := range []string{hash, gitHash} {
			if value != "" {
				parts := strings.Split(value, ":")
				if len(parts) != 2 || (parts[0] != "100644" && parts[0] != "100755") || (len(parts[1]) != 64 && (i == 0 || len(parts[1]) != 40)) {
					return j, fmt.Errorf("workspace history: invalid pending hash for %q", p)
				}
				if _, err := hex.DecodeString(parts[1]); err != nil {
					return j, err
				}
			}
		}
	}
	return j, nil
}

func safeHistoryPath(p string) bool {
	return p != "" && p != "." && !strings.ContainsAny(p, "\\\x00") && !strings.HasPrefix(p, "/") && filepath.ToSlash(filepath.Clean(p)) == p && p != ".." && !strings.HasPrefix(p, "../")
}

func transientHistoryPart(p string) bool {
	return strings.HasSuffix(p, ".lock") || strings.HasSuffix(p, ".tmp") || p == ".staging" || strings.HasPrefix(p, ".staging-") || strings.HasPrefix(p, ".stage-") || strings.HasPrefix(p, ".onto-") || strings.HasPrefix(p, ".to-") || strings.HasPrefix(p, ".homonto-")
}

func ownedHistoryPath(p string) bool {
	if !safeHistoryPath(p) {
		return false
	}
	parts := strings.Split(p, "/")
	for _, part := range parts {
		if part == ".git" || transientHistoryPart(part) {
			return false
		}
	}
	for _, tree := range ownedTrees {
		if parts[0] == tree {
			return true
		}
	}
	return false
}

func historyHash(mode string, data []byte) string {
	return fmt.Sprintf("%s:%x", mode, sha256.Sum256(data))
}

func historyFileHash(l Layout, p string) (string, error) {
	if !safeHistoryPath(p) {
		return "", fmt.Errorf("workspace history: unsafe path %q", p)
	}
	parts := strings.Split(p, "/")
	full := l.WorkflowRoot
	for i, part := range parts {
		full = filepath.Join(full, part)
		info, err := os.Lstat(full)
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !info.IsDir()) {
			return "", fmt.Errorf("workspace history: refusing symlink or non-directory ancestor %s", full)
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("workspace history: not a regular record: %s", full)
			}
			data, err := os.ReadFile(full)
			if err != nil {
				return "", err
			}
			mode := "100644"
			if info.Mode()&0o111 != 0 {
				mode = "100755"
			}
			return historyHash(mode, data), nil
		}
	}
	return "", nil
}

func normalizedHistoryPaths(l Layout, paths map[string]string) (map[string]string, error) {
	if err := verifyHistoryFiles(l, paths); err != nil {
		return nil, err
	}
	filemode, err := historyGit(l, "config", "--type=bool", "--default=true", "--get", "core.filemode")
	if err != nil {
		return nil, err
	}
	normalized := make(map[string]string, len(paths))
	for p, raw := range paths {
		if raw == "" {
			normalized[p] = ""
			continue
		}
		data, err := os.ReadFile(filepath.Join(l.WorkflowRoot, p))
		if err != nil {
			return nil, err
		}
		mode, _, _ := strings.Cut(raw, ":")
		if historyHash(mode, data) != raw {
			return nil, fmt.Errorf("workspace history: record %q changed during normalization", p)
		}
		// --path applies attributes, EOL conversion and clean filters exactly as
		// Git does for this record. No -w and no index writes during preflight.
		cmd := historyGitCommand(l, "hash-object", "--path="+p, "--stdin")
		cmd.Dir = l.WorkflowRoot
		cmd.Stdin = bytes.NewReader(data)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("workspace history: Git normalization failed for %q; fix Git attributes/clean filters before retrying the checkpoint: %s: %w", p, strings.TrimSpace(stderr.String()), err)
		}
		if strings.TrimSpace(string(filemode)) == "false" {
			// Git ignores working executable bits in this mode, but the raw
			// fingerprint must still detect mode changes during recovery.
			mode = "100644"
			staged, err := historyGit(l, "ls-files", "--stage", "-z", "--", p)
			if err != nil {
				return nil, err
			}
			if len(staged) != 0 {
				meta, _, _ := strings.Cut(string(staged), "\t")
				fields := strings.Fields(meta)
				if len(fields) != 3 || fields[2] != "0" || (fields[0] != "100644" && fields[0] != "100755") {
					return nil, fmt.Errorf("workspace history: unsupported index entry for %q", p)
				}
				mode = fields[0]
			}
		}
		normalized[p] = mode + ":" + strings.TrimSpace(string(out))
	}
	if err := verifyHistoryFiles(l, paths); err != nil {
		return nil, err
	}
	return normalized, nil
}

func historySnapshot(l Layout) (map[string]string, error) {
	return historySnapshotTrees(l, ownedTrees)
}

func historySnapshotTrees(l Layout, trees []string) (map[string]string, error) {
	files := make(map[string]string)
	for _, tree := range trees {
		if err := fsutil.RequireRealParents(l.WorkflowRoot, filepath.Dir(filepath.Join(l.WorkflowRoot, tree))); err != nil {
			return nil, err
		}
		err := filepath.WalkDir(filepath.Join(l.WorkflowRoot, tree), func(path string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && path == filepath.Join(l.WorkflowRoot, tree) {
				return nil
			}
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(l.WorkflowRoot, path)
			if err != nil {
				return err
			}
			p := filepath.ToSlash(rel)
			if d.Name() == ".git" || d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("workspace history: refusing symlink or nested .git at %s", p)
			}
			if transientHistoryPart(d.Name()) {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			hash, err := historyFileHash(l, p)
			if err == nil {
				files[p] = hash
			}
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	return files, nil
}

func changedHistoryPaths(before, after map[string]string) map[string]string {
	changed := make(map[string]string)
	for p, hash := range after {
		if before[p] != hash {
			changed[p] = hash
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			changed[p] = ""
		}
	}
	return changed
}

func historyTree(l Layout, ref string) (map[string]string, error) {
	out, err := historyGit(l, "ls-tree", "-r", "-z", ref)
	if err != nil {
		return nil, err
	}
	files := make(map[string]string)
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry == "" {
			continue
		}
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 {
			return nil, fmt.Errorf("workspace history: malformed Git tree")
		}
		if !ownedHistoryPath(path) && path != ownershipFile && path != ".gitignore" {
			continue
		}
		if fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return nil, fmt.Errorf("workspace history: unsupported tracked record %s", path)
		}
		files[path] = fields[0] + ":" + fields[2]
	}
	return files, nil
}

func historyHead(l Layout) (string, error) {
	out, err := historyGit(l, "rev-parse", "--verify", "HEAD")
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	// Only an unborn symbolic branch counts as no HEAD, not corrupt Git data.
	ref, symErr := historyGit(l, "symbolic-ref", "-q", "HEAD")
	if symErr == nil {
		_, refErr := historyGit(l, "show-ref", "--verify", "--quiet", strings.TrimSpace(string(ref)))
		var exit *exec.ExitError
		if errors.As(refErr, &exit) && exit.ExitCode() == 1 {
			return "", nil
		}
	}
	return "", err
}

// A clean index is mandatory at entry. Recovery alone may reuse staged paths,
// and only when their exact blobs match the journal; unrelated staging is never
// reset, stashed, or folded into a history commit.
func historyIndex(l Layout, pending map[string]string) error {
	for _, state := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "index.lock"} {
		if _, err := os.Lstat(filepath.Join(l.WorkflowRoot, ".git", state)); err == nil {
			return fmt.Errorf("workspace history: Git operation or index lock present: %s", state)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	unmerged, err := historyGit(l, "ls-files", "--unmerged", "-z")
	if err != nil {
		return err
	}
	if len(unmerged) != 0 {
		return fmt.Errorf("workspace history: index has unresolved conflicts")
	}
	out, err := historyGit(l, "diff", "--cached", "--name-only", "--no-renames", "-z")
	if err != nil {
		return err
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" {
			continue
		}
		expected, ok := pending[p]
		if !ok {
			return fmt.Errorf("workspace history: index contains staged path %q; unstage it before checkpointing", p)
		}
		staged, err := historyGit(l, "ls-files", "--stage", "-z", "--", p)
		if err != nil {
			return err
		}
		if expected == "" && len(staged) == 0 {
			continue
		}
		meta, _, ok := strings.Cut(string(staged), "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[2] != "0" {
			return fmt.Errorf("workspace history: pending index differs at %q", p)
		}
		if fields[0]+":"+fields[1] != expected {
			return fmt.Errorf("workspace history: pending index differs at %q", p)
		}
	}
	return nil
}

func verifyHistoryFiles(l Layout, paths map[string]string) error {
	for p, expected := range paths {
		actual, err := historyFileHash(l, p)
		if err != nil {
			return err
		}
		if actual != expected {
			return fmt.Errorf("workspace history: pending record %q changed since checkpoint; restore its saved content before homonto workspace recover", p)
		}
	}
	return nil
}

func recordHistory(l Layout, paths map[string]string, message string, intents ...*historyJournal) error {
	var intent *historyJournal
	if len(intents) != 0 {
		intent = intents[0]
	}
	if len(paths) == 0 {
		if intent != nil {
			return os.Remove(journalPath(l))
		}
		return nil
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("workspace history: checkpoint message must not be empty")
	}
	if err := historyIndex(l, nil); err != nil {
		return err
	}
	base, err := historyHead(l)
	if err != nil {
		return err
	}
	if intent != nil && base != intent.Base {
		return fmt.Errorf("workspace history: HEAD moved from mutation intent base")
	}
	gitPaths, err := normalizedHistoryPaths(l, paths)
	if err != nil {
		return err
	}
	if err := historyIndex(l, nil); err != nil {
		return err
	}
	// A filesystem change can normalize back to HEAD. That is not a new
	// history event and must not create an empty commit.
	if base != "" {
		tree, err := historyTree(l, "HEAD")
		if err != nil {
			return err
		}
		for p, hash := range gitPaths {
			if tree[p] == hash {
				delete(paths, p)
				delete(gitPaths, p)
			}
		}
	}
	if len(paths) == 0 {
		if intent != nil {
			return os.Remove(journalPath(l))
		}
		return nil
	}
	j := historyJournal{Version: 2, Base: base, Message: message, Paths: paths, GitPaths: gitPaths}
	if intent == nil {
		if err := noPendingHistory(l); err != nil {
			return err
		}
	}
	if err := writeHistoryJournal(l, j); err != nil {
		return err
	}
	return finishHistory(l, j)
}

func writeHistoryJournal(l Layout, j historyJournal) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteControlPlaneWithin(filepath.Join(l.WorkflowRoot, ".git"), journalPath(l), append(data, '\n'), 0o600)
}

func finishHistory(l Layout, j historyJournal) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("workspace history checkpoint pending; run homonto workspace recover: %w", err)
		}
	}()
	gitPaths, err := normalizedHistoryPaths(l, j.Paths)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(gitPaths, j.GitPaths) {
		return fmt.Errorf("workspace history: pending Git normalization changed; restore Git attributes/clean filter configuration before recovery")
	}
	if err := historyIndex(l, j.GitPaths); err != nil {
		return err
	}
	head, err := historyHead(l)
	if err != nil {
		return err
	}
	if head != j.Base {
		return fmt.Errorf("workspace history: HEAD moved from pending checkpoint base")
	}
	paths := make([]string, 0, len(j.Paths))
	for p, hash := range j.Paths {
		if hash == "" {
			staged, err := historyGit(l, "ls-files", "-z", "--", p)
			if err != nil {
				return err
			}
			if len(staged) == 0 {
				continue // Recovery of a deletion already removed from the index.
			}
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	// NUL-separated literal pathspecs handle leading dashes, glob characters,
	// newlines and deletions without expanding a directory or shell expression.
	if len(paths) != 0 {
		cmd := historyGitCommand(l, "add", "--all", "--pathspec-from-file=-", "--pathspec-file-nul")
		cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git add: %s: %w", strings.TrimSpace(string(out)), err)
		}
	}
	if err := historyIndex(l, j.GitPaths); err != nil {
		return err
	}
	if err := verifyHistoryFiles(l, j.Paths); err != nil {
		return err
	}
	if _, err := historyGit(l, "commit", "-m", j.Message); err != nil {
		return err
	}
	if err := verifyHistoryCommit(l, j); err != nil {
		return err
	}
	return os.Remove(journalPath(l))
}

func verifyHistoryCommit(l Layout, j historyJournal) error {
	out, err := historyGit(l, "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil {
		return err
	}
	parents := strings.Fields(string(out))
	if (j.Base == "" && len(parents) != 1) || (j.Base != "" && (len(parents) != 2 || parents[1] != j.Base)) {
		return fmt.Errorf("workspace history: HEAD moved from pending checkpoint base")
	}
	out, err = historyGit(l, "diff-tree", "--root", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z", "HEAD")
	if err != nil {
		return err
	}
	actual := make(map[string]string)
	tree, err := historyTree(l, "HEAD")
	if err != nil {
		return err
	}
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			actual[p] = tree[p]
		}
	}
	if !reflect.DeepEqual(actual, j.GitPaths) {
		return fmt.Errorf("workspace history: committed paths or content differ from pending checkpoint")
	}
	return verifyHistoryFiles(l, j.Paths)
}

func historyIdentity(l Layout) error {
	for _, who := range []string{"GIT_AUTHOR_IDENT", "GIT_COMMITTER_IDENT"} {
		if _, err := historyGit(l, "var", who); err != nil {
			return fmt.Errorf("workspace history: configured Git author and committer identity required; configure your Git identity before initializing or mutating records: %w", err)
		}
	}
	return nil
}

func historyGitCommand(l Layout, args ...string) *exec.Cmd {
	args = append([]string{"--git-dir=" + filepath.Join(l.WorkflowRoot, ".git"), "--work-tree=" + l.WorkflowRoot, "-c", "user.useConfigOnly=true"}, args...)
	return historyCommand(l.ConfigRoot, args...)
}

func historyCommand(dir string, args ...string) *exec.Cmd {
	// Keep automatic maintenance inside the history operation's lifetime and
	// lock, without disabling maintenance or changing the user's Git config.
	args = append([]string{"-c", "maintenance.autoDetach=false", "-c", "gc.autoDetach=false"}, args...)
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Never let a caller's Git plumbing environment redirect writes to source
	// repositories or an alternate index. Identity/configuration remains intact.
	blocked := map[string]bool{"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true, "GIT_INDEX_FILE": true, "GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_PREFIX": true, "GIT_NAMESPACE": true, "GIT_LITERAL_PATHSPECS": true, "GIT_GLOB_PATHSPECS": true, "GIT_NOGLOB_PATHSPECS": true, "GIT_ICASE_PATHSPECS": true, "GIT_OPTIONAL_LOCKS": true}
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		if !blocked[key] {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_LITERAL_PATHSPECS=1", "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

func historyGit(l Layout, args ...string) ([]byte, error) {
	out, err := historyGitCommand(l, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

func runHistoryGit(dir string, args ...string) ([]byte, error) {
	out, err := historyCommand(dir, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %s: %w", args[0], strings.TrimSpace(string(out)), err)
	}
	return out, nil
}
