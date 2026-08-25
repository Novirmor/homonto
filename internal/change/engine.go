package change

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/archive"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/finding"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/verify"
)

// Typed engine errors.
var (
	// ErrUnknownChange: no change with that id.
	ErrUnknownChange = errors.New("change: no such change")
	// ErrTerminal: the change has finished.
	ErrTerminal = errors.New("change: the change has finished")
	// ErrStaleAction: the submission answers an action from a superseded
	// generation.
	ErrStaleAction = errors.New("change: action belongs to a superseded generation")
	// ErrResultRejected: an assignment's final diff contained changes it
	// was not issued to make.
	ErrResultRejected = errors.New("change: assignment result rejected by the write boundary")
)

// Member is one confirmed repository. It mirrors the Task engine's member
// because both engines ask the workspace the same questions.
type Member struct {
	ID   identity.RepositoryID
	Path string
	Git  bool
}

// Unit is one parallel piece of a Change's implementation work, with the
// same shape and the same rules as the Task engine's partition.
type Unit struct {
	Label       string
	Member      Member
	Items       []int
	Integration bool
	Base        string
	Root        string
	Scope       []string
	Prompt      string
}

// Result is one finished implementation unit.
type Result struct {
	ActionID identity.ActionID
	Unit     Unit
	Material protocol.Material
}

// Environment is the workspace-shaped knowledge the Change engine needs.
// It is the Task engine's environment plus the diff a preset's scope count
// is measured from — the one question only a Change asks.
type Environment interface {
	Control(ctx context.Context) (Member, error)
	Members(ctx context.Context) ([]Member, error)
	Fingerprints(ctx context.Context) (Baseline, error)
	Partition(ctx context.Context, workID identity.WorkID, items []artifact.Item) ([]Unit, error)
	Isolate(ctx context.Context, workID identity.WorkID, actionID identity.ActionID, unit Unit) (Unit, error)
	Integrations(ctx context.Context, workID identity.WorkID, results []Result) ([]Unit, error)
	SourceFingerprints(ctx context.Context, workID identity.WorkID) ([]fingerprint.Digest, error)
	RunChecks(ctx context.Context, workID identity.WorkID) (verify.Set, error)
	ResultDiff(ctx context.Context, action protocol.Action, unit Unit) (guard.ResultDiff, error)
	// WorkspaceDiff returns the integrated workspace diff against the
	// change's immutable work baseline — the input to the preset scope
	// count.
	WorkspaceDiff(ctx context.Context, workID identity.WorkID, baseline []fingerprint.Digest) ([]pathclass.DiffEntry, error)
	// Matchers resolves a member's path-class matcher by member path.
	Matchers(member string) (*pathclass.Matcher, error)
}

// Clock is the engine's time source; tests inject a fixed one.
type Clock func() time.Time

// Engine drives one workspace's Changes.
type Engine struct {
	db          *store.DB
	assignments *assignment.Store
	artifacts   *artifact.Service
	findings    *finding.Service
	evidence    *verify.Store
	archive     *archive.Service
	guard       *guard.Guard
	env         Environment
	now         Clock
}

// Dependencies are the collaborators an Engine needs.
type Dependencies struct {
	DB          *store.DB
	Assignments *assignment.Store
	Artifacts   *artifact.Service
	Findings    *finding.Service
	Evidence    *verify.Store
	Archive     *archive.Service
	Guard       *guard.Guard
	Environment Environment
	Now         Clock
}

