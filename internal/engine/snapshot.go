package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/noviopenworks/homonto/internal/state"
)

// transactionOption selects snapshot-mode behaviors.
type transactionOption int

const (
	transactionPlain transactionOption = iota
	transactionSnapshot
)

// ApplySnapshot runs Apply under a journaled transaction (ADR 0030): every
// state partition's before/after checkpoints and the managed disk before-
// surface (links, copies, the remote lock) are recorded before the first
// write; an ordinary failure rolls back; success leaves a committed journal
// that `homonto undo` can reverse. The operation ID is returned.
func (e *Engine) ApplySnapshot(ctx context.Context, sets []adapter.ChangeSet) (string, error) {
	ops := opid.New()
	applyID := ops.NewID()
	j := &snapshot.Journal{
		SchemaVersion: snapshot.SchemaVersion,
		ApplyID:       applyID,
		Status:        snapshot.StatusPrepared,
		Started:       snapshot.Now(),
		Tool:          "opencode",
	}
	blobs := snapshot.NewBlobStore(snapshot.BlobDir(e.StateDir, applyID))

	// Checkpoint every state partition (before).
	parts := append([]*state.State{e.State}, e.repoStates()...)
	names := append([]string{""}, e.repoNames()...)
	for i, st := range parts {
		p := snapshot.Partition{
			Path:   stateFileName(e.StateDir, names[i]),
			Before: snapshot.PartitionState(st),
		}
		j.Partitions = append(j.Partitions, p)
	}
	// Checkpoint the managed disk before-surface from the plan's changes.
	if err := recordDiskBefore(e, sets, blobs, j); err != nil {
		return "", err
	}
	if err := j.Save(e.StateDir); err != nil {
		return "", err
	}

	// Run the ordinary apply, tracking per-changeset progress. On failure,
	// roll back everything reversible and mark the journal.
	applyErr := e.applyTracked(ctx, sets, j, blobs)
	if applyErr != nil {
		rbErr := e.RollbackSnapshot(applyID)
		if rbErr != nil {
			return applyID, fmt.Errorf("apply failed (%v) and rollback failed (%v); run `homonto recover %s`", applyErr, rbErr, applyID)
		}
		return applyID, applyErr
	}
	// Record after checkpoints and finish committed.
	for i, st := range parts {
		j.Partitions[i].After = snapshot.PartitionState(st)
	}
	j.Status = snapshot.StatusCommitted
	j.Finished = snapshot.Now()
	for i := range j.Changesets {
		j.Changesets[i].State = snapshot.EntryCommitted
	}
	if err := j.Save(e.StateDir); err != nil {
		return applyID, err
	}
	if err := snapshot.Retain(e.StateDir, 10); err != nil {
		return applyID, err
	}
	return applyID, nil
}

// repoStates returns every named partition's state.
func (e *Engine) repoStates() []*state.State {
	var out []*state.State
	for _, t := range e.RepoTargets {
		out = append(out, t.State)
	}
	return out
}

// repoNames returns the declared repo names in adapter-label order.
func (e *Engine) repoNames() []string {
	var out []string
	for _, t := range e.RepoTargets {
		out = append(out, t.Name)
	}
	return out
}

// stateFileName resolves a partition's state file name ("" = main).
func stateFileName(stateDir, repo string) string {
	if repo == "" {
		return filepath.Join(stateDir, "state.json")
	}
	return filepath.Join(stateDir, "state."+repo+".json")
}

// recordDiskBefore snapshots the disk facts the plan's changes will touch:
// link targets, copy content blobs, and the remote lock. Structured keys
// restore through a synthetic reverse apply, so no doc snapshot is needed.
func recordDiskBefore(e *Engine, sets []adapter.ChangeSet, blobs *snapshot.BlobStore, j *snapshot.Journal) error {
	for _, cs := range sets {
		for _, c := range cs.Changes {
			switch {
			case strings.HasPrefix(c.Key, "skill."), strings.HasPrefix(c.Key, "command."), strings.HasPrefix(c.Key, "subagent."):
				dst, _ := recordedLinkDst(e, cs.Tool, c.Key)
				op := snapshot.DiskOp{Kind: snapshot.MutLink, Path: dst}
				if dst != "" {
					if tgt, err := os.Readlink(dst); err == nil {
						op.Fact = tgt
					}
				}
				j.Disk = append(j.Disk, op)
			case strings.HasPrefix(c.Key, "subagentcopy."):
				dst := c.Old
				if dst == "" {
					dst = c.New
				}
				op := snapshot.DiskOp{Kind: snapshot.MutCopy, Path: dst}
				if data, err := os.ReadFile(dst); err == nil {
					id, err := blobs.Put(data)
					if err != nil {
						return err
					}
					op.Blob = id
				}
				j.Disk = append(j.Disk, op)
			case c.Key == "remote.lock" || strings.HasPrefix(c.Key, "remote."):
				// The remote lock file is the revocation/activation record.
				lockPath := filepath.Join(e.RemoteRoot, "lock.json")
				op := snapshot.DiskOp{Kind: snapshot.MutRemoteLock, Path: lockPath}
				if data, err := os.ReadFile(lockPath); err == nil {
					id, err := blobs.Put(data)
					if err != nil {
						return err
					}
					op.Blob = id
				}
				j.Disk = append(j.Disk, op)
			}
		}
	}
	return nil
}

