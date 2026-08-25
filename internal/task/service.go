package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/noviopenworks/homonto/internal/archive"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/finding"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/verify"
	"github.com/noviopenworks/homonto/internal/workname"
)

// Typed engine errors. Callers branch with errors.Is.
var (
	// ErrUnknownWork: no Task with that id.
	ErrUnknownWork = errors.New("task: no such task")
	// ErrWorkExists: a Task with that id was already started.
	ErrWorkExists = errors.New("task: task already started")
	// ErrTerminal: the Task has finished and accepts nothing further.
	ErrTerminal = errors.New("task: task has finished")
	// ErrStaleAction: the submission answers an action from a superseded
	// generation.
	ErrStaleAction = errors.New("task: action belongs to a superseded generation")
	// ErrResultRejected: the assignment's final diff contained changes it
	// was not issued to make.
	ErrResultRejected = errors.New("task: assignment result rejected by the write boundary")
)

// Environment is everything about the workspace the engine cannot compute
// itself: who the members are, what the current input fingerprints are,
// how work is partitioned into isolation areas, and how checks and result
// diffs are actually obtained.
//
// It is an interface because the engine's job is sequencing and gating,
// not Git plumbing — and because the sequencing must be testable without
// a repository on disk.
type Environment interface {
	// Control returns the control repository member.
	Control(ctx context.Context) (Member, error)
	// Members returns every confirmed repository, control included.
	Members(ctx context.Context) ([]Member, error)
	// Fingerprints returns the current membership, path-class, and
	// check-configuration digests.
	Fingerprints(ctx context.Context) (Baseline, error)
	// Partition turns checklist items into parallel implementation units.
	// The units carry no isolation area: an isolation worktree is named
	// after the action it serves, so it can only be created once the
	// action's identity exists. See Isolate.
	Partition(ctx context.Context, workID identity.WorkID, items []artifact.Item) ([]Partition, error)
	// Isolate creates the isolation area for one action and returns the
	// unit with its Root filled in. It runs after the action id is minted
	// and before the assignment is persisted, so an assignment never
	// exists without the area it was issued for.
	Isolate(ctx context.Context, workID identity.WorkID, actionID identity.ActionID, unit Partition) (Partition, error)
	// Integrations returns the integration units — one per affected member
	// — that combine the parallel output, isolation areas included.
	Integrations(ctx context.Context, workID identity.WorkID, results []Result) ([]Partition, error)
	// SourceFingerprints returns the integrated source fingerprints the
	// checks and the final reviews are taken against.
	SourceFingerprints(ctx context.Context, workID identity.WorkID) ([]fingerprint.Digest, error)
	// RunChecks executes the configured verification commands against the
	// integrated result.
	RunChecks(ctx context.Context, workID identity.WorkID) (verify.Set, error)
	// ResultDiff observes what an assignment actually changed, given the
	// unit it was issued for. It is observed by Homonto, never reported by
	// the host — which is exactly why the final-diff gate catches what the
	// write hook missed.
	ResultDiff(ctx context.Context, action protocol.Action, unit Partition) (guard.ResultDiff, error)
}

// Result is one finished implementation unit: which action produced it,
// what it was issued to do, and the material it returned — a Git commit or
// a snapshot patch manifest. It is what the integration units combine.
type Result struct {
	ActionID  identity.ActionID
	Partition Partition
	Material  protocol.Material
}

// Clock is the engine's time source; tests inject a fixed one.
type Clock func() time.Time

// Engine drives one workspace's Tasks.
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

// Dependencies are the collaborators an Engine needs. Every one is
// required: an engine missing any of them could reach a step it cannot
// execute, and discovering that mid-workflow is worse than refusing to
// start.
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

// NewEngine binds an engine to its collaborators.
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
		return nil, fmt.Errorf("task: engine needs a %s", missing)
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

// StartInput names a new Task.
type StartInput struct {
	// Name is the normalized work name; the task document is created at
	// active/<name>/tasks.md.
	Name string
	// Goal is the initial outcome statement. It may be empty: the host
	// fills it in during the draft step.
	Goal string
}

// Start creates a Task: its document, its baseline, and its first step.
// The document is created before the state, so a crash between the two
// leaves an orphan document rather than a state pointing at nothing.
func (e *Engine) Start(ctx context.Context, in StartInput) (State, error) {
	if err := workname.Validate(in.Name); err != nil {
		return State{}, err
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		return State{}, err
	}
	path, err := artifact.Path(in.Name, artifact.KindTaskDocument)
	if err != nil {
		return State{}, err
	}
	if _, err := e.artifacts.Create(ctx, path, artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: workID, Name: in.Name, Kind: artifact.KindTaskDocument,
	}); err != nil {
		return State{}, err
	}
	ref := artifact.Ref{WorkID: workID, Kind: artifact.KindTaskDocument, Path: path}
	if in.Goal != "" {
		if err := e.seedGoal(ctx, ref, in.Goal); err != nil {
			return State{}, err
		}
	}
	baseline, err := e.currentBaseline(ctx, workID, ref, nil)
	if err != nil {
		return State{}, err
	}
	st := State{
		WorkID: workID, Name: in.Name, Step: StepPlanExplore,
		Generation: 1, Baseline: baseline, UpdatedAt: e.now().UTC(),
	}
	if err := e.insertState(ctx, st); err != nil {
		return State{}, err
	}
	return st, nil
}

