package app

import (
	"context"
	"fmt"
	"os"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/change"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/task"
	"github.com/noviopenworks/homonto/internal/verify"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// changeEnvironment adapts the workspace Environment to the Change
// engine's contract.
//
// The two engines ask the same questions in their own vocabularies, and
// the adapter is a translation rather than a second implementation: the
// alternative — having one engine import the other's types — would couple
// the Task workflow to the Change workflow for no reason beyond saving
// this file.
type changeEnvironment struct{ env *Environment }

func (c changeEnvironment) Control(ctx context.Context) (change.Member, error) {
	m, err := c.env.Control(ctx)
	return toChangeMember(m), err
}

func (c changeEnvironment) Members(ctx context.Context) ([]change.Member, error) {
	members, err := c.env.Members(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]change.Member, len(members))
	for i, m := range members {
		out[i] = toChangeMember(m)
	}
	return out, nil
}

func (c changeEnvironment) Fingerprints(ctx context.Context) (change.Baseline, error) {
	base, err := c.env.Fingerprints(ctx)
	if err != nil {
		return change.Baseline{}, err
	}
	return change.Baseline{
		Membership:  base.Membership,
		PathClass:   base.PathClass,
		CheckConfig: base.CheckConfig,
	}, nil
}

func (c changeEnvironment) Partition(ctx context.Context, workID identity.WorkID, items []artifact.Item) ([]change.Unit, error) {
	units, err := c.env.Partition(ctx, workID, items)
	if err != nil {
		return nil, err
	}
	out := make([]change.Unit, len(units))
	for i, u := range units {
		out[i] = toChangeUnit(u)
	}
	return out, nil
}

func (c changeEnvironment) Isolate(ctx context.Context, workID identity.WorkID, actionID identity.ActionID, unit change.Unit) (change.Unit, error) {
	isolated, err := c.env.Isolate(ctx, workID, actionID, toTaskPartition(unit))
	if err != nil {
		return change.Unit{}, err
	}
	return toChangeUnit(isolated), nil
}

func (c changeEnvironment) Integrations(ctx context.Context, workID identity.WorkID, results []change.Result) ([]change.Unit, error) {
	converted := make([]task.Result, len(results))
	for i, r := range results {
		converted[i] = task.Result{
			ActionID: r.ActionID, Partition: toTaskPartition(r.Unit), Material: r.Material,
		}
	}
	units, err := c.env.Integrations(ctx, workID, converted)
	if err != nil {
		return nil, err
	}
	out := make([]change.Unit, len(units))
	for i, u := range units {
		out[i] = toChangeUnit(u)
	}
	return out, nil
}

func (c changeEnvironment) SourceFingerprints(ctx context.Context, workID identity.WorkID) ([]fingerprint.Digest, error) {
	return c.env.SourceFingerprints(ctx, workID)
}

func (c changeEnvironment) RunChecks(ctx context.Context, workID identity.WorkID) (verify.Set, error) {
	return c.env.RunChecks(ctx, workID)
}

func (c changeEnvironment) ResultDiff(ctx context.Context, action protocol.Action, unit change.Unit) (guard.ResultDiff, error) {
	return c.env.ResultDiff(ctx, action, toTaskPartition(unit))
}

// WorkspaceDiff returns the integrated workspace diff against the change's
// immutable work baseline — the input to the preset scope count.
func (c changeEnvironment) WorkspaceDiff(ctx context.Context, workID identity.WorkID, baseline []fingerprint.Digest) ([]pathclass.DiffEntry, error) {
	return c.env.WorkspaceDiff(ctx, workID, baseline)
}

// Matchers resolves a member's path-class matcher by member path.
func (c changeEnvironment) Matchers(member string) (*pathclass.Matcher, error) {
	return c.env.Matchers(member)
}

func toChangeMember(m task.Member) change.Member {
	return change.Member{ID: m.ID, Path: m.Path, Git: m.Git}
}

func toTaskMember(m change.Member) task.Member {
	return task.Member{ID: m.ID, Path: m.Path, Git: m.Git}
}

func toChangeUnit(p task.Partition) change.Unit {
	return change.Unit{
		Label: p.Label, Member: toChangeMember(p.Member), Items: p.Items,
		Integration: p.Integration, Base: p.Base, Root: p.Root,
		Scope: p.Scope, Prompt: p.Prompt,
	}
}

func toTaskPartition(u change.Unit) task.Partition {
	return task.Partition{
		Label: u.Label, Member: toTaskMember(u.Member), Items: u.Items,
		Integration: u.Integration, Base: u.Base, Root: u.Root,
		Scope: u.Scope, Prompt: u.Prompt,
	}
}

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
	if err := a.requireCleanMembers(ctx); err != nil {
		return change.PreflightState{}, protocol.NextResponse{}, err
	}
	return a.changes.StartPreflight(ctx, in)
}

// ConfirmChangePath records the human's confirmed path and creates the
// change.
//
// Confirmation, not preflight, is where the work becomes this machine's:
// a candidate that is still being classified has nothing to hand over.
func (a *App) ConfirmChangePath(ctx context.Context, in change.ConfirmInput) (change.State, error) {
	st, err := a.changes.ConfirmPreflight(ctx, in)
	if err != nil {
		return st, err
	}
	if err := a.activate(ctx, st.WorkID, WorkChange, st.Name,
		artifact.ChangesDir+"/"+st.Name+".md", st.Step); err != nil {
		return st, err
	}
	return st, nil
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
	return st, a.deactivate(ctx, id)
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
