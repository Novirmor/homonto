package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// ReportSubmission is the envelope a host sends to answer an assignment:
// which action, with which freshness token and role, from which host
// session, and the role-specific report payload carried verbatim as raw
// JSON until DecodeReport selects the schema.
type ReportSubmission struct {
	ProtocolVersion int               `json:"protocol_version"`
	ActionID        identity.ActionID `json:"action_id"`
	FreshnessToken  identity.Token    `json:"freshness_token"`
	Role            Role              `json:"role"`
	Session         Session           `json:"session"`
	Report          json.RawMessage   `json:"report"`
}

// Session records the host process provenance of a submission.
type Session struct {
	HostID     identity.SessionID `json:"host_id"`
	Hostname   string             `json:"hostname"`
	PID        int                `json:"pid"`
	Executable string             `json:"executable"`
	StartedAt  time.Time          `json:"started_at"`
}

// Severity grades a finding. Critical and high findings block advancement
// until resolved or explicitly accepted by a human decision.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// Finding is one issue raised by a reviewer or skeptic, with the evidence
// backing it and what to do about it.
type Finding struct {
	ID             string   `json:"id"`
	Severity       Severity `json:"severity"`
	Summary        string   `json:"summary"`
	Evidence       []string `json:"evidence"`
	Recommendation string   `json:"recommendation"`
}

// Validate checks the finding's fields. Uniqueness of IDs within a report
// is checked at the report level.
func (f Finding) Validate() error {
	if strings.TrimSpace(f.ID) == "" {
		return fmt.Errorf("finding id must not be blank")
	}
	switch f.Severity {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow:
	default:
		return fmt.Errorf("finding %s severity %q must be critical, high, medium, or low", f.ID, f.Severity)
	}
	if strings.TrimSpace(f.Summary) == "" {
		return fmt.Errorf("finding %s summary must not be blank", f.ID)
	}
	if len(f.Evidence) == 0 {
		return fmt.Errorf("finding %s must carry at least one evidence entry", f.ID)
	}
	for i, e := range f.Evidence {
		if strings.TrimSpace(e) == "" {
			return fmt.Errorf("finding %s evidence[%d] must not be blank", f.ID, i)
		}
	}
	if strings.TrimSpace(f.Recommendation) == "" {
		return fmt.Errorf("finding %s recommendation must not be blank", f.ID)
	}
	return nil
}

// Question is an open question an agent raises for a human.
type Question struct {
	ID          string `json:"id"`
	Text        string `json:"text"`
	Consequence string `json:"consequence"`
}

// Validate checks the question's fields.
func (q Question) Validate() error {
	if strings.TrimSpace(q.ID) == "" {
		return fmt.Errorf("question id must not be blank")
	}
	if strings.TrimSpace(q.Text) == "" {
		return fmt.Errorf("question %s text must not be blank", q.ID)
	}
	if strings.TrimSpace(q.Consequence) == "" {
		return fmt.Errorf("question %s consequence must not be blank", q.ID)
	}
	return nil
}

// Report is the sealed interface implemented by the four role report
// payloads. Callers type-switch on the concrete type returned by
// DecodeReport.
type Report interface {
	Validate() error
	isReport()
}

// ExplorerReport is the explorer's read-only survey of the terrain.
type ExplorerReport struct {
	Facts       []string   `json:"facts"`
	Constraints []string   `json:"constraints"`
	Surfaces    []string   `json:"surfaces"`
	Tests       []string   `json:"tests"`
	Questions   []Question `json:"questions"`
}

// ImplementerReport is the implementer's record of the change: the
// material it produced, the paths it touched, and the checks it ran.
type ImplementerReport struct {
	Material         Material   `json:"material"`
	ChangedPaths     []string   `json:"changed_paths"`
	AssignmentChecks []string   `json:"assignment_checks"`
	Questions        []Question `json:"questions"`
}

// ReviewerReport is the reviewer's acceptance and finding record.
type ReviewerReport struct {
	Acceptance []string   `json:"acceptance"`
	Findings   []Finding  `json:"findings"`
	Questions  []Question `json:"questions"`
}

// SkepticReport is the skeptic's adversarial review record.
type SkepticReport struct {
	Assumptions []string   `json:"assumptions"`
	Findings    []Finding  `json:"findings"`
	Questions   []Question `json:"questions"`
}

// MaterialKind selects how an implementer's output is captured.
type MaterialKind string

