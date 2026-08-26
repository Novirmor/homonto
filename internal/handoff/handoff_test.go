package handoff

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
)

// assertConvergedHandoff verifies the durable end state of a completed
// portable handoff: transferable checkpoint at generation+1, leases and
// sentinel gone, and the handoff commit present with matching content.
func assertConvergedHandoff(t *testing.T, m *machine) {
	t.Helper()
	cp := readCP(t, m.root)
	if cp.Handoff.State != checkpoint.HandoffTransferable || cp.Handoff.Generation != 2 {
		t.Errorf("handoff: checkpoint handoff = %+v, want transferable@2", cp.Handoff)
	}
	if err := identity.ValidateToken(string(cp.Handoff.TransferID)); err != nil {
		t.Errorf("handoff: transfer id: %v", err)
	}
	for _, target := range m.leaseTargets() {
		if _, err := os.Stat(target.Path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("handoff: lease %s still present (err %v)", target.Path, err)
		}
	}
	if _, err := os.Stat(lease.SentinelPath(m.root, m.workID)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("handoff: lease sentinel still present")
	}
	subject := headSubject(t, m.runner, m.root)
	if subject != "homonto: portable handoff "+string(m.workID) {
		t.Errorf("handoff: HEAD subject = %q, want portable handoff commit", subject)
	}
	committed := gitShow(t, m.runner, m.root, "HEAD:.homonto/checkpoint.json")
	onDisk, err := os.ReadFile(CheckpointPath(m.root))
	if err != nil {
		t.Fatalf("handoff: read checkpoint: %v", err)
	}
	if string(committed) != string(onDisk) {
		t.Errorf("handoff: committed checkpoint differs from on-disk checkpoint")
	}

	// The journal reached a terminal state.
	db, err := store.Open(context.Background(), RuntimeDBPath(m.root), store.OpenOptions{})
	if err != nil {
		t.Fatalf("handoff: open runtime: %v", err)
	}
	defer func() { _ = db.Close() }()
	assertOpTerminal(t, db, "handoff.portable")
}

func TestPreparePortableHappyPath(t *testing.T) {
	m := newMachine(t)
	m.close(t)
	if err := m.prepare(); err != nil {
		t.Fatalf("handoff: prepare: %v", err)
	}
	assertConvergedHandoff(t, m)
}

func TestPreparePortableRequiresLocalCheckpoint(t *testing.T) {
	m := newMachine(t)
	m.close(t)
	if err := m.prepare(); err != nil {
		t.Fatalf("handoff: prepare: %v", err)
	}
	err := m.prepare()
	if !errors.Is(err, ErrNotLocal) {
		t.Fatalf("handoff: second prepare err = %v, want ErrNotLocal", err)
	}
}

func TestPreparePortableRejectsForeignWork(t *testing.T) {
	m := newMachine(t)
	m.close(t)
	other, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("handoff: work id: %v", err)
	}
	err = PreparePortable(context.Background(), PortableRequest{
		WorkspaceID: m.wsID,
		WorkID:      other,
		ControlRoot: m.root,
		Git:         m.runner,
	})
	if !errors.Is(err, ErrNoActiveWork) {
		t.Fatalf("handoff: foreign work err = %v, want ErrNoActiveWork", err)
	}
}

func TestPreparePortableNothingToCommit(t *testing.T) {
	m := newMachine(t)
	m.close(t)

	// Pre-commit exactly the bytes the handoff would write: the checkpoint
	// transition then produces no diff and the required commit must refuse.
	// The handoff normally mints a fresh transfer id per run, so the
	// pre-committed id is supplied through the request to make the
	// transition deterministic.
	token := mustToken(t)
	precommitTransition(t, m, token)

	err := PreparePortable(context.Background(), PortableRequest{
		WorkspaceID: m.wsID,
		WorkID:      m.workID,
		ControlRoot: m.root,
		Git:         m.runner,
		TransferID:  token,
	})
	if !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("handoff: nothing-to-commit err = %v, want ErrNothingToCommit", err)
	}
	// The failed handoff rolled back completely: local checkpoint, held
	// leases, sentinel present, no handoff commit.
	after := readCP(t, m.root)
	if after.Handoff.State != checkpoint.HandoffLocal || after.Handoff.Generation != 1 {
		t.Errorf("handoff: checkpoint = %+v, want rolled back to local@1", after.Handoff)
	}
	for _, target := range m.leaseTargets() {
		if _, err := os.Stat(target.Path); err != nil {
			t.Errorf("handoff: lease %s not restored: %v", target.Path, err)
		}
	}
	if _, err := os.Stat(lease.SentinelPath(m.root, m.workID)); err != nil {
		t.Errorf("handoff: sentinel not restored: %v", err)
	}
	if subject := headSubject(t, m.runner, m.root); subject != "preflight" {
		t.Errorf("handoff: HEAD subject = %q, want preflight", subject)
	}
}

