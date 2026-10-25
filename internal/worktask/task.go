// Package worktask provides engine-independent checkout bookkeeping, not a scheduler.
package worktask

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

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

func (s *Store) Load(name string) (*Task, error) {
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
		if err := s.validateCheckout(&t, alias, checkout); err != nil {
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
// Repeating creation resumes only the identical task; it cannot switch engines or bases.
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
	if _, err := os.Lstat(filepath.Join(dir, "task.json")); err == nil {
		t, err := s.Load(name)
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
			if t.Checkouts[alias].Isolation != mode || t.Checkouts[alias].Removed {
				return nil, fmt.Errorf("task isolation differs or checkout was removed; resume its recorded choice")
			}
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
		if !write && overrides[alias] != "worktree" {
			if err := clean(ctx, path); err != nil {
				return nil, fmt.Errorf("%s: ask for dirty verification-input disposition; explicit %s=worktree override pins committed input: %w", alias, alias, err)
			}
		}
		if mode == "existing" || path != source {
			if err := clean(ctx, path); err != nil {
				return nil, fmt.Errorf("%s: dirty checkout needs user disposition: %w", alias, err)
			}
		} else if s.Layout.WorktreesDir == "" {
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
	for _, alias := range inputs {
		c := t.Checkouts[alias]
		if c.Ready {
			continue
		}
		if c.Changed && engine != "no" {
			if c.Path == c.Source {
				w, err := workspace.CreateWorktree(s.Layout, engine, change, alias, "refs/heads/"+c.BaseBranch, "work-"+name+"-"+alias)
				if err != nil {
					return t, err
				}
				c.Path = w.Path
			}
		} else {
			c.Path = filepath.Join(s.Layout.WorktreesDir, alias, "workspace-"+name)
			if err := fsutil.RequireRealParents(filepath.Dir(s.Layout.WorktreesDir), c.Path); err != nil {
				return t, err
			}
			if _, err := os.Lstat(c.Path); !os.IsNotExist(err) {
				return t, fmt.Errorf("worktree destination already exists or cannot be inspected: %s", c.Path)
			}
			args := []string{"worktree", "add", "--detach", c.Path, c.Base}
			if c.Changed {
				args = []string{"worktree", "add", "-b", "work-" + name + "-" + alias, c.Path, c.Base}
			}
			// Persist allocation intent before Git runs, so an interrupted command
			// leaves the exact destination available for manual recovery inspection.
			c.Owned = true
			t.Checkouts[alias] = c
			if err := s.save(t); err != nil {
				return t, err
			}
			if _, err := git(ctx, c.Source, args...); err != nil {
				return t, err
			}
		}
		_, c.CommonDir, c.GitDir, c.Branch, err = identity(ctx, c.Path)
		if err != nil {
			return t, err
		}
		c.Ready = true
		t.Checkouts[alias] = c
		if err := s.save(t); err != nil {
			return t, err
		}
	}
	return t, nil
}

func (s *Store) validateCheckout(t *Task, alias string, c Checkout) error {
	if c.Source == "" || c.Source != s.Layout.Repos[alias] {
		return fmt.Errorf("repository %s was rebound", alias)
	}
	if !c.Ready {
		return fmt.Errorf("%s allocation is incomplete; inspect task metadata and Git worktrees before recovery", alias)
	}
	if c.Removed {
		return nil
	}
	if c.Isolation != "existing" && c.Isolation != "worktree" {
		return fmt.Errorf("invalid isolation for %s", alias)
	}
	if c.Owned {
		if s.Layout.WorktreesDir == "" || c.Path != filepath.Join(s.Layout.WorktreesDir, alias, "workspace-"+t.Name) {
			return fmt.Errorf("owned checkout path changed for %s", alias)
		}
		if c.Changed && t.Engine != "no" {
			return fmt.Errorf("lifecycle checkout must use native bindings")
		}
	} else if c.Changed && t.Engine != "no" {
		dirs, err := workspace.SourceDirsForLayout(s.Layout, t.Engine, t.Change, t.Changed)
		if err != nil {
			return err
		}
		if dirs[alias] != c.Path {
			return fmt.Errorf("native binding changed for %s", alias)
		}
	} else if c.Path != c.Source {
		return fmt.Errorf("unowned checkout path changed for %s", alias)
	}
	_, common, gd, branch, err := identity(context.Background(), c.Path)
	if err != nil {
		return err
	}
	_, sourceCommon, _, _, err := identity(context.Background(), c.Source)
	if err != nil {
		return err
	}
	if common != c.CommonDir || common != sourceCommon || gd != c.GitDir || branch != c.Branch {
		return fmt.Errorf("checkout identity changed for %s", alias)
	}
	return nil
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

func identity(ctx context.Context, dir string) (head, common, gd, branch string, err error) {
	top, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", "", "", err
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", "", "", "", err
	}
	if filepath.Clean(top) != filepath.Clean(real) {
		return "", "", "", "", fmt.Errorf("%s is not a checkout root", dir)
	}
	for _, p := range []struct {
		dst  *string
		args []string
	}{
		{&head, []string{"rev-parse", "HEAD"}},
		{&common, []string{"rev-parse", "--path-format=absolute", "--git-common-dir"}},
		{&gd, []string{"rev-parse", "--absolute-git-dir"}},
		{&branch, []string{"branch", "--show-current"}},
	} {
		*p.dst, err = git(ctx, dir, p.args...)
		if err != nil {
			return "", "", "", "", err
		}
	}
	return
}

func clean(ctx context.Context, dir string) error {
	status, err := git(ctx, dir, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("uncommitted input at %s:\n%s", dir, status)
	}
	return nil
}

// Cleanup removes only this task's clean, integrated allocations, never source checkouts.
func (s *Store) Cleanup(ctx context.Context, t *Task, yes bool) error {
	if !yes {
		return fmt.Errorf("workspace task cleanup requires --yes")
	}
	for _, alias := range sortedCheckouts(t) {
		c := t.Checkouts[alias]
		if !c.Owned || c.Removed {
			continue
		}
		if err := s.validateCheckout(t, alias, c); err != nil {
			return err
		}
		if err := clean(ctx, c.Path); err != nil {
			return err
		}
		ignored, err := git(ctx, c.Path, "ls-files", "--others", "--ignored", "--exclude-standard")
		if err != nil {
			return err
		}
		if ignored != "" {
			return fmt.Errorf("%s has ignored files; inspect and remove them explicitly before cleanup", alias)
		}
		if c.Changed {
			if c.BaseBranch == "" {
				return fmt.Errorf("%s has no recorded integration target", alias)
			}
			if _, err := git(ctx, c.Path, "merge-base", "--is-ancestor", "HEAD", "refs/heads/"+c.BaseBranch); err != nil {
				return fmt.Errorf("%s is not integrated into %s: %w", alias, c.BaseBranch, err)
			}
		} else {
			head, err := git(ctx, c.Path, "rev-parse", "HEAD")
			if err != nil {
				return err
			}
			if head != c.Base {
				return fmt.Errorf("verification input %s moved from pinned revision %s; preserve its commits before cleanup", alias, c.Base)
			}
		}
		if _, err := git(ctx, c.Source, "worktree", "remove", c.Path); err != nil {
			return err
		}
		c.Removed = true
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