const (
	// MaterialGitCommit: output committed on the integration branch.
	MaterialGitCommit MaterialKind = "git_commit"
	// MaterialSnapshotPatch: output captured as a snapshot patch set.
	MaterialSnapshotPatch MaterialKind = "snapshot_patch"
)

// Material is the implementer's change carrier: a commit reference, a
// patch manifest, and the digest of the produced content.
type Material struct {
	Kind          MaterialKind       `json:"kind"`
	Commit        string             `json:"commit,omitempty"`
	PatchManifest []string           `json:"patch_manifest,omitempty"`
	Content       fingerprint.Digest `json:"content"`
}

func (*ExplorerReport) isReport()    {}
func (*ImplementerReport) isReport() {}
func (*ReviewerReport) isReport()    {}
func (*SkepticReport) isReport()     {}

// Validate checks the explorer payload.
func (r ExplorerReport) Validate() error {
	for _, list := range []struct {
		name string
		vals []string
	}{
		{"facts", r.Facts},
		{"constraints", r.Constraints},
		{"surfaces", r.Surfaces},
		{"tests", r.Tests},
	} {
		for i, v := range list.vals {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("%s[%d] must not be blank", list.name, i)
			}
		}
	}
	return validateQuestions(r.Questions)
}

// Validate checks the implementer payload.
func (r ImplementerReport) Validate() error {
	if err := r.Material.Validate(); err != nil {
		return fmt.Errorf("material: %w", err)
	}
	if err := validateCleanPaths(r.ChangedPaths, "changed_paths"); err != nil {
		return err
	}
	seen := make(map[string]bool, len(r.AssignmentChecks))
	for i, c := range r.AssignmentChecks {
		if strings.TrimSpace(c) == "" {
			return fmt.Errorf("assignment_checks[%d] must not be blank", i)
		}
		if seen[c] {
			return fmt.Errorf("assignment_checks[%d] %q is a duplicate", i, c)
		}
		seen[c] = true
	}
	return validateQuestions(r.Questions)
}

// Validate checks the material's kind-specific rules.
func (m Material) Validate() error {
	switch m.Kind {
	case MaterialGitCommit:
		if m.Commit == "" {
			return fmt.Errorf("git_commit material must carry a commit")
		}
		if !isCommitID(m.Commit) {
			return fmt.Errorf("commit %q must be 40 or 64 lowercase hex characters", m.Commit)
		}
		if len(m.PatchManifest) != 0 {
			return fmt.Errorf("git_commit material must not carry a patch manifest")
		}
	case MaterialSnapshotPatch:
		if len(m.PatchManifest) == 0 {
			return fmt.Errorf("snapshot_patch material must carry a patch manifest")
		}
		for i, p := range m.PatchManifest {
			if strings.TrimSpace(p) == "" {
				return fmt.Errorf("patch_manifest[%d] must not be blank", i)
			}
		}
		if m.Commit != "" {
			return fmt.Errorf("snapshot_patch material must not carry a commit")
		}
	default:
		return fmt.Errorf("kind %q must be %q or %q", m.Kind, MaterialGitCommit, MaterialSnapshotPatch)
	}
	if err := m.Content.Validate(); err != nil {
		return fmt.Errorf("content: %w", err)
	}
	return nil
}

// Validate checks the reviewer payload.
func (r ReviewerReport) Validate() error {
	for i, a := range r.Acceptance {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("acceptance[%d] must not be blank", i)
		}
	}
	if err := validateFindings(r.Findings); err != nil {
		return err
	}
	return validateQuestions(r.Questions)
}

// Validate checks the skeptic payload.
func (r SkepticReport) Validate() error {
	for i, a := range r.Assumptions {
		if strings.TrimSpace(a) == "" {
			return fmt.Errorf("assumptions[%d] must not be blank", i)
		}
	}
	if err := validateFindings(r.Findings); err != nil {
		return err
	}
	return validateQuestions(r.Questions)
}

// validateFindings checks each finding and the uniqueness of their IDs.
func validateFindings(findings []Finding) error {
	seen := make(map[string]bool, len(findings))
	for i := range findings {
		if err := findings[i].Validate(); err != nil {
			return fmt.Errorf("findings[%d]: %w", i, err)
		}
		if seen[findings[i].ID] {
			return fmt.Errorf("findings[%d] id %q is a duplicate", i, findings[i].ID)
		}
		seen[findings[i].ID] = true
	}
	return nil
}

