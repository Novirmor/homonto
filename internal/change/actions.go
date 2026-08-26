package change

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// validateUnit checks a unit before an assignment is issued for it. An empty
// scope is refused here rather than silently issued, because the protocol
// reads an empty scope as read-only and the guard would then accept
// nothing — a failure that would surface much later and much less clearly.
func validateUnit(u Unit) error {
	if u.Label == "" {
		return fmt.Errorf("change: unit label must not be empty")
	}
	if err := identity.ValidateUUID(string(u.Member.ID)); err != nil {
		return fmt.Errorf("change: unit %q member: %w", u.Label, err)
	}
	if u.Root == "" {
		return fmt.Errorf("change: unit %q has no isolation area", u.Label)
	}
	if len(u.Scope) == 0 {
		return fmt.Errorf("change: unit %q declares no write scope; an unrestricted assignment is not issuable", u.Label)
	}
	if u.Prompt == "" {
		return fmt.Errorf("change: unit %q has no prompt", u.Label)
	}
	return nil
}

// readOnly builds a read-only assignment for one member.
func (e *Engine) readOnly(st State, step Step, role protocol.Role, member Member, reason, prompt string) (assignment.Spec, error) {
	phase, err := Phase(st.Path, step)
	if err != nil {
		return assignment.Spec{}, err
	}
	return assignment.Spec{
		WorkID:     st.WorkID,
		Step:       string(step),
		Generation: st.Generation,
		Template: protocol.Action{
			Kind:              protocol.KindAssignment,
			Workflow:          workspacecfg.WorkflowChange,
			Path:              st.inputPath(),
			Phase:             string(phase),
			Reason:            reason,
			Role:              role,
			Prompt:            prompt,
			Repository:        protocol.RepositoryRef{ID: member.ID, Path: member.Path},
			WorkingDirectory:  member.Path,
			WriteScope:        protocol.WriteScope{ReadOnly: true},
			InputFingerprints: st.inputs(),
			ExpectedReport:    &protocol.ExpectedReport{Kind: role, SchemaVersion: protocol.CurrentVersion},
		},
	}, nil
}

// implementer builds a writable implementer assignment for one unit.
func (e *Engine) implementer(st State, step Step, u Unit, reason string) (assignment.Spec, error) {
	if err := validateUnit(u); err != nil {
		return assignment.Spec{}, err
	}
	phase, err := Phase(st.Path, step)
	if err != nil {
		return assignment.Spec{}, err
	}
	return assignment.Spec{
		WorkID:     st.WorkID,
		Step:       string(step),
		Generation: st.Generation,
		Template: protocol.Action{
			Kind:              protocol.KindAssignment,
			Workflow:          workspacecfg.WorkflowChange,
			Path:              st.inputPath(),
			Phase:             string(phase),
			Reason:            reason,
			Role:              protocol.RoleImplementer,
			Prompt:            u.Prompt,
			Repository:        protocol.RepositoryRef{ID: u.Member.ID, Path: u.Member.Path},
			WorkingDirectory:  u.Root,
			WriteScope:        protocol.WriteScope{Paths: u.Scope},
			InputFingerprints: st.inputs(),
			ExpectedReport: &protocol.ExpectedReport{
				Kind: protocol.RoleImplementer, SchemaVersion: protocol.CurrentVersion,
			},
		},
	}, nil
}

// editSpec builds the host's own document-edit action from an issued
// artifact grant.
func (e *Engine) editSpec(st State, step Step, control Member, grant artifact.EditGrant, reason, prompt string) (assignment.Spec, error) {
	phase, err := Phase(st.Path, step)
	if err != nil {
		return assignment.Spec{}, err
	}
	regions := make([]string, len(grant.Regions))
	for i, r := range grant.Regions {
		regions[i] = string(r)
	}
	return assignment.Spec{
		WorkID:     st.WorkID,
		Step:       string(step),
		Generation: st.Generation,
		Template: protocol.Action{
			Kind:              protocol.KindEdit,
			Workflow:          workspacecfg.WorkflowChange,
			Path:              grant.Ref.Path,
			Phase:             string(phase),
			Reason:            reason,
			Prompt:            prompt,
			Repository:        protocol.RepositoryRef{ID: control.ID, Path: control.Path},
			WorkingDirectory:  control.Path,
			WriteScope:        protocol.WriteScope{Paths: []string{grant.Ref.Path}},
			InputFingerprints: st.inputs(),
			Edit: &protocol.EditPermission{
				GrantID:    grant.ID,
				GrantToken: grant.FreshnessToken,
				Document:   grant.Ref.Path,
				Kind:       string(grant.Ref.Kind),
				Regions:    regions,
			},
		},
	}, nil
}

