package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/noviopenworks/homonto/internal/archive"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/change"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/finding"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/task"
	"github.com/noviopenworks/homonto/internal/verify"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// ControlDir is the control-plane directory inside the workspace root.
const ControlDir = ".homonto"

// ManifestName is the workspace manifest file inside the control
// directory.
const ManifestName = "workspace.toml"

// ErrNoActiveWork reports a command that needs one unambiguous active task
// and found none, or found more than one.
var ErrNoActiveWork = errors.New("app: no single active work; name one explicitly")

// App is one opened workspace with every service wired. It is the object a
// CLI command holds: commands parse flags and render output, and every
// decision behind them lives in the engines.
type App struct {
	root    string
	cfg     workspacecfg.Config
	db      *store.DB
	engine  *task.Engine
	changes *change.Engine
	env     *Environment

	assignments *assignment.Store
	artifacts   *artifact.Service
	findings    *finding.Service
	evidence    *verify.Store
	archive     *archive.Service
	guard       *guard.Guard
	now         func() time.Time
}

// Options configure how a workspace is opened.
type Options struct {
	// Root is the workspace root. Empty means the working directory.
	Root string
	// Git overrides the git runner; tests inject a recording one.
	Git gitx.Runner
	// Now overrides the clock.
	Now func() time.Time
	// ReadOnly opens the workspace without changing anything: no
	// scaffolding, no migration, no recovery pass. It is what the resume
	// probe uses, because a host runs that at the start of every session
	// and starting a session must change nothing.
	ReadOnly bool
}

// Open opens the workspace at Root: it reads and validates the manifest,
// migrates the runtime database, and wires every service. Opening is
// read-write and journaled — a workspace with an interrupted operation is
// recovered before anything new is started, because building on an
// unfinished operation is how a half-applied effect becomes permanent.
func Open(ctx context.Context, opts Options) (*App, error) {
	abs, err := resolveRoot(opts.Root)
	if err != nil {
		return nil, err
	}

	manifest := filepath.Join(abs, ControlDir, ManifestName)
	cfg, err := workspacecfg.Load(manifest)
	if err != nil {
		return nil, err
	}
	if err := workspacecfg.Validate(abs, cfg); err != nil {
		return nil, err
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	db, err := store.Open(ctx, filepath.Join(abs, ControlDir, "runtime.db"),
		store.OpenOptions{ReadOnly: opts.ReadOnly})
	if err != nil {
		return nil, err
	}
	app, err := build(ctx, abs, cfg, db, opts.Git, now, opts.ReadOnly)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return app, nil
}

// build wires every service over an already-open database.
func build(ctx context.Context, root string, cfg workspacecfg.Config, db *store.DB, runner gitx.Runner, now func() time.Time, readOnly bool) (*App, error) {
	ops := operation.NewManager(db)
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	git, err := gitx.NewService(runner, db, ops, root)
	if err != nil {
		return nil, err
	}
	snapshotStore := filepath.Join(root, ControlDir, "snapshots")
	if !readOnly {
		if err := os.MkdirAll(snapshotStore, 0o700); err != nil {
			return nil, fmt.Errorf("app: create the snapshot store: %w", err)
		}
	}
	snap, err := snapshot.NewService(db, ops, snapshotStore)
	if err != nil {
		return nil, err
	}
	controlRoot := filepath.Join(root, filepath.FromSlash(normalizePath(cfg.Control.Path)))
	if !readOnly {
		// Finish what a previous run started before starting anything new.
		if err := ops.RecoverPending(ctx); err != nil {
			return nil, fmt.Errorf("app: recover pending operations: %w", err)
		}
		// The archive service never creates a directory itself, so the
		// document tree is scaffolded here, once, when the workspace
		// opens for work.
		for _, sub := range archive.Dirs() {
			if err := os.MkdirAll(filepath.Join(controlRoot, filepath.FromSlash(sub)), 0o755); err != nil {
				return nil, fmt.Errorf("app: create %s: %w", sub, err)
			}
		}
	}
	journal, err := artifact.NewStoreJournal(db)
	if err != nil {
		return nil, err
	}
	artifacts, err := artifact.NewService(controlRoot, journal, now)
	if err != nil {
		return nil, err
	}
	assignments, err := assignment.NewStore(ctx, db, now)
	if err != nil {
		return nil, err
	}
	findings, err := finding.NewService(db, now)
	if err != nil {
		return nil, err
	}
	evidence, err := verify.NewStore(db, now)
	if err != nil {
		return nil, err
	}
	arch, err := archive.NewService(controlRoot)
	if err != nil {
		return nil, err
	}
	g, err := guard.New(assignments, journal)
	if err != nil {
		return nil, err
	}
	env, err := NewEnvironment(root, cfg, git, runner, snap, snapshotStore)
	if err != nil {
		return nil, err
	}
	engine, err := task.NewEngine(task.Dependencies{
		DB: db, Assignments: assignments, Artifacts: artifacts, Findings: findings,
		Evidence: evidence, Archive: arch, Guard: g, Environment: env, Now: now,
	})
	if err != nil {
		return nil, err
	}
	changes, err := change.NewEngine(change.Dependencies{
		DB: db, Assignments: assignments, Artifacts: artifacts, Findings: findings,
		Evidence: evidence, Archive: arch, Guard: g,
		Environment: changeEnvironment{env: env}, Now: now,
	})
	if err != nil {
		return nil, err
	}
	return &App{
		root: root, cfg: cfg, db: db, engine: engine, changes: changes, env: env,
		assignments: assignments, artifacts: artifacts, findings: findings,
		evidence: evidence, archive: arch, guard: g, now: now,
	}, nil
}

// resolveRoot turns a possibly-empty root into an absolute clean path.
func resolveRoot(root string) (string, error) {
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("app: resolve working directory: %w", err)
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("app: resolve %s: %w", root, err)
	}
	return filepath.Clean(abs), nil
}

