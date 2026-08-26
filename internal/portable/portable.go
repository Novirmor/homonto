// Package portable maintains the checkpoint and leases that let active work
// move safely between machines.
package portable

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
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Manager maintains the portable record for one opened workspace.
type Manager struct {
	root          string
	cfg           workspacecfg.Config
	runner        gitx.Runner
	snapshotStore string
	leases        *lease.Manager
}

// NewManager binds the portable record to an opened workspace.
func NewManager(root string, cfg workspacecfg.Config, runner gitx.Runner, snapshotStore string, leases *lease.Manager) *Manager {
	if runner == nil {
		runner = gitx.ExecRunner{}
	}
	return &Manager{root: root, cfg: cfg, runner: runner, snapshotStore: snapshotStore, leases: leases}
}

// Activate anchors a newly started work to this machine: it leases every
// member and writes the work's first checkpoint.
//
// Leases first, checkpoint second. The lease is what makes the claim
// exclusive, so a second machine that raced this one loses at the lease
// and never writes a checkpoint over the winner's.
func (m *Manager) Activate(ctx context.Context, workID identity.WorkID, kind, name, docPath, phase string) error {
	targets, err := m.leaseTargets(ctx)
	if err != nil {
		return err
	}
	provenance, err := lease.CurrentProcess()
	if err != nil {
		return fmt.Errorf("app: describe this process: %w", err)
	}
	if _, err := m.leases.AcquireAll(ctx, lease.AcquireRequest{
		WorkspaceID: m.cfg.Workspace.ID,
		WorkID:      workID,
		Generation:  1,
		Provenance:  provenance,
		ControlRoot: m.controlRoot(),
		WorkKind:    kind,
		Title:       name,
		Targets:     targets,
	}); err != nil {
		return fmt.Errorf("app: lease the workspace for %s: %w", workID, err)
	}
	cp, err := m.buildCheckpoint(ctx, &checkpoint.Work{
		ID: workID, Name: name, Workflow: m.cfg.Workspace.Workflow,
		Path: docPath, Phase: phase, Generation: 1,
	}, checkpoint.Handoff{State: checkpoint.HandoffLocal, Generation: 1})
	if err != nil {
		return err
	}
	return m.writeCheckpoint(cp)
}

// RefreshCheckpoint brings the committed record up to date with where the
// work actually is.
//
// A workspace with no initial checkpoint is left alone: inventing one
// mid-flight would claim anchors that were never the work's starting point.
func (m *Manager) RefreshCheckpoint(ctx context.Context, workID identity.WorkID, phase string) error {
	current, err := m.Checkpoint()
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
	if current.Handoff.State != checkpoint.HandoffLocal {
		return nil
	}
	work := *current.Work
	work.Phase = phase
	next, err := m.buildCheckpoint(ctx, &work, current.Handoff)
	if err != nil {
		return err
	}
	if sameCheckpoint(next, current) {
		return nil
	}
	return m.writeCheckpoint(next)
}

// Deactivate releases the work's hold on this machine when it archives or
// is abandoned: the leases go, and the checkpoint stops naming a work.
func (m *Manager) Deactivate(ctx context.Context, workID identity.WorkID) error {
	sentinel, err := lease.ReadSentinel(lease.SentinelPath(m.controlRoot(), workID))
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
			if err := m.leases.ReleaseAll(ctx, held); err != nil {
				return fmt.Errorf("app: release the leases of %s: %w", workID, err)
			}
		}
		if err := os.Remove(lease.SentinelPath(m.controlRoot(), workID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("app: remove the lease sentinel of %s: %w", workID, err)
		}
	}
	current, err := m.Checkpoint()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if current.Work == nil || current.Work.ID != workID {
		return nil
	}
	next, err := m.buildCheckpoint(ctx, nil, checkpoint.Handoff{
		State: checkpoint.HandoffLocal, Generation: current.Handoff.Generation,
	})
	if err != nil {
		return err
	}
	return m.writeCheckpoint(next)
}

// RequireCleanMembers refuses to start work over uncommitted member changes.
func (m *Manager) RequireCleanMembers(ctx context.Context) error {
	for _, member := range m.cfg.Members {
		if member.Kind != workspacecfg.KindGit || member.ID == m.cfg.Control.ID {
			continue
		}
		dir := filepath.Join(m.root, filepath.FromSlash(normalizePath(member.Path)))
		files, err := gitx.DirtyPaths(ctx, m.runner, dir)
		if err != nil {
			return fmt.Errorf("app: check %s: %w", member.Path, err)
		}
		if len(files) > 0 {
			return fmt.Errorf(
				"app: %s has uncommitted changes (%s); commit, stash, or discard them first: %w",
				member.Path, strings.Join(files, ", "), gitx.ErrDirtyWorktree)
		}
	}
	return nil
}

// AnchoredWork returns the work this machine currently holds leases for.
func (m *Manager) AnchoredWork() (identity.WorkID, error) {
	dir := filepath.Join(m.controlRoot(), ".homonto", "leases")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("app: read %s: %w", dir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".active") {
			return identity.WorkID(strings.TrimSuffix(name, ".active")), nil
		}
	}
	return "", nil
}

// Checkpoint reads this workspace's committed checkpoint.
func (m *Manager) Checkpoint() (checkpoint.Checkpoint, error) {
	cp, _, err := checkpoint.Load(handoff.CheckpointPath(m.controlRoot()))
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("app: read the checkpoint: %w", err)
	}
	return cp, nil
}

// StateRoot is this machine's platform state base for non-Git member leases.
func StateRoot() (string, error) {
	root, err := registration.StateRoot()
	if err != nil {
		return "", fmt.Errorf("app: resolve the platform state root: %w", err)
	}
	return root, nil
}

