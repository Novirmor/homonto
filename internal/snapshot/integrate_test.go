package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
)

// senv wires a member source tree, a store root, a runtime database, and
// a Service together for one test.
type senv struct {
	root      string
	source    string
	storePath string
	dbPath    string
	db        *store.DB
	ops       *operation.Manager
	svc       *Service
	repoID    identity.RepositoryID
	workID    identity.WorkID
}

func newSenv(t *testing.T, files map[string]string) *senv {
	t.Helper()
	e := &senv{
		root:   t.TempDir(),
		source: t.TempDir(),
		dbPath: filepath.Join(t.TempDir(), "runtime.sqlite"),
		repoID: mustUUID(t, identity.NewRepositoryID),
		workID: mustUUID(t, identity.NewWorkID),
	}
	writeTree(t, e.source, files)
	e.storePath = filepath.Join(e.root, ".homonto", "integrations",
		string(e.workID), string(e.repoID))
	e.open(t)
	return e
}

func (e *senv) open(t *testing.T) {
	t.Helper()
	db, err := store.Open(context.Background(), e.dbPath, store.OpenOptions{})
	if err != nil {
		t.Fatalf("snapshot: open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e.db = db
	e.ops = operation.NewManager(db)
	e.svc, err = NewService(db, e.ops, e.storePath)
	if err != nil {
		t.Fatalf("snapshot: new service: %v", err)
	}
}

func (e *senv) close(t *testing.T) {
	t.Helper()
	if err := e.db.Close(); err != nil {
		t.Fatalf("snapshot: close store: %v", err)
	}
	e.db = nil
}

func (e *senv) action(t *testing.T) identity.ActionID {
	t.Helper()
	return mustUUID(t, identity.NewActionID)
}

// assign creates an assignment through the service.
func (e *senv) assign(t *testing.T, action identity.ActionID, exclusions ...string) Assignment {
	t.Helper()
	a, err := e.svc.CreateAssignment(context.Background(), AssignmentRequest{
		WorkID:       e.workID,
		ActionID:     action,
		RepositoryID: e.repoID,
		SourceDir:    e.source,
		Exclusions:   exclusions,
	})
	if err != nil {
		t.Fatalf("snapshot: create assignment: %v", err)
	}
	return a
}

// implement edits the work tree of an assignment.
func implementWork(t *testing.T, a Assignment, files map[string]string, remove ...string) {
	t.Helper()
	for _, rel := range remove {
		if err := os.RemoveAll(filepath.Join(a.WorkPath, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("snapshot: remove %s: %v", rel, err)
		}
	}
	writeTree(t, a.WorkPath, files)
}

// stageDigest captures the stage tree's root digest.
func stageDigest(t *testing.T, e *senv) string {
	t.Helper()
	m, err := capture(context.Background(), e.svc.StagePath(), Limits{}.withDefaults(), nil, "")
	if err != nil {
		t.Fatalf("snapshot: capture stage: %v", err)
	}
	return string(m.RootDigest)
}

func readBaseManifest(t *testing.T, path string) Manifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("snapshot: read manifest: %v", err)
	}
	m, err := DecodeManifest(data)
	if err != nil {
		t.Fatalf("snapshot: decode manifest: %v", err)
	}
	return m
}

func TestCreateAssignment(t *testing.T) {
	e := newSenv(t, map[string]string{"README.md": "read me", "src/app.go": "code", "junk.log": "log"})
	action := e.action(t)
	a := e.assign(t, action, "junk.log")

	if a.BaseDigest == "" {
		t.Fatal("snapshot: assignment lacks base digest")
	}
	if a.WorkPath != WorkTreePath(e.storePath, action) || a.ManifestPath != BaseManifestPath(e.storePath, action) {
		t.Fatalf("snapshot: assignment paths wrong: %s %s", a.WorkPath, a.ManifestPath)
	}
	// The persisted manifest is strict-decodable, carries the repository
	// id, and excludes excluded content.
	m := readBaseManifest(t, a.ManifestPath)
	if m.RepositoryID != e.repoID {
		t.Fatalf("snapshot: manifest repo id %q, want %q", m.RepositoryID, e.repoID)
	}
	for _, entry := range m.Entries {
		if entry.Path == "junk.log" {
			t.Fatal("snapshot: exclusion not applied")
		}
	}
	// The work tree exists, verifies, and holds the captured content.
	if err := Verify(context.Background(), a.WorkPath, m); err != nil {
		t.Fatalf("snapshot: work tree: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(a.WorkPath, "README.md")); string(b) != "read me" {
		t.Fatalf("snapshot: work content: %q", b)
	}
	// Blobs for every content entry are in the store.
	for _, entry := range m.Entries {
		if entry.Digest != "" {
			if _, err := os.Stat(filepath.Join(BlobDir(e.storePath), entry.Digest)); err != nil {
				t.Fatalf("snapshot: blob of %s: %v", entry.Path, err)
			}
		}
	}

	// A second assignment of the same action converges: effects are
	// idempotent.
	a2 := e.assign(t, action, "junk.log")
	if a2.BaseDigest != a.BaseDigest {
		t.Fatalf("snapshot: re-assignment changed the base: %s vs %s", a2.BaseDigest, a.BaseDigest)
	}
}

func TestDiffResultAndApplyToStage(t *testing.T) {
	e := newSenv(t, map[string]string{"a.txt": "aaa", "edit.txt": "old", "gone.txt": "gone"})
	action := e.action(t)
	a := e.assign(t, action)
	implementWork(t, a,
		map[string]string{"edit.txt": "new content", "added.txt": "added"}, "gone.txt")

	patch, err := e.svc.DiffResult(context.Background(), a)
	if err != nil {
		t.Fatalf("snapshot: diff result: %v", err)
	}
	data, err := os.ReadFile(PatchManifestPath(e.storePath, action))
	if err != nil {
		t.Fatalf("snapshot: patch file: %v", err)
	}
	if _, err := DecodePatch(data); err != nil {
		t.Fatalf("snapshot: decode patch file: %v", err)
	}

	if err := e.svc.ApplyToStage(context.Background(), a); err != nil {
		t.Fatalf("snapshot: apply to stage: %v", err)
	}
	if got := stageDigest(t, e); got != string(patch.ResultDigest) {
		t.Fatalf("snapshot: stage digest %s, want %s", got, patch.ResultDigest)
	}

	// Re-applying through the service converges (idempotent effect).
	if err := e.svc.ApplyToStage(context.Background(), a); err != nil {
		t.Fatalf("snapshot: re-apply to stage: %v", err)
	}
	if got := stageDigest(t, e); got != string(patch.ResultDigest) {
		t.Fatalf("snapshot: stage diverged on re-apply: %s", got)
	}
}

func TestApplyToStageSequentialPatches(t *testing.T) {
	e := newSenv(t, map[string]string{"one.txt": "one"})
	first, second := e.action(t), e.action(t)
	a1 := e.assign(t, first)
	implementWork(t, a1, map[string]string{"one.txt": "one changed"})
	p1, err := e.svc.DiffResult(context.Background(), a1)
	if err != nil {
		t.Fatalf("snapshot: diff 1: %v", err)
	}
	if err := e.svc.ApplyToStage(context.Background(), a1); err != nil {
		t.Fatalf("snapshot: apply 1: %v", err)
	}

	// The second action starts from the SAME base snapshot (the engine's
	// sequential-patch contract, mirroring cherry-pick ordering).
	a2 := e.assign(t, second)
	implementWork(t, a2, map[string]string{"two.txt": "two"})
	if _, err := e.svc.DiffResult(context.Background(), a2); err != nil {
		t.Fatalf("snapshot: diff 2: %v", err)
	}
	if err := e.svc.ApplyToStage(context.Background(), a2); err != nil {
		t.Fatalf("snapshot: apply 2: %v", err)
	}
	// Stage holds both materials' content.
	for path, want := range map[string]string{"one.txt": "one changed", "two.txt": "two"} {
		got, err := os.ReadFile(filepath.Join(e.svc.StagePath(), path))
		if err != nil || string(got) != want {
			t.Fatalf("snapshot: stage %s = %q (%v), want %q", path, got, err, want)
		}
	}
	if p1.ResultDigest == "" {
		t.Fatal("unreachable")
	}
}

// snapMustCrash runs run and fails the test unless a failpoint panicked.
func snapMustCrash(t *testing.T, run func() error) {
	t.Helper()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		if err := run(); err != nil {
			panic(fmt.Sprintf("snapshot: returned error before crash point: %v", err))
		}
	}()
	if !panicked {
		t.Fatal("snapshot: expected simulated crash at failpoint")
	}
}

// snapFailpoint panics the FIRST time point fires (one-shot: recovery
// legitimately re-crosses journal boundaries, and those re-fires must
// run through): exact match, or prefix match for the per-operation
// windows like "effect-applied-unrecorded:<op>:<seq>".
func snapFailpoint(t *testing.T, point string, prefixMatch bool) (restore func()) {
	t.Helper()
	fired := false
	restore = operation.SetFailpointHook(func(p string) {
		hit := p == point
		if prefixMatch {
			hit = strings.HasPrefix(p, point)
		}
		if hit && !fired {
			fired = true
			panic(fmt.Sprintf("snapshot: simulated crash at %s", p))
		}
	})
	t.Cleanup(restore)
	return restore
}

func TestCreateAssignmentCrashConverges(t *testing.T) {
	for _, point := range []string{
		"pending", "prepared",
		"effect-applied-unrecorded", "effect-applied",
		"finalized",
	} {
		t.Run(point, func(t *testing.T) {
			e := newSenv(t, map[string]string{"a.txt": "aaa"})
			snapFailpoint(t, point, strings.HasPrefix(point, "effect-applied-unrecorded"))
			action := e.action(t)
			snapMustCrash(t, func() error {
				_, err := e.svc.CreateAssignment(context.Background(), AssignmentRequest{
					WorkID:       e.workID,
					ActionID:     action,
					RepositoryID: e.repoID,
					SourceDir:    e.source,
				})
				return err
			})
			e.close(t)
			e.open(t)
			if err := e.svc.Recover(context.Background()); err != nil {
				t.Fatalf("snapshot: recover: %v", err)
			}
			if point == "pending" {
				// A pending operation never ran a side effect
				// (ADR 0025): recovery aborts it, nothing is created.
				if _, err := os.Stat(BaseManifestPath(e.storePath, action)); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("snapshot: pending op must abort without effects: %v", err)
				}
				return
			}
			// Every later boundary converges: manifest on disk, work
			// tree verifies.
			m := readBaseManifest(t, BaseManifestPath(e.storePath, action))
			if err := Verify(context.Background(), WorkTreePath(e.storePath, action), m); err != nil {
				t.Fatalf("snapshot: work tree after recovery: %v", err)
			}
		})
	}
}

