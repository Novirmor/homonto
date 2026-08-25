package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/handoff"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/securefs"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// The portable record — the committed checkpoint and the leases that
// anchor a work to this machine — is written here, where a work becomes
// active and where it stops being active.
//
// It is kept apart from the engines on purpose. Nothing in the Task or
// Change workflow depends on it: a workspace that never leaves one
// machine behaves identically with or without it. What it buys is the
// answer to "someone else has to pick this up", and that is a property of
// the workspace rather than of a workflow step.

// activate anchors a newly started work to this machine: it leases every
// member and writes the work's first checkpoint.
//
// Leases first, checkpoint second. The lease is what makes the claim
// exclusive, so a second machine that raced this one loses at the lease
// and never writes a checkpoint over the winner's.
func (a *App) activate(ctx context.Context, workID identity.WorkID, kind WorkKind, name, docPath, phase string) error {
	targets, err := a.leaseTargets(ctx)
	if err != nil {
		return err
	}
	provenance, err := lease.CurrentProcess()
	if err != nil {
		return fmt.Errorf("app: describe this process: %w", err)
	}
	if _, err := a.leases.AcquireAll(ctx, lease.AcquireRequest{
		WorkspaceID: a.cfg.Workspace.ID,
		WorkID:      workID,
		Generation:  1,
		Provenance:  provenance,
		ControlRoot: a.controlRoot(),
		WorkKind:    string(kind),
		Title:       name,
		Targets:     targets,
	}); err != nil {
		return fmt.Errorf("app: lease the workspace for %s: %w", workID, err)
	}
	cp, err := a.buildCheckpoint(ctx, &checkpoint.Work{
		ID: workID, Name: name, Workflow: a.cfg.Workspace.Workflow,
		Path: docPath, Phase: phase, Generation: 1,
	}, checkpoint.Handoff{State: checkpoint.HandoffLocal, Generation: 1})
	if err != nil {
		return err
	}
	return a.writeCheckpoint(cp)
}

// refreshCheckpoint brings the committed record up to date with where the
// work actually is.
//
// It is deliberately best-effort in one narrow way: a workspace whose
// checkpoint has not been written yet (one that predates this wiring, or
// one whose work was started before it) is left alone rather than being
// given a checkpoint mid-flight. A checkpoint invented halfway through a
// work would claim anchors that were never the ones the work started
// from.
func (a *App) refreshCheckpoint(ctx context.Context, workID identity.WorkID, phase string) error {
	current, err := a.checkpoint()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if current.Work == nil || current.Work.ID != workID {
		return nil
	}
	// A transferable checkpoint belongs to whoever attaches it next.
	// Rewriting it here would silently un-hand-off the work.
	if current.Handoff.State != checkpoint.HandoffLocal {
		return nil
	}
	work := *current.Work
	work.Phase = phase
	next, err := a.buildCheckpoint(ctx, &work, current.Handoff)
	if err != nil {
		return err
	}
	if sameCheckpoint(next, current) {
		return nil
	}
	return a.writeCheckpoint(next)
}

// deactivate releases the work's hold on this machine when it archives or
// is abandoned: the leases go, and the checkpoint stops naming a work.
//
// Releasing is not the same as forgetting. The checkpoint stays, still
// describing the workspace, so the next work starts from a record rather
// than from nothing.
func (a *App) deactivate(ctx context.Context, workID identity.WorkID) error {
	sentinel, err := lease.ReadSentinel(lease.SentinelPath(a.controlRoot(), workID))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("app: read the lease sentinel of %s: %w", workID, err)
	}
	if err == nil {
		var held []lease.Lease
		for _, l := range sentinel.Leases {
			content, err := lease.ReadLease(l.Path)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				return fmt.Errorf("app: read the lease of %s: %w", l.RepositoryID, err)
			}
			held = append(held, lease.Lease{Path: l.Path, Content: content})
		}
		if len(held) > 0 {
			if err := a.leases.ReleaseAll(ctx, held); err != nil {
				return fmt.Errorf("app: release the leases of %s: %w", workID, err)
			}
		}
		if err := os.Remove(lease.SentinelPath(a.controlRoot(), workID)); err != nil &&
			!errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("app: remove the lease sentinel of %s: %w", workID, err)
		}
	}
	current, err := a.checkpoint()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if current.Work == nil || current.Work.ID != workID {
		return nil
	}
	next, err := a.buildCheckpoint(ctx, nil, checkpoint.Handoff{
		State: checkpoint.HandoffLocal, Generation: current.Handoff.Generation,
	})
	if err != nil {
		return err
	}
	return a.writeCheckpoint(next)
}

