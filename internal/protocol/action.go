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
	// KindEdit: a document the HOST itself writes, under a single-use
	// artifact edit grant. It is not subagent work and carries no role:
	// the phases where the host authors a workflow document — drafting a
	// task's goal and checklist, writing a proposal or a design — are the
	// host's own, and this is how it is told which document is open, which
	// regions of it, and with what permission.
	KindEdit ActionKind = "edit"
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
	Edit              *EditPermission       `json:"edit,omitempty"`
}

// EditPermission is the single-use grant an edit action carries: which
// document is open, which of its regions, and the grant id and token the
// host presents to the write guard and back to Homonto when it is done.
// The grant, not the write scope, is what actually authorizes the write.
type EditPermission struct {
	GrantID    identity.ActionID `json:"grant_id"`
	GrantToken identity.Token    `json:"grant_token"`
	Document   string            `json:"document"`
	Kind       string            `json:"document_kind"`
	Regions    []string          `json:"regions"`
}

// Validate checks the edit permission: well-formed grant, a clean document
// path, a named kind, and at least one region.
func (e EditPermission) Validate() error {
	if err := identity.ValidateUUID(string(e.GrantID)); err != nil {
		return fmt.Errorf("grant_id: %w", err)
	}
	if err := identity.ValidateToken(string(e.GrantToken)); err != nil {
		return fmt.Errorf("grant_token: %w", err)
	}
	if err := validateCleanRelPath(e.Document, "document"); err != nil {
		return err
	}
	if strings.TrimSpace(e.Kind) == "" {
		return fmt.Errorf("document_kind must not be blank")
	}
	if len(e.Regions) == 0 {
		return fmt.Errorf("an edit permission must open at least one region")
	}
	seen := make(map[string]bool, len(e.Regions))
	for i, r := range e.Regions {
		if strings.TrimSpace(r) == "" {
			return fmt.Errorf("regions[%d] must not be blank", i)
		}
		if seen[r] {
			return fmt.Errorf("regions[%d] %q is a duplicate", i, r)
		}
		seen[r] = true
	}
	return nil
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
	if err := validateActionIdentity(a); err != nil {
		return err
	}
	if err := validateActionBrief(a); err != nil {
		return err
	}
	if err := validateActionTarget(a); err != nil {
		return err
	}
	if err := validateActionRelations(a); err != nil {
		return err
	}
	switch a.Kind {
	case KindAssignment:
		return validateAssignment(a)
	case KindDecision:
		return validateDecision(a)
	case KindEdit:
		return validateEdit(a)
	}
	return nil
}

// validateActionIdentity checks what the action is: id and freshness
// token formats, a known kind, and a known workflow.
func validateActionIdentity(a Action) error {
	if err := identity.ValidateUUID(string(a.ID)); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	switch a.Kind {
	case KindAssignment, KindDecision, KindEdit:
	default:
		return fmt.Errorf("kind %q must be %q, %q, or %q", a.Kind, KindAssignment, KindDecision, KindEdit)
	}
	if err := identity.ValidateToken(string(a.FreshnessToken)); err != nil {
		return fmt.Errorf("freshness_token: %w", err)
	}
	switch a.Workflow {
	case workspacecfg.WorkflowTask, workspacecfg.WorkflowChange:
	default:
		return fmt.Errorf("workflow %q must be %q or %q", a.Workflow, workspacecfg.WorkflowTask, workspacecfg.WorkflowChange)
	}
	return nil
}

// validateActionBrief checks the fields the host reads beyond identity:
// where the action sits (path, phase) and why it runs (reason, prompt).
func validateActionBrief(a Action) error {
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
	return nil
}

// validateActionTarget checks where the action runs and what it may touch
// there: the repository ref, the working directory, and the write scope.
func validateActionTarget(a Action) error {
	if err := identity.ValidateUUID(string(a.Repository.ID)); err != nil {
		return fmt.Errorf("repository.id: %w", err)
	}
	if err := validateCleanRelPath(a.Repository.Path, "repository.path"); err != nil {
		return err
	}
	if err := validateRootRelPath(a.WorkingDirectory, "working_directory"); err != nil {
		return err
	}
	return validateWriteScope(a.WriteScope)
}

// validateActionRelations checks how the action stands among the rest of
// the plan: a trimmed parallel group id, well-formed dependencies, and
// unique valid input fingerprints.
func validateActionRelations(a Action) error {
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
	return nil
}

// validateAssignment enforces the assignment field split: a known role, a
// matching expected report, and neither a decision schema nor an edit
// permission.
func validateAssignment(a Action) error {
	switch a.Role {
	case RoleExplorer, RoleImplementer, RoleReviewer, RoleSkeptic:
	default:
		return fmt.Errorf("role %q must be one of explorer, implementer, reviewer, skeptic", a.Role)
	}
	if err := validateExpectedReport(a.ExpectedReport, a.Role); err != nil {
		return err
	}
	if a.Decision != nil {
		return fmt.Errorf("assignment must not carry a decision schema")
	}
	if a.Edit != nil {
		return fmt.Errorf("assignment must not carry an edit permission")
	}
	return nil
}

// validateDecision enforces the decision field split: no role and no
// expected report, exactly one decision schema, and no edit permission.
func validateDecision(a Action) error {
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
	if a.Edit != nil {
		return fmt.Errorf("decision must not carry an edit permission")
	}
	return nil
}

// validateEdit enforces the edit field split: the host's own write carries
// no role, report, or decision schema, only the edit permission itself,
// over a writable scope.
func validateEdit(a Action) error {
	if a.Role != "" {
		return fmt.Errorf("edit must not carry a role; it is the host's own write, not subagent work")
	}
	if a.ExpectedReport != nil {
		return fmt.Errorf("edit must not declare expected_report; it is answered by accepting the edit")
	}
	if a.Decision != nil {
		return fmt.Errorf("edit must not carry a decision schema")
	}
	if a.Edit == nil {
		return fmt.Errorf("edit must carry an edit permission")
	}
	if err := a.Edit.Validate(); err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	if a.WriteScope.ReadOnly {
		return fmt.Errorf("edit must be writable; it exists to write one document")
	}
	return nil
}

// validateExpectedReport checks the report an assignment must declare:
// present, matching the assignment's role, at the current schema version.
func validateExpectedReport(er *ExpectedReport, role Role) error {
	if er == nil {
		return fmt.Errorf("assignment must declare expected_report")
	}
	if er.Kind != role {
		return fmt.Errorf("expected_report.kind %q must match the assignment role %q", er.Kind, role)
	}
	if er.SchemaVersion != CurrentVersion {
		return fmt.Errorf("expected_report.schema_version %d, want exactly %d",
			er.SchemaVersion, CurrentVersion)
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
