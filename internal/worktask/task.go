// Package worktask provides engine-independent checkout bookkeeping, not a scheduler.
package worktask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/workspace"
)

type Checkout struct {
	Source     string `json:"source"`
	Path       string `json:"path"`
	CommonDir  string `json:"common_dir"`
	GitDir     string `json:"git_dir"`
	Base       string `json:"base"`
	BaseBranch string `json:"base_branch"`
	Branch     string `json:"branch"`
	Isolation  string `json:"isolation"`
	Changed    bool   `json:"changed"`
	Owned      bool   `json:"owned"`
	Ready      bool   `json:"ready"`
	Removing   bool   `json:"removing,omitempty"`
	Removed    bool   `json:"removed"`
}

type Task struct {
	NativeID      string              `json:"native_id,omitempty"`
	Version       int                 `json:"version"`
	Name          string              `json:"name"`
	Engine        string              `json:"engine"`
	Change        string              `json:"change,omitempty"`
	Config        string              `json:"config"`
	Changed       []string            `json:"changed"`
	Checkouts     map[string]Checkout `json:"checkouts"`
	Results       map[string]Result   `json:"results"`
	NeedsFallback bool                `json:"needs_fallback"`
}

type Store struct {
	Config *config.Config
	Layout workspace.Layout
}

func Open(path string) (*Store, error) {
	c, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	l, err := workspace.Load(path)
	if err != nil {
		return nil, err
	}
	if !l.ExplicitRepos() {
		return nil, fmt.Errorf("workspace tasks require schema_version = 2 with explicit [repos]")
	}
	return &Store{Config: c, Layout: l}, nil
}

var safeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func (s *Store) dir(name string) (string, error) {
	if !safeName.MatchString(name) {
		return "", fmt.Errorf("invalid workspace task name %q", name)
	}
	dir := filepath.Join(s.Layout.ConfigRoot, ".homonto", "workspace-tasks", name)
	if err := fsutil.RequireRealParents(s.Layout.ConfigRoot, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// lockWait bounds how long one command waits for another to finish its
// task.json update; allocation can hold the lock across git worktree add.
const lockWait = time.Minute

// lock serializes every task.json read-modify-write for one task.
func (s *Store) lock(name string) (func(), error) {
	dir, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		lock, err := applylock.AcquirePath(filepath.Join(dir, "task.lock"), filepath.Join(s.Layout.ConfigRoot, ".homonto"))
		if err == nil {
			return func() { _ = lock.Release() }, nil
		}
		if !errors.Is(err, applylock.ErrHeld) || time.Now().After(deadline) {
			return nil, fmt.Errorf("workspace task %s is busy: %w", name, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// save writes the whole task; callers hold its lock.
func (s *Store) save(t *Task) error {
	dir, err := s.dir(t.Name)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteControlPlane(filepath.Join(dir, "task.json"), append(data, '\n'), 0o600)
}

// update applies one mutation to the recorded task under its lock, so
// concurrent commands keep each other's results and removal markers.
func (s *Store) update(name string, mutate func(*Task)) error {
	unlock, err := s.lock(name)
	if err != nil {
		return err
	}
	defer unlock()
	dir, err := s.dir(name)
	if err != nil {
		return err
	}
	var t Task
	if err := readJSON(filepath.Join(dir, "task.json"), &t); err != nil {
		return err
	}
	if t.Results == nil {
		t.Results = map[string]Result{}
	}
	mutate(&t)
	return s.save(&t)
}

func readJSON(path string, value any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("expected regular metadata file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

// Load validates recorded checkouts. Incomplete allocations load so create can
// resume them and cleanup can abandon them; checks refuse them. A lifecycle
// checkout whose native binding was released loads as removed.
func (s *Store) Load(ctx context.Context, name string) (*Task, error) {
	dir, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	var t Task
	if err := readJSON(filepath.Join(dir, "task.json"), &t); err != nil {
		return nil, err
	}
	if t.Version != 1 || t.Name != name || t.Config != s.Layout.ConfigPath || !s.installed(t.Engine) {
		return nil, fmt.Errorf("workspace task identity/config/selected engine no longer matches")
	}
	if len(t.Changed) == 0 || len(t.Checkouts) == 0 {
		return nil, fmt.Errorf("workspace task has no repositories")
	}
	for i, alias := range t.Changed {
		c, ok := t.Checkouts[alias]
		if !ok || !c.Changed || (i > 0 && t.Changed[i-1] >= alias) {
			return nil, fmt.Errorf("invalid changed repository selection")
		}
	}
	if t.Engine == "no" {
		if t.Change != "" || t.NativeID != "" {
			return nil, fmt.Errorf("no cannot own lifecycle state")
		}
	} else {
		if t.NativeID == "" {
			return nil, fmt.Errorf("missing lifecycle generation")
		}
		id, _, err := workspace.SourceAnchors(s.Layout, t.Engine, t.Change, t.NativeID)
		if err != nil {
			return nil, err
		}
		if id != t.NativeID {
			return nil, fmt.Errorf("lifecycle generation changed")
		}
	}
	if t.Results == nil {
		t.Results = map[string]Result{}
	}
	for alias, checkout := range t.Checkouts {
		if checkout.Changed != slices.Contains(t.Changed, alias) {
			return nil, fmt.Errorf("changed flag mismatch for %s", alias)
		}
		if _, err := s.validateCheckout(ctx, &t, alias, checkout); errors.Is(err, errReleased) {
			checkout.Removed = true
			t.Checkouts[alias] = checkout
		} else if err != nil {
			return nil, err
		}
	}
	return &t, nil
}

func (s *Store) installed(engine string) bool {
	if engine != "no" && engine != "onto" && engine != "to" {
		return false
	}
	for _, r := range s.Config.Engines {
		if r.Source == "builtin:"+engine {
			return true
		}
	}
	return false
}

// Create records an explicit isolation decision. Overrides permit per-repository choices.
// Repeating creation resumes only the identical task, including an interrupted
// allocation; it cannot switch engines or bases.
func (s *Store) Create(ctx context.Context, name, engine, change, isolation string, changed []string, overrides map[string]string) (*Task, error) {
	dir, err := s.dir(name)
	if err != nil {
		return nil, err
	}
	if !s.installed(engine) {
		return nil, fmt.Errorf("select one installed engine: no, onto, or to")
	}
	if isolation != "existing" && isolation != "worktree" {
		return nil, fmt.Errorf("choose isolation explicitly: existing or worktree")
	}
	changed = slices.Clone(changed)
	slices.Sort(changed)
	if len(changed) == 0 {
		return nil, fmt.Errorf("select at least one changed repository")
	}
	for i, alias := range changed {
		if s.Layout.Repos[alias] == "" || (i > 0 && changed[i-1] == alias) {
			return nil, fmt.Errorf("unknown or duplicate changed repository %q", alias)
		}
	}
	checks := s.Config.IntegrationChecksFor(changed)
	inputs := slices.Clone(changed)
	for _, name := range checks {
		inputs = append(inputs, s.Config.Integration[name].Repos...)
	}
	slices.Sort(inputs)
	inputs = slices.Compact(inputs)
	for alias, mode := range overrides {
		if !slices.Contains(inputs, alias) || (mode != "existing" && mode != "worktree") {
			return nil, fmt.Errorf("invalid isolation override %q=%q", alias, mode)
		}
	}
	unlock, err := s.lock(name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err := os.Lstat(filepath.Join(dir, "task.json")); err == nil {
		t, err := s.Load(ctx, name)
		if err != nil {
			return nil, err
		}
		if t.Engine != engine || t.Change != change || !slices.Equal(t.Changed, changed) || len(t.Checkouts) != len(inputs) {
			return nil, fmt.Errorf("task already exists with another engine, change or repository selection; resume its recorded choice")
		}
		for _, alias := range inputs {
			mode := isolation
			if overrides[alias] != "" {
				mode = overrides[alias]
			}
			if t.Checkouts[alias].Isolation != mode || t.Checkouts[alias].Removed || t.Checkouts[alias].Removing {
				return nil, fmt.Errorf("task isolation differs or checkout was removed; resume its recorded choice")
			}
		}
		if err := s.allocate(ctx, t, inputs); err != nil {
			return t, err
		}
		s.RefreshResults(ctx, t)
		return t, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	var native map[string]string
	var nativeID string
	var anchors map[string]workspace.SourceAnchor
	if engine != "no" {
		if change == "" {
			return nil, fmt.Errorf("%s requires its existing active --change", engine)
		}
		native, err = workspace.SourceDirsForActiveState(s.Layout, engine, change)
		if err != nil {
			return nil, err
		}
		for _, alias := range changed {
			if native[alias] == "" {
				return nil, fmt.Errorf("%s is not selected in %s change %s", alias, engine, change)
			}
		}
		nativeID, anchors, err = workspace.SourceAnchors(s.Layout, engine, change, "")
		if err != nil {
			return nil, err
		}
	} else if change != "" {
		return nil, fmt.Errorf("no has no lifecycle change")
	}
	t := &Task{Version: 1, Name: name, Engine: engine, Change: change, NativeID: nativeID, Config: s.Layout.ConfigPath, Changed: changed, Checkouts: map[string]Checkout{}, Results: map[string]Result{}, NeedsFallback: len(checks) == 0}
	// Preflight every input before allocation; dirty originals may be left untouched
	// only when the user explicitly chose a separate worktree.
	for _, alias := range inputs {
		source := s.Layout.Repos[alias]
		path := source
		mode := isolation
		if overrides[alias] != "" {
			mode = overrides[alias]
		}
		write := slices.Contains(changed, alias)
		if write && native != nil {
			path = native[alias]
		}
		base, common, gitdir, branch, err := identity(ctx, path)
		if err != nil {
			return nil, err
		}
		input := !write && overrides[alias] != "worktree"
		if input || mode == "existing" || path != source {
			if err := clean(ctx, path); err != nil && input {
				return nil, fmt.Errorf("%s: ask for dirty verification-input disposition; explicit %s=worktree override pins committed input: %w", alias, alias, err)
			} else if err != nil {
				return nil, fmt.Errorf("%s: dirty checkout needs user disposition: %w", alias, err)
			}
		}
		if mode == "worktree" && path == source && s.Layout.WorktreesDir == "" {
			return nil, fmt.Errorf("configure worktrees.dir before choosing worktree isolation")
		}
		if write && branch == "" {
			return nil, fmt.Errorf("%s: writable source must have a local branch", alias)
		}
		baseBranch := branch
		if write && native != nil {
			anchor := anchors[alias]
			if anchor.Commit == "" || anchor.Branch == "" {
				return nil, fmt.Errorf("%s has no native source anchor", alias)
			}
			base, baseBranch = anchor.Commit, anchor.Branch
		}
		if mode == "worktree" && (engine == "no" || !write) {
			destination := filepath.Join(s.Layout.WorktreesDir, alias, "workspace-"+name)
			if err := fsutil.RequireRealParents(filepath.Dir(s.Layout.WorktreesDir), destination); err != nil {
				return nil, err
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				return nil, fmt.Errorf("worktree destination exists or cannot be inspected: %s", destination)
			}
		}
		t.Checkouts[alias] = Checkout{Source: source, Path: path, Base: base, BaseBranch: baseBranch, Branch: branch, CommonDir: common, GitDir: gitdir, Isolation: mode, Changed: write, Ready: mode == "existing"}
	}
	if err := s.save(t); err != nil {
		return nil, err
	}
	return t, s.allocate(ctx, t, inputs)
}

// allocate completes every pending checkout. It is safe to repeat after an
// interruption: native bindings are idempotent, and a task-owned destination
// left by Git is adopted only when it is exactly this task's worktree.
func (s *Store) allocate(ctx context.Context, t *Task, inputs []string) error {
	for _, alias := range inputs {
		c := t.Checkouts[alias]
		if c.Ready {
			continue
		}
		if c.Changed && t.Engine != "no" {
			if c.Path == c.Source {
				// The recorded base, not the branch's current tip: the branch may
				// have moved on since the change began.
				w, err := workspace.AnchoredWorktree(s.Layout, t.Engine, t.Change, alias, taskBranch(t.Name, alias))
				if err != nil {
					return err
				}
				c.Path = w.Path
			}
		} else {
			c.Path = filepath.Join(s.Layout.WorktreesDir, alias, "workspace-"+t.Name)
			if err := fsutil.RequireRealParents(filepath.Dir(s.Layout.WorktreesDir), c.Path); err != nil {
				return err
			}
			if !c.Owned {
				if _, err := os.Lstat(c.Path); !os.IsNotExist(err) {
					return fmt.Errorf("worktree destination already exists or cannot be inspected: %s", c.Path)
				}
				// Persist allocation intent before Git runs, so an interrupted command
				// leaves the exact destination for resume or cleanup to inspect.
				c.Owned = true
				t.Checkouts[alias] = c
				if err := s.save(t); err != nil {
					return err
				}
			}
			if _, err := os.Lstat(c.Path); os.IsNotExist(err) {
				args := []string{"worktree", "add", "--detach", c.Path, c.Base}
				if c.Changed {
					args = []string{"worktree", "add", "-b", taskBranch(t.Name, alias), c.Path, c.Base}
				}
				if _, err := git(ctx, c.Source, args...); err != nil {
					return fmt.Errorf("%w; resolve the cause and rerun create to resume, or run cleanup to abandon %s", err, alias)
				}
			} else if err != nil {
				return err
			}
		}
		c, err := s.adopt(ctx, t, alias, c)
		if err != nil {
			return err
		}
		t.Checkouts[alias] = c
		if err := s.save(t); err != nil {
			return err
		}
	}
	return nil
}

func taskBranch(name, alias string) string { return "work-" + name + "-" + alias }

// adopt records the identity of an allocated checkout. A task-owned checkout
// must still be exactly what this task asked Git to create.
func (s *Store) adopt(ctx context.Context, t *Task, alias string, c Checkout) (Checkout, error) {
	head, common, gd, branch, err := identity(ctx, c.Path)
	if err != nil {
		return c, err
	}
	if c.Owned {
		_, sourceCommon, _, _, err := identity(ctx, c.Source)
		if err != nil {
			return c, err
		}
		want := ""
		if c.Changed {
			want = taskBranch(t.Name, alias)
		}
		if common != sourceCommon || branch != want || (!c.Changed && head != c.Base) {
			return c, fmt.Errorf("%s is not this task's allocation for %s; inspect and remove it explicitly", c.Path, alias)
		}
	}
	c.CommonDir, c.GitDir, c.Branch, c.Ready = common, gd, branch, true
	return c, nil
}

// errReleased marks a lifecycle checkout whose native binding was removed
// through the workspace worktree commands; the task no longer has it.
var errReleased = errors.New("native binding was released")

// validateCheckout returns the HEAD of a ready, active checkout.
func (s *Store) validateCheckout(ctx context.Context, t *Task, alias string, c Checkout) (string, error) {
	if c.Source == "" || c.Source != s.Layout.Repos[alias] {
		return "", fmt.Errorf("repository %s was rebound", alias)
	}
	if c.Removed {
		return "", nil
	}
	if c.Isolation != "existing" && c.Isolation != "worktree" {
		return "", fmt.Errorf("invalid isolation for %s", alias)
	}
	if c.Removing && (!c.Owned || !c.Ready) {
		return "", fmt.Errorf("invalid removal intent for %s", alias)
	}
	if c.Owned {
		if s.Layout.WorktreesDir == "" || c.Path != filepath.Join(s.Layout.WorktreesDir, alias, "workspace-"+t.Name) {
			return "", fmt.Errorf("owned checkout path changed for %s", alias)
		}
		if c.Changed && t.Engine != "no" {
			return "", fmt.Errorf("lifecycle checkout must use native bindings")
		}
	} else if c.Changed && t.Engine != "no" {
		// A pending binding still records its source until allocation.
		if c.Ready || c.Path != c.Source {
			dirs, err := workspace.SourceDirsForLayout(s.Layout, t.Engine, t.Change, t.Changed)
			if err != nil {
				return "", err
			}
			if dirs[alias] == c.Source && c.Path != c.Source {
				return "", fmt.Errorf("%s: %w", alias, errReleased)
			}
			if dirs[alias] != c.Path {
				return "", fmt.Errorf("native binding changed for %s", alias)
			}
		}
	} else if c.Path != c.Source {
		return "", fmt.Errorf("unowned checkout path changed for %s", alias)
	}
	if c.Removing {
		if complete, err := removalComplete(ctx, c); err != nil || complete {
			return "", err
		}
	}
	if !c.Ready {
		return "", nil
	}
	head, common, gd, branch, err := identity(ctx, c.Path)
	if err != nil {
		return "", err
	}
	_, sourceCommon, _, _, err := identity(ctx, c.Source)
	if err != nil {
		return "", err
	}
	if common != c.CommonDir || common != sourceCommon || gd != c.GitDir || branch != c.Branch {
		return "", fmt.Errorf("checkout identity changed for %s", alias)
	}
	return head, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "GIT_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	data, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, data)
	}
	return strings.TrimSpace(string(data)), nil
}

// identity reads a checkout's root, HEAD, and Git directories in one process;
// branch is empty when HEAD is detached.
func identity(ctx context.Context, dir string) (head, common, gd, branch string, err error) {
	out, err := git(ctx, dir, "rev-parse", "--show-toplevel", "--path-format=absolute", "--git-common-dir", "--absolute-git-dir", "HEAD", "--symbolic-full-name", "HEAD")
	if err != nil {
		return "", "", "", "", err
	}
	fields := strings.Split(out, "\n")
	if len(fields) != 5 {
		return "", "", "", "", fmt.Errorf("unexpected git rev-parse output in %s", dir)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", "", "", err
	}
	if filepath.Clean(fields[0]) != filepath.Clean(real) {
		return "", "", "", "", fmt.Errorf("%s is not a checkout root", dir)
	}
	branch, _ = strings.CutPrefix(fields[4], "refs/heads/")
	if branch == fields[4] {
		branch = ""
	}
	return fields[3], fields[1], fields[2], branch, nil
}

func clean(ctx context.Context, dir string) error {
	// Status trusts these flags even when the working contents differ from HEAD.
	// Refuse them without changing the user's index or sparse checkout.
	files, err := git(ctx, dir, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(files, "\x00") {
		if entry == "" {
			continue
		}
		if len(entry) < 3 || entry[1] != ' ' {
			return fmt.Errorf("cannot verify index flags at %s", dir)
		}
		if entry[0] == 'S' || (entry[0] >= 'a' && entry[0] <= 'z') {
			return fmt.Errorf("nondefault index flags (assume-unchanged or skip-worktree) for %q at %s", entry[2:], dir)
		}
	}
	status, err := git(ctx, dir, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("uncommitted input at %s:\n%s", dir, status)
	}
	return nil
}

// removalComplete reconciles only a journaled removal. Both the checkout and its
// Git registration must be gone from the same repository. A missing directory
// alone could be a moved worktree or a partial removal and is not completion.
func removalComplete(ctx context.Context, c Checkout) (bool, error) {
	if _, err := os.Lstat(c.Path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	_, common, _, _, err := identity(ctx, c.Source)
	if err != nil {
		return false, err
	}
	if common != c.CommonDir || filepath.Dir(c.GitDir) != filepath.Join(common, "worktrees") {
		return false, fmt.Errorf("removal repository identity changed for %s", c.Path)
	}
	if _, err := os.Lstat(c.GitDir); err == nil {
		return false, fmt.Errorf("checkout %s is missing but its Git registration remains; inspect before recovery", c.Path)
	} else if !os.IsNotExist(err) {
		return false, err
	}
	listed, err := git(ctx, c.Source, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return false, err
	}
	for _, field := range strings.Split(listed, "\x00") {
		if field == "worktree "+c.Path {
			return false, fmt.Errorf("checkout %s is still registered; inspect before recovery", c.Path)
		}
	}
	return true, nil
}

// Cleanup removes only this task's clean, integrated allocations, never source
// checkouts. An allocation Git never created is released without inspection.
func (s *Store) Cleanup(ctx context.Context, name string, yes bool) (*Task, error) {
	if !yes {
		return nil, fmt.Errorf("workspace task cleanup requires --yes")
	}
	unlock, err := s.lock(name)
	if err != nil {
		return nil, err
	}
	defer unlock()
	t, err := s.Load(ctx, name)
	if err != nil {
		return nil, err
	}
	return t, s.cleanup(ctx, t)
}

func (s *Store) cleanup(ctx context.Context, t *Task) error {
	for _, alias := range sortedCheckouts(t) {
		c := t.Checkouts[alias]
		if !c.Owned || c.Removed {
			continue
		}
		if c.Removing {
			complete, err := removalComplete(ctx, c)
			if err != nil {
				return err
			}
			if complete {
				c.Removing, c.Removed = false, true
				t.Checkouts[alias] = c
				if err := s.save(t); err != nil {
					return err
				}
				continue
			}
		}
		if !c.Ready {
			if _, err := os.Lstat(c.Path); os.IsNotExist(err) {
				c.Removed = true
				t.Checkouts[alias] = c
				if err := s.save(t); err != nil {
					return err
				}
				continue
			} else if err != nil {
				return err
			}
			adopted, err := s.adopt(ctx, t, alias, c)
			if err != nil {
				return err
			}
			c = adopted
		}
		head, err := s.validateCheckout(ctx, t, alias, c)
		if err != nil {
			return err
		}
		ignored, err := git(ctx, c.Path, "ls-files", "--others", "--ignored", "--exclude-standard")
		if err != nil {
			return err
		}
		if ignored != "" {
			return fmt.Errorf("%s has ignored files; inspect and remove them explicitly before cleanup", alias)
		}
		// The same cleanliness rules as worktree remove, including hidden index
		// flags and in-progress Git operations.
		if err := workspace.CleanWorktree(c.Path); err != nil {
			return err
		}
		if c.Changed {
			if c.BaseBranch == "" {
				return fmt.Errorf("%s has no recorded integration target", alias)
			}
			if _, err := git(ctx, c.Path, "merge-base", "--is-ancestor", "HEAD", "refs/heads/"+c.BaseBranch); err != nil {
				return fmt.Errorf("%s is not integrated into %s: %w", alias, c.BaseBranch, err)
			}
		} else if head != c.Base {
			return fmt.Errorf("verification input %s moved from pinned revision %s; preserve its commits before cleanup", alias, c.Base)
		}
		// Persist intent before Git removes either the directory or registration.
		// A retry revalidates a surviving checkout or reconciles a completed removal.
		c.Removing = true
		t.Checkouts[alias] = c
		if err := s.save(t); err != nil {
			return err
		}
		if _, err := git(ctx, c.Source, "worktree", "remove", c.Path); err != nil {
			return err
		}
		c.Removing, c.Removed = false, true
		t.Checkouts[alias] = c
		if err := s.save(t); err != nil {
			return err
		}
	}
	return nil
}

func sortedCheckouts(t *Task) []string {
	names := make([]string, 0, len(t.Checkouts))
	for name := range t.Checkouts {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
