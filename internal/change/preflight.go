package change

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workname"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Typed preflight errors.
var (
	// ErrUnknownPreflight: no classification candidate with that id.
	ErrUnknownPreflight = errors.New("change: no such classification candidate")
	// ErrPreflightFinished: the candidate was already confirmed or
	// abandoned.
	ErrPreflightFinished = errors.New("change: the classification candidate has finished")
	// ErrNotConfirmable: the candidate has no suggestion yet, so there is
	// nothing to confirm.
	ErrNotConfirmable = errors.New("change: the classification is not ready to confirm")
	// ErrOverrideNeedsRationale: the human chose a path other than the
	// suggested one without saying why.
	ErrOverrideNeedsRationale = errors.New("change: overriding the suggested path requires a rationale")
	// ErrNameTaken: an active change already uses that name.
	ErrNameTaken = errors.New("change: an active change already uses that name")
)

// PreflightInput starts a classification candidate.
type PreflightInput struct {
	// Name is the work name the change would take if confirmed.
	Name string
	// Request is the human's description of the work. It is what the
	// explorers and the skeptic assess, and what a later intent-expansion
	// signal is measured against.
	Request string
}

// ConfirmInput is a human's answer to the classification gate.
type ConfirmInput struct {
	WorkID identity.WorkID
	// Path is the confirmed workflow. It need not be the suggested one —
	// the suggestion is evidence, not a verdict — but choosing against it
	// requires saying why.
	Path Path
	// Rationale explains an override. Required when Path differs from the
	// suggestion.
	Rationale string
	// DecisionID links the confirmation to the decision action that
	// carried it, when one did.
	DecisionID identity.ActionID
}

// StartPreflight opens a local classification candidate and issues the
// read-only assignments that assess it.
//
// Nothing portable is created: no document, no change directory, no
// record. A candidate that is never confirmed leaves the repository
// exactly as it was, which is what makes it safe to start one on a hunch.
func (e *Engine) StartPreflight(ctx context.Context, in PreflightInput) (PreflightState, protocol.NextResponse, error) {
	if err := workname.Validate(in.Name); err != nil {
		return PreflightState{}, protocol.NextResponse{}, err
	}
	if strings.TrimSpace(in.Request) == "" {
		return PreflightState{}, protocol.NextResponse{},
			fmt.Errorf("change: a classification candidate needs a request to classify")
	}
	if err := e.requireNameFree(ctx, in.Name); err != nil {
		return PreflightState{}, protocol.NextResponse{}, err
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		return PreflightState{}, protocol.NextResponse{}, err
	}
	st := PreflightState{
		WorkID: workID, Name: in.Name, Request: in.Request,
		Step: PreflightAssess, Generation: 1, UpdatedAt: e.now().UTC(),
	}
	if err := e.insertPreflight(ctx, st); err != nil {
		return PreflightState{}, protocol.NextResponse{}, err
	}
	if err := e.issueAssessment(ctx, st); err != nil {
		return PreflightState{}, protocol.NextResponse{}, err
	}
	resp, err := e.nextPreflight(ctx, st)
	if err != nil {
		return PreflightState{}, protocol.NextResponse{}, err
	}
	return st, resp, nil
}

