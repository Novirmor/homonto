package app

import (
	"context"
	"fmt"

	"github.com/noviopenworks/homonto/internal/change"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// StartChangePreflight opens a local classification candidate.
func (a *App) StartChangePreflight(ctx context.Context, in change.PreflightInput) (change.PreflightState, protocol.NextResponse, error) {
	if err := a.requireNoActiveWork(ctx); err != nil {
		return change.PreflightState{}, protocol.NextResponse{}, err
	}
	if err := a.portable.RequireCleanMembers(ctx); err != nil {
		return change.PreflightState{}, protocol.NextResponse{}, err
	}
	return a.changes.StartPreflight(ctx, in)
}

// AbandonChangePreflight drops a classification candidate.
func (a *App) AbandonChangePreflight(ctx context.Context, id identity.WorkID) (change.PreflightState, error) {
	return a.changes.AbandonPreflight(ctx, id)
}

// ChangeState returns one change's state.
func (a *App) ChangeState(ctx context.Context, id identity.WorkID) (change.State, error) {
	return a.changes.State(ctx, id)
}

// Changes returns every recorded change, oldest first.
func (a *App) Changes(ctx context.Context) ([]change.State, error) {
	return a.changes.States(ctx)
}

// AbandonChange stops a change, leaving its isolation areas and evidence
// for external handling.
func (a *App) AbandonChange(ctx context.Context, id identity.WorkID) (change.State, error) {
	st, err := a.changes.Abandon(ctx, id)
	if err != nil {
		return st, err
	}
	return st, a.portable.Deactivate(ctx, id)
}

// ReconcileChange checks a change's recorded step against the world.
func (a *App) ReconcileChange(ctx context.Context, id identity.WorkID) (change.State, []change.Invalidation, error) {
	return a.changes.Reconcile(ctx, id)
}

// changeWorkflow reports whether the workspace is configured for Changes.
func (a *App) changeWorkflow() bool {
	return a.cfg.Workspace.Workflow == workspacecfg.WorkflowChange
}

// requireChangeWorkflow refuses Change commands in a Task workspace. The
// workflow is a workspace-level choice, and running the other one would
// write documents the workspace does not expect.
func (a *App) requireChangeWorkflow() error {
	if a.changeWorkflow() {
		return nil
	}
	return fmt.Errorf("app: this workspace runs the %s workflow, not %s",
		a.cfg.Workspace.Workflow, workspacecfg.WorkflowChange)
}
