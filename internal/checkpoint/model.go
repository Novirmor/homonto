// Package checkpoint defines the portable workflow checkpoint: the small,
// stable, content-free record committed to the control repository that lets
// another machine rebuild local runtime state through `homonto attach`.
//
// The checkpoint carries only portable facts — schema version, workspace
// identity, the configuration fingerprint, the active work's identity and
// phase, per-member branch/commit anchors with source fingerprints,
// unresolved decision gates, a next-step hint, and the portable-handoff
// state. It must never carry local recovery tokens, raw report text,
// command output, credentials, or secrets; TestCheckpointCarriesNoSecrets
// enforces that by reflecting over the schema, so any field whose JSON name
// contains "token", "secret", "output", "report", or "recovery" fails the
// suite. Raw evidence and command output live in the local runtime database
// and are recomputed or rerun, never transported.
//
// # Codec contract
//
// Encode is canonical and byte-stable: members are sorted by repository ID
// and unresolved gates lexicographically before marshalling, nil slices
// encode as empty arrays, and encoding the same value twice yields
// identical bytes. Decode is strict — unknown fields are rejected, trailing
// JSON values are rejected, and schema_version must be exactly
// CurrentSchemaVersion. Decode accepts unsorted slices; canonical order is
// an output property, not an input requirement.
//
// # Validation boundary
//
// Validate cross-checks a checkpoint against the workspace configuration it
// claims to describe: workspace identity, member membership and kinds, and
// the configuration fingerprint must all match. ValidateTransition checks
// the portable-handoff state machine between two successive checkpoints.
// Neither touches the filesystem; Load and Store own all I/O.
package checkpoint

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workname"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// CurrentSchemaVersion is the checkpoint schema version this binary reads
// and writes. Checkpoints declare exactly this version.
const CurrentSchemaVersion = 1

// Checkpoint is the portable workflow state committed to the control
// repository. See the package doc for the secrecy and codec contracts.
type Checkpoint struct {
	SchemaVersion     int                  `json:"schema_version"`
	WorkspaceID       identity.WorkspaceID `json:"workspace_id"`
	ConfigFingerprint fingerprint.Digest   `json:"config_fingerprint"`
	Work              *Work                `json:"work,omitempty"`
	Members           []Member             `json:"members"`
	UnresolvedGates   []string             `json:"unresolved_gates"`
	Next              *Next                `json:"next,omitempty"`
	Handoff           Handoff              `json:"handoff"`
}

// Work names the single active Task or Change the checkpoint describes. It
// is omitted entirely when the workspace has no active work.
type Work struct {
	ID         identity.WorkID       `json:"id"`
	Name       string                `json:"name"`
	Workflow   workspacecfg.Workflow `json:"workflow"`
	Path       string                `json:"path"`
	Phase      string                `json:"phase"`
	Generation uint64                `json:"generation"`
}

// Member anchors one enrolled repository: which branches and commits the
// work started from and how far integration has progressed, plus the
// fingerprint of the member's source content. Non-Git members carry no
// branches or commits — their source fingerprint is their whole anchor.
type Member struct {
	ID                identity.RepositoryID   `json:"id"`
	Kind              workspacecfg.MemberKind `json:"kind"`
	BaseBranch        string                  `json:"base_branch"`
	BaseCommit        string                  `json:"base_commit"`
	IntegrationBranch string                  `json:"integration_branch"`
	IntegrationCommit string                  `json:"integration_commit"`
	SourceFingerprint fingerprint.Digest      `json:"source_fingerprint"`
}

// Next is a content-free resume hint recorded for humans and for attach:
// what the runtime intended to do when the checkpoint was written. It is
// advisory, never an execution obligation.
type Next struct {
	Summary string `json:"summary"`
}

// HandoffState is the portable-handoff state of the checkpoint.
type HandoffState string

