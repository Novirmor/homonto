package handoff

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/lease"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

func TestAttachHappyPath(t *testing.T) {
	m := newMachine(t)

	// An action token minted under the old machine's runtime key must be
	// invalid after attach re-keys the runtime.
	oldDB := openDB(t, m.root)
	oldKey, err := RuntimeKey(context.Background(), oldDB)
	if err != nil {
		t.Fatalf("handoff: old runtime key: %v", err)
	}
	actionID, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("handoff: action id: %v", err)
	}
	oldToken := IssueFreshnessToken(oldKey, actionID)
	if err := oldDB.Close(); err != nil {
		t.Fatalf("handoff: close old db: %v", err)
	}

	root, stateRoot, transferID := newAttachedHost(t, m)
	if err := Attach(context.Background(), attachReq(root, stateRoot, false)); err != nil {
		t.Fatalf("handoff: attach: %v", err)
	}
	assertConvergedAttach(t, root, stateRoot, m.wsID, m.workID, 2, transferID)

	// Fresh runtime key: old action tokens fail under it, new ones verify.
	db := openDB(t, root)
	newKey, err := RuntimeKey(context.Background(), db)
	if err != nil {
		t.Fatalf("handoff: new runtime key: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("handoff: close db: %v", err)
	}
	if newKey == oldKey {
		t.Fatal("handoff: attach kept the old runtime key")
	}
	if VerifyFreshnessToken(newKey, actionID, oldToken) {
		t.Fatal("handoff: old action token verifies under the new key")
	}
	if !VerifyFreshnessToken(newKey, actionID, IssueFreshnessToken(newKey, actionID)) {
		t.Fatal("handoff: freshly issued token fails verification")
	}
}

func TestAttachProposesExactMappingsForClonedLayout(t *testing.T) {
	m := newMachine(t)
	root, _, _ := newAttachedHost(t, m)

	cp := readCP(t, root)
	cfg, err := workspacecfg.Load(workspace.ConfigPath(root))
	if err != nil {
		t.Fatalf("handoff: load cloned config: %v", err)
	}
	candidates := []workspace.Candidate{
		{Path: filepath.Join(root, "member-a"), Kind: workspacecfg.KindNonGit, Manifest: "go.mod"},
		{Path: filepath.Join(root, "member-b"), Kind: workspacecfg.KindNonGit, Manifest: "go.mod"},
	}
	proposals := ProposeMappings(cp, cfg, candidates)
	if len(proposals) != 2 {
		t.Fatalf("handoff: proposals = %d, want 2", len(proposals))
	}
	for _, p := range proposals {
		if p.Status != StatusExact {
			t.Errorf("handoff: proposal %s = %s (%v), want exact", p.RepositoryID, p.Status, p.Reasons)
		}
	}
}

func TestAttachOnConsumedCheckpoint(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, _ := newAttachedHost(t, m)
	if err := Attach(context.Background(), attachReq(root, stateRoot, false)); err != nil {
		t.Fatalf("handoff: attach: %v", err)
	}
	err := Attach(context.Background(), attachReq(root, stateRoot, false))
	if !errors.Is(err, ErrCheckpointConsumed) {
		t.Fatalf("handoff: re-attach err = %v, want ErrCheckpointConsumed", err)
	}
}

func TestAttachOnLocalCheckpointRefused(t *testing.T) {
	m := newMachine(t)
	m.close(t)

	// A stale clone that predates the handoff still sees a local checkpoint.
	root := filepath.Join(t.TempDir(), "ws")
	copyTree(t, m.root, root)
	stateRoot := t.TempDir()
	err := Attach(context.Background(), attachReq(root, stateRoot, false))
	if !errors.Is(err, ErrNotTransferable) {
		t.Fatalf("handoff: local-checkpoint attach err = %v, want ErrNotTransferable", err)
	}
	// Nothing was claimed or leased by the refused attach.
	if _, err := os.Stat(filepath.Join(root, ".git", "homonto", "registration.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff: control claimed during refused attach")
	}
	for _, p := range memberPathsOnHost(root) {
		reg, err := registration.NonGitRegistrationPath(stateRoot, p)
		if err != nil {
			t.Fatalf("handoff: reg path: %v", err)
		}
		if _, err := os.Stat(reg); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("handoff: member claimed during refused attach: %s", reg)
		}
	}
}

