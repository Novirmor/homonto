package app

import (
	"context"
	"fmt"
	"os"

	"github.com/noviopenworks/homonto/internal/change"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// WorkspaceDiff returns what changed across the workspace since a work
// baseline, as path-class diff entries.
//
// A Git member's diff is its integration branch against the commit the
// baseline pinned; a non-Git member's is its staged result against the
// captured snapshot. A member with no integration area yet has changed
// nothing, which is the honest answer before any implementation lands.
func (e *Environment) WorkspaceDiff(ctx context.Context, workID identity.WorkID, _ []fingerprint.Digest) ([]pathclass.DiffEntry, error) {
	members, err := e.Members(ctx)
	if err != nil {
		return nil, err
	}
	var out []pathclass.DiffEntry
	for _, m := range members {
		if !m.Git {
			continue
		}
		dir := gitx.IntegrationPath(e.root, workID, m.ID)
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		raw, err := e.runner.Run(ctx, dir, "diff", "--name-status", "-z", "HEAD@{upstream}...HEAD")
		if err != nil {
			// No upstream to compare against; the member has produced
			// nothing comparable yet.
			continue
		}
		for _, c := range parseNameStatus(raw) {
			out = append(out, pathclass.DiffEntry{
				Member: m.Path, Path: c.Path, Op: toPathClassOp(c.Kind),
			})
		}
	}
	return out, nil
}

// Matchers resolves a member's path-class matcher by its workspace-
// relative path.
func (e *Environment) Matchers(member string) (*pathclass.Matcher, error) {
	for _, m := range e.cfg.Members {
		if normalizePath(m.Path) == normalizePath(member) {
			return pathclass.NewMatcher(m.Paths)
		}
	}
	// The control repository, and any member with no declared classes,
	// classifies everything as source. That is the conservative answer:
	// counting a path that should have been excluded pauses a preset for a
	// human, while excluding one that should have counted lets a large
	// change through unremarked.
	return pathclass.NewMatcher(nil)
}

// toPathClassOp translates a guard change kind into a diff operation.
func toPathClassOp(k guard.ChangeKind) pathclass.Op {
	switch k {
	case guard.ChangeAdded:
		return pathclass.OpAdded
	case guard.ChangeDeleted:
		return pathclass.OpDeleted
	}
	return pathclass.OpModified
}

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
