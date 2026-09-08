package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// Worktree is a per-change execution binding, not a replacement [repos] entry.
// BaseCommit is immutable; BaseRef is the integration target resolved at removal.
type Worktree struct {
	Role         string `json:"role,omitempty"`
	Workflow     string `json:"workflow"`
	Change       string `json:"change"`
	Repo         string `json:"repo"`
	StateID      string `json:"stateID"`
	WorkflowRoot string `json:"workflowRoot"`
	Parent       string `json:"parent"`
	Path         string `json:"path"`
	CommonDir    string `json:"commonDir"`
	GitDir       string `json:"gitDir,omitempty"`
	Ownership    string `json:"ownership"`
	Branch       string `json:"branch"`
	BaseRef      string `json:"baseRef"`
	BaseTarget   string `json:"baseTarget"`
	BaseCommit   string `json:"baseCommit"`
	Status       string `json:"status"`
}

type worktreeRegistry struct {
	Version int        `json:"version"`
	Entries []Worktree `json:"entries"`
}

var worktreeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validWorktreeAlias(alias string) bool {
	return alias != "" && alias != "." && alias != ".." &&
		!strings.ContainsAny(alias, `/\`) && strings.IndexFunc(alias, unicode.IsControl) < 0
}

func checkWorktreeNames(workflow, change string) error {
	if workflow != "onto" && workflow != "to" {
		return fmt.Errorf("worktree: workflow must be onto or to")
	}
	if !worktreeName.MatchString(change) || change == "archive" {
		return fmt.Errorf("worktree: unsafe path: invalid change name %q", change)
	}
	return nil
}

func worktreeAliases(l Layout, aliases []string) ([]string, error) {
	selected := append([]string(nil), aliases...)
	sort.Strings(selected)
	for i, alias := range selected {
		if !validWorktreeAlias(alias) {
			return nil, fmt.Errorf("worktree: unsafe path: invalid repository alias %q", alias)
		}
		if i > 0 && alias == selected[i-1] {
			return nil, fmt.Errorf("worktree: duplicate repository alias %q", alias)
		}
		if l.Repos[alias] == "" {
			return nil, fmt.Errorf("worktree: missing declaration: repository %q is not in [repos]", alias)
		}
	}
	if !l.ExplicitRepos() {
		selected = append([]string{""}, selected...)
	}
	return selected, nil
}

func worktreeAuthority(l Layout, alias string) (string, error) {
	if alias == "" && !l.ExplicitRepos() {
		return l.ConfigRoot, nil
	}
	if !validWorktreeAlias(alias) {
		return "", fmt.Errorf("worktree: unsafe path: invalid repository alias %q", alias)
	}
	path := l.Repos[alias]
	if path == "" {
		return "", fmt.Errorf("worktree: missing declaration: repository %q is not in [repos]", alias)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(l.ConfigRoot, path)
	}
	return filepath.Clean(path), nil
}

func worktreeParent(l Layout) (string, error) {
	if l.WorktreesDir == "" {
		return "", fmt.Errorf("worktree: missing declaration: configure worktrees.dir before using registered worktrees")
	}
	path := l.WorktreesDir
	if !filepath.IsAbs(path) {
		path = filepath.Join(l.ConfigRoot, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := realWorktreePath(path, true); err != nil {
		return "", err
	}
	return path, nil
}

// Check every existing component, including ancestors of a configured parent.
// Missing trailing directories are allowed, but links (even internal ones) are not.
func realWorktreePath(path string, directory bool) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent := abs
	if !directory {
		parent = filepath.Dir(abs)
	}
	root := filepath.VolumeName(abs) + string(os.PathSeparator)
	if err := fsutil.RequireRealParents(root, parent); err != nil {
		return fmt.Errorf("worktree: unsafe path %s: %w", path, err)
	}
	if !directory {
		info, err := os.Lstat(abs)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("worktree: unsafe path %s: expected a regular file, not a symlink", path)
		}
	}
	return nil
}

func worktreeGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	// Do not inherit a caller's repository/index override. Read-only Git probes
	// must not refresh the original checkout's index either.
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("worktree: git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func worktreeCommon(dir string) (string, error) {
	if err := realWorktreePath(dir, true); err != nil {
		return "", err
	}
	if err := realWorktreePath(filepath.Join(dir, ".git"), true); err != nil {
		// Linked worktrees have a regular .git file, not a directory.
		if err := realWorktreePath(filepath.Join(dir, ".git"), false); err != nil {
			return "", err
		}
	}
	top, err := worktreeGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	if filepath.Clean(top) != filepath.Clean(dir) {
		return "", fmt.Errorf("worktree: ownership: %s is not an exact Git worktree root", dir)
	}
	common, err := worktreeGit(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if err := realWorktreePath(common, true); err != nil {
		return "", err
	}
	return filepath.Clean(common), nil
}

func registryPath(l Layout) string {
	return filepath.Join(l.ConfigRoot, ".homonto", "worktrees.json")
}

func readWorktreeRegistry(l Layout) (worktreeRegistry, error) {
	r := worktreeRegistry{Version: 1, Entries: []Worktree{}}
	path := registryPath(l)
	if err := realWorktreePath(path, false); err != nil {
		return r, err
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	defer f.Close()
	r = worktreeRegistry{}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("worktree: invalid registry: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF || r.Version != 1 || r.Entries == nil {
		return r, fmt.Errorf("worktree: invalid registry version, entries, or trailing data")
	}
	seen := map[string]bool{}
	paths := map[string]bool{}
	for _, w := range r.Entries {
		role := w.Role
		if role == "" {
			role = "execution" // Persisted bindings predate receiver roles.
		}
		if role != "execution" && role != "receiver" {
			return r, fmt.Errorf("worktree: ownership: unknown role %q", role)
		}
		key := w.Workflow + "/" + w.Change + "/" + w.Repo + "/" + role
		if err := checkWorktreeNames(w.Workflow, w.Change); err != nil {
			return r, err
		}
		if w.StateID == "" || w.Path == "" || seen[key] || paths[w.Path] {
			return r, fmt.Errorf("worktree: ownership: invalid or duplicate registry binding %s", key)
		}
		seen[key], paths[w.Path] = true, true
	}
	return r, nil
}

// EnsureNameAvailable is a read-only guard for new workflow records. Even stale
// or pending bindings reserve their workflow/name until explicitly cleaned up;
// replacing the active state first would strand the old generation's binding.
// Callers must hold their workflow lifecycle lock across this check and creation.
func EnsureNameAvailable(l Layout, workflow, change string) error {
	if err := checkWorktreeNames(workflow, change); err != nil {
		return err
	}
	r, err := readWorktreeRegistry(l)
	if err != nil {
		return err
	}
	for _, w := range r.Entries {
		if w.Workflow == workflow && w.Change == change {
			return fmt.Errorf("worktree: ownership: %s/%s still has a registered binding for repository %q at %s; remove or recover it before reusing the change name", workflow, change, w.Repo, w.Path)
		}
	}
	return nil
}

func saveWorktreeRegistry(l Layout, r worktreeRegistry) error {
	r.Entries = append([]Worktree{}, r.Entries...)
	sort.Slice(r.Entries, func(i, j int) bool {
		a, b := r.Entries[i], r.Entries[j]
		return a.Workflow+"/"+a.Change+"/"+a.Repo+"/"+a.Role < b.Workflow+"/"+b.Change+"/"+b.Repo+"/"+b.Role
	})
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := realWorktreePath(registryPath(l), false); err != nil {
		return err
	}
	return fsutil.WriteControlPlaneWithin(l.ConfigRoot, registryPath(l), append(b, '\n'), 0o600)
}

func lockWorktreeRegistry(l Layout) (func(), error) {
	path := filepath.Join(l.ConfigRoot, ".homonto", "worktrees.lock")
	if err := realWorktreePath(path, false); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := realWorktreePath(path, false); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("worktree: registry lock %s: %w; remove a stale lock only after confirming its owner is no longer running", path, err)
	}
	fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	f.Close()
	return func() { _ = os.Remove(path) }, nil
}

// LockLifecycle shares the active-name reservation used by both workflow CLIs.
// Hold it from the first state read through registry/install writes. The managed
// history wrapper, when present, is outermost; then workflow locks, this name
// lock, and the registry lock. Never acquire history from inside these locks.
// Callers already holding the name lock must not acquire it again.
func LockLifecycle(l Layout) (func(), error) {
	path := filepath.Join(filepath.Dir(worktreeStatesDir(l, "onto")), ".change-names.lock")
	return lockWorktreeLifecyclePath(path)
}

func lockWorktreeLifecyclePath(path string) (func(), error) {
	if err := realWorktreePath(path, false); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("worktree: lifecycle/name lock %s: %w; wait for its owner, or remove only after confirming it is no longer running", path, err)
	}
	_, writeErr := fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	closeErr := f.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}

func lockBindingLifecycle(l Layout, workflow string) (func(), error) {
	// Share the framework's mutation lock so archive/terminal writes cannot
	// interleave with the active-state read, even in existing-history mode.
	unlockState, err := lockWorktreeLifecyclePath(filepath.Join(worktreeStatesDir(l, workflow), "."+workflow+".lock"))
	if err != nil {
		return nil, err
	}
	unlockNames, err := LockLifecycle(l)
	if err != nil {
		unlockState()
		return nil, err
	}
	return func() { unlockNames(); unlockState() }, nil
}

// Decode only identity/scope/terminal fields here to avoid a cycle through
// ontostate -> workcli -> workspace. This reader never writes workflow state.
type worktreeState struct {
	Change    string   `yaml:"change"`
	ID        string   `yaml:"id"`
	Created   string   `yaml:"created"`
	Repos     []string `yaml:"repos"`
	RepoMode  string   `yaml:"repo_mode"`
	Phase     string   `yaml:"phase"`
	Archived  bool     `yaml:"archived"`
	Abandoned bool     `yaml:"abandoned"`
	RepoBases map[string]struct {
		BaseRef      string `yaml:"base_ref"`
		BaseBranch   string `yaml:"base_branch"`
		GitCommonDir string `yaml:"git_common_dir"`
	} `yaml:"repo_bases"`
	identity string
	dir      string
}

func (s worktreeState) checkBase(repo, commit, target string) error {
	base, ok := s.RepoBases[repo]
	// Older provenance-only records did not capture integration anchors.
	if !ok || (base.BaseRef == "" && base.BaseBranch == "") {
		return nil
	}
	if base.BaseRef != commit || target != "refs/heads/"+base.BaseBranch {
		return fmt.Errorf("worktree: base mismatch for repository %q: requested commit %s and target %q disagree with state repo_bases (base_ref %q, base_branch %q); select the intended base when creating the change", repo, commit, target, base.BaseRef, base.BaseBranch)
	}
	return nil
}

func (s worktreeState) terminal(workflow string) bool {
	if workflow == "to" {
		return s.Phase == "done" || s.Phase == "abandoned"
	}
	return s.Archived || s.Abandoned
}

func readWorktreeState(dir, workflow string) (worktreeState, error) {
	var s worktreeState
	path := filepath.Join(dir, workflow+"-state.yaml")
	if err := realWorktreePath(path, false); err != nil {
		return s, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) && workflow == "onto" {
		path = filepath.Join(dir, "state.yaml")
		if err := realWorktreePath(path, false); err != nil {
			return s, err
		}
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return s, err
	}
	if err := yaml.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("worktree: invalid state %s: %w", path, err)
	}
	switch workflow + "/" + s.Phase {
	case "onto/open", "onto/design", "onto/build", "onto/verify", "onto/close", "to/plan", "to/do", "to/done", "to/abandoned":
	default:
		return s, fmt.Errorf("worktree: invalid state phase %q in %s", s.Phase, path)
	}
	s.dir = dir
	if s.ID != "" {
		s.identity = "id:" + s.ID
	} else {
		// Legacy records have date-only Created values. The directory inode
		// survives state-file atomic writes and archive renames, unlike a hash
		// of mutable state or its pathname. Refuse unsupported filesystems.
		info, err := os.Stat(dir)
		if err != nil {
			return s, err
		}
		v := reflect.Indirect(reflect.ValueOf(info.Sys()))
		if !v.IsValid() || v.Kind() != reflect.Struct {
			return s, fmt.Errorf("worktree: ownership: state needs a stable id on this filesystem")
		}
		dev, ino := v.FieldByName("Dev"), v.FieldByName("Ino")
		if !dev.IsValid() || !ino.IsValid() {
			return s, fmt.Errorf("worktree: ownership: state needs a stable id on this filesystem")
		}
		s.identity = fmt.Sprintf("legacy:%v:%v:%s", dev.Interface(), ino.Interface(), s.Created)
	}
	return s, nil
}

func worktreeStatesDir(l Layout, workflow string) string {
	root := l.WorkflowRoot
	if !filepath.IsAbs(root) {
		root = filepath.Join(l.ConfigRoot, root)
	}
	if workflow == "to" {
		return filepath.Join(root, "tasks")
	}
	return filepath.Join(root, "changes")
}

func bindingState(l Layout, workflow, change, identity string, activeOnly bool) (worktreeState, error) {
	root := worktreeStatesDir(l, workflow)
	s, err := readWorktreeState(filepath.Join(root, change), workflow)
	if err == nil {
		if s.Change != change || (identity != "" && s.identity != identity) {
			return s, fmt.Errorf("worktree: ownership: state identity mismatch for %s/%s (change name was reused)", workflow, change)
		}
		if activeOnly && s.terminal(workflow) {
			return s, fmt.Errorf("worktree: matching active state required for %s/%s", workflow, change)
		}
		return s, nil
	}
	if !os.IsNotExist(err) || activeOnly {
		return s, fmt.Errorf("worktree: matching active state required for %s/%s: %w", workflow, change, err)
	}
	archive := filepath.Join(root, "archive")
	if err := realWorktreePath(archive, true); err != nil {
		return s, err
	}
	entries, err := os.ReadDir(archive)
	if err != nil {
		return s, fmt.Errorf("worktree: ownership: state for %s/%s is missing: %w", workflow, change, err)
	}
	var matches []worktreeState
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate, err := readWorktreeState(filepath.Join(archive, entry.Name()), workflow)
		if err != nil {
			return s, fmt.Errorf("worktree: cannot verify archive identity: %w", err)
		}
		if candidate.Change == change && (identity == "" || candidate.identity == identity) {
			if !candidate.terminal(workflow) {
				return s, fmt.Errorf("worktree: ownership: archived state for %s/%s is not terminal", workflow, change)
			}
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		if identity == "" && len(matches) > 1 {
			return s, fmt.Errorf("worktree: ownership: found %d archived states for %s/%s; receiver requires --state-id to disambiguate", len(matches), workflow, change)
		}
		return s, fmt.Errorf("worktree: ownership: expected one state with identity %q for %s/%s, found %d", identity, workflow, change, len(matches))
	}
	return matches[0], nil
}

func validateWorktree(l Layout, w Worktree) error {
	parent, err := worktreeParent(l)
	if err != nil {
		return err
	}
	authority, err := worktreeAuthority(l, w.Repo)
	if err != nil {
		return err
	}
	repoDir := w.Repo
	if repoDir == "" {
		repoDir = "_config"
	}
	expected := filepath.Join(parent, repoDir, w.Workflow+"-"+w.Change)
	if w.Role == "receiver" {
		expected = filepath.Join(parent, repoDir, ".receivers", w.Workflow+"-"+w.Change)
		if w.BaseTarget != "refs/heads/"+w.Branch {
			return fmt.Errorf("worktree: receiver branch does not match its recorded target")
		}
	}
	if w.Parent != parent || w.Path != expected {
		return fmt.Errorf("worktree: unsafe path: registered %s is not owned under configured parent %s (parent changes do not retarget bindings)", w.Path, parent)
	}
	if w.WorkflowRoot != filepath.Dir(worktreeStatesDir(l, w.Workflow)) {
		return fmt.Errorf("worktree: ownership: workflow root changed for %s/%s", w.Workflow, w.Change)
	}
	if w.Status != "ready" {
		return fmt.Errorf("worktree: incomplete registry operation %q at %s; inspect and recover manually, no automatic adoption or deletion", w.Status, w.Path)
	}
	if w.BaseCommit == "" || w.BaseTarget == "" || w.BaseRef == "" {
		return fmt.Errorf("worktree: ownership: registry is missing its integration base")
	}
	common, err := worktreeCommon(authority)
	if err != nil {
		return err
	}
	actual, err := worktreeCommon(w.Path)
	if err != nil {
		return fmt.Errorf("worktree: ownership: registered path unavailable: %w", err)
	}
	if common != w.CommonDir || actual != common {
		return fmt.Errorf("worktree: ownership: Git common-dir mismatch for repository %q", w.Repo)
	}
	gitDir, err := worktreeGit(w.Path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	if gitDir != w.GitDir || filepath.Dir(gitDir) != filepath.Join(common, "worktrees") {
		return fmt.Errorf("worktree: ownership: registered Git worktree identity changed at %s", w.Path)
	}
	ownerPath := filepath.Join(gitDir, "homonto-owner")
	if err := realWorktreePath(ownerPath, false); err != nil {
		return err
	}
	owner, err := os.ReadFile(ownerPath)
	if err != nil || w.Ownership == "" || string(owner) != w.Ownership {
		return fmt.Errorf("worktree: ownership: creation token mismatch at %s; refusing to adopt a replacement worktree", w.Path)
	}
	// Verify the administrative backlink as well as the worktree's .git file.
	backlink := filepath.Join(gitDir, "gitdir")
	if err := realWorktreePath(backlink, false); err != nil {
		return err
	}
	b, err := os.ReadFile(backlink)
	if err != nil || strings.TrimSpace(string(b)) != filepath.Join(w.Path, ".git") {
		return fmt.Errorf("worktree: ownership: Git administrative backlink mismatch at %s", w.Path)
	}
	branch, err := worktreeGit(w.Path, "symbolic-ref", "--quiet", "HEAD")
	if err != nil || branch != "refs/heads/"+w.Branch {
		return fmt.Errorf("worktree: ownership: expected attached branch %q at %s", w.Branch, w.Path)
	}
	s, err := bindingState(l, w.Workflow, w.Change, w.StateID, false)
	if err != nil {
		return err
	}
	if err := s.checkAuthority(l, w.Repo, common); err != nil {
		return err
	}
	return s.checkBase(w.Repo, w.BaseCommit, w.BaseTarget)
}

func (s worktreeState) checkAuthority(l Layout, repo, common string) error {
	if s.RepoMode != "" && (s.RepoMode == "explicit") != l.ExplicitRepos() {
		return fmt.Errorf("worktree: source scope mode changed; restore the recorded configuration")
	}
	aliases, err := worktreeAliases(l, s.Repos)
	if err != nil {
		return err
	}
	selected := false
	for _, alias := range aliases {
		selected = selected || alias == repo
	}
	if !selected {
		return fmt.Errorf("worktree: repository %q is no longer selected by the bound state", repo)
	}
	if recorded := s.RepoBases[repo].GitCommonDir; recorded != "" && filepath.Clean(recorded) != common {
		return fmt.Errorf("worktree: ownership: recorded source common-dir mismatch for repository %q", repo)
	}
	return nil
}

// LoadScopeRoot loads homonto.toml without resolving unused declarations for a
// legacy empty scope. Other scopes use LoadRoot's full validation. A genuinely
// absent config retains the legacy default; malformed/future configs never do.
// This reads layout only: callers resolving execution paths must use SourceDirs
// so that registered bindings are still checked.
func LoadScopeRoot(root string, aliases []string) (l Layout, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return l, err
	}
	l = Layout{ConfigPath: filepath.Join(root, "homonto.toml"), ConfigRoot: root, WorkflowRoot: filepath.Join(root, "docs"), GitMode: "existing"}
	defer func() {
		if err != nil {
			err = fmt.Errorf("config %q: %w", l.ConfigPath, err)
		}
	}()
	data, err := os.ReadFile(l.ConfigPath)
	if err != nil {
		if _, statErr := os.Lstat(l.ConfigPath); !os.IsNotExist(statErr) || !errors.Is(err, os.ErrNotExist) {
			return l, err
		}
		return l, workflowroot.ValidateLayout(l.ConfigPath, "docs", l.GitMode, 0)
	}
	var c struct {
		SchemaVersion int `toml:"schema_version"`
		Workflow      struct {
			Root string  `toml:"root"`
			Git  *string `toml:"git"`
		} `toml:"workflow"`
		Worktrees *struct {
			Dir string `toml:"dir"`
		} `toml:"worktrees"`
		Repos map[string]string `toml:"repos"`
	}
	if err := toml.Unmarshal(data, &c); err != nil {
		return l, fmt.Errorf("parse config: %w", err)
	}
	if c.SchemaVersion < 0 || c.SchemaVersion >= 2 || len(aliases) != 0 || c.Workflow.Git != nil || c.Worktrees != nil {
		return LoadRoot(root)
	}
	l.SchemaVersion = c.SchemaVersion
	workflow := strings.TrimSpace(c.Workflow.Root)
	if workflow == "" {
		workflow = "docs"
	}
	workflow = filepath.Clean(workflow)
	if filepath.IsAbs(workflow) || workflow == "." || workflow == ".." || strings.HasPrefix(workflow, ".."+string(filepath.Separator)) || strings.Contains(workflow, `\`) || strings.IndexFunc(workflow, unicode.IsControl) >= 0 {
		return l, fmt.Errorf("workflow.root %q must be a safe relative path below the configuration repository", c.Workflow.Root)
	}
	l.WorkflowRoot = filepath.Join(root, workflow)
	resolved, err := canonical(l.WorkflowRoot)
	if err != nil {
		return l, err
	}
	base, err := canonical(root)
	if err != nil {
		return l, err
	}
	if !within(base, resolved) {
		return l, fmt.Errorf("workflow.root resolves outside the configuration repository through symlink")
	}
	return l, workflowroot.ValidateLayout(l.ConfigPath, workflow, l.GitMode, l.SchemaVersion)
}

// SourceDirs resolves only a change's selected authorities to execution paths.
// A missing config is the legacy root-only case; malformed config never falls back.
func SourceDirs(configRoot, workflow, change string, aliases []string) (map[string]string, error) {
	if err := checkWorktreeNames(workflow, change); err != nil {
		return nil, err
	}
	l, err := LoadScopeRoot(configRoot, aliases)
	if err != nil {
		return nil, err
	}
	selected, err := worktreeAliases(l, aliases)
	if err != nil {
		return nil, err
	}
	r, err := readWorktreeRegistry(l)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(selected))
	for _, alias := range selected {
		path, err := worktreeAuthority(l, alias)
		if err != nil {
			return nil, err
		}
		for _, w := range r.Entries {
			if w.Workflow == workflow && w.Change == change && w.Repo == alias && w.Role != "receiver" {
				if err := validateWorktree(l, w); err != nil {
					return nil, err
				}
				path = w.Path
			}
		}
		out[alias] = path
	}
	return out, nil
}

// CreateWorktree registers and creates an isolated checkout without touching the
// declared checkout's HEAD, index, or working files. Repeating a ready binding is
// idempotent; an interrupted operation remains visible and fails closed.
func CreateWorktree(l Layout, workflow, change, repo, base, branch string) (Worktree, error) {
	return createWorktree(l, workflow, change, repo, base, branch, "execution", "")
}

// ReceiverWorktree allocates a clean checkout attached to the recorded local
// integration target. It never switches, cleans, or adopts another checkout.
// A target checked out elsewhere is a blocker, not permission to force it free.
// Terminal/archived recovery uses the registered generation, or one unambiguous
// archived state when no binding remains. Execution creation stays active-only.
// An optional state ID selects an ambiguous archive but cannot override bindings.
func ReceiverWorktree(l Layout, workflow, change, repo string, stateID ...string) (Worktree, error) {
	if len(stateID) > 1 {
		return Worktree{}, fmt.Errorf("worktree: receiver accepts at most one state ID")
	}
	identity := ""
	if len(stateID) == 1 {
		identity = stateID[0]
	}
	if identity != "" && !strings.HasPrefix(identity, "id:") && !strings.HasPrefix(identity, "legacy:") {
		identity = "id:" + identity
	}
	return createWorktree(l, workflow, change, repo, "", "", "receiver", identity)
}

func createWorktree(l Layout, workflow, change, repo, base, branch, role, identity string) (Worktree, error) {
	var w Worktree
	if err := checkWorktreeNames(workflow, change); err != nil {
		return w, err
	}
	authority, err := worktreeAuthority(l, repo)
	if err != nil {
		return w, err
	}
	parent, err := worktreeParent(l)
	if err != nil {
		return w, err
	}
	releaseLifecycle, err := lockBindingLifecycle(l, workflow)
	if err != nil {
		return w, err
	}
	defer releaseLifecycle()
	r, err := readWorktreeRegistry(l)
	if err != nil {
		return w, err
	}
	for _, existing := range r.Entries {
		if existing.Workflow == workflow && existing.Change == change {
			if identity != "" && identity != existing.StateID {
				return w, fmt.Errorf("worktree: ownership: bindings for %s/%s disagree on state identity", workflow, change)
			}
			identity = existing.StateID
		}
	}
	s, err := bindingState(l, workflow, change, identity, role != "receiver")
	if err != nil {
		return w, err
	}
	aliases, err := worktreeAliases(l, s.Repos)
	if err != nil {
		return w, err
	}
	selected := false
	for _, alias := range aliases {
		selected = selected || alias == repo
	}
	if !selected {
		return w, fmt.Errorf("worktree: repository %q is not selected by state %s/%s", repo, workflow, change)
	}
	if role != "receiver" && (base == "" || strings.HasPrefix(base, "-") || branch == "" || strings.HasPrefix(branch, "-")) {
		return w, fmt.Errorf("worktree: explicit --base REF and --branch NAME are required")
	}
	if role != "receiver" {
		if _, err := worktreeGit(authority, "check-ref-format", "refs/heads/"+branch); err != nil {
			return w, err
		}
	}
	common, err := worktreeCommon(authority)
	if err != nil {
		return w, err
	}
	if err := s.checkAuthority(l, repo, common); err != nil {
		return w, err
	}
	var boundTarget, boundCommit string
	for _, existing := range r.Entries {
		if existing.Workflow != workflow || existing.Change != change || existing.Repo != repo {
			continue
		}
		if err := validateWorktree(l, existing); err != nil {
			return w, err
		}
		if boundTarget != "" && (boundTarget != existing.BaseTarget || boundCommit != existing.BaseCommit) {
			return w, fmt.Errorf("worktree: ownership: execution and receiver bindings disagree on integration base")
		}
		boundTarget, boundCommit = existing.BaseTarget, existing.BaseCommit
	}
	for _, existing := range r.Entries {
		if existing.Workflow == workflow && existing.Change == change && existing.Repo == repo && (existing.Role == "receiver") == (role == "receiver") {
			if role == "receiver" {
				if err := cleanWorktree(existing.Path); err != nil {
					return w, err
				}
				return existing, nil
			}
			if existing.BaseRef != base || existing.Branch != branch {
				return w, fmt.Errorf("worktree: ownership: binding already exists with a different base or branch")
			}
			return existing, nil
		}
	}
	if role == "receiver" {
		anchor := s.RepoBases[repo]
		base, branch = anchor.BaseRef, anchor.BaseBranch
		if base == "" || branch == "" {
			for _, existing := range r.Entries {
				if existing.Workflow == workflow && existing.Change == change && existing.Repo == repo && existing.Role != "receiver" {
					if strings.HasPrefix(existing.BaseTarget, "refs/heads/") {
						base, branch = existing.BaseCommit, strings.TrimPrefix(existing.BaseTarget, "refs/heads/")
					}
				}
			}
		}
		if base == "" || branch == "" {
			return w, fmt.Errorf("worktree: receiver requires a recorded local base target and commit; no default branch is guessed")
		}
		if _, err := worktreeGit(authority, "check-ref-format", "refs/heads/"+branch); err != nil {
			return w, err
		}
	}
	commit, err := worktreeGit(authority, "rev-parse", "--verify", "--end-of-options", base+"^{commit}")
	if err != nil {
		return w, err
	}
	// Resolve HEAD in the authority, never later in the execution checkout.
	// Revision expressions/immutable objects integrate only into that commit.
	target, targetErr := worktreeGit(authority, "rev-parse", "--symbolic-full-name", "--verify", "--end-of-options", base)
	if targetErr != nil || !strings.HasPrefix(target, "refs/") || strings.ContainsAny(target, "\r\n") {
		target = commit
	}
	if role == "receiver" {
		target = "refs/heads/" + branch
		if _, err := worktreeGit(authority, "merge-base", "--is-ancestor", commit, target); err != nil {
			return w, fmt.Errorf("worktree: receiver target no longer contains the recorded base: %w", err)
		}
		listed, err := worktreeGit(authority, "worktree", "list", "--porcelain", "-z")
		if err != nil {
			return w, err
		}
		path := ""
		for _, field := range strings.Split(listed, "\x00") {
			if p, ok := strings.CutPrefix(field, "worktree "); ok {
				path = p
			}
			if field == "branch "+target {
				return w, fmt.Errorf("worktree: receiver target %q is already checked out at %s; refusing to adopt or modify that checkout, release it explicitly before allocation", target, path)
			}
		}
	}
	if err := s.checkBase(repo, commit, target); err != nil {
		return w, err
	}
	if boundTarget != "" && (boundTarget != target || boundCommit != commit) {
		return w, fmt.Errorf("worktree: ownership: requested integration base disagrees with the existing binding")
	}
	unlock, err := lockWorktreeRegistry(l)
	if err != nil {
		return w, err
	}
	defer unlock()
	r, err = readWorktreeRegistry(l)
	if err != nil {
		return w, err
	}
	for _, existing := range r.Entries {
		if existing.Workflow == workflow && existing.Change == change && existing.Repo == repo && (existing.Role == "receiver") == (role == "receiver") {
			return w, fmt.Errorf("worktree: registry changed during creation preflight; retry to validate the existing binding")
		}
	}
	if role != "receiver" {
		if _, err := worktreeGit(authority, "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
			return w, fmt.Errorf("worktree: ownership: branch %q already exists; refusing overwrite", branch)
		} else {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 {
				return w, err
			}
		}
	}
	repoDir := repo
	if repoDir == "" {
		repoDir = "_config"
	}
	dest := filepath.Join(parent, repoDir, workflow+"-"+change)
	if role == "receiver" {
		dest = filepath.Join(parent, repoDir, ".receivers", workflow+"-"+change)
	}
	if err := realWorktreePath(dest, true); err != nil {
		return w, err
	}
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return w, fmt.Errorf("worktree: ownership: destination %s already exists or cannot be inspected; refusing overwrite", dest)
	}
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return w, err
	}
	w = Worktree{Role: role, Workflow: workflow, Change: change, Repo: repo, StateID: s.identity,
		WorkflowRoot: filepath.Dir(worktreeStatesDir(l, workflow)), Parent: parent, Path: dest,
		CommonDir: common, Ownership: hex.EncodeToString(token[:]), Branch: branch, BaseRef: base, BaseTarget: target, BaseCommit: commit, Status: "creating"}
	r.Entries = append(r.Entries, w)
	// Journal intent before Git changes anything. Failures retain this record and
	// never force-remove a directory/branch whose contents might now be user work.
	if err := saveWorktreeRegistry(l, r); err != nil {
		return w, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return w, err
	}
	if err := realWorktreePath(dest, true); err != nil {
		return w, err
	}
	if err := os.Mkdir(dest, 0o755); err != nil {
		return w, fmt.Errorf("worktree: ownership: reserving destination: %w", err)
	}
	args := []string{"-c", "core.hooksPath=" + os.DevNull, "worktree", "add"}
	if role == "receiver" {
		args = append(args, "--", dest, branch)
	} else {
		args = append(args, "-b", branch, "--", dest, commit)
	}
	if _, err := worktreeGit(authority, args...); err != nil {
		return w, fmt.Errorf("%w; pending registry retained at %s, inspect before recovery", err, registryPath(l))
	}
	w.GitDir, err = worktreeGit(dest, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return w, err
	}
	ownerPath := filepath.Join(w.GitDir, "homonto-owner")
	if filepath.Dir(w.GitDir) != filepath.Join(common, "worktrees") {
		return w, fmt.Errorf("worktree: ownership: unexpected Git administrative directory %s", w.GitDir)
	}
	if err := realWorktreePath(ownerPath, false); err != nil {
		return w, err
	}
	f, err := os.OpenFile(ownerPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return w, fmt.Errorf("worktree: ownership: creating token: %w", err)
	}
	_, writeErr := f.WriteString(w.Ownership)
	syncErr := f.Sync()
	closeErr := f.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return w, err
	}
	w.Status = "ready"
	if err := validateWorktree(l, w); err != nil {
		return w, err
	}
	if role == "receiver" {
		if err := cleanWorktree(w.Path); err != nil {
			return w, err
		}
	}
	for i := range r.Entries {
		if r.Entries[i].Path == dest {
			r.Entries[i] = w
		}
	}
	if err := saveWorktreeRegistry(l, r); err != nil {
		return w, fmt.Errorf("worktree created at %s but registry completion failed: %w; pending record retained, do not delete unknown work", dest, err)
	}
	return w, nil
}

// ListWorktrees validates every binding, rather than presenting stale paths as
// usable execution directories. Entries are ordered by workflow/change/repo.
func ListWorktrees(l Layout) ([]Worktree, error) {
	r, err := readWorktreeRegistry(l)
	if err != nil {
		return nil, err
	}
	for _, w := range r.Entries {
		if err := validateWorktree(l, w); err != nil {
			return nil, err
		}
	}
	sort.Slice(r.Entries, func(i, j int) bool {
		a, b := r.Entries[i], r.Entries[j]
		return a.Workflow+"/"+a.Change+"/"+a.Repo+"/"+a.Role < b.Workflow+"/"+b.Change+"/"+b.Repo+"/"+b.Role
	})
	return r.Entries, nil
}

// RemoveWorktree removes only a clean, owned, terminal binding whose current
// source HEAD is an ancestor of its recorded integration target. Branches are
// deliberately retained. --yes acknowledges deletion, not a force override.
func RemoveWorktree(l Layout, workflow, change, repo string, yes bool) (Worktree, error) {
	return RemoveWorktreeRole(l, workflow, change, repo, "execution", yes)
}

// RemoveWorktreeRole removes only the selected owned role. Receiver removal
// obeys the same terminal, clean, and integrated checks as execution removal.
func RemoveWorktreeRole(l Layout, workflow, change, repo, role string, yes bool) (Worktree, error) {
	var w Worktree
	if role != "execution" && role != "receiver" {
		return w, fmt.Errorf("worktree: role must be execution or receiver")
	}
	if !yes {
		return w, fmt.Errorf("worktree: removal requires --yes (does not override safety checks)")
	}
	if err := checkWorktreeNames(workflow, change); err != nil {
		return w, err
	}
	if _, err := worktreeAuthority(l, repo); err != nil {
		return w, err
	}
	releaseLifecycle, err := lockBindingLifecycle(l, workflow)
	if err != nil {
		return w, err
	}
	defer releaseLifecycle()
	unlock, err := lockWorktreeRegistry(l)
	if err != nil {
		return w, err
	}
	defer unlock()
	r, err := readWorktreeRegistry(l)
	if err != nil {
		return w, err
	}
	index := -1
	for i, entry := range r.Entries {
		if entry.Workflow == workflow && entry.Change == change && entry.Repo == repo && (entry.Role == "receiver") == (role == "receiver") {
			index, w = i, entry
			break
		}
	}
	if index < 0 {
		return w, fmt.Errorf("worktree: ownership: no registered binding for %s/%s repository %q", workflow, change, repo)
	}
	if err := validateWorktree(l, w); err != nil {
		return w, err
	}
	s, err := bindingState(l, workflow, change, w.StateID, false)
	if err != nil {
		return w, err
	}
	if !s.terminal(workflow) {
		return w, fmt.Errorf("worktree: active binding cannot be removed; complete or abandon %s/%s first", workflow, change)
	}
	if err := cleanWorktree(w.Path); err != nil {
		return w, err
	}
	head, err := worktreeGit(w.Path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return w, err
	}
	base, err := worktreeGit(w.Path, "rev-parse", "--verify", "--end-of-options", w.BaseTarget+"^{commit}")
	if err != nil {
		return w, err
	}
	if _, err := worktreeGit(w.Path, "merge-base", "--is-ancestor", head, base); err != nil {
		return w, fmt.Errorf("worktree: unmerged source commit %s is not integrated into configured base %q; refusing removal: %w", head, w.BaseRef, err)
	}
	r.Entries[index].Status = "removing"
	if err := saveWorktreeRegistry(l, r); err != nil {
		return w, err
	}
	authority, err := worktreeAuthority(l, repo)
	if err != nil {
		return w, err
	}
	if _, err := worktreeGit(authority, "worktree", "remove", "--", w.Path); err != nil {
		// Git's non-force removal is the final dirty/locked-worktree guard.
		r.Entries[index].Status = "ready"
		if saveErr := saveWorktreeRegistry(l, r); saveErr != nil {
			return w, fmt.Errorf("%w; restoring registry failed: %v", err, saveErr)
		}
		return w, err
	}
	r.Entries = append(r.Entries[:index], r.Entries[index+1:]...)
	if err := saveWorktreeRegistry(l, r); err != nil {
		return w, fmt.Errorf("worktree removed at %s but registry cleanup failed: %w; inspect pending record before recovery", w.Path, err)
	}
	return w, nil
}

func cleanWorktree(path string) error {
	// Status trusts index flags that hide modified files. Refuse those flags
	// rather than clearing them or materializing sparse files in a user's tree.
	files, err := worktreeGit(path, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(files, "\x00") {
		if entry == "" {
			continue
		}
		if len(entry) < 3 || entry[1] != ' ' {
			return fmt.Errorf("worktree: cannot verify index flags at %s", path)
		}
		if entry[0] == 'S' || (entry[0] >= 'a' && entry[0] <= 'z') {
			return fmt.Errorf("worktree: nondefault index flags (assume-unchanged or skip-worktree) for %q at %s", entry[2:], path)
		}
	}
	dirt, err := worktreeGit(path, "status", "--porcelain=v1", "--untracked-files=all", "--ignored", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	if dirt != "" {
		return fmt.Errorf("worktree: dirty registered path %s (including untracked/ignored files)", path)
	}
	gitDir, err := worktreeGit(path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	for _, state := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer", "index.lock", "locked"} {
		if _, err := os.Lstat(filepath.Join(gitDir, state)); err == nil {
			return fmt.Errorf("worktree: Git operation or locked worktree at %s: %s", path, state)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
