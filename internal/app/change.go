package app

import (
	"context"

	"github.com/noviopenworks/homonto/internal/change"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
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
