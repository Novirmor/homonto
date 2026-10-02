package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// journalToolLabel names the tools a snapshot journal covers: the distinct
// base tool ids of its changesets, comma-joined in sorted order. A
// single-tool apply keeps its historical label ("opencode"); a multi-tool
// apply ("claude,opencode") no longer misattributes itself. Each changeset row
// carries its own exact Tool regardless.
func journalToolLabel(sets []adapter.ChangeSet) string {
	seen := map[string]bool{}
	for _, cs := range sets {
		if len(cs.Changes) == 0 {
			continue // an idle adapter's empty changeset is no work of record
		}
		seen[BaseToolID(cs.Tool)] = true
	}
	if len(seen) == 0 {
		return "none" // a no-op apply: no tool did work of record
	}
	tools := make([]string, 0, len(seen))
	for t := range seen {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	return strings.Join(tools, ",")
}

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
		Tool:          journalToolLabel(sets),
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
	// Record every changeset's per-key mutations BEFORE any write: a crash
	// mid-apply must leave a journal carrying what to reverse, not just what
	// was checkpointed. All changesets start prepared; applyTracked flips the
	// ones whose adapter completes. Only structured keys are journaled here —
	// link/copy mutations restore through the disk facts above.
	for _, cs := range sets {
		entry := snapshot.ChangesetState{Tool: cs.Tool, State: snapshot.EntryPrepared}
		for _, c := range cs.Changes {
			if !journaledStructuredKey(c.Key) {
				continue
			}
			switch c.Action {
			case adapter.ActionCreate, adapter.ActionUpdate, adapter.ActionDelete:
				entry.Changes = append(entry.Changes, snapshot.RecordedChange{
					Action: string(c.Action), Key: c.Key, New: c.New,
				})
			}
		}
		j.Changesets = append(j.Changesets, entry)
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
				if dst == "" && (c.Action == adapter.ActionCreate || c.Action == adapter.ActionUpdate) && c.New != "" {
					// A link this apply CREATES has no state record to read the
					// destination from — but the change itself carries it
					// ("dst -> src"). Recording it with an absent fact makes
					// restore remove the created link; without this, undo and
					// rollback left first-time links behind as unmanaged
					// artifacts. Updates carrying the same form are scope
					// relocations, whose old link the same removal restores.
					if before, _, found := strings.Cut(c.New, " -> "); found {
						dst = before
					}
				}
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

// stateFor resolves an adapter label to its state partition and the state key
// recorded inside that partition. Repo adapters add @<repo> to their adapter
// label, while their partition keeps the base adapter key.
func stateFor(e *Engine, tool string) (*state.State, string) {
	for _, t := range e.RepoTargets {
		for _, a := range t.Adapters {
			if a.Name() == tool {
				key, _ := strings.CutSuffix(tool, "@"+t.Name)
				return t.State, key
			}
		}
	}
	return e.State, tool
}

// recordedLinkDst recovers a link key's recorded destination from state.
func recordedLinkDst(e *Engine, tool, key string) (string, bool) {
	st, stateTool := stateFor(e, tool)
	if st == nil {
		return "", false
	}
	entry, ok := st.Get(stateTool, key)
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
		for _, a := range t.Adapters {
			byName[a.Name()] = a
			pair[a.Name()] = t
		}
	}
	enrich := e.enrichApply()
	// Resolve every changeset's secrets BEFORE any adapter writes — the same
	// two-phase contract as the plain apply. Resolving per-changeset instead
	// let an early tool commit (e.g. opencode) before a later tool's secret
	// failed (e.g. claude), leaving disk half-applied after rollback — a gap
	// the multi-tool fan-out made reachable.
	for _, cs := range sets {
		for _, c := range cs.Changes {
			// Deletes carry no New value; adopt is non-secret by
			// construction — neither has anything to resolve.
			if c.Action == "noop" || c.Action == "delete" || c.Action == "adopt" {
				continue
			}
			if _, err := e.Resolver.Resolve(c.New); err != nil {
				return err
			}
		}
	}
	for _, cs := range sets {
		a, ok := byName[cs.Tool]
		if !ok {
			continue
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
	// Refuse before mutating anything: a reversal that cannot complete must
	// not land on top of restored disk facts.
	if err := e.validateReversible(j); err != nil {
		return err
	}
	if err := restoreAll(e, j); err != nil {
		return err
	}
	j.Status = snapshot.StatusRolledBack
	j.Finished = snapshot.Now()
	markChangesetsRolledBack(j)
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
	// Refuse before mutating anything: a reversal that cannot complete must
	// not land on top of restored disk facts.
	if err := e.validateReversible(j); err != nil {
		return err
	}
	// Verify the partition images are consistent with a crash: the after
	// image is only written at commit, so a prepared journal can only be
	// compared per key — every current entry must match its before-image or
	// its after-image entry (a mid-apply crash leaves exactly that mix), and
	// no unknown key may have appeared. Anything else is a hand edit.
	for _, p := range j.Partitions {
		st := stateAt(e, p.Path)
		if st == nil {
			continue
		}
		cur := snapshot.PartitionState(st)
		if !partitionCrashConsistent(cur, p.Before, p.After) {
			return fmt.Errorf("snapshot: %s matches neither a before, after, nor interrupted-apply state; manual inspection required", p.Path)
		}
	}
	if err := restoreAll(e, j); err != nil {
		return err
	}
	j.Status = snapshot.StatusRolledBack
	j.Finished = snapshot.Now()
	markChangesetsRolledBack(j)
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
	// Refuse before mutating anything: a reversal that cannot complete must
	// not land on top of restored disk facts (zero-mutation-on-refusal).
	if err := e.validateReversible(j); err != nil {
		return err
	}
	// Verify the AFTER-images against DISK, freshly loaded — the in-memory
	// states reflect the apply, not a subsequent user edit.
	for _, p := range j.Partitions {
		st, err := loadSnapshotPartition(e, p.Path)
		if err != nil {
			return err
		}
		cur := snapshot.PartitionState(st)
		if !snapshot.EqualEntries(cur, p.After) {
			return fmt.Errorf("snapshot: %s changed since the apply; refusing to undo over a user edit", p.Path)
		}
	}
	if err := restoreAll(e, j); err != nil {
		return err
	}
	j.Status = snapshot.StatusRolledBack
	j.Finished = snapshot.Now()
	markChangesetsRolledBack(j)
	return j.Save(e.StateDir)
}

// markChangesetsRolledBack aligns every changeset row with its journal: a
// rolled-back journal's rows must not keep reading committed/prepared.
func markChangesetsRolledBack(j *snapshot.Journal) {
	for i := range j.Changesets {
		j.Changesets[i].State = snapshot.EntryRolledBack
	}
}

// loadSnapshotPartition reads the exact partition recorded by a journal. A
// named repository partition is not state.json; loading it as the main state
// would make undo compare the wrong repository's after-image.
func loadSnapshotPartition(e *Engine, path string) (*state.State, error) {
	if path == stateFileName(e.StateDir, "") {
		return state.Load(e.StateDir)
	}
	for _, t := range e.RepoTargets {
		if path == stateFileName(e.StateDir, t.Name) {
			return state.LoadNamed(e.StateDir, t.Name)
		}
	}
	return nil, fmt.Errorf("snapshot: journal references unknown state partition %s", path)
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

// restoreAll restores the before-state: disk facts, the journaled reverse of
// every committed changeset's structured writes, then the state partitions'
// before checkpoints (the reverse apply re-records state as it writes; the
// checkpoints must win).
func restoreAll(e *Engine, j *snapshot.Journal) error {
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
	if err := e.reverseJournaledStructured(j); err != nil {
		return err
	}
	// State partitions last: the reverse apply re-records state for the
	// values it restored; the before checkpoints must win.
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

// reverseJournaledStructured inverts every RECORDED changeset's structured
// writes from the journal itself — never by re-planning against the current
// config, which may have changed since the apply (the old replan-based
// reverse could "restore" the after-values and call it undo). The reverse
// runs in reverse changeset order, last write first; values come from the
// partition before-images (unresolved desired, so secrets re-resolve at
// reverse time and a failing resolver refuses the reversal rather than
// guessing — ADR 0030).
//
// PREPARED changesets are reversed too, not only committed ones: a changeset
// is marked committed only after its doc write, state save, and journal save,
// so a crash inside that window leaves the write on disk under a prepared
// marker. Reversing a change that never reached disk is convergent — the
// reverse delete of an absent key is state-only, and the reverse update of a
// still-before value rewrites the same value.
func (e *Engine) reverseJournaledStructured(j *snapshot.Journal) error {
	if err := e.validateReversible(j); err != nil {
		return err
	}
	for i := len(j.Changesets) - 1; i >= 0; i-- {
		cs := j.Changesets[i]
		if len(cs.Changes) == 0 {
			continue
		}
		// A prepared changeset's reversal is best-effort: it was marked
		// committed only after its write, so prepared usually means "never
		// applied" — and a tool file too broken to read proves nothing of
		// ours reached it. Skipping its reversal is then safe. A COMMITTED
		// changeset wrote; failing to reverse it is fatal.
		if cs.State != snapshot.EntryCommitted {
			if err := e.reverseOneChangeset(j, cs); err != nil {
				continue
			}
			continue
		}
		if err := e.reverseOneChangeset(j, cs); err != nil {
			return err
		}
	}
	return nil
}

// reverseOneChangeset inverts one recorded changeset's structured changes
// against its partition's before-image.
func (e *Engine) reverseOneChangeset(j *snapshot.Journal, cs snapshot.ChangesetState) error {
	rev := adapter.ChangeSet{Tool: cs.Tool}
	for k := len(cs.Changes) - 1; k >= 0; k-- {
		rec := cs.Changes[k]
		base := BaseToolID(cs.Tool)
		var before state.Entry
		var hadBefore bool
		for _, p := range j.Partitions {
			if p.Path == partitionPathFor(e, cs.Tool) {
				before, hadBefore = p.Before[base][rec.Key]
				break
			}
		}
		switch rec.Action {
		case string(adapter.ActionCreate):
			// The apply created the key; remove it again.
			rev.Changes = append(rev.Changes, adapter.Change{Action: adapter.ActionDelete, Key: rec.Key, Old: adapter.SecretRedaction, Cause: adapter.CauseRemove})
		case string(adapter.ActionUpdate), string(adapter.ActionDelete):
			// Restore the pre-apply desired value (a delete's reverse is a
			// create with the recorded before value).
			if !hadBefore || before.Desired == "" {
				return fmt.Errorf("snapshot: journal %s lacks the before value for %s %s; cannot reverse", j.ApplyID, cs.Tool, rec.Key)
			}
			action := adapter.ActionUpdate
			if rec.Action == string(adapter.ActionDelete) {
				action = adapter.ActionCreate
			}
			rev.Changes = append(rev.Changes, adapter.Change{Action: action, Key: rec.Key, New: before.Desired, Cause: adapter.CauseDriftFix})
		default:
			return fmt.Errorf("snapshot: journal %s records unsupported action %q for %s", j.ApplyID, rec.Action, rec.Key)
		}
	}
	if len(rev.Changes) == 0 {
		return nil
	}
	a := e.adapterFor(rev.Tool)
	if a == nil {
		return fmt.Errorf("snapshot: no adapter for %s while reversing %s", rev.Tool, j.ApplyID)
	}
	if t, isRepo := e.repoPairFor(rev.Tool); isRepo {
		if err := a.Apply(e.Cfg, rev, e.Resolver, t.State); err != nil {
			return fmt.Errorf("%s: reverse apply: %w", rev.Tool, err)
		}
		return t.State.SaveNamed(e.StateDir, t.Name)
	}
	if err := a.Apply(e.Cfg, rev, e.Resolver, e.State); err != nil {
		return fmt.Errorf("%s: reverse apply: %w", rev.Tool, err)
	}
	return e.State.Save(e.StateDir)
}

// partitionCrashConsistent reports whether a partition's current entries
// could result from the journaled apply crashing at some point: every entry
// matches its before-image or its after-image value, and no key outside both
// images appeared. The after image may be nil for a prepared journal.
func partitionCrashConsistent(cur, before, after map[string]map[string]state.Entry) bool {
	for tool, entries := range cur {
		for k, e := range entries {
			b, inBefore := before[tool][k]
			a, inAfter := after[tool][k]
			matchBefore := inBefore && b.Desired == e.Desired && b.Applied == e.Applied
			matchAfter := inAfter && a.Desired == e.Desired && a.Applied == e.Applied
			if !matchBefore && !matchAfter {
				return false
			}
		}
	}
	return true
}

// validateReversible checks everything a structured reversal needs BEFORE any
// disk or state mutation, so a refusal never lands on top of a half-restored
// surface: the journal must carry per-key records (schema 2), and every
// recorded update/delete must have a before value in its partition's
// before-image to restore.
func (e *Engine) validateReversible(j *snapshot.Journal) error {
	if j.SchemaVersion < 2 {
		// A v1 journal carries no per-key records; without them a structured
		// reversal would be guesswork. Refuse rather than half-restore.
		for _, cs := range j.Changesets {
			if len(cs.Changes) > 0 || cs.State == snapshot.EntryCommitted {
				return fmt.Errorf("snapshot: journal %s predates reversible per-key records (schema %d); managed values cannot be reversed automatically", j.ApplyID, j.SchemaVersion)
			}
		}
		return nil
	}
	before := func(partitionPath, base, key string) (state.Entry, bool) {
		for _, p := range j.Partitions {
			if p.Path == partitionPath {
				e, ok := p.Before[base][key]
				return e, ok
			}
		}
		return state.Entry{}, false
	}
	for _, cs := range j.Changesets {
		for _, rec := range cs.Changes {
			if rec.Action != string(adapter.ActionUpdate) && rec.Action != string(adapter.ActionDelete) {
				continue
			}
			e, ok := before(partitionPathFor(e, cs.Tool), BaseToolID(cs.Tool), rec.Key)
			if !ok || e.Desired == "" {
				return fmt.Errorf("snapshot: journal %s lacks the before value for %s %s; cannot reverse", j.ApplyID, cs.Tool, rec.Key)
			}
		}
	}
	return nil
}

// partitionPathFor resolves the state partition file an adapter label records
// into: the main state for a plain tool id, the named partition for
// "<tool>@<repo>".
func partitionPathFor(e *Engine, toolLabel string) string {
	if name := partitionRepoName(toolLabel); name != "" {
		return stateFileName(e.StateDir, name)
	}
	return stateFileName(e.StateDir, "")
}

// partitionRepoName extracts the repo alias from an adapter label ("" = main).
func partitionRepoName(toolLabel string) string {
	if _, name, ok := strings.Cut(toolLabel, "@"); ok {
		return name
	}
	return ""
}

func isStructuredKey(key string) bool {
	for _, p := range []string{"mcp.", "projmcp.", "setting.", "projsetting.", "tui.", "plugin."} {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// journaledStructuredKey narrows isStructuredKey to keys whose apply is
// DISK-mutating, and therefore reversible through the adapter's writer. The
// bare legacy "tui." prefix (pre-V2 TUI state) is deliberately excluded: its
// retirement is state-only by design, no V1 file is ever written, and its
// state entry returns via the partition before-checkpoint. V2's "tui.cli.*"
// keys live in cli.json and do reverse on disk.
func journaledStructuredKey(key string) bool {
	if !isStructuredKey(key) {
		return false
	}
	return !strings.HasPrefix(key, "tui.") || strings.HasPrefix(key, "tui.cli.")
}

func (e *Engine) adapterFor(tool string) adapter.Adapter {
	for _, a := range e.Adapters {
		if a.Name() == tool {
			return a
		}
	}
	for _, t := range e.RepoTargets {
		for _, a := range t.Adapters {
			if a.Name() == tool {
				return a
			}
		}
	}
	return nil
}

func (e *Engine) repoPairFor(tool string) (RepoTarget, bool) {
	for _, t := range e.RepoTargets {
		for _, a := range t.Adapters {
			if a.Name() == tool {
				return t, true
			}
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
