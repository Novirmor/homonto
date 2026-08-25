// Package workspacecfg defines the workspace manifest that anchors every
// workflow command: which repositories constitute the workspace (control plus
// members), how each member is verified (checks), which path classes its files
// fall into (tests/generated/vendored globs), and how agent roles route to
// host models (claude/opencode × explorer/implementer/reviewer/skeptic).
//
// # Decoding and schema
//
// Decode is strict: unknown fields are rejected with an error naming the key
// and its dotted path (e.g. members[0].paths.bogus), and schema_version must
// be present and exactly 1. A version above 1 fails with errors.Is on both
// ErrUnsupportedSchema and schema.ErrTooNew so callers can detect "binary too
// old" without matching message text.
//
// # Path grammar
//
// Every declared path (control.path, member.path, check working_dir) is a
// root-relative clean slash-separated path: non-empty, no NUL byte, no
// backslash (forward slashes only, so the manifest is portable), no leading
// "/", no empty/"."/".." segment, and byte-equal to its filepath.Clean form.
// "." itself is allowed only where a root value is legal: control.path, check
// working_dir, and a member path whose member IS the control repository
// (membership of control is confirmed by listing it, never automatic).
//
// # Glob grammar
//
// Path-class patterns are doublestar-style. This package validates only the
// lexical safety grammar: non-empty, no NUL byte, no backslash, no leading
// "/", and no "/"-separated segment equal to ".." — so a pattern can never
// match an absolute or root-escaping path. Full pattern compilation and
// matching happen at the use-site (the runtime matcher), not here.
//
// # Environment and timeout grammar
//
// Check environment entries are variable NAMES ONLY, each matching
// ^[A-Za-z_][A-Za-z0-9_]*$. Check timeout is a TOML string parsed with
// time.ParseDuration; the accepted range is 1s..24h inclusive, and an absent
// timeout means the default 10m (materialized at decode time).
//
// # Canonical form
//
// Marshal emits the canonical form: members sorted by id, checks sorted by
// name, defaults materialized (timeout "10m", working_dir "."). Marshal is a
// fixed point: Marshal(Decode(Marshal(cfg))) is byte-identical to
// Marshal(cfg). Fingerprints hash the same canonical form.
//
// # Validation boundary
//
// Validate performs structural and lexical path checks only; it never touches
// the filesystem. On-disk existence and kind verification (a "git" member has
// .git, a "non_git" member does not) belong to workspace discovery, not to
// configuration. When workspaceRoot is non-empty, Validate additionally
// requires that joining each declared path to the root stays lexically inside
// it — a belt-and-braces check; it does not read the directory. Zero members
// are valid at load: init can legitimately be mid-flight.
package workspacecfg

import (
	"errors"

	"github.com/noviopenworks/homonto/internal/identity"
)

// CurrentSchemaVersion is the workspace-manifest schema version this binary
// supports. The manifest must declare exactly this version.
const CurrentSchemaVersion = 1

// DefaultCheckTimeout materializes in place of an omitted check timeout.
const DefaultCheckTimeout = "10m"

// Workflow selects which workflow a workspace runs.
type Workflow string

const (
	WorkflowTask   Workflow = "task"
	WorkflowChange Workflow = "change"
)

// MemberKind declares how a member repository is tracked.
type MemberKind string

const (
	KindGit    MemberKind = "git"
	KindNonGit MemberKind = "non_git"
)

// UpdateChannel selects the release channel for self-update.
type UpdateChannel string

const (
	ChannelStable     UpdateChannel = "stable"
	ChannelPrerelease UpdateChannel = "prerelease"
)

// Workspace identifies the workspace and its workflow.
type Workspace struct {
	ID       identity.WorkspaceID `toml:"id"`
	Workflow Workflow             `toml:"workflow"`
}

// Control is the control repository anchor: the member of truth for workflow
// state, normally at the workspace root itself.
type Control struct {
	ID      identity.RepositoryID `toml:"id"`
	Path    string                `toml:"path"`
	Remotes []string              `toml:"remotes,omitempty"`
}

// Member is one repository in the workspace. Path is relative to the
// workspace root (never the member's own root).
type Member struct {
	ID           identity.RepositoryID `toml:"id"`
	Path         string                `toml:"path"`
	Kind         MemberKind            `toml:"kind"`
	Remotes      []string              `toml:"remotes,omitempty"`
	Verification []Check               `toml:"verification,omitempty"`
	Paths        *PathClasses          `toml:"paths,omitempty"`
}