func TestApplyToStageCrashConverges(t *testing.T) {
	for _, point := range []string{
		"pending", "prepared",
		"effect-applied-unrecorded", "effect-applied",
		"finalized",
	} {
		t.Run(point, func(t *testing.T) {
			e := newSenv(t, map[string]string{"a.txt": "aaa"})
			action := e.action(t)
			a := e.assign(t, action)
			implementWork(t, a, map[string]string{"a.txt": "changed", "b.txt": "new"})
			patch, err := e.svc.DiffResult(context.Background(), a)
			if err != nil {
				t.Fatalf("snapshot: diff: %v", err)
			}
			snapFailpoint(t, point, strings.HasPrefix(point, "effect-applied-unrecorded"))
			snapMustCrash(t, func() error {
				return e.svc.ApplyToStage(context.Background(), a)
			})
			e.close(t)
			e.open(t)
			if err := e.svc.Recover(context.Background()); err != nil {
				t.Fatalf("snapshot: recover: %v", err)
			}
			if point == "pending" {
				// A pending operation never ran a side effect: recovery
				// aborts it, no stage exists.
				if _, err := os.Stat(e.svc.StagePath()); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("snapshot: pending op must abort without effects: %v", err)
				}
			} else if got := stageDigest(t, e); got != string(patch.ResultDigest) {
				t.Fatalf("snapshot: stage after recovery %s, want %s", got, patch.ResultDigest)
			}
			// The recovered service can still extend the stage.
			if err := e.svc.ApplyToStage(context.Background(), a); err != nil {
				t.Fatalf("snapshot: post-recovery re-apply: %v", err)
			}
			if got := stageDigest(t, e); got != string(patch.ResultDigest) {
				t.Fatalf("snapshot: stage after re-apply %s, want %s", got, patch.ResultDigest)
			}
		})
	}
}

