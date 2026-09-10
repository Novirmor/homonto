// Package workspace resolves the read-only workspace layout independently of
// projection configuration and workflow commands.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/schema"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/pelletier/go-toml/v2"
)

type Layout struct {
	SchemaVersion                                               int
	ConfigPath, ConfigRoot, WorkflowRoot, GitMode, WorktreesDir string
	Repos                                                       map[string]string
}

// MigrationLayoutError carries a safe diagnostic code for the dedicated
// read-only migration loader. Its wrapped error remains useful to direct API
// callers but must not be rendered into a migration plan.
type MigrationLayoutError struct {
	Code string
	err  error
}

func (e *MigrationLayoutError) Error() string { return e.err.Error() }
func (e *MigrationLayoutError) Unwrap() error { return e.err }

func migrationLayoutError(code string, err error) error {
	return &MigrationLayoutError{Code: code, err: err}
}

// MigrationLayoutCode returns the safe structured reason for a failed
// LoadMigration call without exposing arbitrary config or parser text.
func MigrationLayoutCode(err error) string {
	var diagnostic *MigrationLayoutError
	if errors.As(err, &diagnostic) && diagnostic.Code != "" {
		return diagnostic.Code
	}
	return "unknown"
}

func (l Layout) ExplicitRepos() bool { return l.SchemaVersion >= 2 }

// LoadRoot opens homonto.toml in root; it does not discover parents or initialize anything.
func LoadRoot(root string) (Layout, error) { return Load(filepath.Join(root, "homonto.toml")) }

// LoadMigration opens only the deliberately narrow schema-2 legacy layout that
// the read-only migration planner understands. It does not weaken Load's
// ownership guard, write a marker, initialize history, or adopt records.
func LoadMigration(configPath string) (Layout, error) {
	if err := requireRealRegularFile(configPath); err != nil {
		return Layout{}, migrationLayoutError("config_not_regular", fmt.Errorf("workspace migration requires a real regular config file: %w", err))
	}
	l, err := loadPaths(configPath)
	if err != nil {
		return l, migrationLayoutError("config_validation_failed", err)
	}
	if l.SchemaVersion != 2 || !l.ExplicitRepos() {
		return l, migrationLayoutError("schema_not_two", fmt.Errorf("config %q: workspace migration requires schema_version = 2", l.ConfigPath))
	}
	if l.GitMode != "existing" {
		return l, migrationLayoutError("git_mode_not_existing", fmt.Errorf("config %q: workspace migration requires workflow.git = \"existing\"", l.ConfigPath))
	}
	// Schema-2 migration separates a non-Git control directory from records.
	// A Git configuration root remains an ordinary schema-2 layout, not this
	// legacy-ownership transition. Inspect control files directly rather than
	// treating every failed Git probe as proof that the directory is non-Git.
	insideGit, err := hasGitControlAncestor(l.ConfigRoot)
	if err != nil {
		return l, migrationLayoutError("config_root_uninspectable", fmt.Errorf("config %q: inspecting configuration Git ancestry: %w", l.ConfigPath, err))
	}
	if insideGit {
		return l, migrationLayoutError("config_root_is_git", fmt.Errorf("config %q: workspace migration requires a non-Git configuration root", l.ConfigPath))
	}
	if err := workflowroot.ValidateLegacyMigrationRead(l.ConfigPath, l.WorkflowRoot, l.GitMode, l.SchemaVersion); err != nil {
		return l, migrationLayoutError(workflowroot.LegacyMigrationReadCode(err), fmt.Errorf("config %q: %w", l.ConfigPath, err))
	}
	return l, nil
}

// Load resolves paths relative to the config file, not the process directory.
// An omitted worktrees.dir stays empty: allocation must require an explicit parent.
func Load(configPath string) (Layout, error) {
	l, err := loadPaths(configPath)
	if err != nil {
		return l, err
	}
	if err := workflowroot.ValidateLayout(l.ConfigPath, l.WorkflowRoot, l.GitMode, l.SchemaVersion); err != nil {
		return l, fmt.Errorf("config %q: %w", l.ConfigPath, err)
	}
	return l, nil
}