func TestAttachForceTakeover(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, _ := newAttachedHost(t, m)
	if err := Attach(context.Background(), attachReq(root, stateRoot, false)); err != nil {
		t.Fatalf("handoff: attach: %v", err)
	}

	// A third machine force-attaches the already-consumed checkpoint.
	forceRoot := cloneControl(t, root)
	for _, name := range []string{"member-a", "member-b"} {
		if err := os.MkdirAll(filepath.Join(forceRoot, name), 0o755); err != nil {
			t.Fatalf("handoff: mkdir %s: %v", name, err)
		}
	}
	forceState := t.TempDir()
	forceReq := attachReq(forceRoot, forceState, true)

	// Without force the consumed checkpoint refuses.
	if err := Attach(context.Background(), attachReq(forceRoot, forceState, false)); !errors.Is(err, ErrCheckpointConsumed) {
		t.Fatalf("handoff: consumed attach err = %v, want ErrCheckpointConsumed", err)
	}
	if err := Attach(context.Background(), forceReq); err != nil {
		t.Fatalf("handoff: force attach: %v", err)
	}

	// The takeover incremented the generation and consumed at that
	// generation, recorded its decision, and marked evidence stale.
	assertForceConverged(t, forceRoot, forceState, m, 3)
}

// assertForceConverged asserts the full post-force-attach state: the
// converged attach at gen plus the forced_takeover decision and the
// evidence_stale marker.
func assertForceConverged(t *testing.T, root, stateRoot string, m *machine, gen uint64) {
	t.Helper()
	assertConvergedAttach(t, root, stateRoot, m.wsID, m.workID, gen, "")
	summaries := decisionSummaries(t, root)
	found := false
	for _, s := range summaries {
		if strings.HasPrefix(s, "forced_takeover") {
			found = true
		}
	}
	if !found {
		t.Errorf("handoff: no forced_takeover decision recorded: %v", summaries)
	}
	if got := metaValue(t, root, MetaEvidenceStale); got != "1" {
		t.Errorf("handoff: evidence_stale = %q, want 1", got)
	}
}

// TestAttachForceRollback pins the force-path revert chain: a failed force
// attach undoes the takeover write, the claims, leases, sentinel, and the
// rebuild rows — including the forced_takeover decision and the
// evidence_stale marker — restoring the original consumed checkpoint, and
// a retry after the obstruction converges. The force attach runs on a
// fresh clone: a same-host force attach would conflict with the host's own
// gen-2 leases.
func TestAttachForceRollback(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, _ := newAttachedHost(t, m)
	if err := Attach(context.Background(), attachReq(root, stateRoot, false)); err != nil {
		t.Fatalf("handoff: attach: %v", err)
	}

	forceRoot := cloneControl(t, root)
	for _, name := range []string{"member-a", "member-b"} {
		if err := os.MkdirAll(filepath.Join(forceRoot, name), 0o755); err != nil {
			t.Fatalf("handoff: mkdir %s: %v", name, err)
		}
	}
	forceState := t.TempDir()
	tid := readCP(t, forceRoot).Handoff.TransferID
	hook := filepath.Join(forceRoot, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("handoff: write hook: %v", err)
	}

	err := Attach(context.Background(), attachReq(forceRoot, forceState, true))
	if err == nil {
		t.Fatal("handoff: force attach with broken commit succeeded")
	}

	// The revert chain restored the original consumed@2 checkpoint with its
	// original transfer id; the takeover write is gone.
	cp := readCP(t, forceRoot)
	if cp.Handoff.State != checkpoint.HandoffConsumed || cp.Handoff.Generation != 2 || cp.Handoff.TransferID != tid {
		t.Errorf("handoff: checkpoint = %+v, want consumed@2 with the original transfer id", cp.Handoff)
	}
	// No decision row, no evidence marker, no runtime rows.
	for _, s := range decisionSummaries(t, forceRoot) {
		if strings.HasPrefix(s, "forced_takeover") {
			t.Errorf("handoff: forced_takeover decision survived rollback")
		}
	}
	if got := metaValue(t, forceRoot, MetaEvidenceStale); got != "" {
		t.Errorf("handoff: evidence_stale = %q, want absent", got)
	}
	if got := metaValue(t, forceRoot, MetaRuntimeKey); got != "" {
		t.Errorf("handoff: runtime rows survived rollback (runtime_key present)")
	}
	// No claims, leases, or sentinel.
	if _, err := os.Stat(filepath.Join(forceRoot, ".git", "homonto", "registration.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff: control claim not rolled back")
	}
	for _, p := range memberPathsOnHost(forceRoot) {
		reg, rerr := registration.NonGitRegistrationPath(forceState, p)
		if rerr != nil {
			t.Fatalf("handoff: reg path: %v", rerr)
		}
		if _, err := registration.Read(reg); !errors.Is(err, registration.ErrNotRegistered) {
			t.Errorf("handoff: member claim not rolled back: %s", reg)
		}
		lp, lerr := registration.NonGitLeasePath(forceState, p)
		if lerr != nil {
			t.Fatalf("handoff: lease path: %v", lerr)
		}
		if _, err := lease.ReadLease(lp); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("handoff: lease not rolled back: %s", lp)
		}
	}
	if _, err := os.Stat(lease.SentinelPath(forceRoot, m.workID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff: sentinel not rolled back")
	}
	// HEAD still carries the first attach's commit, nothing more.
	if subject := headSubject(t, m.runner, forceRoot); subject != "homonto: attach 2" {
		t.Errorf("handoff: HEAD subject = %q, want the first attach's commit", subject)
	}

	// Removing the obstruction lets the force attach complete on retry.
	if err := os.Remove(hook); err != nil {
		t.Fatalf("handoff: remove hook: %v", err)
	}
	if err := Attach(context.Background(), attachReq(forceRoot, forceState, true)); err != nil {
		t.Fatalf("handoff: retry force attach: %v", err)
	}
	assertForceConverged(t, forceRoot, forceState, m, 3)
}