// seedGoal writes the caller's initial outcome statement into the goal
// region. It is a binary write in a phase the binary does not own, so it
// goes through a grant the engine issues and immediately accepts — the
// same path a host would take, with no shortcut around the ownership
// table.
func (e *Engine) seedGoal(ctx context.Context, ref artifact.Ref, goal string) error {
	grant, err := e.artifacts.GrantEdit(ctx, artifact.GrantRequest{
		Ref: ref, Phase: artifact.PhasePlan, Regions: []artifact.Region{artifact.RegionTaskGoal},
	})
	if err != nil {
		return err
	}
	doc, err := e.artifacts.Read(ctx, ref)
	if err != nil {
		return err
	}
	body := goal
	if body[len(body)-1] != '\n' {
		body += "\n"
	}
	for i := range doc.Regions {
		if doc.Regions[i].Region == artifact.RegionTaskGoal {
			doc.Regions[i].Content = []byte(body)
		}
	}
	if err := e.writeDocument(ctx, ref, doc); err != nil {
		return err
	}
	_, err = e.artifacts.AcceptEdit(ctx, grant)
	return err
}

// State returns one Task's recorded state.
func (e *Engine) State(ctx context.Context, id identity.WorkID) (State, error) {
	var st State
	err := e.db.View(ctx, func(tx *store.Tx) error {
		var err error
		st, err = loadState(ctx, tx, id)
		return err
	})
	return st, err
}

// Abandon stops a Task. Its isolation areas, branches, and evidence are
// left exactly where they are: abandoning is a decision to stop working,
// not an instruction to destroy the work.
func (e *Engine) Abandon(ctx context.Context, id identity.WorkID) (State, error) {
	st, err := e.State(ctx, id)
	if err != nil {
		return State{}, err
	}
	next, err := Advance(st.Step, EventAbandon)
	if err != nil {
		return State{}, err
	}
	open, err := e.assignments.Actions(ctx, id)
	if err != nil {
		return State{}, err
	}
	var ids []identity.ActionID
	for _, act := range open {
		if act.State == assignment.StatePending || act.State == assignment.StateIssued {
			ids = append(ids, act.ID)
		}
	}
	if err := e.assignments.Invalidate(ctx, ids...); err != nil {
		return State{}, err
	}
	return e.moveTo(ctx, st, next)
}

// moveTo persists a step change.
func (e *Engine) moveTo(ctx context.Context, st State, next Step) (State, error) {
	st.Step = next
	st.UpdatedAt = e.now().UTC()
	if err := e.saveState(ctx, st); err != nil {
		return State{}, err
	}
	return st, nil
}

// currentBaseline reads today's fingerprints for a task.
func (e *Engine) currentBaseline(ctx context.Context, workID identity.WorkID, ref artifact.Ref, sources []fingerprint.Digest) (Baseline, error) {
	baseline, err := e.env.Fingerprints(ctx)
	if err != nil {
		return Baseline{}, err
	}
	doc, err := e.documentDigest(ctx, ref)
	if err != nil {
		return Baseline{}, err
	}
	baseline.Document = doc
	baseline.Sources = sources
	return baseline, nil
}

// documentDigest digests the SEMANTICS of a task document: the goal, and
// the checklist with its checkbox states normalized away.
//
// Two things are deliberately excluded, and both for the same reason —
// Homonto's own writes must not invalidate the plan that produced them.
// The evidence region is appended by Homonto at the end. And a checked box
// is Homonto recording progress against the plan, not a change to it;
// digesting the raw checklist would send the workflow back to the draft
// every time an implementer's work was accepted.
func (e *Engine) documentDigest(ctx context.Context, ref artifact.Ref) (fingerprint.Digest, error) {
	doc, err := e.artifacts.Read(ctx, ref)
	if err != nil {
		return "", err
	}
	var buf []byte
	buf = append(buf, artifact.RegionTaskGoal...)
	buf = append(buf, '\n')
	buf = append(buf, doc.Region(artifact.RegionTaskGoal)...)
	buf = append(buf, '\n')
	buf = append(buf, artifact.RegionTaskChecklist...)
	buf = append(buf, '\n')
	buf = append(buf, artifact.SemanticChecklist(doc.Region(artifact.RegionTaskChecklist))...)
	buf = append(buf, '\n')
	return fingerprint.Bytes("task-document", buf), nil
}

// ref returns the task document reference for a state.
func (s State) ref() artifact.Ref {
	return artifact.Ref{WorkID: s.WorkID, Kind: artifact.KindTaskDocument, Path: s.documentPath()}
}

