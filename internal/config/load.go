package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/schema"
	toml "github.com/pelletier/go-toml/v2"
)

// decode parses the raw TOML and enforces the schema-version forward-safety
// guard. It is the first config-loading phase.
func decode(data []byte) (*Config, error) {
	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// Forward-safety: refuse a config from a newer schema before any adapter,
	// plan, or apply logic runs, so an older binary never silently mis-applies
	// fields it does not understand (TOML unmarshal drops unknown keys). Absent/0
	// is a legacy config, treated as the current version.
	if c.SchemaVersion > CurrentConfigSchemaVersion {
		return nil, fmt.Errorf("parse config: unknown config schema version %d (this binary supports up to %d) — upgrade homonto: %w", c.SchemaVersion, CurrentConfigSchemaVersion, schema.ErrTooNew)
	}
	return &c, nil
}

// migrate folds legacy declaration forms into their current equivalents.
func migrate(c *Config) {
	// Option C: the imperative [agents.<name>] model is superseded by the
	// declarative [subagents.<name>] one. Fold every declared agent into an
	// equivalent copy-mode subagent (a declared [agents.X] wins over an explicit
	// [subagents.X] of the same name) and drop the agents table, so [agents.X]
	// still parses but is now projected by `apply` like any other subagent.
	if len(c.Agents) > 0 {
		if c.Subagents == nil {
			c.Subagents = map[string]Subagent{}
		}
		for name, ag := range c.Agents {
			mode := ag.Mode
			if mode == "" && strings.HasPrefix(ag.Source, "builtin:") {
				mode = "copy" // builtin agents had no linkable path — copy-only
			}
			// [agents.X] wins the DECLARATION, but a same-named [subagents.X]'s
			// per-tool model blocks are tuning, which [agents.X] has no syntax
			// for — carry them over instead of silently deleting them.
			prev := c.Subagents[name]
			c.Subagents[name] = Subagent{
				Source:   ag.Source,
				Scope:    "user", // agents installed at user scope
				Mode:     mode,
				Version:  ag.Version,
				Targets:  ag.Targets,
				Claude:   prev.Claude,
				OpenCode: prev.OpenCode,
			}
		}
		c.Agents = nil
	}
}

// normalize applies defaulting so downstream projection sees concrete values.
func normalize(c *Config) {
	// Subagents default to project scope when omitted (skills and commands still
	// require an explicit scope). Normalize before validation so downstream
	// projection sees a concrete scope. Model-route values are whitespace-trimmed
	// here too: validation used to trim while the render did not, so
	// `model = "opus "` passed the alias check and then missed the alias map at
	// render, silently dropping its variant.
	trimRoute := func(r ModelRoute) ModelRoute {
		return ModelRoute{
			Model:        strings.TrimSpace(r.Model),
			Effort:       strings.TrimSpace(r.Effort),
			Variant:      strings.TrimSpace(r.Variant),
			BashAllowAdd: append([]string(nil), r.BashAllowAdd...),
		}
	}
	for name, r := range c.Subagents {
		if r.Scope == "" {
			r.Scope = "project"
		}
		r.Claude = trimRoute(r.Claude)
		r.OpenCode = trimRoute(r.OpenCode)
		c.Subagents[name] = r
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	c, err := decode(data)
	if err != nil {
		return nil, err
	}
	migrate(c)
	normalize(c)
	if err := validate(c); err != nil {
		return nil, err
	}
	if abs, err := filepath.Abs(filepath.Dir(path)); err == nil {
		c.baseDir = abs
	} else {
		c.baseDir = filepath.Dir(path)
	}
	if err := resolveRepos(c); err != nil {
		return nil, err
	}
	if err := validateWorkflowRootLocation(c); err != nil {
		return nil, err
	}
	if err := validateWorkflowStateRoot(c); err != nil {
		return nil, err
	}
	return c, nil
}

// validateWorkflowRootLocation rejects an existing configured root component
// that resolves outside the repository before projection can create workflow
// state through it.
func validateWorkflowRootLocation(c *Config) error {
	configRoot, err := filepath.EvalSymlinks(c.baseDir)
	if err != nil {
		return fmt.Errorf("parse config: resolving configuration repository: %w", err)
	}
	current := c.baseDir
	for _, component := range strings.Split(filepath.ToSlash(c.Workflow.RootOrDefault()), "/") {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("parse config: inspecting workflow.root %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil {
			return fmt.Errorf("parse config: resolving workflow.root symlink %s: %w", current, err)
		}
		inside, err := filepath.Rel(configRoot, resolved)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return fmt.Errorf("parse config: workflow.root resolves outside the configuration repository through symlink %s", current)
		}
	}
	return nil
}

