// Package protocol defines the versioned JSON contract between Homonto and
// the host tools (Claude Code, OpenCode) that execute assignments: the
// `homonto next --json` response, the report and decision submissions
// hosts send back, and the guard request/decision pair used by the write
// hook.
//
// # Freshness boundary
//
// Actions carry a freshness token. This package validates only the token's
// FORMAT (identity.ValidateToken) and the submission's consistency with the
// action it answers. Whether a token is still FRESH — matching the stored
// hash of the live assignment in the local runtime database — is the
// assignment store's job (workspace-isolation workstream), not this
// package's. A structurally valid submission can therefore still be stale
// at the runtime layer; this package must never be the freshness
// authority.
//
// # Codec contract
//
// All decoders are strict: unknown fields are rejected, trailing JSON is
// rejected, and the protocol version must match exactly where the payload
// declares one. Encoders are deterministic — the same value always encodes
// to identical bytes — and responses render with two-space indentation for
// human review. The complete state carries an explicitly empty actions
// array, never an omitted key.
package protocol

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// CurrentVersion is the protocol version this binary speaks. Responses and
// submissions declare exactly this version.
const CurrentVersion = 1

// NextState is the top-level workflow state returned by `homonto next`.
type NextState string

const (
	// NextReady: actions are available to execute now.
	NextReady NextState = "ready"
	// NextBlocked: exactly one blocking decision awaits a human.
	NextBlocked NextState = "blocked"
	// NextComplete: the workflow has no further actions.
	NextComplete NextState = "complete"
)

// ActionKind distinguishes role assignments from human decisions.
type ActionKind string

const (
	// KindAssignment: work for an agent role.
	KindAssignment ActionKind = "assignment"
	// KindDecision: a human decision gate.
	KindDecision ActionKind = "decision"
)

// Role is the agent role an assignment addresses. Explorer, implementer,
// reviewer, and skeptic are mandatory in every workflow path.
type Role string

const (
	RoleExplorer    Role = "explorer"
	RoleImplementer Role = "implementer"
	RoleReviewer    Role = "reviewer"
	RoleSkeptic     Role = "skeptic"
)

// NextResponse is the top-level `homonto next --json` payload: exactly one
// blocking decision, one action or one parallel action group when ready,
// or an explicitly empty action list when complete.
type NextResponse struct {
	ProtocolVersion int       `json:"protocol_version"`
	State           NextState `json:"state"`
	Actions         []Action  `json:"actions"`
}

// Action is one unit of work presented to a host. Assignments carry a
// role and an expected report; decisions carry a decision schema instead.
type Action struct {
	ID                identity.ActionID     `json:"id"`
	Kind              ActionKind            `json:"kind"`
	FreshnessToken    identity.Token        `json:"freshness_token"`
	Workflow          workspacecfg.Workflow `json:"workflow"`
	Path              string                `json:"path"`
	Phase             string                `json:"phase"`
	Reason            string                `json:"reason"`
	Role              Role                  `json:"role,omitempty"`
	Prompt            string                `json:"prompt"`
	Repository        RepositoryRef         `json:"repository"`
	WorkingDirectory  string                `json:"working_directory"`
	WriteScope        WriteScope            `json:"write_scope"`
	ParallelGroupID   string                `json:"parallel_group_id,omitempty"`
	Dependencies      []identity.ActionID   `json:"dependencies,omitempty"`
	InputFingerprints []fingerprint.Digest  `json:"input_fingerprints"`
	ExpectedReport    *ExpectedReport       `json:"expected_report,omitempty"`
	Decision          *DecisionSchema       `json:"decision,omitempty"`
}

// RepositoryRef names the repository an action targets by its logical ID
// and workspace-relative path.
type RepositoryRef struct {
	ID   identity.RepositoryID `json:"id"`
	Path string                `json:"path"`
}

// WriteScope bounds what an action may write: read-only actions carry an
// empty path list, writable actions at least one clean relative path.
type WriteScope struct {
	ReadOnly bool     `json:"read_only"`
	Paths    []string `json:"paths"`
}

// ExpectedReport declares which role report schema an assignment expects
// and at which version.
type ExpectedReport struct {
	Kind          Role `json:"kind"`
	SchemaVersion int  `json:"schema_version"`
}

// Validate checks the response envelope: protocol version, state spelling,
// per-action validity, and the state/action-count contract (ready ≥ 1,
// blocked exactly 1, complete 0).
func (r NextResponse) Validate() error {
	if r.ProtocolVersion != CurrentVersion {
		return fmt.Errorf("protocol: protocol_version %d, want exactly %d", r.ProtocolVersion, CurrentVersion)
	}
	switch r.State {
	case NextReady, NextBlocked, NextComplete:
	default:
		return fmt.Errorf("protocol: state %q must be %q, %q, or %q", r.State, NextReady, NextBlocked, NextComplete)
	}
	for i := range r.Actions {
		if err := r.Actions[i].Validate(); err != nil {
			return fmt.Errorf("protocol: actions[%d]: %w", i, err)
		}
	}
	switch r.State {
	case NextReady:
		if len(r.Actions) < 1 {
			return fmt.Errorf("protocol: ready state must offer at least one action")
		}
	case NextBlocked:
		if len(r.Actions) != 1 {
			return fmt.Errorf("protocol: blocked state must carry exactly one action, got %d", len(r.Actions))
		}
	case NextComplete:
		if len(r.Actions) != 0 {
			return fmt.Errorf("protocol: complete state must carry no actions, got %d", len(r.Actions))
		}
	}
	return nil
}