// TestAttachForceOnTransferableIsNormalAttach pins the design choice (ADR
// 0027): force is the remedy for consumption elsewhere, so an
// already-transferable checkpoint attaches normally — no generation bump,
// no forced_takeover decision, no evidence invalidation.
func TestAttachForceOnTransferableIsNormalAttach(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, tid := newAttachedHost(t, m)
	if err := Attach(context.Background(), attachReq(root, stateRoot, true)); err != nil {
		t.Fatalf("handoff: force attach on transferable: %v", err)
	}
	assertConvergedAttach(t, root, stateRoot, m.wsID, m.workID, 2, tid)
	for _, s := range decisionSummaries(t, root) {
		if strings.HasPrefix(s, "forced_takeover") {
			t.Errorf("handoff: forced_takeover decision on a non-consumed force attach")
		}
	}
	if got := metaValue(t, root, MetaEvidenceStale); got != "" {
		t.Errorf("handoff: evidence_stale = %q, want absent", got)
	}
}

// TestAttachForceCrashMatrix crashes a force attach at every journal
// boundary: pending aborts and re-runs converge; prepared, finalized, and
// every one of the eleven effects (takeover write, three claims, three
// leases, sentinel, rebuild, consume write, commit) roll forward to the
// converged force state. The force attach runs on a fresh clone of the
// consumed host, as a real takeover would.
func TestAttachForceCrashMatrix(t *testing.T) {
	for _, point := range []string{"pending", "prepared", "finalized"} {
		t.Run(point, func(t *testing.T) {
			m := newMachine(t)
			root, stateRoot, _ := newAttachedHost(t, m)
			if err := Attach(context.Background(), attachReq(root, stateRoot, false)); err != nil {
				t.Fatalf("handoff: attach: %v", err)
			}
			forceRoot, forceState := newForceHost(t, root)
			r := attachReq(forceRoot, forceState, true)
			restore := setFailpoint(t, point, 1)
			mustCrash(t, func() error { return Attach(context.Background(), r) })
			restore()

			recoverDB(t, forceRoot)
			db := openDB(t, forceRoot)
			state := latestOpState(t, db, "handoff.attach")
			_ = db.Close()

			switch point {
			case "pending":
				if state != "rolled_back" {
					t.Fatalf("handoff: op state = %s, want rolled_back", state)
				}
				cp := readCP(t, forceRoot)
				if cp.Handoff.State != checkpoint.HandoffConsumed || cp.Handoff.Generation != 2 {
					t.Fatalf("handoff: checkpoint = %+v, want consumed@2 (takeover never ran)", cp.Handoff)
				}
				if err := Attach(context.Background(), r); err != nil {
					t.Fatalf("handoff: rerun force attach: %v", err)
				}
				assertForceConverged(t, forceRoot, forceState, m, 3)
			default:
				if state != "finalized" {
					t.Fatalf("handoff: op state = %s, want finalized", state)
				}
				assertForceConverged(t, forceRoot, forceState, m, 3)
			}
		})
	}

	for nth := 1; nth <= 11; nth++ {
		t.Run("effect", func(t *testing.T) {
			m := newMachine(t)
			root, stateRoot, _ := newAttachedHost(t, m)
			if err := Attach(context.Background(), attachReq(root, stateRoot, false)); err != nil {
				t.Fatalf("handoff: attach: %v", err)
			}
			forceRoot, forceState := newForceHost(t, root)
			r := attachReq(forceRoot, forceState, true)
			restore := setFailpoint(t, "effect-applied", nth)
			mustCrash(t, func() error { return Attach(context.Background(), r) })
			restore()

			recoverDB(t, forceRoot)
			db := openDB(t, forceRoot)
			state := latestOpState(t, db, "handoff.attach")
			_ = db.Close()
			if state != "finalized" {
				t.Fatalf("handoff: op state = %s, want finalized", state)
			}
			assertForceConverged(t, forceRoot, forceState, m, 3)
		})
	}
}