// issueAssessment dispatches the read-only preflight assignments: one
// explorer per member to establish what the request would touch, and a
// skeptic to attack the classification itself.
func (e *Engine) issueAssessment(ctx context.Context, st PreflightState) error {
	members, err := e.env.Members(ctx)
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return fmt.Errorf("change: the workspace has no confirmed members to assess")
	}
	for _, m := range members {
		spec, err := e.preflightSpec(st, protocol.RoleExplorer, m,
			fmt.Sprintf("assess what %q would touch in %s", st.Name, m.Path),
			"Read "+m.Path+" and report what this request would touch:\n\n"+st.Request+
				"\n\nReport the affected surfaces, whether a public API or a storage "+
				"schema is involved, and whether the work crosses modules. Do not write anything.")
		if err != nil {
			return err
		}
		if _, err := e.assignments.Create(ctx, spec); err != nil {
			return err
		}
	}
	control, err := e.env.Control(ctx)
	if err != nil {
		return err
	}
	spec, err := e.preflightSpec(st, protocol.RoleSkeptic, control,
		"challenge the suggested classification",
		"Attack the assumption that this request is small:\n\n"+st.Request+
			"\n\nName every reason it might be a new capability, an architectural change, "+
			"a public API or schema change, cross-module work, or several changes in one. "+
			"Do not write anything.")
	if err != nil {
		return err
	}
	_, err = e.assignments.Create(ctx, spec)
	return err
}

// NextPreflight returns what a host should do for a classification
// candidate, reading its current state first.
func (e *Engine) NextPreflight(ctx context.Context, id identity.WorkID) (protocol.NextResponse, error) {
	st, err := e.Preflight(ctx, id)
	if err != nil {
		return protocol.NextResponse{}, err
	}
	return e.nextPreflight(ctx, st)
}

// nextPreflight returns what a host should do for a candidate: run the
// assessment assignments, answer the classification gate, or nothing when
// the candidate has finished.
func (e *Engine) nextPreflight(ctx context.Context, st PreflightState) (protocol.NextResponse, error) {
	for i := 0; i < 8; i++ {
		if st.Step.Terminal() {
			return assignment.CompleteResponse(), nil
		}
		group, ok, err := e.assignments.ReadyGroup(ctx, st.WorkID)
		if err != nil {
			return protocol.NextResponse{}, err
		}
		if ok {
			return group.Response(), nil
		}
		if st.Step != PreflightAssess {
			return protocol.NextResponse{}, fmt.Errorf(
				"change: candidate %s is at %s with nothing to do", st.WorkID, st.Step)
		}
		// Every assessment is answered; suggest a path and put it to a
		// human.
		if st, err = e.suggest(ctx, st); err != nil {
			return protocol.NextResponse{}, err
		}
	}
	return protocol.NextResponse{}, fmt.Errorf("change: candidate %s made no progress", st.WorkID)
}

// suggest assesses the answered reports, records the suggestion, and
// issues the confirmation gate.
func (e *Engine) suggest(ctx context.Context, st PreflightState) (PreflightState, error) {
	signals, err := e.observedSignals(ctx, st.WorkID)
	if err != nil {
		return PreflightState{}, err
	}
	// Preflight has no work baseline yet — there is nothing implemented to
	// count — so the assessment weighs the observed signals alone.
	assessment, err := pathclass.AssessPreset(pathclass.AssessmentInput{Observed: signals})
	if err != nil {
		return PreflightState{}, err
	}
	st.Suggestion = Suggestion{
		Path:     suggestPath(assessment),
		Signals:  assessment.Signals,
		Evidence: assessment.Evidence,
	}
	st.Step = PreflightConfirm
	st.UpdatedAt = e.now().UTC()
	if err := e.savePreflight(ctx, st); err != nil {
		return PreflightState{}, err
	}
	control, err := e.env.Control(ctx)
	if err != nil {
		return PreflightState{}, err
	}
	spec, err := e.preflightDecisionSpec(st, control)
	if err != nil {
		return PreflightState{}, err
	}
	if _, err := e.assignments.Create(ctx, spec); err != nil {
		return PreflightState{}, err
	}
	return st, nil
}

// suggestPath turns an assessment into a suggestion. Any signal at all
// suggests Full: the signals are the spec's list of things a preset cannot
// carry. With none, Tweak is suggested rather than Fix, because Fix
// asserts something Homonto cannot observe — that the behavior is a defect
// — and asserting it on the human's behalf would be a guess dressed as a
// classification.
func suggestPath(a pathclass.Assessment) Path {
	if a.Pause {
		return PathFull
	}
	return PathTweak
}