// insertState writes a new task state, refusing to overwrite one.
func (e *Engine) insertState(ctx context.Context, st State) error {
	if err := st.Validate(); err != nil {
		return err
	}
	baseline, err := json.Marshal(st.Baseline)
	if err != nil {
		return fmt.Errorf("task: encode baseline: %w", err)
	}
	return e.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_states (work_id, name, step, generation, baseline, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			string(st.WorkID), st.Name, string(st.Step), st.Generation,
			string(baseline), st.UpdatedAt.UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("task: start %s: %w: %w", st.WorkID, ErrWorkExists, err)
		}
		return nil
	})
}

// saveState overwrites an existing task state.
func (e *Engine) saveState(ctx context.Context, st State) error {
	if err := st.Validate(); err != nil {
		return err
	}
	baseline, err := json.Marshal(st.Baseline)
	if err != nil {
		return fmt.Errorf("task: encode baseline: %w", err)
	}
	return e.db.Update(ctx, func(tx *store.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE task_states SET name = ?, step = ?, generation = ?, baseline = ?, updated_at = ?
			 WHERE work_id = ?`,
			st.Name, string(st.Step), st.Generation, string(baseline),
			st.UpdatedAt.UTC().Format(time.RFC3339Nano), string(st.WorkID))
		if err != nil {
			return fmt.Errorf("task: save %s: %w", st.WorkID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("task: save %s: %w", st.WorkID, err)
		}
		if n == 0 {
			return fmt.Errorf("task: %s: %w", st.WorkID, ErrUnknownWork)
		}
		return nil
	})
}

// loadState reads one task state.
func loadState(ctx context.Context, tx *store.Tx, id identity.WorkID) (State, error) {
	var (
		st        State
		baseline  string
		updatedAt string
	)
	err := tx.QueryRowContext(ctx, `
		SELECT work_id, name, step, generation, baseline, updated_at
		  FROM task_states WHERE work_id = ?`, string(id)).
		Scan(&st.WorkID, &st.Name, &st.Step, &st.Generation, &baseline, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return State{}, fmt.Errorf("task: %s: %w", id, ErrUnknownWork)
	}
	if err != nil {
		return State{}, fmt.Errorf("task: read %s: %w", id, err)
	}
	if err := json.Unmarshal([]byte(baseline), &st.Baseline); err != nil {
		return State{}, fmt.Errorf("task: decode baseline of %s: %w", id, err)
	}
	if st.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt); err != nil {
		return State{}, fmt.Errorf("task: decode update time of %s: %w", id, err)
	}
	if !st.Step.Known() {
		return State{}, fmt.Errorf("task: %s carries unknown step %q", id, st.Step)
	}
	return st, nil
}

// writeDocument renders and writes a document through the artifact
// service's confined root.
func (e *Engine) writeDocument(ctx context.Context, ref artifact.Ref, doc artifact.Document) error {
	return artifact.WriteRaw(e.artifacts, ref, doc)
}

// actionsForStep returns the actions issued for one step at the current
// generation.
func (e *Engine) actionsForStep(ctx context.Context, st State, step Step) ([]assignment.Action, error) {
	all, err := e.assignments.Actions(ctx, st.WorkID)
	if err != nil {
		return nil, err
	}
	var out []assignment.Action
	for _, act := range all {
		if act.Step == string(step) && act.Generation == st.Generation &&
			act.State != assignment.StateInvalidated {
			out = append(out, act)
		}
	}
	return out, nil
}

// allAnswered reports whether every action of a set has been answered, and
// whether the set is non-empty.
func allAnswered(actions []assignment.Action) (answered bool, any bool) {
	if len(actions) == 0 {
		return false, false
	}
	for _, act := range actions {
		if act.State != assignment.StateSubmitted {
			return false, true
		}
	}
	return true, true
}

// sortedDigests returns digests sorted for stable baselines.
func sortedDigests(in []fingerprint.Digest) []fingerprint.Digest {
	out := append([]fingerprint.Digest(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// decisionChoice returns the recorded choice of an answered decision.
func (e *Engine) decisionChoice(ctx context.Context, id identity.ActionID) (decision.Submission, bool, error) {
	return e.assignments.Decision(ctx, id)
}

// validateResult runs the independent final-diff gate over an assignment's
// observed changes. It is deliberately not optional and deliberately not
// the host's word: the process gate can be bypassed, this cannot.
func (e *Engine) validateResult(ctx context.Context, action protocol.Action, unit Partition) error {
	diff, err := e.env.ResultDiff(ctx, action, unit)
	if err != nil {
		return err
	}
	if err := e.guard.ValidateAssignmentResult(ctx, action, diff); err != nil {
		return fmt.Errorf("%w: %w", ErrResultRejected, err)
	}
	return nil
}

// blockingFindings reports whether any critical or high finding is open.
func (e *Engine) blockingFindings(ctx context.Context, id identity.WorkID) (bool, error) {
	blockers, err := e.findings.Blockers(ctx, id)
	if err != nil {
		return false, err
	}
	return len(blockers) > 0, nil
}