// rollbackRun runs ApplyToStage's journaled operation directly under the
// RollBack policy (the service always issues RollForward; the crash tests
// need the conservative policy).
func (e *senv) rollbackRun(a Assignment) error {
	opID, err := identity.NewOperationID()
	if err != nil {
		return err
	}
	payload := stageApplyPayload{
		StageDir:         e.svc.StagePath(),
		BlobDir:          BlobDir(e.storePath),
		PatchPath:        PatchManifestPath(e.storePath, a.ActionID),
		BaseManifestPath: a.ManifestPath,
		Store:            e.storePath,
	}
	op := &stageApplyOperation{
		id:      opID,
		workID:  e.workID,
		policy:  operation.RollBack,
		payload: payload,
		effects: []operation.Effect{&stageApplyEffect{payload: payload}},
	}
	return e.ops.Run(context.Background(), op)
}

func TestApplyToStageRollBackRestoresStage(t *testing.T) {
	t.Run("recorded apply is reverted", func(t *testing.T) {
		e := newSenv(t, map[string]string{"a.txt": "aaa"})
		action := e.action(t)
		a := e.assign(t, action)
		implementWork(t, a, map[string]string{"a.txt": "changed"})
		if _, err := e.svc.DiffResult(context.Background(), a); err != nil {
			t.Fatalf("snapshot: diff: %v", err)
		}
		// Crash AFTER the applied row committed (before finalize): the
		// effect is recorded applied, so roll-back recovery reverts it —
		// the stage returns to the base tree via the inverse patch.
		snapFailpoint(t, "effect-applied", false)
		snapMustCrash(t, func() error { return e.rollbackRun(a) })
		e.close(t)
		e.open(t)
		if err := e.svc.Recover(context.Background()); err != nil {
			t.Fatalf("snapshot: recover: %v", err)
		}
		base := readBaseManifest(t, a.ManifestPath)
		if err := Verify(context.Background(), e.svc.StagePath(), base); err != nil {
			t.Fatalf("snapshot: stage not restored to base: %v", err)
		}
	})
	t.Run("unrecorded apply leaks by design", func(t *testing.T) {
		e := newSenv(t, map[string]string{"a.txt": "aaa"})
		action := e.action(t)
		a := e.assign(t, action)
		implementWork(t, a, map[string]string{"a.txt": "changed"})
		patch, err := e.svc.DiffResult(context.Background(), a)
		if err != nil {
			t.Fatalf("snapshot: diff: %v", err)
		}
		// Crash in the unrecorded apply window: the journal never saw
		// the effect (ADR 0025 RollBack leak), so roll-back closes the
		// row without a Revert and the stage keeps the patch result.
		snapFailpoint(t, "effect-applied-unrecorded", true)
		snapMustCrash(t, func() error { return e.rollbackRun(a) })
		e.close(t)
		e.open(t)
		if err := e.svc.Recover(context.Background()); err != nil {
			t.Fatalf("snapshot: recover: %v", err)
		}
		if got := stageDigest(t, e); got != string(patch.ResultDigest) {
			t.Fatalf("snapshot: leaked stage %s, want patch result %s", got, patch.ResultDigest)
		}
	})
}

