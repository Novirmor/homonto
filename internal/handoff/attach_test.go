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
	"github.com/noviopenworks/homonto/internal/registration"
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
	assertConvergedAttach(t, forceRoot, forceState, m.wsID, m.workID, 3, "")
	summaries := decisionSummaries(t, forceRoot)
	found := false
	for _, s := range summaries {
		if strings.HasPrefix(s, "forced_takeover") {
			found = true
		}
	}
	if !found {
		t.Errorf("handoff: no forced_takeover decision recorded: %v", summaries)
	}
	if got := metaValue(t, forceRoot, MetaEvidenceStale); got != "1" {
		t.Errorf("handoff: evidence_stale = %q, want 1", got)
	}
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