// ErrWorkAlreadyActive: a workspace already has an active work.
var ErrWorkAlreadyActive = errors.New("app: a work is already active in this workspace")

// requireNoActiveWork refuses to start a second work.
//
// Exactly one top-level Task or Change may be active in a workspace;
// parallelism happens INSIDE that work, through subagents and isolated
// worktrees. Two top-level works would share every member, so their
// isolation areas, their integration branches, and their checks would all
// be measuring a tree the other one is also changing — and only one of
// them could hold the leases that make the work portable.
//
// Finish or abandon the first one.
func (a *App) requireNoActiveWork(ctx context.Context) error {
	active, err := a.activeWorks(ctx)
	if err != nil {
		return err
	}
	if len(active) == 0 {
		return nil
	}
	names := make([]string, 0, len(active))
	for _, w := range active {
		names = append(names, w.Name)
	}
	return fmt.Errorf("app: %s is already active; finish or abandon it first: %w",
		strings.Join(names, ", "), ErrWorkAlreadyActive)
}

// requireCleanMembers refuses to start work over uncommitted changes.
//
// An assignment cannot be cut from a dirty member — ADR 0024: dirty trees
// are rejected, never tidied — and that refusal used to arrive several
// steps in, after the work existed, the document was written, and the
// explorer and skeptic had been answered. The tree was dirty the whole
// time. Checking here costs one `git status` per member and turns a
// wasted round into a sentence.
//
// The CONTROL repository is exempt. It holds the workflow documents
// Homonto itself writes, so it is dirty as a matter of course; whether a
// member is clean is a question about the member.
func (a *App) requireCleanMembers(ctx context.Context) error {
	for _, m := range a.cfg.Members {
		if m.Kind != workspacecfg.KindGit || m.ID == a.cfg.Control.ID {
			continue
		}
		dir := filepath.Join(a.root, filepath.FromSlash(normalizePath(m.Path)))
		files, err := gitx.DirtyPaths(ctx, a.env.runner, dir)
		if err != nil {
			return fmt.Errorf("app: check %s: %w", m.Path, err)
		}
		if len(files) > 0 {
			return fmt.Errorf(
				"app: %s has uncommitted changes (%s); commit, stash, or discard them first: %w",
				m.Path, strings.Join(files, ", "), gitx.ErrDirtyWorktree)
		}
	}
	return nil
}

// anchoredWork returns the work this machine currently holds the
// workspace's leases for, or empty when none does.
func (a *App) anchoredWork() (identity.WorkID, error) {
	dir := filepath.Join(a.controlRoot(), ControlDir, "leases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("app: read %s: %w", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".active") {
			continue
		}
		return identity.WorkID(strings.TrimSuffix(name, ".active")), nil
	}
	return "", nil
}

// buildCheckpoint assembles the current portable record.
func (a *App) buildCheckpoint(ctx context.Context, work *checkpoint.Work, hand checkpoint.Handoff) (checkpoint.Checkpoint, error) {
	fp, err := workspacecfg.Fingerprint(a.cfg)
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("app: fingerprint the manifest: %w", err)
	}
	cp := checkpoint.Checkpoint{
		SchemaVersion:     checkpoint.CurrentSchemaVersion,
		WorkspaceID:       a.cfg.Workspace.ID,
		ConfigFingerprint: fp,
		Work:              work,
		Handoff:           hand,
	}
	for _, m := range append([]workspacecfg.Member{a.controlMember()}, a.cfg.Members...) {
		member, err := a.checkpointMember(ctx, m, work)
		if err != nil {
			return checkpoint.Checkpoint{}, err
		}
		cp.Members = append(cp.Members, member)
	}
	sort.Slice(cp.Members, func(i, j int) bool { return cp.Members[i].ID < cp.Members[j].ID })
	if err := checkpoint.Validate(cp, a.cfg); err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("app: build the checkpoint: %w", err)
	}
	return cp, nil
}

// controlMember presents the control repository in member shape.
func (a *App) controlMember() workspacecfg.Member {
	return workspacecfg.Member{
		ID: a.cfg.Control.ID, Path: a.cfg.Control.Path, Kind: workspacecfg.KindGit,
	}
}