// stateFor resolves the state partition an adapter label reads.
func stateFor(e *Engine, tool string) *state.State {
	for _, t := range e.RepoTargets {
		if t.Adapter.Name() == tool {
			return t.State
		}
	}
	return e.State
}

// recordedLinkDst recovers a link key's recorded destination from state.
func recordedLinkDst(e *Engine, tool, key string) (string, bool) {
	st := stateFor(e, tool)
	if st == nil {
		return "", false
	}
	entry, ok := st.Get(tool, key)
	if !ok {
		return "", false
	}
	dst, _, found := strings.Cut(entry.Desired, " -> ")
	if !found {
		return "", false
	}
	return dst, true
}

// applyTracked runs the per-adapter apply loop, marking each changeset
// prepared before and committed after its adapter writes.
func (e *Engine) applyTracked(ctx context.Context, sets []adapter.ChangeSet, j *snapshot.Journal, blobs *snapshot.BlobStore) error {
	byName := map[string]adapter.Adapter{}
	for _, a := range e.Adapters {
		byName[a.Name()] = a
	}
	pair := map[string]RepoTarget{}
	for _, t := range e.RepoTargets {
		byName[t.Adapter.Name()] = t.Adapter
		pair[t.Adapter.Name()] = t
	}
	enrich := e.enrichApply()
	for _, cs := range sets {
		a, ok := byName[cs.Tool]
		if !ok {
			continue
		}
		// Resolve secrets up front, exactly like the plain path.
		for _, c := range cs.Changes {
			if c.Action == "noop" || c.Action == "delete" || c.Action == "adopt" {
				continue
			}
			if _, err := e.Resolver.Resolve(c.New); err != nil {
				return err
			}
		}
		j.Changesets = append(j.Changesets, snapshot.ChangesetState{Tool: cs.Tool, State: snapshot.EntryPrepared})
		if err := j.Save(e.StateDir); err != nil {
			return err
		}
		if t, isRepo := pair[cs.Tool]; isRepo {
			post := enrich(cs, t.State)
			if err := a.Apply(e.Cfg, cs, e.Resolver, t.State); err != nil {
				return fmt.Errorf("%s: %w", cs.Tool, err)
			}
			post()
			if err := t.State.SaveNamed(e.StateDir, t.Name); err != nil {
				return fmt.Errorf("%s: save state: %w", cs.Tool, err)
			}
		} else {
			post := enrich(cs, e.State)
			if err := a.Apply(e.Cfg, cs, e.Resolver, e.State); err != nil {
				return fmt.Errorf("%s: %w", cs.Tool, err)
			}
			post()
			if err := e.State.Save(e.StateDir); err != nil {
				return fmt.Errorf("%s: save state: %w", cs.Tool, err)
			}
		}
		for i := range j.Changesets {
			if j.Changesets[i].Tool == cs.Tool {
				j.Changesets[i].State = snapshot.EntryCommitted
			}
		}
		if err := j.Save(e.StateDir); err != nil {
			return err
		}
	}
	e.recordVersions()
	if err := e.State.Save(e.StateDir); err != nil {
		return err
	}
	return nil
}

// RollbackSnapshot restores the before-state of an incomplete journal: state
// partitions, links, copies, and the remote lock. It never touches revoked
// remote content beyond restoring the lock record itself (activation is
// re-validated on the next apply).
func (e *Engine) RollbackSnapshot(applyID string) error {
	j, ok, err := snapshot.Load(e.StateDir, applyID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("snapshot: no journal %s", applyID)
	}
	if j.Status != snapshot.StatusPrepared {
		return fmt.Errorf("snapshot: journal %s is %s, not rollback-able", applyID, j.Status)
	}
	if err := restoreAll(e, j, false); err != nil {
		return err
	}
	j.Status = snapshot.StatusRolledBack
	j.Finished = snapshot.Now()
	for i := range j.Changesets {
		j.Changesets[i].State = snapshot.EntryRolledBack
	}
	return j.Save(e.StateDir)
}