func TestApplyToStageTerminalVerify(t *testing.T) {
	e := newSenv(t, map[string]string{"one.txt": "one"})
	first, second := e.action(t), e.action(t)
	a1 := e.assign(t, first)
	implementWork(t, a1, map[string]string{"one.txt": "first change"})
	if _, err := e.svc.DiffResult(context.Background(), a1); err != nil {
		t.Fatalf("snapshot: diff 1: %v", err)
	}
	if err := e.svc.ApplyToStage(context.Background(), a1); err != nil {
		t.Fatalf("snapshot: apply 1: %v", err)
	}
	a2 := e.assign(t, second)
	implementWork(t, a2, map[string]string{"two.txt": "two"})
	if _, err := e.svc.DiffResult(context.Background(), a2); err != nil {
		t.Fatalf("snapshot: diff 2: %v", err)
	}

	// The last apply of the sequence carries terminal verification: the
	// cumulative state (base + both patches) must equal the stage.
	if err := e.svc.ApplyToStage(context.Background(), a2, WithTerminalVerify(a1)); err != nil {
		t.Fatalf("snapshot: terminal apply: %v", err)
	}

	// A divergent extra file planted before the final apply sails past
	// the per-op preimage checks (no op touches it) and is caught by the
	// terminal verification, typed and named.
	e2 := newSenv(t, map[string]string{"one.txt": "one"})
	b1, b2 := e2.action(t), e2.action(t)
	c1 := e2.assign(t, b1)
	implementWork(t, c1, map[string]string{"one.txt": "first change"})
	if _, err := e2.svc.DiffResult(context.Background(), c1); err != nil {
		t.Fatalf("snapshot: diff 1: %v", err)
	}
	if err := e2.svc.ApplyToStage(context.Background(), c1); err != nil {
		t.Fatalf("snapshot: apply 1: %v", err)
	}
	c2 := e2.assign(t, b2)
	implementWork(t, c2, map[string]string{"two.txt": "two"})
	if _, err := e2.svc.DiffResult(context.Background(), c2); err != nil {
		t.Fatalf("snapshot: diff 2: %v", err)
	}
	if err := os.WriteFile(filepath.Join(e2.svc.StagePath(), "rogue"), []byte("x"), 0o644); err != nil {
		t.Fatalf("snapshot: plant rogue: %v", err)
	}
	err := e2.svc.ApplyToStage(context.Background(), c2, WithTerminalVerify(c1))
	if !errors.Is(err, ErrVerifyFailed) {
		t.Fatalf("snapshot: want ErrVerifyFailed from terminal verify, got %v", err)
	}
	var ve *VerifyError
	if !errors.As(err, &ve) || ve.Path != "rogue" {
		t.Fatalf("snapshot: terminal verify names wrong path: %v", err)
	}

	// Single-material terminal verify: no prior assignments.
	e3 := newSenv(t, map[string]string{"solo.txt": "solo"})
	a := e3.assign(t, e3.action(t))
	implementWork(t, a, map[string]string{"solo.txt": "changed"})
	if _, err := e3.svc.DiffResult(context.Background(), a); err != nil {
		t.Fatalf("snapshot: diff: %v", err)
	}
	if err := e3.svc.ApplyToStage(context.Background(), a, WithTerminalVerify()); err != nil {
		t.Fatalf("snapshot: solo terminal apply: %v", err)
	}
}