// checkpointMember anchors one member: where its work started and how far
// integration has got. Anchors are read from the repository rather than
// remembered, because what a checkpoint must describe is where the work
// actually sits.
func (a *App) checkpointMember(ctx context.Context, m workspacecfg.Member, work *checkpoint.Work) (checkpoint.Member, error) {
	out := checkpoint.Member{ID: m.ID, Kind: m.Kind}
	dir := filepath.Join(a.root, filepath.FromSlash(normalizePath(m.Path)))
	if m.Kind != workspacecfg.KindGit {
		// A non-Git member's whole anchor is what its content digests to.
		digest, err := a.env.SourceDigest(ctx, dir)
		if err != nil {
			return checkpoint.Member{}, err
		}
		out.SourceFingerprint = digest
		return out, nil
	}
	branch, commit, err := a.head(ctx, dir)
	if err != nil {
		return checkpoint.Member{}, err
	}
	out.BaseBranch, out.BaseCommit = branch, commit
	out.IntegrationBranch, out.IntegrationCommit = branch, commit
	digest, err := a.env.git.TreeFingerprint(ctx, dir, commit)
	if err != nil {
		return checkpoint.Member{}, err
	}
	out.SourceFingerprint = digest
	if work == nil {
		return out, nil
	}
	// Once the work has an integration area, that is how far it has got.
	integration := gitx.IntegrationPath(a.root, work.ID, m.ID)
	if _, err := os.Stat(integration); err != nil {
		return out, nil
	}
	iBranch, iCommit, err := a.head(ctx, integration)
	if err != nil {
		return checkpoint.Member{}, err
	}
	out.IntegrationBranch, out.IntegrationCommit = iBranch, iCommit
	return out, nil
}

// head reads a repository's current branch and commit.
func (a *App) head(ctx context.Context, dir string) (string, string, error) {
	branch, err := a.env.runner.Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("app: read the branch of %s: %w", dir, err)
	}
	commit, err := a.env.runner.Run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("app: read the head of %s: %w", dir, err)
	}
	return strings.TrimSpace(branch), strings.TrimSpace(commit), nil
}

// writeCheckpoint replaces the committed record atomically.
func (a *App) writeCheckpoint(cp checkpoint.Checkpoint) error {
	dir := filepath.Dir(handoff.CheckpointPath(a.controlRoot()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("app: create %s: %w", dir, err)
	}
	root, err := securefs.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("app: open %s: %w", dir, err)
	}
	defer root.Close()
	store, err := checkpoint.NewStore(root, filepath.Base(handoff.CheckpointPath(a.controlRoot())))
	if err != nil {
		return fmt.Errorf("app: open the checkpoint store: %w", err)
	}
	if _, err := store.Write(cp); err != nil {
		return fmt.Errorf("app: write the checkpoint: %w", err)
	}
	return nil
}

// leaseTargets names every member's lease slot, in the layout the
// registration package defines: a Git member's slot lives beside its
// repository, a non-Git member's in this machine's state directory.
func (a *App) leaseTargets(ctx context.Context) ([]lease.Target, error) {
	stateRoot, err := stateRoot()
	if err != nil {
		return nil, err
	}
	var targets []lease.Target
	for _, m := range append([]workspacecfg.Member{a.controlMember()}, a.cfg.Members...) {
		dir := filepath.Join(a.root, filepath.FromSlash(normalizePath(m.Path)))
		if m.Kind == workspacecfg.KindGit {
			repo, isGit, err := gitx.Inspect(ctx, a.env.runner, dir)
			if err != nil {
				return nil, fmt.Errorf("app: inspect %s: %w", dir, err)
			}
			if !isGit {
				return nil, fmt.Errorf("app: %s is enrolled as a git member but is not a git repository", m.Path)
			}
			targets = append(targets, lease.Target{
				RepositoryID: m.ID, Path: registration.GitLeasePath(repo.CommonDir),
			})
			continue
		}
		path, err := registration.NonGitLeasePath(stateRoot, dir)
		if err != nil {
			return nil, fmt.Errorf("app: lease slot of %s: %w", m.Path, err)
		}
		targets = append(targets, lease.Target{RepositoryID: m.ID, Path: path})
	}
	return targets, nil
}

// sameCheckpoint reports whether two checkpoints describe the same thing,
// so an unchanged workflow does not rewrite an identical record. The
// encoding is canonical, which is what makes a byte comparison sound.
func sameCheckpoint(a, b checkpoint.Checkpoint) bool {
	x, xerr := checkpoint.Encode(a)
	y, yerr := checkpoint.Encode(b)
	return xerr == nil && yerr == nil && string(x) == string(y)
}