func (m *Manager) buildCheckpoint(ctx context.Context, work *checkpoint.Work, hand checkpoint.Handoff) (checkpoint.Checkpoint, error) {
	fp, err := workspacecfg.Fingerprint(m.cfg)
	if err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("app: fingerprint the manifest: %w", err)
	}
	cp := checkpoint.Checkpoint{
		SchemaVersion: checkpoint.CurrentSchemaVersion, WorkspaceID: m.cfg.Workspace.ID,
		ConfigFingerprint: fp, Work: work, Handoff: hand,
	}
	for _, member := range append([]workspacecfg.Member{m.controlMember()}, m.cfg.Members...) {
		anchored, err := m.checkpointMember(ctx, member, work)
		if err != nil {
			return checkpoint.Checkpoint{}, err
		}
		cp.Members = append(cp.Members, anchored)
	}
	sort.Slice(cp.Members, func(i, j int) bool { return cp.Members[i].ID < cp.Members[j].ID })
	if err := checkpoint.Validate(cp, m.cfg); err != nil {
		return checkpoint.Checkpoint{}, fmt.Errorf("app: build the checkpoint: %w", err)
	}
	return cp, nil
}

func (m *Manager) controlMember() workspacecfg.Member {
	return workspacecfg.Member{ID: m.cfg.Control.ID, Path: m.cfg.Control.Path, Kind: workspacecfg.KindGit}
}

func (m *Manager) checkpointMember(ctx context.Context, member workspacecfg.Member, work *checkpoint.Work) (checkpoint.Member, error) {
	out := checkpoint.Member{ID: member.ID, Kind: member.Kind}
	dir := filepath.Join(m.root, filepath.FromSlash(normalizePath(member.Path)))
	if member.Kind != workspacecfg.KindGit {
		manifest, err := snapshot.Capture(ctx, dir, m.snapshotStore, snapshot.CaptureOptions{})
		if err != nil {
			return checkpoint.Member{}, fmt.Errorf("app: digest %s: %w", dir, err)
		}
		out.SourceFingerprint = snapshot.DigestManifest(manifest)
		return out, nil
	}
	branch, commit, err := m.head(ctx, dir)
	if err != nil {
		return checkpoint.Member{}, err
	}
	out.BaseBranch, out.BaseCommit = branch, commit
	out.IntegrationBranch, out.IntegrationCommit = branch, commit
	digest, err := gitx.TreeFingerprint(ctx, m.runner, dir, commit)
	if err != nil {
		return checkpoint.Member{}, err
	}
	out.SourceFingerprint = digest
	if work == nil {
		return out, nil
	}
	integration := gitx.IntegrationPath(m.root, work.ID, member.ID)
	if _, err := os.Stat(integration); err != nil {
		return out, nil
	}
	iBranch, iCommit, err := m.head(ctx, integration)
	if err != nil {
		return checkpoint.Member{}, err
	}
	out.IntegrationBranch, out.IntegrationCommit = iBranch, iCommit
	return out, nil
}

func (m *Manager) head(ctx context.Context, dir string) (string, string, error) {
	branch, err := m.runner.Run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("app: read the branch of %s: %w", dir, err)
	}
	commit, err := m.runner.Run(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", "", fmt.Errorf("app: read the head of %s: %w", dir, err)
	}
	return strings.TrimSpace(branch), strings.TrimSpace(commit), nil
}

func (m *Manager) writeCheckpoint(cp checkpoint.Checkpoint) error {
	dir := filepath.Dir(handoff.CheckpointPath(m.controlRoot()))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("app: create %s: %w", dir, err)
	}
	root, err := securefs.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("app: open %s: %w", dir, err)
	}
	defer root.Close()
	store, err := checkpoint.NewStore(root, filepath.Base(handoff.CheckpointPath(m.controlRoot())))
	if err != nil {
		return fmt.Errorf("app: open the checkpoint store: %w", err)
	}
	if _, err := store.Write(cp); err != nil {
		return fmt.Errorf("app: write the checkpoint: %w", err)
	}
	return nil
}

func (m *Manager) leaseTargets(ctx context.Context) ([]lease.Target, error) {
	stateRoot, err := StateRoot()
	if err != nil {
		return nil, err
	}
	var targets []lease.Target
	for _, member := range append([]workspacecfg.Member{m.controlMember()}, m.cfg.Members...) {
		dir := filepath.Join(m.root, filepath.FromSlash(normalizePath(member.Path)))
		if member.Kind == workspacecfg.KindGit {
			repo, isGit, err := gitx.Inspect(ctx, m.runner, dir)
			if err != nil {
				return nil, fmt.Errorf("app: inspect %s: %w", member.Path, err)
			}
			if !isGit {
				return nil, fmt.Errorf("app: %s is enrolled as a git member but is not a git repository", member.Path)
			}
			targets = append(targets, lease.Target{RepositoryID: member.ID, Path: registration.GitLeasePath(repo.CommonDir)})
			continue
		}
		path, err := registration.NonGitLeasePath(stateRoot, dir)
		if err != nil {
			return nil, fmt.Errorf("app: lease slot of %s: %w", member.Path, err)
		}
		targets = append(targets, lease.Target{RepositoryID: member.ID, Path: path})
	}
	return targets, nil
}

func (m *Manager) controlRoot() string {
	return filepath.Join(m.root, filepath.FromSlash(normalizePath(m.cfg.Control.Path)))
}

func normalizePath(path string) string {
	if path == "" {
		return "."
	}
	return filepath.ToSlash(filepath.Clean(path))
}

func sameCheckpoint(a, b checkpoint.Checkpoint) bool {
	x, xerr := checkpoint.Encode(a)
	y, yerr := checkpoint.Encode(b)
	return xerr == nil && yerr == nil && string(x) == string(y)
}