// loadPaths shares schema, path, and repository identity validation between
// normal loading and the migration planner. Ownership validation intentionally
// remains at the two explicit callers above.
func loadPaths(configPath string) (l Layout, err error) {
	l.ConfigPath, err = filepath.Abs(configPath)
	if err != nil {
		return l, err
	}
	defer func() {
		if err != nil {
			err = fmt.Errorf("config %q: %w", l.ConfigPath, err)
		}
	}()
	data, err := os.ReadFile(l.ConfigPath)
	if err != nil {
		return l, err
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
	l.SchemaVersion = c.SchemaVersion
	if c.SchemaVersion > 2 {
		return l, fmt.Errorf("unknown config schema version %d (this binary supports up to 2); upgrade homonto: %w", c.SchemaVersion, schema.ErrTooNew)
	}
	if c.SchemaVersion < 0 {
		return l, fmt.Errorf("schema_version must be non-negative")
	}
	if !l.ExplicitRepos() && (c.Workflow.Git != nil || c.Worktrees != nil) {
		return l, fmt.Errorf("workflow.git and [worktrees] require schema_version = 2; upgrade schema_version=2 explicitly")
	}
	l.ConfigRoot = filepath.Dir(l.ConfigPath)
	l.GitMode = "existing"
	if c.Workflow.Git != nil {
		l.GitMode = *c.Workflow.Git
		if l.GitMode != "existing" && l.GitMode != "managed" {
			return l, fmt.Errorf("workflow.git %q must be 'existing' or 'managed'", l.GitMode)
		}
	}
	if l.ExplicitRepos() && strings.IndexFunc(c.Workflow.Root, unicode.IsControl) >= 0 {
		return l, fmt.Errorf("workflow.root: invalid path %q", c.Workflow.Root)
	}
	root := strings.TrimSpace(c.Workflow.Root)
	if root == "" {
		root = "docs"
	}
	if !l.ExplicitRepos() {
		if filepath.IsAbs(root) || root == "." || strings.Contains(root, `\`) {
			return l, fmt.Errorf("workflow.root %q must be a relative path below the configuration repository", root)
		}
		root = filepath.Clean(root)
		if root == ".." || strings.HasPrefix(root, ".."+string(filepath.Separator)) {
			return l, fmt.Errorf("workflow.root %q must remain below the configuration repository", root)
		}
		l.WorkflowRoot = filepath.Join(l.ConfigRoot, root)
		resolved, err := canonical(l.WorkflowRoot)
		if err != nil {
			return l, err
		}
		base, err := canonical(l.ConfigRoot)
		if err != nil {
			return l, err
		}
		if !within(base, resolved) {
			return l, fmt.Errorf("workflow.root resolves outside the configuration repository through symlink")
		}
	} else {
		l.WorkflowRoot, err = layoutPath(l.ConfigRoot, root)
		if err != nil {
			return l, fmt.Errorf("workflow.root: %w", err)
		}
	}
	l.Repos = make(map[string]string, len(c.Repos))
	names := make([]string, 0, len(c.Repos))
	for name := range c.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := map[string]string{}
	gitMetadata := map[string]string{}
	for _, name := range names {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return l, fmt.Errorf("repos entry %q is not a plain name", name)
		}
		if digits := strings.TrimPrefix(name, "-"); digits != "" && strings.Trim(digits, "0123456789") == "" {
			return l, fmt.Errorf("repos entry %q would be treated as a JSON array index", name)
		}
		p := strings.TrimSpace(c.Repos[name])
		if p == "" {
			return l, fmt.Errorf("repos.%s has an empty path", name)
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(l.ConfigRoot, p)
		}
		p = filepath.Clean(p)
		if strings.ContainsAny(p, "*?") {
			return l, fmt.Errorf("repos.%s: %q contains OpenCode permission wildcard characters", name, p)
		}
		info, err := os.Stat(p)
		if err != nil {
			return l, fmt.Errorf("repos.%s: %q does not exist (paths resolve relative to the config file): %w", name, p, err)
		}
		if !info.IsDir() {
			return l, fmt.Errorf("repos.%s: %q is not a directory", name, p)
		}
		identity := p
		if l.ExplicitRepos() {
			p, err = canonical(p)
			if err != nil {
				return l, err
			}
			if strings.ContainsAny(p, "*?") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
				return l, fmt.Errorf("repos.%s: invalid resolved path %q", name, p)
			}
			top, common, err := gitRoots(p)
			if err != nil {
				return l, fmt.Errorf("repos.%s: %w", name, err)
			}
			if top != p {
				return l, fmt.Errorf("repos.%s: %q must be the exact Git top-level %q", name, p, top)
			}
			identity = common
			gitMetadata[name] = common
		} else {
			if _, err := os.Stat(filepath.Join(p, ".git")); err != nil {
				return l, fmt.Errorf("repos.%s: %q is not a git worktree (no .git)", name, p)
			}
			if p == l.ConfigRoot {
				return l, fmt.Errorf("repos.%s resolves to the config repository itself; the config repo is implicit", name)
			}
		}
		if prev, ok := seen[identity]; ok {
			return l, fmt.Errorf("repos.%s and repos.%s resolve to the same repository (%q)", name, prev, p)
		}
		seen[identity] = name
		l.Repos[name] = p
	}
	if !l.ExplicitRepos() {
		return l, nil
	}
	if c.Worktrees != nil && c.Worktrees.Dir != "" {
		l.WorktreesDir, err = layoutPath(l.ConfigRoot, c.Worktrees.Dir)
		if err != nil {
			return l, fmt.Errorf("worktrees.dir: %w", err)
		}
	}
	base, err := canonical(l.ConfigRoot)
	if err != nil {
		return l, err
	}
	for _, target := range []struct{ key, path string }{{"workflow.root", l.WorkflowRoot}, {"worktrees.dir", l.WorktreesDir}} {
		if target.path == "" {
			continue
		}
		if within(target.path, base) {
			return l, fmt.Errorf("%s %q overlaps the configuration root %q", target.key, target.path, base)
		}
		for _, reserved := range []string{".homonto", ".opencode", ".claude", ".git", "homonto.toml", "opencode.json", "opencode.jsonc"} {
			control, err := canonical(filepath.Join(l.ConfigRoot, reserved))
			if err != nil {
				return l, err
			}
			if overlaps(target.path, control) {
				return l, fmt.Errorf("%s %q overlaps control plane %q", target.key, target.path, control)
			}
		}
		for _, name := range names {
			repo := l.Repos[name]
			if overlaps(target.path, gitMetadata[name]) {
				return l, fmt.Errorf("%s %q overlaps repos.%s Git control plane %q", target.key, target.path, name, gitMetadata[name])
			}
			if target.key == "workflow.root" && l.GitMode == "existing" && target.path != repo && within(repo, target.path) {
				// Existing records may live in a source tree, but never its Git/tool metadata.
				for _, reserved := range []string{".git", ".homonto", ".opencode", ".claude", "homonto.toml", "opencode.json", "opencode.jsonc"} {
					control, err := canonical(filepath.Join(repo, reserved))
					if err != nil {
						return l, err
					}
					if overlaps(target.path, control) {
						return l, fmt.Errorf("workflow.root %q overlaps repos.%s control plane %q", target.path, name, control)
					}
				}
				continue
			}
			if overlaps(target.path, repo) {
				return l, fmt.Errorf("%s %q overlaps source repos.%s %q", target.key, target.path, name, repo)
			}
		}
	}
	if l.WorktreesDir != "" && overlaps(l.WorkflowRoot, l.WorktreesDir) {
		return l, fmt.Errorf("worktrees.dir %q overlaps workflow.root %q", l.WorktreesDir, l.WorkflowRoot)
	}
	if l.GitMode == "managed" {
		parent := filepath.Dir(l.WorkflowRoot)
		for {
			if _, err := os.Stat(parent); err == nil {
				break
			}
			next := filepath.Dir(parent)
			if next == parent {
				break
			}
			parent = next
		}
		if top, _, err := gitRoots(parent); err == nil {
			return l, fmt.Errorf("managed workflow.root %q is inside Git repository %q; use a standalone root", l.WorkflowRoot, top)
		}
		// A missing .git is not permission to initialize. The initializer owns
		// adoption of empty/populated directories; loading only checks structure.
		if _, err := os.Lstat(filepath.Join(l.WorkflowRoot, ".git")); err == nil {
			top, common, err := gitRoots(l.WorkflowRoot)
			if err != nil {
				return l, fmt.Errorf("managed workflow.root: %w", err)
			}
			gitDir, err := canonical(filepath.Join(l.WorkflowRoot, ".git"))
			if err != nil {
				return l, err
			}
			info, err := os.Lstat(filepath.Join(l.WorkflowRoot, ".git"))
			if err != nil {
				return l, err
			}
			if top != l.WorkflowRoot || common != gitDir || !info.IsDir() {
				return l, fmt.Errorf("managed workflow.root %q must be an exact standalone Git root", l.WorkflowRoot)
			}
		} else if !os.IsNotExist(err) {
			return l, err
		}
	}
	return l, nil
}

func requireRealRegularFile(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	root := filepath.VolumeName(abs) + string(os.PathSeparator)
	if err := fsutil.RequireRealParents(root, filepath.Dir(abs)); err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%q must be a regular file (symlinks are refused)", abs)
	}
	return nil
}

func hasGitControlAncestor(dir string) (bool, error) {
	for dir = filepath.Clean(dir); ; dir = filepath.Dir(dir) {
		_, err := os.Lstat(filepath.Join(dir, ".git"))
		if err == nil {
			return true, nil
		}
		if !os.IsNotExist(err) {
			return false, err
		}
		if filepath.Dir(dir) == dir {
			return false, nil
		}
	}
}

func layoutPath(base, p string) (string, error) {
	if strings.TrimSpace(p) == "" || strings.ContainsAny(p, "*?\\") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("invalid path %q", p)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	p, err := canonical(p)
	if err != nil {
		return "", err
	}
	if strings.ContainsAny(p, "*?") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("invalid resolved path %q", p)
	}
	if filepath.Dir(p) == p {
		return "", fmt.Errorf("filesystem root %q is not a dedicated directory", p)
	}
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", p)
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return p, nil
}

// canonical resolves existing ancestors, including symlinks, without creating
// missing suffixes. Lstat prevents a dangling symlink from looking like absence.
func canonical(p string) (string, error) {
	p = filepath.Clean(p)
	if _, err := os.Lstat(p); err == nil {
		return filepath.EvalSymlinks(p)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(p)
	if parent == p {
		return "", fmt.Errorf("cannot resolve %q", p)
	}
	resolved, err := canonical(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(p)), nil
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func overlaps(a, b string) bool { return within(a, b) || within(b, a) }

func gitRoots(dir string) (string, string, error) {
	probe := func(arg string) (string, error) {
		cmd := exec.Command("git", "-c", "core.fsmonitor=false", "-C", dir, "rev-parse", "--path-format=absolute", arg)
		// Git environment overrides must not make a bogus source pass validation.
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "GIT_") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("%q is not a git worktree (%s): %w", dir, arg, err)
		}
		return canonical(strings.TrimSuffix(string(out), "\n"))
	}
	top, err := probe("--show-toplevel")
	if err != nil {
		return "", "", err
	}
	common, err := probe("--git-common-dir")
	return top, common, err
}

// GitIdentity returns the canonical top-level and common Git directory for an
// exact worktree. It is a read-only identity check shared by migration planning.
func GitIdentity(dir string) (string, string, error) { return gitRoots(dir) }

// ReadGit runs a read-only Git probe with caller-controlled plumbing removed.
// GIT_OPTIONAL_LOCKS=0 and command-local core.fsmonitor=false keep inspection
// from refreshing an index or invoking a configured fsmonitor hook. Callers
// must not use it for mutation commands.
func ReadGit(dir string, args ...string) ([]byte, error) {
	if _, _, err := gitRoots(dir); err != nil {
		return nil, err
	}
	cmd := exec.Command("git", append([]string{"-c", "core.fsmonitor=false", "-C", dir}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s in %q: %w", strings.Join(args, " "), dir, err)
	}
	return out, nil
}
