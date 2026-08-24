package workspacecfg

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
)

// Validate checks cfg structurally and lexically. It never touches the
// filesystem: on-disk existence and member-kind verification belong to
// workspace discovery. When workspaceRoot is non-empty, every declared path
// must join to a location lexically inside it. Zero members are valid (init
// can be mid-flight). See the package doc for the field grammars.
func Validate(workspaceRoot string, cfg Config) error {
	if err := checkSchemaVersionForValidate(cfg.SchemaVersion); err != nil {
		return err
	}
	if err := identity.ValidateUUID(string(cfg.Workspace.ID)); err != nil {
		return fmt.Errorf("workspacecfg: workspace.id: %w", err)
	}
	switch cfg.Workspace.Workflow {
	case WorkflowTask, WorkflowChange:
	default:
		return fmt.Errorf("workspacecfg: workspace.workflow %q must be %q or %q",
			cfg.Workspace.Workflow, WorkflowTask, WorkflowChange)
	}

	if err := identity.ValidateUUID(string(cfg.Control.ID)); err != nil {
		return fmt.Errorf("workspacecfg: control.id: %w", err)
	}
	if err := validateRootRelativePath(cfg.Control.Path, "control.path"); err != nil {
		return err
	}
	if err := validateRemotes(cfg.Control.Remotes, "control"); err != nil {
		return err
	}

	ids := make(map[identity.RepositoryID]bool, len(cfg.Members))
	paths := make(map[string]bool, len(cfg.Members))
	for i := range cfg.Members {
		m := &cfg.Members[i]
		label := fmt.Sprintf("members[%d]", i)
		if err := identity.ValidateUUID(string(m.ID)); err != nil {
			return fmt.Errorf("workspacecfg: %s.id: %w", label, err)
		}
		if ids[m.ID] {
			return fmt.Errorf("workspacecfg: %s id %s: %w", label, m.ID, ErrDuplicateMemberID)
		}
		ids[m.ID] = true
		// "." is the control slot: a member may occupy it only by being the
		// control repository itself (membership is confirmed, not automatic).
		isControl := m.ID == cfg.Control.ID
		if err := validateMemberPath(m.Path, isControl, label); err != nil {
			return err
		}
		if paths[m.Path] {
			return fmt.Errorf("workspacecfg: %s path %q: %w", label, m.Path, ErrDuplicateMemberPath)
		}
		paths[m.Path] = true
		switch m.Kind {
		case KindGit, KindNonGit:
		default:
			return fmt.Errorf("workspacecfg: %s.kind %q must be %q or %q", label, m.Kind, KindGit, KindNonGit)
		}
		if err := validateRemotes(m.Remotes, label); err != nil {
			return err
		}
		if err := validateChecks(m.Verification, label); err != nil {
			return err
		}
		if m.Paths != nil {
			if err := validatePathClasses(m.Paths, label); err != nil {
				return err
			}
		}
	}

	if err := validateRoutes(cfg.Routes); err != nil {
		return err
	}
	switch cfg.Update.Channel {
	case "", ChannelStable, ChannelPrerelease:
	default:
		return fmt.Errorf("workspacecfg: update.channel %q must be %q or %q",
			cfg.Update.Channel, ChannelStable, ChannelPrerelease)
	}

	if workspaceRoot != "" {
		root := filepath.Clean(workspaceRoot)
		if !containedInRoot(root, cfg.Control.Path) {
			return fmt.Errorf("workspacecfg: control.path %q resolves outside workspace root %q: %w", cfg.Control.Path, root, ErrInvalidPath)
		}
		for i := range cfg.Members {
			if !containedInRoot(root, cfg.Members[i].Path) {
				return fmt.Errorf("workspacecfg: members[%d].path %q resolves outside workspace root %q: %w",
					i, cfg.Members[i].Path, root, ErrInvalidPath)
			}
		}
	}
	return nil
}

// checkSchemaVersionForValidate treats 0 as absent: Validate sees hand-built
// configs where presence cannot be distinguished. Decode does distinguish and
// reports explicit zeros as unsupported.
func checkSchemaVersionForValidate(v int) error {
	if v == 0 {
		return fmt.Errorf("%w: declare schema_version = %d", ErrMissingSchemaVersion, CurrentSchemaVersion)
	}
	return checkSchemaVersion(v)
}

// validateRootRelativePath validates a path where "." is a legal value
// (control.path, check working_dir).
func validateRootRelativePath(path, field string) error {
	if path == "." {
		return nil
	}
	return validateCleanRelPath(path, field)
}

// validateMemberPath validates a member path, where "." is legal only for
// the control member.
func validateMemberPath(path string, isControl bool, label string) error {
	if path == "." && !isControl {
		return fmt.Errorf("workspacecfg: %s.path %q: only the control member may sit at the workspace root: %w", label, path, ErrInvalidPath)
	}
	return validateCleanRelPath(path, fmt.Sprintf("%s.path", label))
}