// validateQuestions checks each question and the uniqueness of their IDs.
func validateQuestions(questions []Question) error {
	seen := make(map[string]bool, len(questions))
	for i := range questions {
		if err := questions[i].Validate(); err != nil {
			return fmt.Errorf("questions[%d]: %w", i, err)
		}
		if seen[questions[i].ID] {
			return fmt.Errorf("questions[%d] id %q is a duplicate", i, questions[i].ID)
		}
		seen[questions[i].ID] = true
	}
	return nil
}

// isCommitID reports whether s looks like a Git object id.
func isCommitID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// DecodeReport strictly decodes the report payload of a submission using
// the schema selected by the action's role, validates the decoded payload,
// and returns it as the concrete Report implementation — callers
// type-switch on *ExplorerReport, *ImplementerReport, *ReviewerReport, or
// *SkepticReport. The action must be an assignment; decision actions
// answer through DecisionSubmission instead.
func DecodeReport(action Action, raw json.RawMessage) (Report, error) {
	if action.Kind != KindAssignment {
		return nil, fmt.Errorf("protocol: action %s is a %s, not an assignment; reports answer assignments only", action.ID, action.Kind)
	}
	var report Report
	switch action.Role {
	case RoleExplorer:
		report = &ExplorerReport{}
	case RoleImplementer:
		report = &ImplementerReport{}
	case RoleReviewer:
		report = &ReviewerReport{}
	case RoleSkeptic:
		report = &SkepticReport{}
	default:
		return nil, fmt.Errorf("protocol: action %s carries no role to select a report schema", action.ID)
	}
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, fmt.Errorf("%w: report payload is missing", ErrInvalidJSON)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(report); err != nil {
		if isUnknownFieldError(err) {
			return nil, fmt.Errorf("%w: %w", ErrUnknownField, err)
		}
		return nil, fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	if _, err := dec.Token(); !isEOF(err) {
		return nil, fmt.Errorf("%w: %w", ErrTrailingData, errIfNil(err))
	}
	if err := report.Validate(); err != nil {
		return nil, err
	}
	return report, nil
}

// ValidateSubmission checks the report envelope against the assignment it
// claims to answer: protocol version, action identity and kind, role
// agreement, freshness token format, and session provenance. It does not
// decode the report payload (use DecodeReport) and does not judge the
// token's freshness against the stored assignment hash — that is the
// assignment store's job; see the package doc.
func ValidateSubmission(action Action, submission ReportSubmission) error {
	if submission.ProtocolVersion != CurrentVersion {
		return fmt.Errorf("protocol: protocol_version %d, want exactly %d",
			submission.ProtocolVersion, CurrentVersion)
	}
	if action.Kind != KindAssignment {
		return fmt.Errorf("protocol: action %s is a %s, not an assignment; reports answer assignments only", action.ID, action.Kind)
	}
	if submission.ActionID != action.ID {
		return fmt.Errorf("protocol: submission answers action %s, not %s", submission.ActionID, action.ID)
	}
	if submission.Role != action.Role {
		return fmt.Errorf("protocol: submission role %q does not match the assignment role %q",
			submission.Role, action.Role)
	}
	switch submission.Role {
	case RoleExplorer, RoleImplementer, RoleReviewer, RoleSkeptic:
	default:
		return fmt.Errorf("protocol: role %q must be one of explorer, implementer, reviewer, skeptic", submission.Role)
	}
	if err := identity.ValidateToken(string(submission.FreshnessToken)); err != nil {
		return fmt.Errorf("protocol: freshness_token: %w", err)
	}
	if len(submission.Report) == 0 || bytes.Equal(bytes.TrimSpace(submission.Report), []byte("null")) {
		return fmt.Errorf("protocol: report payload is missing")
	}
	return submission.Session.Validate()
}

// Validate checks the session provenance fields.
func (s Session) Validate() error {
	if err := identity.ValidateUUID(string(s.HostID)); err != nil {
		return fmt.Errorf("session.host_id: %w", err)
	}
	if strings.TrimSpace(s.Hostname) == "" {
		return fmt.Errorf("session.hostname must not be blank")
	}
	if s.PID <= 0 {
		return fmt.Errorf("session.pid must be positive, got %d", s.PID)
	}
	if strings.TrimSpace(s.Executable) == "" {
		return fmt.Errorf("session.executable must not be blank")
	}
	if s.StartedAt.IsZero() {
		return fmt.Errorf("session.started_at must not be the zero time")
	}
	return nil
}