// Validate checks the action structurally: identifier and token formats,
// enums, path grammar, write-scope consistency, dependency hygiene, and
// the assignment/decision field split.
func (a Action) Validate() error {
	if err := identity.ValidateUUID(string(a.ID)); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	switch a.Kind {
	case KindAssignment, KindDecision:
	default:
		return fmt.Errorf("kind %q must be %q or %q", a.Kind, KindAssignment, KindDecision)
	}
	if err := identity.ValidateToken(string(a.FreshnessToken)); err != nil {
		return fmt.Errorf("freshness_token: %w", err)
	}
	switch a.Workflow {
	case workspacecfg.WorkflowTask, workspacecfg.WorkflowChange:
	default:
		return fmt.Errorf("workflow %q must be %q or %q", a.Workflow, workspacecfg.WorkflowTask, workspacecfg.WorkflowChange)
	}
	if err := validateCleanRelPath(a.Path, "path"); err != nil {
		return err
	}
	if strings.TrimSpace(a.Phase) == "" {
		return fmt.Errorf("phase must not be blank")
	}
	if strings.TrimSpace(a.Reason) == "" {
		return fmt.Errorf("reason must not be blank")
	}
	if strings.TrimSpace(a.Prompt) == "" {
		return fmt.Errorf("prompt must not be blank")
	}
	if err := identity.ValidateUUID(string(a.Repository.ID)); err != nil {
		return fmt.Errorf("repository.id: %w", err)
	}
	if err := validateCleanRelPath(a.Repository.Path, "repository.path"); err != nil {
		return err
	}
	if err := validateRootRelPath(a.WorkingDirectory, "working_directory"); err != nil {
		return err
	}
	if err := validateWriteScope(a.WriteScope); err != nil {
		return err
	}
	if strings.TrimSpace(a.ParallelGroupID) != a.ParallelGroupID {
		return fmt.Errorf("parallel_group_id %q must not carry surrounding whitespace", a.ParallelGroupID)
	}
	if err := validateDependencies(a); err != nil {
		return err
	}
	seenFP := make(map[fingerprint.Digest]bool, len(a.InputFingerprints))
	for i, fp := range a.InputFingerprints {
		if err := fp.Validate(); err != nil {
			return fmt.Errorf("input_fingerprints[%d]: %w", i, err)
		}
		if seenFP[fp] {
			return fmt.Errorf("input_fingerprints[%d] %s is a duplicate", i, fp)
		}
		seenFP[fp] = true
	}

	switch a.Kind {
	case KindAssignment:
		switch a.Role {
		case RoleExplorer, RoleImplementer, RoleReviewer, RoleSkeptic:
		default:
			return fmt.Errorf("role %q must be one of explorer, implementer, reviewer, skeptic", a.Role)
		}
		if a.ExpectedReport == nil {
			return fmt.Errorf("assignment must declare expected_report")
		}
		if a.ExpectedReport.Kind != a.Role {
			return fmt.Errorf("expected_report.kind %q must match the assignment role %q", a.ExpectedReport.Kind, a.Role)
		}
		if a.ExpectedReport.SchemaVersion != CurrentVersion {
			return fmt.Errorf("expected_report.schema_version %d, want exactly %d",
				a.ExpectedReport.SchemaVersion, CurrentVersion)
		}
		if a.Decision != nil {
			return fmt.Errorf("assignment must not carry a decision schema")
		}
	case KindDecision:
		if a.Role != "" {
			return fmt.Errorf("decision must not carry a role")
		}
		if a.ExpectedReport != nil {
			return fmt.Errorf("decision must not declare expected_report")
		}
		if a.Decision == nil {
			return fmt.Errorf("decision must carry a decision schema")
		}
		if err := a.Decision.Validate(); err != nil {
			return fmt.Errorf("decision: %w", err)
		}
	}
	return nil
}

// validateWriteScope enforces the read-only/writable split.
func validateWriteScope(ws WriteScope) error {
	if ws.ReadOnly {
		if len(ws.Paths) != 0 {
			return fmt.Errorf("write_scope: read-only action must declare no paths")
		}
		return nil
	}
	if len(ws.Paths) == 0 {
		return fmt.Errorf("write_scope: writable action must declare at least one path")
	}
	return validateCleanPaths(ws.Paths, "write_scope.paths")
}

// validateDependencies rejects malformed, duplicate, and self dependencies.
func validateDependencies(a Action) error {
	seen := make(map[identity.ActionID]bool, len(a.Dependencies))
	for i, dep := range a.Dependencies {
		if err := identity.ValidateUUID(string(dep)); err != nil {
			return fmt.Errorf("dependencies[%d]: %w", i, err)
		}
		if dep == a.ID {
			return fmt.Errorf("dependencies[%d] %s is the action itself", i, dep)
		}
		if seen[dep] {
			return fmt.Errorf("dependencies[%d] %s is a duplicate", i, dep)
		}
		seen[dep] = true
	}
	return nil
}

// validateCleanRelPath enforces the shared portable path grammar on a
// required field.
func validateCleanRelPath(path, field string) error {
	fail := func(reason string) error {
		return fmt.Errorf("%s %q: %s", field, path, reason)
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

// validateRootRelPath is validateCleanRelPath plus the root slot ".".
func validateRootRelPath(path, field string) error {
	if path == "." {
		return nil
	}
	return validateCleanRelPath(path, field)
}

// validateCleanPaths checks a list of required clean relative paths for
// blanks, escapes, and duplicates.
func validateCleanPaths(paths []string, field string) error {
	seen := make(map[string]bool, len(paths))
	for i, p := range paths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("%s[%d] must not be blank", field, i)
		}
		if err := validateCleanRelPath(p, fmt.Sprintf("%s[%d]", field, i)); err != nil {
			return err
		}
		if seen[p] {
			return fmt.Errorf("%s[%d] %q is a duplicate", field, i, p)
		}
		seen[p] = true
	}
	return nil
}