// validateWorkflowStateRoot prevents a config edit from silently splitting
// durable workflow records between two trees. The marker is written by the
// workflow CLIs when they first create state; pre-marker docs layouts are also
// recognized so existing repositories get the same fail-closed protection.
func validateWorkflowStateRoot(c *Config) error {
	want := filepath.ToSlash(c.Workflow.RootOrDefault())
	marker := filepath.Join(c.baseDir, ".homonto", "workflow-root")
	if data, err := os.ReadFile(marker); err == nil {
		was := filepath.ToSlash(strings.TrimSpace(string(data)))
		if was != "" && was != want && workflowStateExists(filepath.Join(c.baseDir, filepath.FromSlash(was))) {
			return fmt.Errorf("parse config: workflow.root changed from %q to %q while workflow state exists; move or remove the state explicitly before changing the root", was, want)
		}
	}
	if want != "docs" && workflowStateExists(filepath.Join(c.baseDir, "docs")) {
		return fmt.Errorf("parse config: workflow.root changed from %q to %q while workflow state exists; move or remove the state explicitly before changing the root", "docs", want)
	}
	return nil
}

func workflowStateExists(root string) bool {
	for _, name := range []string{"changes", "tasks", ".to-promote", ".onto-demote"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return true
		}
	}
	return false
}

// resolveRepos turns each [repos] path into the filesystem fact the later
// cross-repo stages will build on, failing closed at load (ADR 0024): the
// directory must exist, it must be a git worktree (a `.git` entry — a
// directory for a normal clone, a file for a linked worktree), two names
// must not resolve to one repository, and the config repo itself is never
// listed — it is implicit and already holds the designated state.
func resolveRepos(c *Config) error {
	if len(c.Repos) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Repos))
	for name := range c.Repos {
		names = append(names, name)
	}
	sort.Strings(names)
	resolved := make(map[string]string, len(names))
	byDir := map[string]string{} // abs dir -> first name
	for _, name := range names {
		p := c.Repos[name]
		// Absolute paths are honored as-is; relative paths resolve against
		// the config file's directory — the same anchoring local: framework
		// roots use.
		dir := p
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(c.baseDir, dir)
		}
		dir = filepath.Clean(dir)
		if strings.ContainsAny(dir, "*?") {
			return fmt.Errorf("parse config: repos.%s: %s contains OpenCode permission wildcard characters", name, dir)
		}
		if info, err := os.Stat(dir); err != nil {
			return fmt.Errorf("parse config: repos.%s: %s does not exist (paths resolve relative to the config file)", name, dir)
		} else if !info.IsDir() {
			return fmt.Errorf("parse config: repos.%s: %s is not a directory", name, dir)
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
			return fmt.Errorf("parse config: repos.%s: %s is not a git worktree (no .git)", name, dir)
		}
		if prev, dup := byDir[dir]; dup {
			return fmt.Errorf("parse config: repos.%s and repos.%s resolve to the same repository (%s)", name, prev, dir)
		}
		byDir[dir] = name
		resolved[name] = dir
	}
	if prev, isSelf := byDir[c.baseDir]; isSelf {
		return fmt.Errorf("parse config: repos.%s resolves to the config repository itself; the config repo is implicit — declare only the OTHER repositories", prev)
	}
	c.repoDirs = resolved
	return nil
}