func TestRollBackKeepsEarlierMaterials(t *testing.T) {
	e := newSenv(t, map[string]string{"one.txt": "one"})
	first, second := e.action(t), e.action(t)
	a1 := e.assign(t, first)
	implementWork(t, a1, map[string]string{"one.txt": "first change"})
	p1, err := e.svc.DiffResult(context.Background(), a1)
	if err != nil {
		t.Fatalf("snapshot: diff 1: %v", err)
	}
	if err := e.svc.ApplyToStage(context.Background(), a1); err != nil {
		t.Fatalf("snapshot: apply 1: %v", err)
	}

	a2 := e.assign(t, second)
	implementWork(t, a2, map[string]string{"two.txt": "second"})
	if _, err := e.svc.DiffResult(context.Background(), a2); err != nil {
		t.Fatalf("snapshot: diff 2: %v", err)
	}
	// Roll back only the second patch (recorded apply): the first
	// material survives — revert runs the patch's inverse operations,
	// not a stage reset.
	snapFailpoint(t, "effect-applied", false)
	snapMustCrash(t, func() error { return e.rollbackRun(a2) })
	e.close(t)
	e.open(t)
	if err := e.svc.Recover(context.Background()); err != nil {
		t.Fatalf("snapshot: recover: %v", err)
	}
	if got := stageDigest(t, e); got != string(p1.ResultDigest) {
		t.Fatalf("snapshot: roll-back wiped earlier materials: stage %s, want %s", got, p1.ResultDigest)
	}
}