const (
	// HandoffLocal: the checkpoint is owned by the machine that wrote it.
	HandoffLocal HandoffState = "local"
	// HandoffTransferable: `homonto handoff --portable` marked the
	// checkpoint for takeover at the recorded generation.
	HandoffTransferable HandoffState = "transferable"
	// HandoffConsumed: `homonto attach` consumed the transfer; the
	// checkpoint may no longer be attached at that generation.
	HandoffConsumed HandoffState = "consumed"
)

// Handoff records the portable-handoff state, the generation that may
// attach, and the transfer identifier correlating one
// transferable→consumed cycle. TransferID is empty while local.
type Handoff struct {
	State      HandoffState   `json:"state"`
	Generation uint64         `json:"generation"`
	TransferID identity.Token `json:"transfer_id,omitempty"`
}

// handoffRank orders the handoff states along the transfer lifecycle.
func handoffRank(s HandoffState) int {
	switch s {
	case HandoffLocal:
		return 0
	case HandoffTransferable:
		return 1
	case HandoffConsumed:
		return 2
	}
	return -1
}

// Validate checks the checkpoint structurally and against cfg: schema
// version, workspace identity, member subset and kinds, configuration
// fingerprint, field formats, and single-checkpoint handoff invariants.
// It never touches the filesystem.
func Validate(cp Checkpoint, cfg workspacecfg.Config) error {
	if cp.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("checkpoint: schema_version %d, want exactly %d: %w",
			cp.SchemaVersion, CurrentSchemaVersion, ErrUnsupportedSchema)
	}
	if err := identity.ValidateUUID(string(cp.WorkspaceID)); err != nil {
		return fmt.Errorf("checkpoint: workspace_id: %w", err)
	}
	if cp.WorkspaceID != cfg.Workspace.ID {
		return fmt.Errorf("checkpoint: workspace_id %s does not match configured workspace %s",
			cp.WorkspaceID, cfg.Workspace.ID)
	}
	if err := cp.ConfigFingerprint.Validate(); err != nil {
		return fmt.Errorf("checkpoint: config_fingerprint: %w", err)
	}
	want, err := workspacecfg.Fingerprint(cfg)
	if err != nil {
		return fmt.Errorf("checkpoint: fingerprint configuration: %w", err)
	}
	if cp.ConfigFingerprint != want {
		return fmt.Errorf("checkpoint: config_fingerprint %s does not match the configuration %s",
			cp.ConfigFingerprint, want)
	}

	if cp.Work != nil {
		if err := validateWork(cp.Work, cfg); err != nil {
			return err
		}
	}

	byID := make(map[identity.RepositoryID]workspacecfg.Member, len(cfg.Members))
	for _, m := range cfg.Members {
		byID[m.ID] = m
	}
	seen := make(map[identity.RepositoryID]bool, len(cp.Members))
	for i := range cp.Members {
		m := &cp.Members[i]
		if err := identity.ValidateUUID(string(m.ID)); err != nil {
			return fmt.Errorf("checkpoint: members[%d].id: %w", i, err)
		}
		if seen[m.ID] {
			return fmt.Errorf("checkpoint: members[%d].id %s is a duplicate", i, m.ID)
		}
		seen[m.ID] = true
		cfgMember, ok := byID[m.ID]
		if !ok {
			return fmt.Errorf("checkpoint: members[%d].id %s is not a configured member", i, m.ID)
		}
		switch m.Kind {
		case workspacecfg.KindGit, workspacecfg.KindNonGit:
		default:
			return fmt.Errorf("checkpoint: members[%d].kind %q must be %q or %q",
				i, m.Kind, workspacecfg.KindGit, workspacecfg.KindNonGit)
		}
		if m.Kind != cfgMember.Kind {
			return fmt.Errorf("checkpoint: members[%d] %s kind %q does not match configured kind %q",
				i, m.ID, m.Kind, cfgMember.Kind)
		}
		if err := m.SourceFingerprint.Validate(); err != nil {
			return fmt.Errorf("checkpoint: members[%d].source_fingerprint: %w", i, err)
		}
		if err := validateMemberAnchors(i, m); err != nil {
			return err
		}
	}

	gates := make(map[string]bool, len(cp.UnresolvedGates))
	for i, g := range cp.UnresolvedGates {
		if strings.TrimSpace(g) == "" {
			return fmt.Errorf("checkpoint: unresolved_gates[%d] must not be blank", i)
		}
		if gates[g] {
			return fmt.Errorf("checkpoint: unresolved_gates[%d] %q is a duplicate", i, g)
		}
		gates[g] = true
	}

	if cp.Next != nil && strings.TrimSpace(cp.Next.Summary) == "" {
		return fmt.Errorf("checkpoint: next.summary must not be blank")
	}

	return validateHandoffInvariants(cp.Handoff)
}