// Check is one verification command run against a member. Command is argv —
// never a shell string. WorkingDir is member-relative. Environment lists
// variable names whose values are forwarded to the command.
type Check struct {
	Name        string   `toml:"name"`
	Command     []string `toml:"command"`
	WorkingDir  string   `toml:"working_dir,omitempty"`
	Environment []string `toml:"environment,omitempty"`
	Timeout     string   `toml:"timeout,omitempty"`
}

// PathClasses partitions a member's files by doublestar globs.
type PathClasses struct {
	Tests     []string `toml:"tests,omitempty"`
	Generated []string `toml:"generated,omitempty"`
	Vendored  []string `toml:"vendored,omitempty"`
}

// RoleRoute binds one agent role to a host model. Model is required when the
// route is declared; effort and variant are optional and, being omitempty,
// cannot be present-but-empty.
type RoleRoute struct {
	Model   string `toml:"model"`
	Effort  string `toml:"effort,omitempty"`
	Variant string `toml:"variant,omitempty"`
}

// ToolRoutes holds the role routes for one host tool.
type ToolRoutes struct {
	Explorer    *RoleRoute `toml:"explorer,omitempty"`
	Implementer *RoleRoute `toml:"implementer,omitempty"`
	Reviewer    *RoleRoute `toml:"reviewer,omitempty"`
	Skeptic     *RoleRoute `toml:"skeptic,omitempty"`
}

// Routes holds per-tool role routes. Nil tool/role pointers mean undeclared.
type Routes struct {
	Claude   *ToolRoutes `toml:"claude,omitempty"`
	OpenCode *ToolRoutes `toml:"opencode,omitempty"`
}

// Integrations holds cross-cutting host-integration policy.
type Integrations struct {
	CommitGenerated bool `toml:"commit_generated"`
}

// Update holds self-update policy. An empty Channel means "not configured";
// the updater, not this package, applies its default.
type Update struct {
	Channel UpdateChannel `toml:"channel,omitempty"`
}

// Config is the workspace manifest (.homonto/config.toml).
type Config struct {
	SchemaVersion int          `toml:"schema_version"`
	Workspace     Workspace    `toml:"workspace"`
	Control       Control      `toml:"control"`
	Members       []Member     `toml:"members,omitempty"`
	Routes        Routes       `toml:"routes"`
	Integrations  Integrations `toml:"integrations"`
	Update        Update       `toml:"update"`
}

// Typed errors. Wrap with field context via fmt.Errorf("%w", ...) so callers
// can branch with errors.Is; messages always name the offending value.
var (
	// ErrInvalidTOML wraps syntax and type errors from the TOML decoder.
	ErrInvalidTOML = errors.New("workspacecfg: invalid TOML")
	// ErrUnknownField names a manifest key the schema does not define.
	ErrUnknownField = errors.New("workspacecfg: unknown field")
	// ErrMissingSchemaVersion: schema_version key absent (or zero via
	// Validate, which cannot distinguish an explicit zero).
	ErrMissingSchemaVersion = errors.New("workspacecfg: missing schema_version")
	// ErrUnsupportedSchema: schema_version present but not exactly 1.
	// Versions above 1 additionally wrap schema.ErrTooNew.
	ErrUnsupportedSchema = errors.New("workspacecfg: unsupported schema_version")
	// ErrInvalidPath: a path field violates the path grammar.
	ErrInvalidPath = errors.New("workspacecfg: invalid path")
	// ErrInvalidGlob: a path-class pattern violates the glob grammar.
	ErrInvalidGlob = errors.New("workspacecfg: invalid glob pattern")
	// ErrInvalidEnvName: an environment entry is not a bare name.
	ErrInvalidEnvName = errors.New("workspacecfg: invalid environment name")
	// ErrInvalidTimeout: a check timeout is unparseable or out of 1s..24h.
	ErrInvalidTimeout = errors.New("workspacecfg: invalid timeout")
	// ErrDuplicateMemberID: two members share one repository id.
	ErrDuplicateMemberID = errors.New("workspacecfg: duplicate member id")
	// ErrDuplicateMemberPath: two members share one path.
	ErrDuplicateMemberPath = errors.New("workspacecfg: duplicate member path")
	// ErrUnknownMember: a per-member lookup named an id that is not a member.
	ErrUnknownMember = errors.New("workspacecfg: unknown member")
)