func TestRevertCrashMidRevertConverges(t *testing.T) {
	e := newSenv(t, map[string]string{"a.txt": "aaa"})
	action := e.action(t)
	a := e.assign(t, action)
	implementWork(t, a, map[string]string{"a.txt": "changed"})
	if _, err := e.svc.DiffResult(context.Background(), a); err != nil {
		t.Fatalf("snapshot: diff: %v", err)
	}

	// Die after the applied row commits but before finalize (the op is
	// pending under roll-back), then die again mid-revert (unrecorded
	// revert window); the third pass completes the re-revert.
	snapFailpoint(t, "effect-applied", false)
	snapMustCrash(t, func() error { return e.rollbackRun(a) })
	e.close(t)
	e.open(t)
	snapFailpoint(t, "effect-reverted-unrecorded", true)
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		_ = e.svc.Recover(context.Background())
	}()
	if !panicked {
		t.Fatal("snapshot: expected crash mid-revert")
	}

	e.close(t)
	e.open(t)
	if err := e.svc.Recover(context.Background()); err != nil {
		t.Fatalf("snapshot: recover: %v", err)
	}
	base := readBaseManifest(t, a.ManifestPath)
	if err := Verify(context.Background(), e.svc.StagePath(), base); err != nil {
		t.Fatalf("snapshot: stage not restored: %v", err)
	}
}

func TestServiceRejectsInvalidRequests(t *testing.T) {
	e := newSenv(t, map[string]string{"a": "a"})
	ctx := context.Background()
	if _, err := e.svc.CreateAssignment(ctx, AssignmentRequest{SourceDir: e.source}); err == nil {
		t.Fatal("snapshot: empty ids accepted")
	}
	if _, err := NewService(e.db, e.ops, filepath.Join("rel", "store")); err == nil {
		t.Fatal("snapshot: relative store accepted")
	}
	if _, err := NewService(nil, e.ops, e.storePath); err == nil {
		t.Fatal("snapshot: nil db accepted")
	}
	req := AssignmentRequest{WorkID: e.workID, ActionID: e.action(t), RepositoryID: e.repoID, SourceDir: e.source, Exclusions: []string{".."}}
	if _, err := e.svc.CreateAssignment(ctx, req); !errors.Is(err, ErrInvalidExclusion) {
		t.Fatalf("snapshot: bad exclusion: %v", err)
	}
	// An assignment whose manifest file is missing fails loudly.
	a := Assignment{ManifestPath: filepath.Join(e.storePath, "missing.json"), WorkPath: e.storePath, ActionID: e.action(t)}
	if _, err := e.svc.DiffResult(ctx, a); err == nil {
		t.Fatal("snapshot: missing manifest accepted")
	}
}

func TestCaptureChangedSourceFailsClosedOnRecovery(t *testing.T) {
	e := newSenv(t, map[string]string{"a.txt": "aaa"})
	action := e.action(t)
	// Crash after the capture effect ran but before its row committed;
	// then the source changes before recovery.
	snapFailpoint(t, "effect-applied-unrecorded", true)
	snapMustCrash(t, func() error {
		_, err := e.svc.CreateAssignment(context.Background(), AssignmentRequest{
			WorkID:       e.workID,
			ActionID:     action,
			RepositoryID: e.repoID,
			SourceDir:    e.source,
		})
		return err
	})
	writeTree(t, e.source, map[string]string{"a.txt": "changed under recovery"})
	e.close(t)
	e.open(t)
	if err := e.svc.Recover(context.Background()); err == nil {
		t.Fatal("snapshot: source changed under recovery must fail closed")
	}
}