// newForceHost clones a consumed control repository into a fresh host with
// member directories and a fresh state root, as a real takeover would.
func newForceHost(t *testing.T, root string) (forceRoot, forceState string) {
	t.Helper()
	forceRoot = cloneControl(t, root)
	for _, name := range []string{"member-a", "member-b"} {
		if err := os.MkdirAll(filepath.Join(forceRoot, name), 0o755); err != nil {
			t.Fatalf("handoff: mkdir %s: %v", name, err)
		}
	}
	return forceRoot, t.TempDir()
}

func TestAttachMappingRefusals(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, _ := newAttachedHost(t, m)

	// Missing mapping for one member.
	req := attachReq(root, stateRoot, false)
	req.Mappings = req.Mappings[:1]
	err := Attach(context.Background(), req)
	if !errors.Is(err, ErrMappingIncomplete) {
		t.Fatalf("handoff: missing mapping err = %v, want ErrMappingIncomplete", err)
	}
	if !strings.Contains(err.Error(), string(fixtureMemberB)) {
		t.Errorf("handoff: missing mapping error does not name the member: %v", err)
	}

	// Mapping for an unknown repository.
	req = attachReq(root, stateRoot, false)
	unknown, uerr := identity.NewRepositoryID()
	if uerr != nil {
		t.Fatalf("handoff: id: %v", uerr)
	}
	req.Mappings = append(req.Mappings, ConfirmedMapping{RepositoryID: unknown, Path: filepath.Join(root, "member-a")})
	if err := Attach(context.Background(), req); !errors.Is(err, ErrMappingIncomplete) {
		t.Fatalf("handoff: unknown mapping err = %v, want ErrMappingIncomplete", err)
	}

	// Duplicate mapping.
	req = attachReq(root, stateRoot, false)
	req.Mappings = append(req.Mappings, ConfirmedMapping{RepositoryID: fixtureMemberA, Path: filepath.Join(root, "member-a")})
	if err := Attach(context.Background(), req); !errors.Is(err, ErrMappingIncomplete) {
		t.Fatalf("handoff: duplicate mapping err = %v, want ErrMappingIncomplete", err)
	}

	// A mapping whose path does not exist.
	req = attachReq(root, stateRoot, false)
	req.Mappings[1].Path = filepath.Join(root, "nowhere")
	if err := Attach(context.Background(), req); !errors.Is(err, ErrMemberUnusable) {
		t.Fatalf("handoff: nonexistent path err = %v, want ErrMemberUnusable", err)
	}

	// A mapping whose path carries the wrong kind: the control repository
	// is a git root, not a non-git member.
	req = attachReq(root, stateRoot, false)
	req.Mappings[0].Path = root
	if err := Attach(context.Background(), req); !errors.Is(err, ErrMemberUnusable) {
		t.Fatalf("handoff: kind mismatch err = %v, want ErrMemberUnusable", err)
	}
}