// observedSignals reads the preset scope signals out of the answered
// preflight reports. A report's findings and questions are prose; the
// signals are carried as finding ids matching the signal vocabulary, so a
// host reports them explicitly rather than having Homonto guess from text.
func (e *Engine) observedSignals(ctx context.Context, workID identity.WorkID) ([]pathclass.Signal, error) {
	actions, err := e.assignments.Actions(ctx, workID)
	if err != nil {
		return nil, err
	}
	seen := map[pathclass.Signal]bool{}
	var out []pathclass.Signal
	for _, act := range actions {
		if act.Kind != protocol.KindAssignment || act.State != assignment.StateSubmitted {
			continue
		}
		sub, found, err := e.assignments.Report(ctx, act.ID)
		if err != nil || !found {
			if err != nil {
				return nil, err
			}
			continue
		}
		wire := act.Spec
		wire.FreshnessToken = e.assignments.Token(act.ID)
		report, err := protocol.DecodeReport(wire, sub.Report)
		if err != nil {
			return nil, err
		}
		for _, f := range findingsOf(report) {
			signal := pathclass.Signal(f.ID)
			if !signal.Semantic() || seen[signal] {
				continue
			}
			seen[signal] = true
			out = append(out, signal)
		}
	}
	return pathclass.SortSignals(out), nil
}

// findingsOf extracts the findings of any role report.
func findingsOf(report protocol.Report) []protocol.Finding {
	switch r := report.(type) {
	case *protocol.ReviewerReport:
		return r.Findings
	case *protocol.SkepticReport:
		return r.Findings
	}
	return nil
}

// classificationSchema is the human confirmation gate. The suggested path
// is named in the prompt but is not the default choice and is not first:
// a confirmation that can be given without reading is not a confirmation.
func classificationSchema(st PreflightState) decision.Schema {
	var b strings.Builder
	fmt.Fprintf(&b, "Homonto suggests the %s path for %q.\n\nRequest: %s\n",
		st.Suggestion.Path, st.Name, st.Request)
	if len(st.Suggestion.Evidence) == 0 {
		b.WriteString("\nNo preset scope signal fired.\n")
	} else {
		b.WriteString("\nEvidence:\n")
		for _, e := range st.Suggestion.Evidence {
			fmt.Fprintf(&b, "- %s\n", e)
		}
	}
	b.WriteString("\nConfirm the path. Choosing against the suggestion requires a rationale.")
	return decision.Schema{
		Kind:   decision.KindConfirmClassification,
		Prompt: b.String(),
		Choices: []decision.Choice{
			{Value: string(PathFix), Label: "Fix — an existing-behavior defect",
				RequiresRationale: st.Suggestion.Path != PathFix},
			{Value: string(PathTweak), Label: "Tweak — a bounded behavior, config, docs, or prompt change",
				RequiresRationale: st.Suggestion.Path != PathTweak},
			{Value: string(PathFull), Label: "Full — proposal, design, plan, verification, record",
				RequiresRationale: st.Suggestion.Path != PathFull},
		},
	}
}