// validateCleanRelPath enforces the path grammar from the package doc:
// non-empty, no NUL, no backslash, no leading "/", no "."/".."/empty
// segments, and byte-equal to its own filepath.Clean form.
func validateCleanRelPath(path, field string) error {
	fail := func(reason string) error {
		return fmt.Errorf("workspacecfg: %s %q: %s: %w", field, path, reason, ErrInvalidPath)
	}
	switch {
	case path == "":
		return fail("must not be empty")
	case strings.ContainsRune(path, '\x00'):
		return fail("must not contain NUL")
	case strings.Contains(path, `\`):
		return fail("must use '/' separators only")
	case strings.HasPrefix(path, "/"):
		return fail("must not be absolute")
	}
	if path != filepath.Clean(path) {
		return fail("must be clean (no empty, '.', or redundant segments)")
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." {
			return fail("must not escape the workspace root")
		}
	}
	return nil
}

func validateRemotes(remotes []string, label string) error {
	for i, r := range remotes {
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("workspacecfg: %s.remotes[%d] must not be blank", label, i)
		}
	}
	return nil
}

func validateChecks(checks []Check, label string) error {
	names := make(map[string]bool, len(checks))
	for i := range checks {
		c := &checks[i]
		cl := fmt.Sprintf("%s.verification[%d]", label, i)
		if c.Name == "" {
			return fmt.Errorf("workspacecfg: %s.name must not be empty", cl)
		}
		if names[c.Name] {
			return fmt.Errorf("workspacecfg: %s.name %q is a duplicate within one member", cl, c.Name)
		}
		names[c.Name] = true
		if len(c.Command) == 0 {
			return fmt.Errorf("workspacecfg: %s.command must be a non-empty argv array (shell strings are not accepted)", cl)
		}
		for j, arg := range c.Command {
			if arg == "" {
				return fmt.Errorf("workspacecfg: %s.command[%d] must not be empty", cl, j)
			}
		}
		if c.WorkingDir != "" {
			if err := validateRootRelativePath(c.WorkingDir, cl+".working_dir"); err != nil {
				return err
			}
		}
		for j, name := range c.Environment {
			if err := validateEnvName(name); err != nil {
				return fmt.Errorf("workspacecfg: %s.environment[%d]: %w", cl, j, err)
			}
		}
		if c.Timeout != "" {
			if _, err := parseCheckTimeout(c.Timeout); err != nil {
				return fmt.Errorf("workspacecfg: %s.timeout %q: %w", cl, c.Timeout, err)
			}
		}
	}
	return nil
}

// validateEnvName enforces NAMES ONLY: ^[A-Za-z_][A-Za-z0-9_]*$.
func validateEnvName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: name must not be empty", ErrInvalidEnvName)
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_':
		case c >= '0' && c <= '9' && i > 0:
		default:
			return fmt.Errorf("%w: %q (names are letters, digits, and underscores, not starting with a digit)", ErrInvalidEnvName, name)
		}
	}
	return nil
}

// parseCheckTimeout parses a TOML duration string and enforces 1s..24h.
func parseCheckTimeout(s string) (time.Duration, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%w: not a TOML duration", ErrInvalidTimeout)
	}
	if d < time.Second {
		return 0, fmt.Errorf("%w: below the 1s minimum", ErrInvalidTimeout)
	}
	if d > 24*time.Hour {
		return 0, fmt.Errorf("%w: above the 24h maximum", ErrInvalidTimeout)
	}
	return d, nil
}

func validatePathClasses(pc *PathClasses, label string) error {
	classes := []struct {
		name string
		list []string
	}{
		{"tests", pc.Tests},
		{"generated", pc.Generated},
		{"vendored", pc.Vendored},
	}
	for _, cl := range classes {
		for i, pattern := range cl.list {
			if err := validateGlobPattern(pattern); err != nil {
				return fmt.Errorf("workspacecfg: %s.paths.%s[%d]: %w", label, cl.name, i, err)
			}
		}
	}
	return nil
}

// validateGlobPattern enforces the lexical safety grammar from the package
// doc. Pattern compilation and matching happen at the use-site.
func validateGlobPattern(pattern string) error {
	fail := func(reason string) error {
		return fmt.Errorf("%w %q: %s", ErrInvalidGlob, pattern, reason)
	}
	switch {
	case pattern == "":
		return fail("must not be empty")
	case strings.ContainsRune(pattern, '\x00'):
		return fail("must not contain NUL")
	case strings.Contains(pattern, `\`):
		return fail("must use '/' separators only")
	case strings.HasPrefix(pattern, "/"):
		return fail("must not match absolute paths")
	}
	for _, seg := range strings.Split(pattern, "/") {
		if seg == ".." {
			return fail("must not match paths escaping the member root")
		}
	}
	return nil
}

func validateRoutes(routes Routes) error {
	tools := []struct {
		name string
		t    *ToolRoutes
	}{
		{"claude", routes.Claude},
		{"opencode", routes.OpenCode},
	}
	for _, tool := range tools {
		if tool.t == nil {
			continue
		}
		roles := []struct {
			name string
			r    *RoleRoute
		}{
			{"explorer", tool.t.Explorer},
			{"implementer", tool.t.Implementer},
			{"reviewer", tool.t.Reviewer},
			{"skeptic", tool.t.Skeptic},
		}
		for _, role := range roles {
			if role.r == nil {
				continue
			}
			if role.r.Model == "" {
				return fmt.Errorf("workspacecfg: routes.%s.%s.model must not be empty when the route is declared",
					tool.name, role.name)
			}
		}
	}
	return nil
}

// containedInRoot reports whether rel joins to a path lexically inside root.
// Purely lexical: no filesystem access.
func containedInRoot(root, rel string) bool {
	joined := filepath.Clean(filepath.Join(root, rel))
	return joined == root || strings.HasPrefix(joined, root+string(filepath.Separator))
}