// precommitTransition writes the transferable@2 transition with token to
// disk, commits it as "preflight", and rewinds the working tree to the
// local state the handoff starts from.
func precommitTransition(t *testing.T, m *machine, token identity.Token) {
	t.Helper()
	cp := readCP(t, m.root)
	next := cp
	next.Handoff = checkpoint.Handoff{State: checkpoint.HandoffTransferable, Generation: 2, TransferID: token}
	data, err := checkpoint.Encode(next)
	if err != nil {
		t.Fatalf("handoff: encode: %v", err)
	}
	if err := os.WriteFile(CheckpointPath(m.root), data, 0o600); err != nil {
		t.Fatalf("handoff: write checkpoint: %v", err)
	}
	if _, err := m.runner.Run(context.Background(), m.root,
		"add", "-f", "--", ".homonto/checkpoint.json", ".homonto/config.toml"); err != nil {
		t.Fatalf("handoff: stage: %v", err)
	}
	if _, err := m.runner.Run(context.Background(), m.root,
		"-c", "user.name=homonto", "-c", "user.email=homonto@localhost",
		"-c", "commit.gpgsign=false", "commit", "-m", "preflight"); err != nil {
		t.Fatalf("handoff: preflight commit: %v", err)
	}
	local, err := checkpoint.Encode(cp)
	if err != nil {
		t.Fatalf("handoff: encode local: %v", err)
	}
	if err := os.WriteFile(CheckpointPath(m.root), local, 0o600); err != nil {
		t.Fatalf("handoff: restore local checkpoint: %v", err)
	}
}

// TestPreparePortableRecoveryNothingToCommit pins the recovery boundary of
// the nothing-to-commit refusal: when the transition is pre-committed with
// identical bytes (the TransferID override scenario), recovery's re-apply
// of the required commit refuses again and the operation stays prepared —
// a diagnosable stuck state, never a silent success. Production cannot
// reach it (fresh transfer ids always produce a staged diff), so the test
// documents the boundary rather than fixing it.
func TestPreparePortableRecoveryNothingToCommit(t *testing.T) {
	m := newMachine(t)
	m.close(t)

	token := mustToken(t)
	precommitTransition(t, m, token)

	restore := setFailpoint(t, "prepared", 1)
	mustCrash(t, func() error {
		return PreparePortable(context.Background(), PortableRequest{
			WorkspaceID: m.wsID,
			WorkID:      m.workID,
			ControlRoot: m.root,
			Git:         m.runner,
			TransferID:  token,
		})
	})
	restore()

	db := openDB(t, m.root)
	defer func() { _ = db.Close() }()
	err := Recover(context.Background(), db)
	if !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("handoff: recovery err = %v, want ErrNothingToCommit", err)
	}
	if state := opState(t, db, "handoff.portable"); state != store.OpPrepared {
		t.Fatalf("handoff: op state = %s, want prepared (diagnosable, not silent success)", state)
	}
	// The boundary is stuck: a second recovery pass refuses the same way.
	err = Recover(context.Background(), db)
	if !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("handoff: second recovery err = %v, want ErrNothingToCommit", err)
	}
}