// Close releases the workspace.
func (a *App) Close() error { return a.db.Close() }

// Root returns the workspace root.
func (a *App) Root() string { return a.root }

// Config returns the workspace manifest.
func (a *App) Config() workspacecfg.Config { return a.cfg }

// Engine exposes the Task engine for callers that drive it directly.
func (a *App) Engine() *task.Engine { return a.engine }

// StartTask creates a new Task and returns its state.
func (a *App) StartTask(ctx context.Context, in task.StartInput) (task.State, error) {
	return a.engine.Start(ctx, in)
}

// TaskState returns one Task's state.
func (a *App) TaskState(ctx context.Context, id identity.WorkID) (task.State, error) {
	return a.engine.State(ctx, id)
}

// AbandonTask stops a Task, leaving its isolation areas and evidence for
// external handling.
func (a *App) AbandonTask(ctx context.Context, id identity.WorkID) (task.State, error) {
	return a.engine.Abandon(ctx, id)
}

// WorkKind names which engine owns a work id.
type WorkKind string

const (
	// WorkTask: a Task.
	WorkTask WorkKind = "task"
	// WorkChange: a confirmed Change.
	WorkChange WorkKind = "change"
	// WorkPreflight: a local Change classification candidate, which is
	// not yet a Change.
	WorkPreflight WorkKind = "preflight"
)

// WorkKindOf reports which engine owns a work id.
func (a *App) WorkKindOf(ctx context.Context, id identity.WorkID) (WorkKind, error) {
	if _, err := a.engine.State(ctx, id); err == nil {
		return WorkTask, nil
	} else if !errors.Is(err, task.ErrUnknownWork) {
		return "", err
	}
	if _, err := a.changes.State(ctx, id); err == nil {
		return WorkChange, nil
	} else if !errors.Is(err, change.ErrUnknownChange) {
		return "", err
	}
	if _, err := a.changes.Preflight(ctx, id); err == nil {
		return WorkPreflight, nil
	} else if !errors.Is(err, change.ErrUnknownPreflight) {
		return "", err
	}
	return "", fmt.Errorf("app: %s: %w", id, task.ErrUnknownWork)
}

// Next returns the actions a host may execute now for the one active work.
func (a *App) Next(ctx context.Context) (protocol.NextResponse, error) {
	id, err := a.ActiveWork(ctx)
	if err != nil {
		return protocol.NextResponse{}, err
	}
	return a.NextFor(ctx, id)
}

