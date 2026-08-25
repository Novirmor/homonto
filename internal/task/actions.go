package task

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

// Member is one confirmed repository of the workspace.
type Member struct {
	ID   identity.RepositoryID
	Path string // workspace-relative
	Git  bool
}

// Partition is one unit of parallel implementation work: which member, in
// which isolation area, allowed to write which paths, addressing which
// checklist items.
type Partition struct {
	// Label distinguishes the partition in assignment reasons and logs.
	Label string
	// Member is the repository the work happens in.
	Member Member
	// Items are the 1-based checklist indexes this partition addresses.
	Items []int
	// Integration marks the unit that COMBINES the parallel results for a
	// member rather than producing one of them. It changes what the
	// isolation area is and what its result is compared against, and only
	// the environment can tell the difference.
	Integration bool
	// Root is the isolation area, workspace-relative — a Git worktree or
	// a non-Git snapshot directory. Partition leaves it empty; the engine
	// fills it in through Environment.Isolate once the action id exists.
	Root string
	// Base identifies what the isolation area started from — a commit for
	// a Git member, a snapshot digest for a non-Git one. Integration needs
	// it: combining results means replaying each one against the state it
	// was actually written on top of.
	Base string
	// Scope is the paths the implementer may write, isolation-relative.
	// It must be non-empty: an assignment with an unrestricted scope is an
	// assignment with no boundary at all.
	Scope []string
	// Prompt is the work statement handed to the implementer.
	Prompt string
}

// Validate checks a partition before an assignment is issued for it. An
// empty scope is refused here rather than silently issued, because the
// protocol reads an empty scope as read-only and the guard would then
// accept nothing — a failure that would surface much later and much less
// clearly.
func (p Partition) Validate() error {
	if p.Label == "" {
		return fmt.Errorf("task: partition label must not be empty")
	}
	if err := identity.ValidateUUID(string(p.Member.ID)); err != nil {
		return fmt.Errorf("task: partition %q member: %w", p.Label, err)
	}
	if p.Root == "" {
		return fmt.Errorf("task: partition %q has no isolation area", p.Label)
	}
	if len(p.Scope) == 0 {
		return fmt.Errorf("task: partition %q declares no write scope; an unrestricted assignment is not issuable", p.Label)
	}
	if p.Prompt == "" {
		return fmt.Errorf("task: partition %q has no prompt", p.Label)
	}
	return nil
}

// readOnly builds a read-only assignment spec for one member.
func (e *Engine) readOnly(st State, step Step, role protocol.Role, member Member, reason, prompt string) (assignment.Spec, error) {
	phase, err := step.Phase()
	if err != nil {
		return assignment.Spec{}, err
	}
	return assignment.Spec{
		WorkID:     st.WorkID,
		Step:       string(step),
		Generation: st.Generation,
		Template: protocol.Action{
			Kind:              protocol.KindAssignment,
			Workflow:          workspacecfg.WorkflowTask,
			Path:              st.documentPath(),
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

// implementer builds a writable implementer assignment for one partition.
func (e *Engine) implementer(st State, step Step, p Partition, reason string) (assignment.Spec, error) {
	if err := p.Validate(); err != nil {
		return assignment.Spec{}, err
	}
	phase, err := step.Phase()
	if err != nil {
		return assignment.Spec{}, err
	}
	return assignment.Spec{
		WorkID:     st.WorkID,
		Step:       string(step),
		Generation: st.Generation,
		Template: protocol.Action{
			Kind:              protocol.KindAssignment,
			Workflow:          workspacecfg.WorkflowTask,
			Path:              st.documentPath(),
			Phase:             string(phase),
			Reason:            reason,
			Role:              protocol.RoleImplementer,
			Prompt:            p.Prompt,
			Repository:        protocol.RepositoryRef{ID: p.Member.ID, Path: p.Member.Path},
			WorkingDirectory:  p.Root,
			WriteScope:        protocol.WriteScope{Paths: p.Scope},
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
	phase, err := step.Phase()
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
			Workflow:          workspacecfg.WorkflowTask,
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
func (e *Engine) decisionSpec(st State, step Step, control Member, schema decision.Schema, reason, prompt string) (assignment.Spec, error) {
	phase, err := step.Phase()
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
			Workflow:          workspacecfg.WorkflowTask,
			Path:              st.documentPath(),
			Phase:             string(phase),
			Reason:            reason,
			Prompt:            prompt,
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

// documentPath is the task document's control-root-relative path. Names
// are validated at Start, so the only way this fails is a state that was
// written by something other than this engine — and a path that cannot be
// built would produce an action that cannot validate anyway, which is the
// louder failure.
func (s State) documentPath() string {
	path, err := artifact.TaskPath(s.Name)
	if err != nil {
		return artifact.TasksDir + "/" + s.Name + ".md"
	}
	return path
}

// inputs is the fingerprint set every action of this state pins. An action
// whose inputs no longer match is exactly what Reconcile invalidates.
func (s State) inputs() []fingerprint.Digest {
	out := make([]fingerprint.Digest, 0, 4+len(s.Baseline.Sources))
	for _, d := range []fingerprint.Digest{
		s.Baseline.Document, s.Baseline.Membership,
		s.Baseline.PathClass, s.Baseline.CheckConfig,
	} {
		if d != "" {
			out = append(out, d)
		}
	}
	seen := make(map[fingerprint.Digest]bool, len(out))
	for _, d := range out {
		seen[d] = true
	}
	for _, d := range s.Baseline.Sources {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return out
}

// repairLimitSchema is the decision put to a human when the third
// consecutive repair round has failed. "Keep going" is deliberately not
// the default and not first: after three failures the honest options are
// to change something or to stop.
func repairLimitSchema(rounds int) decision.Schema {
	return decision.Schema{
		Kind: decision.KindRepairLimit,
		Prompt: fmt.Sprintf(
			"%d consecutive repair rounds have failed. Continue repairing, accept the "+
				"outstanding findings as documented deviations, or abandon the task?", rounds),
		Choices: []decision.Choice{
			{Value: "abandon", Label: "Abandon the task", RequiresRationale: true},
			{Value: "accept", Label: "Accept the outstanding findings as deviations", RequiresRationale: true},
			{Value: "continue", Label: "Continue repairing", RequiresRationale: true},
		},
	}
}

// questionSchema is the gate for one consequential question an agent
// raised. A subagent cannot satisfy a human decision gate itself.
func questionSchema(questionID, question string) decision.Schema {
	return decision.Schema{
		Kind:       decision.KindAnswerQuestion,
		Prompt:     question,
		QuestionID: questionID,
		Choices: []decision.Choice{
			{Value: "answered", Label: "Answer the question"},
		},
	}
}