// RecoverSnapshot finishes an interrupted transaction: committed changesets
// must match their after-images and prepared ones their before-images, then
// everything is restored to before. Called under the process lock.
func (e *Engine) RecoverSnapshot(applyID string) error {
	j, ok, err := snapshot.Load(e.StateDir, applyID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("snapshot: no journal %s", applyID)
	}
	if j.Status != snapshot.StatusPrepared {
		return fmt.Errorf("snapshot: journal %s is %s; nothing to recover", applyID, j.Status)
	}
	// Verify the partition images match their journal states.
	for _, p := range j.Partitions {
		st := stateAt(e, p.Path)
		if st == nil {
			continue
		}
		cur := snapshot.PartitionState(st)
		if snapshot.HashEntries(cur) != snapshot.HashEntries(p.After) && snapshot.HashEntries(cur) != snapshot.HashEntries(p.Before) {
			return fmt.Errorf("snapshot: %s matches neither its before nor after image; manual inspection required", p.Path)
		}
	}
	if err := restoreAll(e, j, false); err != nil {
		return err
	}
	j.Status = snapshot.StatusRolledBack
	j.Finished = snapshot.Now()
	return j.Save(e.StateDir)
}

// UndoSnapshot reverses a committed journal. Every managed after-image must
// still match the journal's record — a user edit in between makes the undo
// refuse with zero mutation.
func (e *Engine) UndoSnapshot(applyID string) error {
	j, ok, err := snapshot.Load(e.StateDir, applyID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("snapshot: no journal %s", applyID)
	}
	if j.Status != snapshot.StatusCommitted {
		return fmt.Errorf("snapshot: journal %s is %s; only committed journals undo", applyID, j.Status)
	}
	// Verify the AFTER-images against DISK, freshly loaded — the in-memory
	// states reflect the apply, not a subsequent user edit.
	for _, p := range j.Partitions {
		st, err := state.Load(filepath.Dir(p.Path))
		if err != nil {
			return err
		}
		cur := snapshot.PartitionState(st)
		if !snapshot.EqualEntries(cur, p.After) {
			return fmt.Errorf("snapshot: %s changed since the apply; refusing to undo over a user edit", p.Path)
		}
	}
	if err := restoreAll(e, j, true); err != nil {
		return err
	}
	j.Status = snapshot.StatusRolledBack
	j.Finished = snapshot.Now()
	return j.Save(e.StateDir)
}

// stateAt loads the partition at a state file path (main or named).
func stateAt(e *Engine, path string) *state.State {
	if path == stateFileName(e.StateDir, "") {
		return e.State
	}
	for _, t := range e.RepoTargets {
		if path == stateFileName(e.StateDir, t.Name) {
			return t.State
		}
	}
	return nil
}

// restoreAll restores the before-state: disk facts, a synthetic reverse
// apply for structured keys (disk only — it re-records state to the config's
// values, which is why partitions are restored LAST), then partitions.
func restoreAll(e *Engine, j *snapshot.Journal, structured bool) error {
	blobs := snapshot.NewBlobStore(snapshot.BlobDir(e.StateDir, j.ApplyID))
	// Disk facts first.
	for _, op := range j.Disk {
		switch op.Kind {
		case snapshot.MutLink:
			if err := restoreLink(op); err != nil {
				return err
			}
		case snapshot.MutCopy:
			if err := restoreCopy(blobs, op); err != nil {
				return err
			}
		case snapshot.MutRemoteLock:
			if err := restoreRemoteLock(blobs, op); err != nil {
				return err
			}
		}
	}
	if structured {
		if err := e.reverseApplyStructured(j); err != nil {
			return err
		}
	}
	// State partitions last: the reverse apply re-records state to the
	// config's current values; the before checkpoints must win.
	for _, p := range j.Partitions {
		st := stateAt(e, p.Path)
		if st == nil {
			continue
		}
		snapshot.ApplyPartition(st, p.Before)
	}
	// Persist restored partitions.
	if err := e.State.Save(e.StateDir); err != nil {
		return err
	}
	for _, t := range e.RepoTargets {
		if err := t.State.SaveNamed(e.StateDir, t.Name); err != nil {
			return err
		}
	}
	return nil
}