func TestPreparePortableRollbackOnCommitFailure(t *testing.T) {
	m := newMachine(t)
	m.close(t)
	// A failing pre-commit hook makes the required commit fail after the
	// checkpoint, leases, and sentinel effects already applied.
	hook := filepath.Join(m.root, ".git", "hooks", "pre-commit")
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatalf("handoff: mkdir hooks: %v", err)
	}
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("handoff: write hook: %v", err)
	}

	err := m.prepare()
	if err == nil || errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("handoff: prepare with broken commit = %v, want failure", err)
	}
	cp := readCP(t, m.root)
	if cp.Handoff.State != checkpoint.HandoffLocal || cp.Handoff.Generation != 1 {
		t.Errorf("handoff: checkpoint = %+v, want rolled back to local@1", cp.Handoff)
	}
	for _, target := range m.leaseTargets() {
		if _, err := os.Stat(target.Path); err != nil {
			t.Errorf("handoff: lease %s not restored: %v", target.Path, err)
		}
	}
	if _, err := os.Stat(lease.SentinelPath(m.root, m.workID)); err != nil {
		t.Errorf("handoff: sentinel not restored: %v", err)
	}
	if subject := headSubject(t, m.runner, m.root); !strings.Contains(subject, "initialize workspace") {
		t.Errorf("handoff: HEAD subject = %q, want bootstrap commit", subject)
	}

	// Removing the obstruction lets the handoff succeed on retry.
	if err := os.Remove(hook); err != nil {
		t.Fatalf("handoff: remove hook: %v", err)
	}
	if err := m.prepare(); err != nil {
		t.Fatalf("handoff: retry prepare: %v", err)
	}
	assertConvergedHandoff(t, m)
}

func TestPreparePortableCrashMatrix(t *testing.T) {
	points := []string{"pending", "prepared", "finalized"}
	for _, point := range points {
		t.Run(point, func(t *testing.T) {
			m := newMachine(t)
			m.close(t)
			restore := setFailpoint(t, point, 1)
			mustCrash(t, m.prepare)
			restore()

			recoverDB(t, m.root)
			db := openDB(t, m.root)
			defer func() { _ = db.Close() }()

			state := opState(t, db, "handoff.portable")
			switch point {
			case "pending":
				// Nothing ran: recovery aborts the operation and leaves the
				// pre-handoff state intact; a re-run completes it.
				if state != store.OpRolledBack {
					t.Fatalf("handoff: op state = %s, want rolled_back", state)
				}
				cp := readCP(t, m.root)
				if cp.Handoff.State != checkpoint.HandoffLocal {
					t.Fatalf("handoff: checkpoint = %s, want local", cp.Handoff.State)
				}
				if err := m.prepare(); err != nil {
					t.Fatalf("handoff: rerun prepare: %v", err)
				}
				assertConvergedHandoff(t, m)
			default:
				if state != store.OpFinalized {
					t.Fatalf("handoff: op state = %s, want finalized", state)
				}
				assertConvergedHandoff(t, m)
			}
		})
	}

	// Crash after each applied effect (6 effects: checkpoint, three lease
	// removals, sentinel removal, commit).
	for nth := 1; nth <= 6; nth++ {
		t.Run("effect", func(t *testing.T) {
			m := newMachine(t)
			m.close(t)
			restore := setFailpoint(t, "effect-applied", nth)
			mustCrash(t, m.prepare)
			restore()

			recoverDB(t, m.root)
			db := openDB(t, m.root)
			defer func() { _ = db.Close() }()
			if state := opState(t, db, "handoff.portable"); state != store.OpFinalized {
				t.Fatalf("handoff: op state = %s, want finalized", state)
			}
			assertConvergedHandoff(t, m)
		})
	}
}

// journaledLeases reconstructs the lease set the acquisition journal
// recorded, linking each lease to the operation whose payload carries its
// recovery token.
func (m *machine) journaledLeases(t *testing.T) []lease.Lease {
	t.Helper()
	db := openDB(t, m.root)
	defer func() { _ = db.Close() }()
	var out []lease.Lease
	err := db.View(context.Background(), func(tx *store.Tx) error {
		rows, err := tx.QueryContext(context.Background(), `
			SELECT e.op_id, e.payload FROM operation_effects e
			JOIN operations o ON o.id = e.op_id
			WHERE e.kind = 'lease.create' AND o.kind = 'lease.acquire'
			ORDER BY e.op_id, e.seq`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var opID identity.OperationID
			var payload string
			if err := rows.Scan(&opID, &payload); err != nil {
				return err
			}
			var p struct {
				Path    string             `json:"path"`
				Content lease.LeaseContent `json:"content"`
			}
			if err := json.Unmarshal([]byte(payload), &p); err != nil {
				return err
			}
			out = append(out, lease.Lease{Path: p.Path, OpID: opID, Content: p.Content})
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("handoff: journaled leases: %v", err)
	}
	return out
}

// openDB opens the runtime database of root for inspection.
func openDB(t *testing.T, root string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), RuntimeDBPath(root), store.OpenOptions{})
	if err != nil {
		t.Fatalf("handoff: open runtime: %v", err)
	}
	return db
}