func TestAttachPartialClaimFailureRollsBack(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, _ := newAttachedHost(t, m)

	// A foreign registration occupies the second member's slot.
	foreignPath, err := registration.NonGitRegistrationPath(stateRoot, filepath.Join(root, "member-b"))
	if err != nil {
		t.Fatalf("handoff: foreign path: %v", err)
	}
	foreignWS, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("handoff: foreign ws: %v", err)
	}
	foreignRepo, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("handoff: foreign repo: %v", err)
	}
	if err := registration.Claim(foreignPath, registration.Registration{
		SchemaVersion: 1,
		WorkspaceID:   foreignWS,
		RepositoryID:  foreignRepo,
		ControlRoot:   "/elsewhere",
		MemberRoot:    filepath.Join(root, "member-b"),
		Kind:          workspacecfg.KindNonGit,
	}); err != nil {
		t.Fatalf("handoff: plant foreign registration: %v", err)
	}

	err = Attach(context.Background(), attachReq(root, stateRoot, false))
	if !errors.Is(err, registration.ErrOwnedByOther) {
		t.Fatalf("handoff: blocked attach err = %v, want ErrOwnedByOther", err)
	}

	// All-or-none: the first member's claim is gone, no leases were taken,
	// the sentinel is absent, and the checkpoint stays transferable.
	firstPath, err := registration.NonGitRegistrationPath(stateRoot, filepath.Join(root, "member-a"))
	if err != nil {
		t.Fatalf("handoff: first path: %v", err)
	}
	if _, err := registration.Read(firstPath); !errors.Is(err, registration.ErrNotRegistered) {
		t.Errorf("handoff: first member still claimed: %v", err)
	}
	if _, err := lease.ReadLease(filepath.Join(root, ".git", "homonto", "lease.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff: control leased by failed attach: %v", err)
	}
	if _, err := os.Stat(lease.SentinelPath(root, m.workID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff: sentinel left by failed attach")
	}
	cp := readCP(t, root)
	if cp.Handoff.State != checkpoint.HandoffTransferable || cp.Handoff.Generation != 2 {
		t.Errorf("handoff: checkpoint = %+v, want transferable@2 after rollback", cp.Handoff)
	}

	// The foreign registration survives untouched.
	reg, err := registration.Read(foreignPath)
	if err != nil || reg.WorkspaceID != foreignWS {
		t.Errorf("handoff: foreign registration disturbed: %v %+v", err, reg)
	}
}

func TestAttachCommitFailureRollsBack(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, _ := newAttachedHost(t, m)
	hook := filepath.Join(root, ".git", "hooks", "pre-commit")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("handoff: write hook: %v", err)
	}

	err := Attach(context.Background(), attachReq(root, stateRoot, false))
	if err == nil {
		t.Fatal("handoff: attach with broken commit succeeded")
	}

	// Everything the attach applied is undone; only the checkpoint stays
	// transferable for a retry.
	cp := readCP(t, root)
	if cp.Handoff.State != checkpoint.HandoffTransferable || cp.Handoff.Generation != 2 {
		t.Errorf("handoff: checkpoint = %+v, want transferable@2", cp.Handoff)
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "homonto", "registration.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff: control claim not rolled back")
	}
	for _, p := range memberPathsOnHost(root) {
		reg, rerr := registration.NonGitRegistrationPath(stateRoot, p)
		if rerr != nil {
			t.Fatalf("handoff: reg path: %v", rerr)
		}
		if _, err := registration.Read(reg); !errors.Is(err, registration.ErrNotRegistered) {
			t.Errorf("handoff: member claim not rolled back: %s", reg)
		}
		lp, lerr := registration.NonGitLeasePath(stateRoot, p)
		if lerr != nil {
			t.Fatalf("handoff: lease path: %v", lerr)
		}
		if _, err := lease.ReadLease(lp); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("handoff: lease not rolled back: %s", lp)
		}
	}
	if _, err := os.Stat(lease.SentinelPath(root, m.workID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff: sentinel not rolled back")
	}
	if got := metaValue(t, root, MetaRuntimeKey); got != "" {
		t.Errorf("handoff: runtime rows not rolled back (runtime_key present)")
	}

	// Removing the obstruction lets the attach complete on retry.
	if err := os.Remove(hook); err != nil {
		t.Fatalf("handoff: remove hook: %v", err)
	}
	if err := Attach(context.Background(), attachReq(root, stateRoot, false)); err != nil {
		t.Fatalf("handoff: retry attach: %v", err)
	}
	assertConvergedAttach(t, root, stateRoot, m.wsID, m.workID, 2, "")
}

