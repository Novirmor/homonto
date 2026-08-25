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
var ErrNoActiveWork = errors.New("app: no single active task; name one explicitly")

// App is one opened workspace with every service wired. It is the object a
// CLI command holds: commands parse flags and render output, and every
// decision behind them lives in the engines.
type App struct {
	root   string
	cfg    workspacecfg.Config
	db     *store.DB
	engine *task.Engine
	env    *Environment

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
}

// Open opens the workspace at Root: it reads and validates the manifest,
// migrates the runtime database, and wires every service. Opening is
// read-write and journaled — a workspace with an interrupted operation is
// recovered before anything new is started, because building on an
// unfinished operation is how a half-applied effect becomes permanent.
func Open(ctx context.Context, opts Options) (*App, error) {
	root := opts.Root
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("app: resolve working directory: %w", err)
		}
		root = wd
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("app: resolve %s: %w", root, err)
	}
	abs = filepath.Clean(abs)

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
	db, err := store.Open(ctx, filepath.Join(abs, ControlDir, "runtime.db"), store.OpenOptions{})
	if err != nil {
		return nil, err
	}
	app, err := build(ctx, abs, cfg, db, opts.Git, now)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return app, nil
}

// build wires every service over an already-open database.
func build(ctx context.Context, root string, cfg workspacecfg.Config, db *store.DB, runner gitx.Runner, now func() time.Time) (*App, error) {
	ops := operation.NewManager(db)
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	git, err := gitx.NewService(runner, db, ops, root)
	if err != nil {
		return nil, err
	}
	snapshotStore := filepath.Join(root, ControlDir, "snapshots")
	if err := os.MkdirAll(snapshotStore, 0o700); err != nil {
		return nil, fmt.Errorf("app: create the snapshot store: %w", err)
	}
	snap, err := snapshot.NewService(db, ops, snapshotStore)
	if err != nil {
		return nil, err
	}
	// Finish what a previous run started before starting anything new.
	if err := ops.RecoverPending(ctx); err != nil {
		return nil, fmt.Errorf("app: recover pending operations: %w", err)
	}

	controlRoot := filepath.Join(root, filepath.FromSlash(normalizePath(cfg.Control.Path)))
	for _, sub := range []string{artifact.ActiveDir, archive.Dir} {
		if err := os.MkdirAll(filepath.Join(controlRoot, sub), 0o700); err != nil {
			return nil, fmt.Errorf("app: create %s: %w", sub, err)
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
	return &App{
		root: root, cfg: cfg, db: db, engine: engine, env: env,
		assignments: assignments, artifacts: artifacts, findings: findings,
		evidence: evidence, archive: arch, guard: g, now: now,
	}, nil
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

// Next returns the actions a host may execute now for the one active Task.
func (a *App) Next(ctx context.Context) (protocol.NextResponse, error) {
	id, err := a.ActiveWork(ctx)
	if err != nil {
		return protocol.NextResponse{}, err
	}
	return a.engine.Next(ctx, id)
}

// NextFor returns the actions for a named Task.
func (a *App) NextFor(ctx context.Context, id identity.WorkID) (protocol.NextResponse, error) {
	return a.engine.Next(ctx, id)
}

// SubmitReport records a host's answer to an assignment.
func (a *App) SubmitReport(ctx context.Context, in protocol.ReportSubmission) (task.State, error) {
	return a.engine.SubmitReport(ctx, in)
}

// Decide records a human's answer to a decision gate.
func (a *App) Decide(ctx context.Context, in decision.Submission) (task.State, error) {
	return a.engine.Decide(ctx, in)
}

// AcceptEdit accepts the host's document edit under the grant it was
// issued.
func (a *App) AcceptEdit(ctx context.Context, actionID identity.ActionID, grantToken identity.Token) (task.State, error) {
	return a.engine.AcceptEdit(ctx, actionID, grantToken)
}

// Authorize answers one guard request from a cooperating host's write
// hook.
func (a *App) Authorize(ctx context.Context, req guard.Request) (protocol.GuardDecision, error) {
	return a.guard.Authorize(ctx, req)
}

// Reconcile checks the recorded step against the world and returns what
// moved.
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

// ActiveWork resolves the one unambiguous active Task. A workspace with
// none, or with more than one, refuses rather than guessing: resuming the
// wrong work is worse than asking.
func (a *App) ActiveWork(ctx context.Context) (identity.WorkID, error) {
	tasks, err := a.Tasks(ctx)
	if err != nil {
		return "", err
	}
	var active []task.State
	for _, st := range tasks {
		if !st.Step.Terminal() {
			active = append(active, st)
		}
	}
	switch len(active) {
	case 1:
		return active[0].WorkID, nil
	case 0:
		return "", fmt.Errorf("app: no active task: %w", ErrNoActiveWork)
	default:
		names := make([]string, 0, len(active))
		for _, st := range active {
			names = append(names, st.Name)
		}
		return "", fmt.Errorf("app: %d active tasks (%v): %w", len(active), names, ErrNoActiveWork)
	}
}

// ResolveWork resolves a task by name or work id. An empty selector means
// the one active task.
func (a *App) ResolveWork(ctx context.Context, selector string) (identity.WorkID, error) {
	if selector == "" {
		return a.ActiveWork(ctx)
	}
	if err := identity.ValidateUUID(selector); err == nil {
		return identity.WorkID(selector), nil
	}
	tasks, err := a.Tasks(ctx)
	if err != nil {
		return "", err
	}
	var matches []task.State
	for _, st := range tasks {
		if st.Name == selector {
			matches = append(matches, st)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].WorkID, nil
	case 0:
		return "", fmt.Errorf("app: no task named %q: %w", selector, task.ErrUnknownWork)
	default:
		return "", fmt.Errorf("app: %d tasks named %q; name one by work id: %w",
			len(matches), selector, ErrNoActiveWork)
	}
}