// validateWork checks the active-work fields, including that the work runs
// the workflow the workspace is configured for.
func validateWork(w *Work, cfg workspacecfg.Config) error {
	if err := identity.ValidateUUID(string(w.ID)); err != nil {
		return fmt.Errorf("checkpoint: work.id: %w", err)
	}
	if err := workname.Validate(w.Name); err != nil {
		return fmt.Errorf("checkpoint: work.name: %w", err)
	}
	switch w.Workflow {
	case workspacecfg.WorkflowTask, workspacecfg.WorkflowChange:
	default:
		return fmt.Errorf("checkpoint: work.workflow %q must be %q or %q",
			w.Workflow, workspacecfg.WorkflowTask, workspacecfg.WorkflowChange)
	}
	if w.Workflow != cfg.Workspace.Workflow {
		return fmt.Errorf("checkpoint: work.workflow %q does not match configured workflow %q",
			w.Workflow, cfg.Workspace.Workflow)
	}
	if err := validateCleanRelPath(w.Path, "checkpoint: work.path"); err != nil {
		return err
	}
	if strings.TrimSpace(w.Phase) == "" {
		return fmt.Errorf("checkpoint: work.phase must not be blank")
	}
	if w.Generation < 1 {
		return fmt.Errorf("checkpoint: work.generation must be at least 1, got %d", w.Generation)
	}
	return nil
}

// validateMemberAnchors enforces the git/non-git anchor rules and commit-id
// shape (40- or 64-character lowercase hex).
func validateMemberAnchors(i int, m *Member) error {
	label := fmt.Sprintf("checkpoint: members[%d]", i)
	for _, c := range []struct {
		field, value string
	}{
		{"base_commit", m.BaseCommit},
		{"integration_commit", m.IntegrationCommit},
	} {
		if c.value == "" {
			continue
		}
		if !isCommitID(c.value) {
			return fmt.Errorf("%s.%s %q must be 40 or 64 lowercase hex characters", label, c.field, c.value)
		}
	}
	if m.Kind == workspacecfg.KindGit {
		if m.BaseBranch == "" {
			return fmt.Errorf("%s.base_branch must not be empty for a git member", label)
		}
		if m.BaseCommit == "" {
			return fmt.Errorf("%s.base_commit must not be empty for a git member", label)
		}
		if m.IntegrationBranch == "" {
			return fmt.Errorf("%s.integration_branch must not be empty for a git member", label)
		}
		return nil
	}
	switch {
	case m.BaseBranch != "":
		return fmt.Errorf("%s.base_branch must be empty for a non-git member", label)
	case m.BaseCommit != "":
		return fmt.Errorf("%s.base_commit must be empty for a non-git member", label)
	case m.IntegrationBranch != "":
		return fmt.Errorf("%s.integration_branch must be empty for a non-git member", label)
	case m.IntegrationCommit != "":
		return fmt.Errorf("%s.integration_commit must be empty for a non-git member", label)
	}
	return nil
}

// isCommitID reports whether s looks like a Git object id (SHA-1 or SHA-256
// hex spelling).
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