func TestAttachCrashMatrix(t *testing.T) {
	// Each subtest needs its own machine: preparing one host transitions
	// its checkpoint to transferable, so a second prepared host cannot be
	// cut from the same machine.
	for _, point := range []string{"pending", "prepared", "finalized"} {
		t.Run(point, func(t *testing.T) {
			m := newMachine(t)
			fresh, freshState, tid := newAttachedHost(t, m)
			r := attachReq(fresh, freshState, false)
			restore := setFailpoint(t, point, 1)
			mustCrash(t, func() error { return Attach(context.Background(), r) })
			restore()

			recoverDB(t, fresh)
			db := openDB(t, fresh)
			state := opState(t, db, "handoff.attach")
			_ = db.Close()

			switch point {
			case "pending":
				if state != "rolled_back" {
					t.Fatalf("handoff: op state = %s, want rolled_back", state)
				}
				cp := readCP(t, fresh)
				if cp.Handoff.State != checkpoint.HandoffTransferable {
					t.Fatalf("handoff: checkpoint = %s, want transferable", cp.Handoff.State)
				}
				if err := Attach(context.Background(), attachReq(fresh, freshState, false)); err != nil {
					t.Fatalf("handoff: rerun attach: %v", err)
				}
				assertConvergedAttach(t, fresh, freshState, m.wsID, m.workID, 2, tid)
			default:
				if state != "finalized" {
					t.Fatalf("handoff: op state = %s, want finalized", state)
				}
				assertConvergedAttach(t, fresh, freshState, m.wsID, m.workID, 2, tid)
			}
		})
	}

	// Crash after each applied effect. The attach journals ten effects:
	// three claims, three leases, sentinel, rebuild, checkpoint, commit.
	for nth := 1; nth <= 9; nth++ {
		t.Run("effect", func(t *testing.T) {
			m := newMachine(t)
			fresh, freshState, tid := newAttachedHost(t, m)
			r := attachReq(fresh, freshState, false)
			restore := setFailpoint(t, "effect-applied", nth)
			mustCrash(t, func() error { return Attach(context.Background(), r) })
			restore()

			recoverDB(t, fresh)
			db := openDB(t, fresh)
			state := opState(t, db, "handoff.attach")
			_ = db.Close()
			if state != "finalized" {
				t.Fatalf("handoff: op state = %s, want finalized", state)
			}
			assertConvergedAttach(t, fresh, freshState, m.wsID, m.workID, 2, tid)
		})
	}

	// The commit boundary (effect 10): crash after the commit performed but
	// before its applied row committed — the unrecorded window ADR 0025
	// closes by idempotent re-apply. Roll-forward recovery re-applies the
	// commit effect, which recognizes the already-made commit by its HEAD
	// message and finalizes; the op converges with the commit in place.
	t.Run("effect#10-commit-window", func(t *testing.T) {
		m := newMachine(t)
		fresh, freshState, tid := newAttachedHost(t, m)
		r := attachReq(fresh, freshState, false)
		restore := setFailpointPrefix(t, "effect-applied-unrecorded:", ":10", 1)
		mustCrash(t, func() error { return Attach(context.Background(), r) })
		restore()

		recoverDB(t, fresh)
		db := openDB(t, fresh)
		state := opState(t, db, "handoff.attach")
		_ = db.Close()
		if state != "finalized" {
			t.Fatalf("handoff: op state = %s, want finalized", state)
		}
		assertConvergedAttach(t, fresh, freshState, m.wsID, m.workID, 2, tid)
	})
}