// NewEngine binds a Change engine to its collaborators. Every one is
// required: an engine missing any could reach a step it cannot execute,
// and discovering that mid-workflow is worse than refusing to start.
func NewEngine(deps Dependencies) (*Engine, error) {
	missing := ""
	switch {
	case deps.DB == nil:
		missing = "database"
	case deps.Assignments == nil:
		missing = "assignment store"
	case deps.Artifacts == nil:
		missing = "artifact service"
	case deps.Findings == nil:
		missing = "finding service"
	case deps.Evidence == nil:
		missing = "verification store"
	case deps.Archive == nil:
		missing = "archive service"
	case deps.Guard == nil:
		missing = "write guard"
	case deps.Environment == nil:
		missing = "environment"
	}
	if missing != "" {
		return nil, fmt.Errorf("change: engine needs a %s", missing)
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Engine{
		db: deps.DB, assignments: deps.Assignments, artifacts: deps.Artifacts,
		findings: deps.Findings, evidence: deps.Evidence, archive: deps.Archive,
		guard: deps.Guard, env: deps.Environment, now: now,
	}, nil
}

// State returns one Change's recorded state.
func (e *Engine) State(ctx context.Context, id identity.WorkID) (State, error) {
	var (
		st        State
		upgraded  sql.NullString
		baseline  string
		updatedAt string
	)
	err := e.db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx, `
			SELECT work_id, name, path, step, upgraded_from, generation, baseline, updated_at
			  FROM change_states WHERE work_id = ?`, string(id)).
			Scan(&st.WorkID, &st.Name, &st.Path, &st.Step, &upgraded,
				&st.Generation, &baseline, &updatedAt)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, fmt.Errorf("change: %s: %w", id, ErrUnknownChange)
	}
	if err != nil {
		return State{}, fmt.Errorf("change: read %s: %w", id, err)
	}
	st.UpgradedFrom = Path(upgraded.String)
	if err := json.Unmarshal([]byte(baseline), &st.Baseline); err != nil {
		return State{}, fmt.Errorf("change: decode baseline of %s: %w", id, err)
	}
	if st.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return State{}, fmt.Errorf("change: decode update time of %s: %w", id, err)
	}
	return st, nil
}