// ConfirmPreflight records the human's confirmed path and creates the
// Change: its documents, its immutable work baseline, and its first step.
// This is the moment portable state begins to exist.
func (e *Engine) ConfirmPreflight(ctx context.Context, in ConfirmInput) (State, error) {
	pre, err := e.Preflight(ctx, in.WorkID)
	if err != nil {
		return State{}, err
	}
	if pre.Step.Terminal() {
		return State{}, fmt.Errorf("change: candidate %s is %s: %w",
			pre.WorkID, pre.Step, ErrPreflightFinished)
	}
	if pre.Step != PreflightConfirm {
		return State{}, fmt.Errorf("change: candidate %s is still assessing: %w",
			pre.WorkID, ErrNotConfirmable)
	}
	if !in.Path.Known() {
		return State{}, fmt.Errorf("change: confirmed path %q is not known", in.Path)
	}
	if in.Path != pre.Suggestion.Path && strings.TrimSpace(in.Rationale) == "" {
		return State{}, fmt.Errorf("change: %s was suggested, %s was chosen: %w",
			pre.Suggestion.Path, in.Path, ErrOverrideNeedsRationale)
	}
	if err := e.requireNameFree(ctx, pre.Name); err != nil {
		return State{}, err
	}

	st := State{
		WorkID: pre.WorkID, Name: pre.Name, Path: in.Path,
		Step: firstStep(in.Path), Generation: 1, UpdatedAt: e.now().UTC(),
	}
	if err := e.createDocuments(ctx, st, pre.Request); err != nil {
		return State{}, err
	}
	// The work baseline is captured HERE, once, at the path-confirmed
	// transition — and never again. Every later preset scope count is
	// measured from it, so continuation, repair, and reconfirmation cannot
	// move the goalposts.
	baseline, err := e.captureBaseline(ctx, st)
	if err != nil {
		return State{}, err
	}
	st.Baseline = baseline
	if err := e.insertState(ctx, st); err != nil {
		return State{}, err
	}
	pre.Step = PreflightConfirmed
	pre.UpdatedAt = e.now().UTC()
	if err := e.savePreflight(ctx, pre); err != nil {
		return State{}, err
	}
	return st, nil
}

// AbandonPreflight drops a classification candidate. Nothing portable was
// created, so nothing is removed.
func (e *Engine) AbandonPreflight(ctx context.Context, id identity.WorkID) (PreflightState, error) {
	pre, err := e.Preflight(ctx, id)
	if err != nil {
		return PreflightState{}, err
	}
	if pre.Step.Terminal() {
		return PreflightState{}, fmt.Errorf("change: candidate %s is %s: %w",
			id, pre.Step, ErrPreflightFinished)
	}
	actions, err := e.assignments.Actions(ctx, id)
	if err != nil {
		return PreflightState{}, err
	}
	var ids []identity.ActionID
	for _, act := range actions {
		if act.State == assignment.StatePending || act.State == assignment.StateIssued {
			ids = append(ids, act.ID)
		}
	}
	if err := e.assignments.Invalidate(ctx, ids...); err != nil {
		return PreflightState{}, err
	}
	pre.Step = PreflightAbandoned
	pre.UpdatedAt = e.now().UTC()
	if err := e.savePreflight(ctx, pre); err != nil {
		return PreflightState{}, err
	}
	return pre, nil
}

// Preflight returns one classification candidate.
func (e *Engine) Preflight(ctx context.Context, id identity.WorkID) (PreflightState, error) {
	var (
		st         PreflightState
		suggestion string
		updatedAt  string
	)
	err := e.db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT work_id, name, request, step, generation, suggestion, updated_at
			  FROM change_preflights WHERE work_id = ?`, string(id)).
			Scan(&st.WorkID, &st.Name, &st.Request, &st.Step, &st.Generation, &suggestion, &updatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return PreflightState{}, fmt.Errorf("change: candidate %s: %w", id, ErrUnknownPreflight)
	}
	if err != nil {
		return PreflightState{}, fmt.Errorf("change: read candidate %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(suggestion), &st.Suggestion); err != nil {
		return PreflightState{}, fmt.Errorf("change: decode suggestion of %s: %w", id, err)
	}
	if st.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return PreflightState{}, fmt.Errorf("change: decode update time of %s: %w", id, err)
	}
	return st, nil
}

// insertPreflight writes a new candidate.
func (e *Engine) insertPreflight(ctx context.Context, st PreflightState) error {
	if err := st.Validate(); err != nil {
		return err
	}
	suggestion, err := json.Marshal(st.Suggestion)
	if err != nil {
		return fmt.Errorf("change: encode suggestion: %w", err)
	}
	return e.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO change_preflights (work_id, name, request, step, generation, suggestion, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			string(st.WorkID), st.Name, st.Request, string(st.Step), st.Generation,
			string(suggestion), st.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("change: start candidate %s: %w", st.WorkID, err)
		}
		return nil
	})
}

// savePreflight overwrites an existing candidate.
func (e *Engine) savePreflight(ctx context.Context, st PreflightState) error {
	if err := st.Validate(); err != nil {
		return err
	}
	suggestion, err := json.Marshal(st.Suggestion)
	if err != nil {
		return fmt.Errorf("change: encode suggestion: %w", err)
	}
	return e.db.Update(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE change_preflights SET step = ?, generation = ?, suggestion = ?, updated_at = ?
			 WHERE work_id = ?`,
			string(st.Step), st.Generation, string(suggestion),
			st.UpdatedAt.UTC().Format(time.RFC3339Nano), string(st.WorkID))
		if err != nil {
			return fmt.Errorf("change: save candidate %s: %w", st.WorkID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("change: save candidate %s: %w", st.WorkID, err)
		}
		if n == 0 {
			return fmt.Errorf("change: candidate %s: %w", st.WorkID, ErrUnknownPreflight)
		}
		return nil
	})
}