// TestAttachCommitLeakOnRollback pins the commit-effect revert semantics
// (ADR 0027): a rolled-back attach cannot uncommit, so the control commit
// survives in both leak classes while the checkpoint revert restores the
// transferable working tree; a fresh attach on that host then rewrites the
// already-committed consumed checkpoint, its best-effort commit finds
// nothing staged (the bytes coincide), and no second commit is made.
func TestAttachCommitLeakOnRollback(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, tid := newAttachedHost(t, m)
	ctx := context.Background()

	cp := readCP(t, root)
	consumed := cp
	consumed.Handoff = checkpoint.Handoff{
		State: checkpoint.HandoffConsumed, Generation: 2, TransferID: cp.Handoff.TransferID,
	}
	if err := checkpoint.ValidateTransition(cp, consumed); err != nil {
		t.Fatalf("handoff: consume transition: %v", err)
	}
	db := openDB(t, root)
	defer func() { _ = db.Close() }()
	ops := operation.NewManager(db)
	registerEffects(ops, db)

	newOp := func() *rollbackAttachOp {
		oid, err := identity.NewOperationID()
		if err != nil {
			t.Fatalf("handoff: operation id: %v", err)
		}
		return &rollbackAttachOp{
			id: oid, workID: m.workID, generation: 2,
			payload: attachPayload{
				WorkspaceID: m.wsID, WorkID: m.workID, Generation: 2,
				ControlRoot: root, TransferID: cp.Handoff.TransferID,
			},
			effects: []operation.Effect{
				&checkpointWriteEffect{payload: checkpointWritePayload{
					Path: CheckpointPath(root), Next: consumed, Prev: cp,
				}},
				&commitEffect{payload: commitPayload{Root: root, Message: "homonto: attach 2"}},
			},
		}
	}

	// The unrecorded-window leak (ADR 0025): the commit performed, its
	// applied row never committed, roll-back closes the row without a
	// Revert — the commit survives unnameable by the journal.
	op := newOp()
	restore := setFailpoint(t, "effect-applied-unrecorded:"+string(op.id)+":2", 1)
	mustCrash(t, func() error { return ops.Run(ctx, op) })
	restore()
	if err := ops.RecoverOne(ctx, op.id); err != nil {
		t.Fatalf("handoff: recover: %v", err)
	}
	if state := opState(t, db, "handoff.attach"); state != "rolled_back" {
		t.Fatalf("handoff: op state = %s, want rolled_back", state)
	}
	assertLeakedCommit(t, root, m, "homonto: attach 2")

	// The recorded-applied leak (ADR 0027): the commit row reads applied,
	// roll-back reverts it — the no-op succeeds, the row journals
	// "reverted", and the commit survives: the journal asserts a lie, and
	// it is accepted because the working-tree checkpoint is what the next
	// attempt converges on.
	op = newOp()
	restore = setFailpoint(t, "effect-applied", 2)
	mustCrash(t, func() error { return ops.Run(ctx, op) })
	restore()
	if err := ops.RecoverOne(ctx, op.id); err != nil {
		t.Fatalf("handoff: recover: %v", err)
	}
	if state := opState(t, db, "handoff.attach"); state != "rolled_back" {
		t.Fatalf("handoff: op state = %s, want rolled_back", state)
	}
	var commitRow string
	if err := db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT state FROM operation_effects WHERE op_id=? AND seq=2`, op.id).Scan(&commitRow)
	}); err != nil {
		t.Fatalf("handoff: commit row: %v", err)
	}
	if commitRow != "reverted" {
		t.Errorf("handoff: commit row = %s, want reverted (the journal's no-op claim)", commitRow)
	}
	assertLeakedCommit(t, root, m, "homonto: attach 2")

	// The roll-forward bytes-coincide case: a fresh attach on the same host
	// rewrites the already-committed consumed checkpoint, so its best-effort
	// commit finds nothing staged and no second commit is made.
	if err := Attach(ctx, attachReq(root, stateRoot, false)); err != nil {
		t.Fatalf("handoff: re-attach after leaked commit: %v", err)
	}
	assertConvergedAttach(t, root, stateRoot, m.wsID, m.workID, 2, tid)
	if n := countSubject(t, m.runner, root, "homonto: attach 2"); n != 1 {
		t.Errorf("handoff: attach commits = %d, want 1 (bytes coincide, commit no-op)", n)
	}
}

// rollbackAttachOp is the attach operation under the roll-back policy the
// leak tests need: production attach operations are always roll_forward, so
// the leak classes are exercised with an explicit roll_back journal entry.
type rollbackAttachOp struct {
	id         identity.OperationID
	workID     identity.WorkID
	generation uint64
	payload    attachPayload
	effects    []operation.Effect
}

func (o *rollbackAttachOp) ID() identity.OperationID    { return o.id }
func (o *rollbackAttachOp) Kind() string                { return "handoff.attach" }
func (o *rollbackAttachOp) WorkID() identity.WorkID     { return o.workID }
func (o *rollbackAttachOp) Generation() int64           { return int64(o.generation) }
func (o *rollbackAttachOp) Policy() operation.Policy    { return operation.RollBack }
func (o *rollbackAttachOp) Payload() any                { return o.payload }
func (o *rollbackAttachOp) Effects() []operation.Effect { return o.effects }

// assertLeakedCommit verifies the rolled-back-commit leak: HEAD carries the
// commit while the working-tree checkpoint no longer matches it.
func assertLeakedCommit(t *testing.T, root string, m *machine, subject string) {
	t.Helper()
	if got := headSubject(t, m.runner, root); got != subject {
		t.Errorf("handoff: HEAD subject = %q, want the leaked %q", got, subject)
	}
	committed := gitShow(t, m.runner, root, "HEAD:.homonto/checkpoint.json")
	onDisk, err := os.ReadFile(CheckpointPath(root))
	if err != nil {
		t.Fatalf("handoff: read checkpoint: %v", err)
	}
	if string(committed) == string(onDisk) {
		t.Errorf("handoff: working-tree checkpoint still matches the leaked commit")
	}
	onDiskCP := readCP(t, root)
	if onDiskCP.Handoff.State != checkpoint.HandoffTransferable {
		t.Errorf("handoff: working-tree checkpoint = %s, want transferable after revert", onDiskCP.Handoff.State)
	}
}

// countSubject counts commits whose subject equals subject.
func countSubject(t *testing.T, runner gitx.Runner, root, subject string) int {
	t.Helper()
	out, err := runner.Run(context.Background(), root, "log", "--pretty=%s")
	if err != nil {
		t.Fatalf("handoff: log: %v", err)
	}
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == subject {
			n++
		}
	}
	return n
}

func TestAttachFreshHome(t *testing.T) {
	m := newMachine(t)

	// The old machine claimed its members' registrations under its state
	// root before handing off.
	ctx := context.Background()
	for _, mem := range m.cfg.Members {
		if mem.Kind != workspacecfg.KindNonGit {
			continue
		}
		regPath, err := registration.NonGitRegistrationPath(m.stateRoot, filepath.Join(m.root, filepath.FromSlash(mem.Path)))
		if err != nil {
			t.Fatalf("handoff: reg path: %v", err)
		}
		if err := registration.Claim(regPath, registration.Registration{
			SchemaVersion: 1,
			WorkspaceID:   m.wsID,
			RepositoryID:  mem.ID,
			ControlRoot:   m.root,
			MemberRoot:    filepath.Join(m.root, filepath.FromSlash(mem.Path)),
			Kind:          mem.Kind,
		}); err != nil {
			t.Fatalf("handoff: claim on old machine: %v", err)
		}
	}

	root, stateRoot, transferID := newAttachedHost(t, m)

	// The old HOME is gone entirely: non-git registrations live in the old
	// machine's state root, so attach must re-claim under the new one.
	if err := os.RemoveAll(m.stateRoot); err != nil {
		t.Fatalf("handoff: scrub old home: %v", err)
	}
	if err := Attach(ctx, attachReq(root, stateRoot, false)); err != nil {
		t.Fatalf("handoff: attach with fresh home: %v", err)
	}
	assertConvergedAttach(t, root, stateRoot, m.wsID, m.workID, 2, transferID)
}

func TestAttachValidatesConfigAgainstCheckpoint(t *testing.T) {
	m := newMachine(t)
	root, stateRoot, _ := newAttachedHost(t, m)

	// Tamper with the cloned config: its fingerprint no longer matches the
	// checkpoint and the attach must refuse before claiming anything.
	cfg, err := workspacecfg.Load(workspace.ConfigPath(root))
	if err != nil {
		t.Fatalf("handoff: load config: %v", err)
	}
	cfg.Members[1].Path = "moved-member-a"
	data, err := workspacecfg.Marshal(cfg)
	if err != nil {
		t.Fatalf("handoff: marshal: %v", err)
	}
	if err := os.WriteFile(workspace.ConfigPath(root), data, 0o644); err != nil {
		t.Fatalf("handoff: write config: %v", err)
	}

	if err := Attach(context.Background(), attachReq(root, stateRoot, false)); err == nil {
		t.Fatal("handoff: attach accepted a tampered config")
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "homonto", "registration.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("handoff: control claimed despite config mismatch")
	}
}

var _ = gitx.ExecRunner{}