// opState returns the state of the (single) operation of kind.
func opState(t *testing.T, db *store.DB, kind string) string {
	t.Helper()
	var state string
	err := db.View(context.Background(), func(tx *store.Tx) error {
		return tx.QueryRowContext(context.Background(),
			`SELECT state FROM operations WHERE kind=? ORDER BY created_at LIMIT 1`, kind).Scan(&state)
	})
	if err != nil {
		t.Fatalf("handoff: op state of %s: %v", kind, err)
	}
	return state
}

// latestOpState returns the state of the most recently created operation of
// kind (a host that attached once and then crashed a force attach holds two
// handoff.attach rows; the first is finalized and only the latest matters).
func latestOpState(t *testing.T, db *store.DB, kind string) string {
	t.Helper()
	var state string
	err := db.View(context.Background(), func(tx *store.Tx) error {
		return tx.QueryRowContext(context.Background(),
			`SELECT state FROM operations WHERE kind=? ORDER BY created_at DESC, rowid DESC LIMIT 1`, kind).Scan(&state)
	})
	if err != nil {
		t.Fatalf("handoff: op state of %s: %v", kind, err)
	}
	return state
}

func mustToken(t *testing.T) identity.Token {
	t.Helper()
	token, err := identity.NewToken()
	if err != nil {
		t.Fatalf("handoff: token: %v", err)
	}
	return token
}

// TestCommitEffectNothingToCommit exercises the commit effect directly.
func TestCommitEffectNothingToCommit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runner := gitx.ExecRunner{}
	if err := gitx.Init(ctx, runner, root); err != nil {
		t.Fatalf("handoff: init: %v", err)
	}
	eff := &commitEffect{payload: commitPayload{
		Root: root, Message: "homonto: portable handoff x", Required: true,
	}}
	rec := operation.EffectRecord{Seq: 1, Kind: eff.Kind()}
	if err := recPayload(t, eff, &rec); err != nil {
		t.Fatalf("handoff: prepare: %v", err)
	}
	err := eff.Apply(ctx, rec)
	if !errors.Is(err, ErrNothingToCommit) {
		t.Fatalf("handoff: required commit on empty diff err = %v, want ErrNothingToCommit", err)
	}

	// With something staged the commit applies, and re-applying with the
	// same message is the idempotent no-op recovery needs.
	if err := os.MkdirAll(filepath.Join(root, ".homonto"), 0o755); err != nil {
		t.Fatalf("handoff: mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".homonto", "checkpoint.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("handoff: mkdir/write: %v", err)
	}
	eff = &commitEffect{payload: commitPayload{
		Root: root, Message: "homonto: portable handoff x", Required: true,
	}}
	rec = operation.EffectRecord{Seq: 1, Kind: eff.Kind()}
	if err := recPayload(t, eff, &rec); err != nil {
		t.Fatalf("handoff: prepare: %v", err)
	}
	if err := eff.Apply(ctx, rec); err != nil {
		t.Fatalf("handoff: apply: %v", err)
	}
	if err := eff.Apply(ctx, rec); err != nil {
		t.Fatalf("handoff: idempotent re-apply: %v", err)
	}
	if subject := headSubject(t, runner, root); subject != "homonto: portable handoff x" {
		t.Fatalf("handoff: HEAD = %q", subject)
	}

	// A non-required commit (attach) skips silently when nothing is staged.
	eff = &commitEffect{payload: commitPayload{
		Root: root, Message: "homonto: attach 2", Required: false,
	}}
	rec = operation.EffectRecord{Seq: 2, Kind: eff.Kind()}
	if err := recPayload(t, eff, &rec); err != nil {
		t.Fatalf("handoff: prepare: %v", err)
	}
	if err := eff.Apply(ctx, rec); err != nil {
		t.Fatalf("handoff: optional commit on empty diff: %v", err)
	}
}

// recPayload runs the effect's Prepare and installs the marshalled payload
// into rec, mirroring what the journal persists.
func recPayload(t *testing.T, eff operation.Effect, rec *operation.EffectRecord) error {
	t.Helper()
	v, err := eff.Prepare(context.Background())
	if err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	rec.Payload = data
	return nil
}