// NextFor returns the actions for a named work, whichever engine owns it.
func (a *App) NextFor(ctx context.Context, id identity.WorkID) (protocol.NextResponse, error) {
	kind, err := a.WorkKindOf(ctx, id)
	if err != nil {
		return protocol.NextResponse{}, err
	}
	switch kind {
	case WorkTask:
		return a.engine.Next(ctx, id)
	case WorkChange:
		return a.changes.Next(ctx, id)
	}
	return a.changes.NextPreflight(ctx, id)
}

// SubmitReport records a host's answer to an assignment, routed to the
// engine that issued it.
func (a *App) SubmitReport(ctx context.Context, in protocol.ReportSubmission) (Status, error) {
	kind, id, err := a.workOfAction(ctx, in.ActionID)
	if err != nil {
		return Status{}, err
	}
	if kind == WorkTask {
		st, err := a.engine.SubmitReport(ctx, in)
		return taskStatus(st), err
	}
	// A preflight assignment is answered through the assignment store
	// directly: a candidate has no workflow to advance, only an
	// assessment to complete.
	if kind == WorkPreflight {
		if _, err := a.assignments.Submit(ctx, in); err != nil {
			return Status{}, err
		}
		pre, err := a.changes.Preflight(ctx, id)
		return preflightStatus(pre), err
	}
	st, err := a.changes.SubmitReport(ctx, in)
	return changeStatus(st), err
}

// Decide records a human's answer to a decision gate.
func (a *App) Decide(ctx context.Context, in decision.Submission) (Status, error) {
	kind, _, err := a.workOfAction(ctx, in.ActionID)
	if err != nil {
		return Status{}, err
	}
	if kind == WorkTask {
		st, err := a.engine.Decide(ctx, in)
		return taskStatus(st), err
	}
	st, err := a.changes.Decide(ctx, in)
	return changeStatus(st), err
}

// AcceptEdit accepts the host's document edit under the grant it was
// issued.
func (a *App) AcceptEdit(ctx context.Context, actionID identity.ActionID, grantToken identity.Token) (Status, error) {
	kind, _, err := a.workOfAction(ctx, actionID)
	if err != nil {
		return Status{}, err
	}
	if kind == WorkTask {
		st, err := a.engine.AcceptEdit(ctx, actionID, grantToken)
		return taskStatus(st), err
	}
	st, err := a.changes.AcceptEdit(ctx, actionID, grantToken)
	return changeStatus(st), err
}

// workOfAction resolves which engine owns the work an action belongs to.
func (a *App) workOfAction(ctx context.Context, id identity.ActionID) (WorkKind, identity.WorkID, error) {
	act, err := a.assignments.Action(ctx, id)
	if err != nil {
		return "", "", err
	}
	kind, err := a.WorkKindOf(ctx, act.WorkID)
	return kind, act.WorkID, err
}

// Authorize answers one guard request from a cooperating host's write
// hook. It is engine-neutral: the guard decides from the assignment or the
// grant a session presents, and neither belongs to a workflow.
func (a *App) Authorize(ctx context.Context, req guard.Request) (protocol.GuardDecision, error) {
	return a.guard.Authorize(ctx, req)
}

// Status is the engine-neutral summary a command prints after recording
// something: which work, what it is, and where it now stands.
type Status struct {
	WorkID identity.WorkID
	Name   string
	Kind   WorkKind
	Step   string
}

func taskStatus(st task.State) Status {
	return Status{WorkID: st.WorkID, Name: st.Name, Kind: WorkTask, Step: string(st.Step)}
}

func changeStatus(st change.State) Status {
	return Status{WorkID: st.WorkID, Name: st.Name, Kind: WorkChange, Step: st.Step}
}

func preflightStatus(st change.PreflightState) Status {
	return Status{WorkID: st.WorkID, Name: st.Name, Kind: WorkPreflight, Step: string(st.Step)}
}

// Reconcile checks a Task's recorded step against the world.
func (a *App) Reconcile(ctx context.Context, id identity.WorkID) (task.State, []task.Invalidation, error) {
	return a.engine.Reconcile(ctx, id)
}