// States returns every recorded Change, oldest first. The ids are read in
// one transaction and the states loaded after it closes: the runtime
// database serializes through a single connection, so a read that opens a
// second transaction inside the first waits for a connection only it could
// release.
func (e *Engine) States(ctx context.Context) ([]State, error) {
	var ids []identity.WorkID
	err := e.db.View(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT work_id FROM change_states ORDER BY updated_at, work_id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id identity.WorkID
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("change: list changes: %w", err)
	}
	out := make([]State, 0, len(ids))
	for _, id := range ids {
		st, err := e.State(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// insertState writes a new change, refusing to overwrite one.
func (e *Engine) insertState(ctx context.Context, st State) error {
	if err := st.Validate(); err != nil {
		return err
	}
	baseline, err := json.Marshal(st.Baseline)
	if err != nil {
		return fmt.Errorf("change: encode baseline: %w", err)
	}
	var upgraded any
	if st.UpgradedFrom != "" {
		upgraded = string(st.UpgradedFrom)
	}
	return e.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO change_states
			  (work_id, name, path, step, upgraded_from, generation, baseline, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			string(st.WorkID), st.Name, string(st.Path), st.Step, upgraded,
			st.Generation, string(baseline), st.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("change: create %s: %w", st.WorkID, err)
		}
		return nil
	})
}

// saveState overwrites an existing change.
func (e *Engine) saveState(ctx context.Context, st State) error {
	if err := st.Validate(); err != nil {
		return err
	}
	baseline, err := json.Marshal(st.Baseline)
	if err != nil {
		return fmt.Errorf("change: encode baseline: %w", err)
	}
	var upgraded any
	if st.UpgradedFrom != "" {
		upgraded = string(st.UpgradedFrom)
	}
	return e.db.Update(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE change_states SET name = ?, path = ?, step = ?, upgraded_from = ?,
			       generation = ?, baseline = ?, updated_at = ?
			 WHERE work_id = ?`,
			st.Name, string(st.Path), st.Step, upgraded, st.Generation, string(baseline),
			st.UpdatedAt.UTC().Format(time.RFC3339Nano), string(st.WorkID))
		if err != nil {
			return fmt.Errorf("change: save %s: %w", st.WorkID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("change: save %s: %w", st.WorkID, err)
		}
		if n == 0 {
			return fmt.Errorf("change: %s: %w", st.WorkID, ErrUnknownChange)
		}
		return nil
	})
}

// requireNameFree refuses a name an unfinished change already uses. Two
// active changes of the same name would write the same documents.
func (e *Engine) requireNameFree(ctx context.Context, name string) error {
	states, err := e.States(ctx)
	if err != nil {
		return err
	}
	for _, st := range states {
		if st.Name == name && !terminalStep(st.Path, st.Step) {
			return fmt.Errorf("change: %s is at %s: %w", name, st.Step, ErrNameTaken)
		}
	}
	return nil
}

// createDocuments creates the change's directory and the documents its
// path starts with, seeded with the confirmed request.
func (e *Engine) createDocuments(ctx context.Context, st State, request string) error {
	inputKind, err := st.Path.InputKind()
	if err != nil {
		return err
	}
	inputPath, err := st.DocumentPath(inputKind)
	if err != nil {
		return err
	}
	meta := artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: st.WorkID, Name: st.Name, Kind: inputKind,
	}
	if _, err := e.artifacts.Create(ctx, inputPath, meta); err != nil {
		return err
	}
	if err := e.seedDocument(ctx, artifact.Ref{
		WorkID: st.WorkID, Kind: inputKind, Path: inputPath,
	}, request); err != nil {
		return err
	}
	// Presets author tasks.md in Open alongside their input document;
	// Full's tasks.md is a Design output and is created there.
	if !st.Path.Preset() {
		return nil
	}
	tasksPath, err := st.DocumentPath(artifact.KindTasks)
	if err != nil {
		return err
	}
	_, err = e.artifacts.Create(ctx, tasksPath, artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: st.WorkID, Name: st.Name, Kind: artifact.KindTasks,
	})
	return err
}

// seedDocument writes the confirmed request into a freshly created
// document. It goes through a grant the engine issues and immediately
// accepts — the same path a host takes, with no shortcut around the
// ownership table.
func (e *Engine) seedDocument(ctx context.Context, ref artifact.Ref, request string) error {
	if strings.TrimSpace(request) == "" {
		return nil
	}
	grant, err := e.artifacts.GrantEdit(ctx, artifact.GrantRequest{
		Ref: ref, Phase: artifact.PhaseOpen, Regions: []artifact.Region{artifact.RegionWholeDocument},
	})
	if err != nil {
		return err
	}
	doc, err := e.artifacts.Read(ctx, ref)
	if err != nil {
		return err
	}
	body := "## Request\n\n" + strings.TrimSpace(request) + "\n"
	for i := range doc.Regions {
		if doc.Regions[i].Region == artifact.RegionWholeDocument {
			doc.Regions[i].Content = []byte(body)
		}
	}
	if err := artifact.WriteRaw(e.artifacts, ref, doc); err != nil {
		return err
	}
	_, err = e.artifacts.AcceptEdit(ctx, grant)
	return err
}

// captureBaseline records the fingerprints a new change rests on,
// including the immutable work baseline the preset scope count measures
// from.
func (e *Engine) captureBaseline(ctx context.Context, st State) (Baseline, error) {
	baseline, err := e.env.Fingerprints(ctx)
	if err != nil {
		return Baseline{}, err
	}
	docs, err := e.documentDigests(ctx, st)
	if err != nil {
		return Baseline{}, err
	}
	baseline.Documents = docs
	work, err := e.env.SourceFingerprints(ctx, st.WorkID)
	if err != nil {
		return Baseline{}, err
	}
	baseline.Work = sortedDigests(work)
	return baseline, nil
}

// hostDocumentKinds are the documents whose content the invalidation graph
// tracks, in canonical order.
var hostDocumentKinds = []artifact.Kind{
	artifact.KindProposal, artifact.KindDesign, artifact.KindTasks,
	artifact.KindPresetTasks, artifact.KindPlan, artifact.KindFix, artifact.KindTweak,
}

// documentDigests digests each of the change's host-authored documents.
// Absent documents get no entry, so one coming into existence moves the
// baseline.
//
// A tasks document's checkbox state is normalized away before digesting:
// a checked box is Homonto recording progress against the plan, not a
// change to it, and digesting it raw would make Homonto's own checkoffs
// invalidate the documents that produced them.
func (e *Engine) documentDigests(ctx context.Context, st State) (map[artifact.Kind]fingerprint.Digest, error) {
	out := map[artifact.Kind]fingerprint.Digest{}
	for _, kind := range hostDocumentKinds {
		path, err := st.DocumentPath(kind)
		if err != nil {
			return nil, err
		}
		doc, err := e.artifacts.Read(ctx, artifact.Ref{WorkID: st.WorkID, Kind: kind, Path: path})
		if errors.Is(err, artifact.ErrArtifactMissing) {
			continue
		}
		if err != nil {
			return nil, err
		}
		content := doc.Region(artifact.RegionWholeDocument)
		if kind == artifact.KindTasks || kind == artifact.KindPresetTasks {
			content = artifact.SemanticChecklist(content)
		}
		out[kind] = fingerprint.Bytes("change-document-"+string(kind), content)
	}
	return out, nil
}

// PresetScope counts the change's diff against its immutable work baseline
// and weighs it with the signals observed so far.
func (e *Engine) PresetScope(ctx context.Context, st State, observed []pathclass.Signal) (pathclass.Assessment, error) {
	entries, err := e.env.WorkspaceDiff(ctx, st.WorkID, st.Baseline.Work)
	if err != nil {
		return pathclass.Assessment{}, err
	}
	count, err := pathclass.CountPresetChanges(entries, e.env.Matchers)
	if err != nil {
		return pathclass.Assessment{}, err
	}
	return pathclass.AssessPreset(pathclass.AssessmentInput{Count: count, Observed: observed})
}

// sortedDigests returns digests sorted for stable baselines.
func sortedDigests(in []fingerprint.Digest) []fingerprint.Digest {
	out := append([]fingerprint.Digest(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Abandon stops a Change. Its isolation areas, branches, and evidence are
// left exactly where they are: abandoning is a decision to stop working,
// not an instruction to destroy the work.
func (e *Engine) Abandon(ctx context.Context, id identity.WorkID) (State, error) {
	st, err := e.State(ctx, id)
	if err != nil {
		return State{}, err
	}
	next, err := Advance(st.Path, Step(st.Step), EventAbandon)
	if err != nil {
		return State{}, err
	}
	if err := e.invalidateOpenActions(ctx, st); err != nil {
		return State{}, err
	}
	return e.moveTo(ctx, st, next)
}

// SubmitReport records a host's answer to an assignment. The result diff
// is validated FIRST for a writable assignment: a report backed by changes
// the assignment was not issued to make is refused rather than recorded,
// so nothing downstream ever reads it.
func (e *Engine) SubmitReport(ctx context.Context, in protocol.ReportSubmission) (State, error) {
	act, err := e.assignments.Action(ctx, in.ActionID)
	if err != nil {
		return State{}, err
	}
	st, err := e.State(ctx, act.WorkID)
	if err != nil {
		return State{}, err
	}
	if terminalStep(st.Path, st.Step) {
		return State{}, fmt.Errorf("change: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if act.Generation != st.Generation {
		return State{}, fmt.Errorf("change: action %s belongs to generation %d, the change is at %d: %w",
			act.ID, act.Generation, st.Generation, ErrStaleAction)
	}
	wire := act.Spec
	wire.FreshnessToken = e.assignments.Token(act.ID)
	if !wire.WriteScope.ReadOnly {
		unit, _, err := e.unitOf(ctx, act.ID)
		if err != nil {
			return State{}, err
		}
		diff, err := e.env.ResultDiff(ctx, wire, unit)
		if err != nil {
			return State{}, err
		}
		if err := e.guard.ValidateAssignmentResult(ctx, wire, diff); err != nil {
			return State{}, fmt.Errorf("%w: %w", ErrResultRejected, err)
		}
	}
	if _, err := e.assignments.Submit(ctx, in); err != nil {
		return State{}, err
	}
	if err := e.recordFindings(ctx, st, act, in); err != nil {
		return State{}, err
	}
	// Only Homonto checks items off, and only for assignments it accepted
	// — which is exactly here, after the final diff gate has passed.
	if act.Role == protocol.RoleImplementer &&
		(act.Step == string(StepBuildImplement) || act.Step == string(StepPresetImplement)) {
		if err := e.checkOffUnit(ctx, st, act.ID); err != nil {
			return State{}, err
		}
	}
	return st, nil
}

// recordFindings persists the findings of a reviewer or skeptic report.
func (e *Engine) recordFindings(ctx context.Context, st State, act assignment.Action, in protocol.ReportSubmission) error {
	switch act.Role {
	case protocol.RoleReviewer, protocol.RoleSkeptic:
	default:
		return nil
	}
	// Findings raised during Open and Design are preset scope SIGNALS, not
	// defects in a result that does not exist yet. Recording them as
	// blocking findings would gate a change on an observation about its
	// own scope.
	if act.Step != string(StepVerifyReview) && act.Step != string(StepPresetReview) {
		return nil
	}
	wire := act.Spec
	wire.FreshnessToken = e.assignments.Token(act.ID)
	report, err := protocol.DecodeReport(wire, in.Report)
	if err != nil {
		return err
	}
	findings := findingsOf(report)
	if len(findings) == 0 {
		return nil
	}
	return e.findings.RecordReport(ctx, st.WorkID, act.ID, act.Role, findings)
}

// Decide records a human's answer to a decision gate. Acting on the answer
// is the next step's job, not this one's: recording and acting are
// separate so a crash between them replays cleanly.
func (e *Engine) Decide(ctx context.Context, in decision.Submission) (State, error) {
	act, err := e.assignments.Action(ctx, in.ActionID)
	if err != nil {
		return State{}, err
	}
	// A classification confirmation answers a candidate, not a change.
	if act.Step == string(PreflightConfirm) {
		if _, err := e.assignments.Decide(ctx, in); err != nil {
			return State{}, err
		}
		pre, err := e.Preflight(ctx, act.WorkID)
		if err != nil {
			return State{}, err
		}
		return e.ConfirmPreflight(ctx, ConfirmInput{
			WorkID: pre.WorkID, Path: Path(in.Choice),
			Rationale: in.Rationale, DecisionID: act.ID,
		})
	}
	st, err := e.State(ctx, act.WorkID)
	if err != nil {
		return State{}, err
	}
	if terminalStep(st.Path, st.Step) {
		return State{}, fmt.Errorf("change: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if _, err := e.assignments.Decide(ctx, in); err != nil {
		return State{}, err
	}
	return st, nil
}

// AcceptEdit accepts the host's document edit and marks the edit action
// answered. The host presents only the grant token it was issued: Homonto
// looks up what that grant actually opened rather than believing a
// structure the host hands back.
func (e *Engine) AcceptEdit(ctx context.Context, actionID identity.ActionID, grantToken identity.Token) (State, error) {
	act, err := e.assignments.Action(ctx, actionID)
	if err != nil {
		return State{}, err
	}
	st, err := e.State(ctx, act.WorkID)
	if err != nil {
		return State{}, err
	}
	if terminalStep(st.Path, st.Step) {
		return State{}, fmt.Errorf("change: %s is %s: %w", st.WorkID, st.Step, ErrTerminal)
	}
	if act.Spec.Edit == nil {
		return State{}, fmt.Errorf("change: action %s is not an edit action", actionID)
	}
	grant, err := e.artifacts.Grant(ctx, act.Spec.Edit.GrantID, grantToken)
	if err != nil {
		return State{}, err
	}
	if _, err := e.artifacts.AcceptEdit(ctx, grant); err != nil {
		return State{}, err
	}
	if _, err := e.assignments.CompleteEdit(ctx, actionID, e.assignments.Token(actionID)); err != nil {
		return State{}, err
	}
	return st, nil
}