// validateHandoffInvariants checks the single-checkpoint handoff rules:
// state spelling, generation floor, and transfer-id presence.
func validateHandoffInvariants(h Handoff) error {
	switch h.State {
	case HandoffLocal, HandoffTransferable, HandoffConsumed:
	default:
		return fmt.Errorf("checkpoint: handoff.state %q must be %q, %q, or %q",
			h.State, HandoffLocal, HandoffTransferable, HandoffConsumed)
	}
	if h.Generation < 1 {
		return fmt.Errorf("checkpoint: handoff.generation must be at least 1, got %d", h.Generation)
	}
	switch h.State {
	case HandoffLocal:
		if h.TransferID != "" {
			return fmt.Errorf("checkpoint: handoff.transfer_id must be empty while local")
		}
	default:
		if err := identity.ValidateToken(string(h.TransferID)); err != nil {
			return fmt.Errorf("checkpoint: handoff.transfer_id: %w", err)
		}
	}
	return nil
}

// ValidateTransition checks the portable-handoff state machine between two
// successive checkpoints of one workspace:
//
//   - local→transferable is `homonto handoff --portable`: the handoff
//     generation increments by exactly one and a fresh transfer id appears.
//   - transferable→consumed is `homonto attach`: same generation, same
//     transfer id.
//   - backwards transitions (transferable→local, consumed→*) are only
//     legal as an explicit generation bump — the forced-takeover and
//     cancel-handoff paths.
//   - staying in the same state keeps the generation: ordinary checkpoint
//     edits never move the handoff generation.
//
// The workspace identity must not change across a transition. ValidateTransition
// does not re-run Validate; callers validate each checkpoint separately.
func ValidateTransition(prev, next Checkpoint) error {
	if prev.WorkspaceID != next.WorkspaceID {
		return fmt.Errorf("checkpoint: transition changes workspace identity from %s to %s",
			prev.WorkspaceID, next.WorkspaceID)
	}
	pr, nr := handoffRank(prev.Handoff.State), handoffRank(next.Handoff.State)
	if pr < 0 || nr < 0 {
		return fmt.Errorf("checkpoint: transition carries an invalid handoff state (%q → %q)",
			prev.Handoff.State, next.Handoff.State)
	}
	pg, ng := prev.Handoff.Generation, next.Handoff.Generation
	switch {
	case pr == nr:
		if ng != pg {
			return fmt.Errorf("checkpoint: %s→%s must keep the generation (%d → %d)",
				prev.Handoff.State, next.Handoff.State, pg, ng)
		}
		return nil
	case pr == 0 && nr == 1:
		if ng != pg+1 {
			return fmt.Errorf("checkpoint: local→transferable must increment the generation by exactly one (%d → %d)",
				pg, ng)
		}
		return nil
	case pr == 1 && nr == 2:
		if ng != pg {
			return fmt.Errorf("checkpoint: transferable→consumed must keep the generation (%d → %d)", pg, ng)
		}
		if next.Handoff.TransferID != prev.Handoff.TransferID {
			return fmt.Errorf("checkpoint: transferable→consumed must keep the transfer id")
		}
		return nil
	case nr < pr:
		if ng <= pg {
			return fmt.Errorf("checkpoint: backwards transition %s→%s requires a generation bump (%d → %d)",
				prev.Handoff.State, next.Handoff.State, pg, ng)
		}
		return nil
	default:
		return fmt.Errorf("checkpoint: illegal transition %s→%s (states advance one step at a time)",
			prev.Handoff.State, next.Handoff.State)
	}
}

// validateCleanRelPath enforces the portable path grammar shared with the
// configuration: non-empty, clean, root-relative, slash-only.
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

// canonical returns a copy of cp in canonical form: members sorted by ID,
// unresolved gates sorted, nil slices materialized as empty. The receiver
// is not mutated.
func canonical(cp Checkpoint) Checkpoint {
	out := cp
	out.Members = make([]Member, 0, len(cp.Members))
	out.Members = append(out.Members, cp.Members...)
	sort.SliceStable(out.Members, func(i, j int) bool {
		return out.Members[i].ID < out.Members[j].ID
	})
	out.UnresolvedGates = make([]string, 0, len(cp.UnresolvedGates))
	out.UnresolvedGates = append(out.UnresolvedGates, cp.UnresolvedGates...)
	sort.Strings(out.UnresolvedGates)
	return out
}