// Tasks returns every recorded Task, oldest first.
//
// The ids are read in one transaction and the states loaded after it
// closes. The runtime database serializes every access through a single
// connection, so a read that starts a second transaction inside the first
// waits for a connection that only it could release.
func (a *App) Tasks(ctx context.Context) ([]task.State, error) {
	var ids []identity.WorkID
	err := a.db.View(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT work_id FROM task_states ORDER BY updated_at, work_id`)
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
		return nil, fmt.Errorf("app: list tasks: %w", err)
	}
	out := make([]task.State, 0, len(ids))
	for _, id := range ids {
		st, err := a.engine.State(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// ActiveWork resolves the one unambiguous active work — a Task, a Change,
// or a Change classification candidate. A workspace with none, or with
// more than one, refuses rather than guessing: resuming the wrong work is
// worse than asking.
func (a *App) ActiveWork(ctx context.Context) (identity.WorkID, error) {
	active, err := a.activeWorks(ctx)
	if err != nil {
		return "", err
	}
	switch len(active) {
	case 1:
		return active[0].WorkID, nil
	case 0:
		return "", fmt.Errorf("app: no active work: %w", ErrNoActiveWork)
	default:
		names := make([]string, 0, len(active))
		for _, w := range active {
			names = append(names, w.Name)
		}
		return "", fmt.Errorf("app: %d active works (%v): %w", len(active), names, ErrNoActiveWork)
	}
}

// activeWorks lists every unfinished work across both engines.
func (a *App) activeWorks(ctx context.Context) ([]Status, error) {
	var out []Status
	tasks, err := a.Tasks(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range tasks {
		if !st.Step.Terminal() {
			out = append(out, taskStatus(st))
		}
	}
	changes, err := a.changes.States(ctx)
	if err != nil {
		return nil, err
	}
	confirmed := map[identity.WorkID]bool{}
	for _, st := range changes {
		confirmed[st.WorkID] = true
		if !change.Terminal(st) {
			out = append(out, changeStatus(st))
		}
	}
	candidates, err := a.changes.Preflights(ctx)
	if err != nil {
		return nil, err
	}
	for _, pre := range candidates {
		// A confirmed candidate IS its change; listing both would make one
		// piece of work look like two and refuse every unqualified
		// command.
		if pre.Step.Terminal() || confirmed[pre.WorkID] {
			continue
		}
		out = append(out, preflightStatus(pre))
	}
	return out, nil
}

// Works returns every recorded work across both engines, for status.
func (a *App) Works(ctx context.Context) ([]Status, error) {
	var out []Status
	tasks, err := a.Tasks(ctx)
	if err != nil {
		return nil, err
	}
	for _, st := range tasks {
		out = append(out, taskStatus(st))
	}
	changes, err := a.changes.States(ctx)
	if err != nil {
		return nil, err
	}
	confirmed := map[identity.WorkID]bool{}
	for _, st := range changes {
		confirmed[st.WorkID] = true
		out = append(out, changeStatus(st))
	}
	candidates, err := a.changes.Preflights(ctx)
	if err != nil {
		return nil, err
	}
	for _, pre := range candidates {
		if confirmed[pre.WorkID] {
			continue
		}
		out = append(out, preflightStatus(pre))
	}
	return out, nil
}

// ResolveWork resolves a work by name or id. An empty selector means the
// one active work.
func (a *App) ResolveWork(ctx context.Context, selector string) (identity.WorkID, error) {
	if selector == "" {
		return a.ActiveWork(ctx)
	}
	if err := identity.ValidateUUID(selector); err == nil {
		return identity.WorkID(selector), nil
	}
	works, err := a.Works(ctx)
	if err != nil {
		return "", err
	}
	var matches []Status
	for _, w := range works {
		if w.Name == selector {
			matches = append(matches, w)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].WorkID, nil
	case 0:
		return "", fmt.Errorf("app: no work named %q: %w", selector, task.ErrUnknownWork)
	default:
		return "", fmt.Errorf("app: %d works named %q; name one by work id: %w",
			len(matches), selector, ErrNoActiveWork)
	}
}