// decisionSpec builds a blocking human decision.
func (e *Engine) decisionSpec(st State, step Step, control Member, schema decision.Schema, reason string) (assignment.Spec, error) {
	phase, err := Phase(st.Path, step)
	if err != nil {
		return assignment.Spec{}, err
	}
	if err := decision.ValidateSchema(schema); err != nil {
		return assignment.Spec{}, err
	}
	choices := make([]protocol.Choice, len(schema.Choices))
	for i, c := range schema.Choices {
		choices[i] = protocol.Choice{Value: c.Value, Label: c.Label, RequiresRationale: c.RequiresRationale}
	}
	return assignment.Spec{
		WorkID:     st.WorkID,
		Step:       string(step),
		Generation: st.Generation,
		Template: protocol.Action{
			Kind:              protocol.KindDecision,
			Workflow:          workspacecfg.WorkflowChange,
			Path:              st.inputPath(),
			Phase:             string(phase),
			Reason:            reason,
			Prompt:            schema.Prompt,
			Repository:        protocol.RepositoryRef{ID: control.ID, Path: control.Path},
			WorkingDirectory:  control.Path,
			WriteScope:        protocol.WriteScope{ReadOnly: true},
			InputFingerprints: st.inputs(),
			Decision: &protocol.DecisionSchema{
				Kind:       protocol.DecisionKind(schema.Kind),
				Prompt:     schema.Prompt,
				Choices:    choices,
				FindingID:  schema.FindingID,
				QuestionID: schema.QuestionID,
			},
		},
	}, nil
}

// inputPath is the change's identifying document — the proposal for Full,
// fix.md or tweak.md for a preset. Every action names it, so a host always
// knows which document the work is about.
func (s State) inputPath() string {
	kind, err := s.Path.InputKind()
	if err != nil {
		return artifact.ChangesDir
	}
	path, err := s.DocumentPath(kind)
	if err != nil {
		return artifact.ChangesDir
	}
	return path
}

// inputs is the fingerprint set every action of this state pins. An action
// whose inputs no longer match is exactly what reconciliation invalidates.
func (s State) inputs() []fingerprint.Digest {
	var out []fingerprint.Digest
	seen := map[fingerprint.Digest]bool{}
	add := func(d fingerprint.Digest) {
		if d == "" || seen[d] {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	for _, kind := range hostDocumentKinds {
		add(s.Baseline.Document(kind))
	}
	add(s.Baseline.Membership)
	add(s.Baseline.PathClass)
	add(s.Baseline.CheckConfig)
	for _, d := range s.Baseline.Sources {
		add(d)
	}
	if len(out) == 0 {
		// Every action must pin something; a change with no baseline at
		// all would issue actions nothing could invalidate.
		add(fingerprint.Bytes("change-empty-baseline", []byte(s.WorkID)))
	}
	return out
}

// approvalSchema is a scope or design approval gate. Rejecting is the
// choice that needs a reason: approving is agreeing with a document the
// human just read, and rejecting is asking for different work.
func approvalSchema(kind decision.Kind, what, path string) decision.Schema {
	return decision.Schema{
		Kind:   kind,
		Prompt: fmt.Sprintf("Approve the %s in %s before the change continues.", what, path),
		Choices: []decision.Choice{
			{Value: "approve", Label: "Approve"},
			{Value: "revise", Label: "Send it back for revision", RequiresRationale: true},
		},
	}
}

// repairLimitSchema is the decision put to a human when the third
// consecutive repair round has failed. "Keep going" is deliberately not
// first: after three failures the honest options are to change something
// or to stop.
func repairLimitSchema(rounds int) decision.Schema {
	return decision.Schema{
		Kind: decision.KindRepairLimit,
		Prompt: fmt.Sprintf(
			"%d consecutive repair rounds have failed. Continue repairing, accept the "+
				"outstanding findings as documented deviations, or abandon the change?", rounds),
		Choices: []decision.Choice{
			{Value: "abandon", Label: "Abandon the change", RequiresRationale: true},
			{Value: "accept", Label: "Accept the outstanding findings as deviations", RequiresRationale: true},
			{Value: "continue", Label: "Continue repairing", RequiresRationale: true},
		},
	}
}