// preflightSpec builds a read-only preflight assignment.
func (e *Engine) preflightSpec(st PreflightState, role protocol.Role, member Member, reason, prompt string) (assignment.Spec, error) {
	return assignment.Spec{
		WorkID:     st.WorkID,
		Step:       string(PreflightAssess),
		Generation: st.Generation,
		Template: protocol.Action{
			Kind:             protocol.KindAssignment,
			Workflow:         workspacecfg.WorkflowChange,
			Path:             artifact.ChangesDir,
			Phase:            string(artifact.PhaseOpen),
			Reason:           reason,
			Role:             role,
			Prompt:           prompt,
			Repository:       protocol.RepositoryRef{ID: member.ID, Path: member.Path},
			WorkingDirectory: member.Path,
			WriteScope:       protocol.WriteScope{ReadOnly: true},
			InputFingerprints: []fingerprint.Digest{
				fingerprint.Bytes("change-preflight-request", []byte(st.Request)),
			},
			ExpectedReport: &protocol.ExpectedReport{Kind: role, SchemaVersion: protocol.CurrentVersion},
		},
	}, nil
}

// preflightDecisionSpec builds the classification confirmation gate.
func (e *Engine) preflightDecisionSpec(st PreflightState, control Member) (assignment.Spec, error) {
	schema := classificationSchema(st)
	if err := decision.ValidateSchema(schema); err != nil {
		return assignment.Spec{}, err
	}
	choices := make([]protocol.Choice, len(schema.Choices))
	for i, c := range schema.Choices {
		choices[i] = protocol.Choice{Value: c.Value, Label: c.Label, RequiresRationale: c.RequiresRationale}
	}
	return assignment.Spec{
		WorkID:     st.WorkID,
		Step:       string(PreflightConfirm),
		Generation: st.Generation,
		Template: protocol.Action{
			Kind:             protocol.KindDecision,
			Workflow:         workspacecfg.WorkflowChange,
			Path:             artifact.ChangesDir,
			Phase:            string(artifact.PhaseOpen),
			Reason:           "confirm the change path before anything is created",
			Prompt:           schema.Prompt,
			Repository:       protocol.RepositoryRef{ID: control.ID, Path: control.Path},
			WorkingDirectory: control.Path,
			WriteScope:       protocol.WriteScope{ReadOnly: true},
			InputFingerprints: []fingerprint.Digest{
				fingerprint.Bytes("change-preflight-request", []byte(st.Request)),
			},
			Decision: &protocol.DecisionSchema{
				Kind:    protocol.DecisionKind(schema.Kind),
				Prompt:  schema.Prompt,
				Choices: choices,
			},
		},
	}, nil
}