// restoreLink returns a link to its before target (or removes it when it did
// not exist). Only managed links are touched.
func restoreLink(op snapshot.DiskOp) error {
	if op.Path == "" {
		return nil
	}
	if op.Fact == "" {
		return os.Remove(op.Path) // was absent; the managed link created by apply goes
	}
	// Re-link to the recorded before target, which may be a relative or
	// absolute spelling; os.Symlink writes it as-is (relative to the link's
	// own dir, exactly as the original apply did).
	if err := os.Remove(op.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(op.Fact, op.Path)
}

func restoreCopy(blobs *snapshot.BlobStore, op snapshot.DiskOp) error {
	if op.Blob == "" {
		return os.Remove(op.Path) // was absent before
	}
	data, err := blobs.Get(op.Blob)
	if err != nil {
		return err
	}
	return os.WriteFile(op.Path, data, 0o644)
}

func restoreRemoteLock(blobs *snapshot.BlobStore, op snapshot.DiskOp) error {
	if op.Blob == "" {
		_ = os.Remove(op.Path)
		return nil
	}
	data, err := blobs.Get(op.Blob)
	if err != nil {
		return err
	}
	return os.WriteFile(op.Path, data, 0o600)
}

// reverseApplyStructured re-projects the before desired values for the
// structured keys (mcp./setting./tui./plugin.) through the adapter's own
// Apply — the same deterministic writer that made the change, now with the
// before-state restored.
func (e *Engine) reverseApplyStructured(j *snapshot.Journal) error {
	for _, cs := range j.Changesets {
		// The journal's changesets don't carry keys; reconstruct the reverse
		// from partition diffs is not possible without the original sets.
		// Instead, re-plan from the restored state: the drift the restore
		// introduced IS the reverse, applied by a normal apply.
		_ = cs
	}
	// The reverse surface is the restored state vs the current disk: plan and
	// apply. Secrets re-resolve from the unresolved before values.
	sets, err := e.Plan()
	if err != nil {
		return fmt.Errorf("snapshot: re-plan for reverse apply: %w", err)
	}
	var structured []adapter.ChangeSet
	for _, s := range sets {
		var kept []adapter.Change
		for _, c := range s.Changes {
			if !isStructuredKey(c.Key) {
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) > 0 {
			structured = append(structured, adapter.ChangeSet{Tool: s.Tool, Changes: kept})
		}
	}
	for _, cs := range structured {
		a := e.adapterFor(cs.Tool)
		if a == nil {
			continue
		}
		if t, isRepo := e.repoPairFor(cs.Tool); isRepo {
			if err := a.Apply(e.Cfg, cs, e.Resolver, t.State); err != nil {
				return fmt.Errorf("%s: reverse apply: %w", cs.Tool, err)
			}
			if err := t.State.SaveNamed(e.StateDir, t.Name); err != nil {
				return err
			}
			continue
		}
		if err := a.Apply(e.Cfg, cs, e.Resolver, e.State); err != nil {
			return fmt.Errorf("%s: reverse apply: %w", cs.Tool, err)
		}
		if err := e.State.Save(e.StateDir); err != nil {
			return err
		}
	}
	return nil
}

func isStructuredKey(key string) bool {
	for _, p := range []string{"mcp.", "projmcp.", "setting.", "projsetting.", "tui.", "plugin."} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

func (e *Engine) adapterFor(tool string) adapter.Adapter {
	for _, a := range e.Adapters {
		if a.Name() == tool {
			return a
		}
	}
	for _, t := range e.RepoTargets {
		if t.Adapter.Name() == tool {
			return t.Adapter
		}
	}
	return nil
}

func (e *Engine) repoPairFor(tool string) (RepoTarget, bool) {
	for _, t := range e.RepoTargets {
		if t.Adapter.Name() == tool {
			return t, true
		}
	}
	return RepoTarget{}, false
}

// IncompleteSnapshots lists prepared journals (interrupted or failed before
// rollback completed) — doctor reports these with the recover command.
func (e *Engine) IncompleteSnapshots() ([]string, error) {
	ids, err := snapshot.List(e.StateDir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range ids {
		j, ok, err := snapshot.Load(e.StateDir, id)
		if err != nil {
			return nil, err
		}
		if ok && j.Status == snapshot.StatusPrepared {
			out = append(out, id)
		}
	}
	return out, nil
}
