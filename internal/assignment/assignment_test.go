package assignment

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

var fixedNow = time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "homonto.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewStore(context.Background(), db, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func mustWorkID(t *testing.T) identity.WorkID {
	t.Helper()
	id, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	return id
}

func mustRepoID(t *testing.T) identity.RepositoryID {
	t.Helper()
	id, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("NewRepositoryID: %v", err)
	}
	return id
}

// assignmentTemplate is a valid writable assignment for a role.
func assignmentTemplate(t *testing.T, role protocol.Role, reason string) protocol.Action {
	t.Helper()
	return protocol.Action{
		Kind:             protocol.KindAssignment,
		Workflow:         workspacecfg.WorkflowTask,
		Path:             "task",
		Phase:            "plan",
		Reason:           reason,
		Role:             role,
		Prompt:           "do the thing",
		Repository:       protocol.RepositoryRef{ID: mustRepoID(t), Path: "repo"},
		WorkingDirectory: ".",
		WriteScope:       protocol.WriteScope{Paths: []string{"src"}},
		InputFingerprints: []fingerprint.Digest{
			fingerprint.Bytes("test-input", []byte(reason)),
		},
		ExpectedReport: &protocol.ExpectedReport{Kind: role, SchemaVersion: protocol.CurrentVersion},
	}
}

// decisionTemplate is a valid blocking decision action.
func decisionTemplate(t *testing.T, reason string) protocol.Action {
	t.Helper()
	return protocol.Action{
		Kind:             protocol.KindDecision,
		Workflow:         workspacecfg.WorkflowTask,
		Path:             "task",
		Phase:            "plan",
		Reason:           reason,
		Prompt:           "approve the scope?",
		Repository:       protocol.RepositoryRef{ID: mustRepoID(t), Path: "repo"},
		WorkingDirectory: ".",
		WriteScope:       protocol.WriteScope{ReadOnly: true},
		InputFingerprints: []fingerprint.Digest{
			fingerprint.Bytes("test-input", []byte(reason)),
		},
		Decision: &protocol.DecisionSchema{
			Kind:   protocol.DecisionKind(decision.KindApproveScope),
			Prompt: "Approve the scope?",
			Choices: []protocol.Choice{
				{Value: "approve", Label: "Approve"},
				{Value: "reject", Label: "Reject", RequiresRationale: true},
			},
		},
	}
}

// report builds a valid explorer/reviewer/skeptic/implementer payload.
func reportPayload(t *testing.T, role protocol.Role) json.RawMessage {
	t.Helper()
	var v any
	switch role {
	case protocol.RoleExplorer:
		v = protocol.ExplorerReport{
			Facts:    []string{"login uses the session store"},
			Surfaces: []string{"internal/session"},
		}
	case protocol.RoleReviewer:
		v = protocol.ReviewerReport{Acceptance: []string{"tests cover the fix"}}
	case protocol.RoleSkeptic:
		v = protocol.SkepticReport{Assumptions: []string{"the store is the only writer"}}
	case protocol.RoleImplementer:
		v = protocol.ImplementerReport{
			Material: protocol.Material{
				Kind:          protocol.MaterialSnapshotPatch,
				PatchManifest: []string{"src/a.go"},
				Content:       fingerprint.Bytes("test-material", []byte("patch")),
			},
			ChangedPaths: []string{"src/a.go"},
		}
	default:
		t.Fatalf("no canned report for role %q", role)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	return b
}

func session(t *testing.T) protocol.Session {
	t.Helper()
	id, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	return protocol.Session{
		HostID:     identity.SessionID(id),
		Hostname:   "test-host",
		PID:        4242,
		Executable: "/usr/bin/claude",
		StartedAt:  fixedNow,
	}
}

func submission(t *testing.T, s *Store, act protocol.Action) protocol.ReportSubmission {
	t.Helper()
	return protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        act.ID,
		FreshnessToken:  s.Token(act.ID),
		Role:            act.Role,
		Session:         session(t),
		Report:          reportPayload(t, act.Role),
	}
}

func TestCreateValidatesTheWireSpec(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)

	if _, err := s.Create(t.Context(), Spec{WorkID: "not-a-uuid", Generation: 1,
		Template: assignmentTemplate(t, protocol.RoleExplorer, "explore")}); err == nil {
		t.Fatal("Create with a malformed work id = nil error, want rejection")
	}
	if _, err := s.Create(t.Context(), Spec{WorkID: workID, Generation: 0,
		Template: assignmentTemplate(t, protocol.RoleExplorer, "explore")}); err == nil {
		t.Fatal("Create with generation 0 = nil error, want rejection")
	}
	// A template that pre-fills store-owned fields is refused, so an issued
	// action can never carry a caller-chosen id or token.
	preFilled := assignmentTemplate(t, protocol.RoleExplorer, "explore")
	preFilled.ID = identity.ActionID("6ba7b810-9dad-41d4-80b4-00c04fd430c8")
	if _, err := s.Create(t.Context(), Spec{WorkID: workID, Generation: 1, Template: preFilled}); err == nil {
		t.Fatal("Create with a pre-filled id = nil error, want rejection")
	}
	// An invalid action never reaches the database.
	bad := assignmentTemplate(t, protocol.RoleExplorer, "explore")
	bad.Prompt = "  "
	if _, err := s.Create(t.Context(), Spec{WorkID: workID, Generation: 1, Template: bad}); err == nil {
		t.Fatal("Create with a blank prompt = nil error, want rejection")
	}
	if got, err := s.Actions(t.Context(), workID); err != nil || len(got) != 0 {
		t.Fatalf("refused Creates left %d action(s) behind (err=%v)", len(got), err)
	}
}

func TestReadyGroupIsMaximal(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	for _, role := range []protocol.Role{protocol.RoleExplorer, protocol.RoleSkeptic, protocol.RoleReviewer} {
		if _, err := s.Create(t.Context(), Spec{
			WorkID: workID, Generation: 1, Template: assignmentTemplate(t, role, string(role)),
		}); err != nil {
			t.Fatalf("Create(%s): %v", role, err)
		}
	}
	group, ok, err := s.ReadyGroup(t.Context(), workID)
	if err != nil || !ok {
		t.Fatalf("ReadyGroup = ok %v, err %v", ok, err)
	}
	if len(group.Actions) != 3 {
		t.Fatalf("ready group carries %d actions, want all 3", len(group.Actions))
	}
	if group.State != protocol.NextReady {
		t.Fatalf("group.State = %q, want ready", group.State)
	}
	if group.ID == "" {
		t.Fatal("ready group carries no group id")
	}
	for _, act := range group.Actions {
		if act.ParallelGroupID != string(group.ID) {
			t.Fatalf("action %s carries group %q, want %q", act.ID, act.ParallelGroupID, group.ID)
		}
		if err := act.Validate(); err != nil {
			t.Fatalf("issued action %s is not a valid wire action: %v", act.ID, err)
		}
	}
	if err := group.Response().Validate(); err != nil {
		t.Fatalf("group response: %v", err)
	}
}

func TestReadyGroupIsIdempotentWhileOutstanding(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, _, err := s.ReadyGroup(t.Context(), workID)
	if err != nil {
		t.Fatalf("first ReadyGroup: %v", err)
	}
	second, _, err := s.ReadyGroup(t.Context(), workID)
	if err != nil {
		t.Fatalf("second ReadyGroup: %v", err)
	}
	if first.ID != second.ID || len(second.Actions) != 1 || first.Actions[0].ID != second.Actions[0].ID {
		t.Fatalf("re-asking re-issued the group: %+v vs %+v", first, second)
	}
	if first.Actions[0].FreshnessToken != second.Actions[0].FreshnessToken {
		t.Fatal("freshness token changed between identical next calls")
	}
}

func TestReadyGroupDefersDependentActions(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	explore, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	})
	if err != nil {
		t.Fatalf("Create(explorer): %v", err)
	}
	review, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Dependencies: []identity.ActionID{explore.ID},
		Template: assignmentTemplate(t, protocol.RoleReviewer, "review"),
	})
	if err != nil {
		t.Fatalf("Create(reviewer): %v", err)
	}

	group, ok, err := s.ReadyGroup(t.Context(), workID)
	if err != nil || !ok {
		t.Fatalf("ReadyGroup = ok %v, err %v", ok, err)
	}
	if len(group.Actions) != 1 || group.Actions[0].ID != explore.ID {
		t.Fatalf("first group = %+v, want only the explorer", group.Actions)
	}

	if _, err := s.Submit(t.Context(), submission(t, s, group.Actions[0])); err != nil {
		t.Fatalf("Submit(explorer): %v", err)
	}
	group, ok, err = s.ReadyGroup(t.Context(), workID)
	if err != nil || !ok {
		t.Fatalf("second ReadyGroup = ok %v, err %v", ok, err)
	}
	if len(group.Actions) != 1 || group.Actions[0].ID != review.ID {
		t.Fatalf("second group = %+v, want the reviewer", group.Actions)
	}
	if group.Actions[0].Dependencies[0] != explore.ID {
		t.Fatalf("reviewer dependencies = %v, want the explorer", group.Actions[0].Dependencies)
	}

	if _, err := s.Submit(t.Context(), submission(t, s, group.Actions[0])); err != nil {
		t.Fatalf("Submit(reviewer): %v", err)
	}
	if _, ok, err := s.ReadyGroup(t.Context(), workID); err != nil || ok {
		t.Fatalf("ReadyGroup after everything answered = ok %v, err %v, want complete", ok, err)
	}
	if err := CompleteResponse().Validate(); err != nil {
		t.Fatalf("CompleteResponse: %v", err)
	}
}

func TestReadyGroupReleasesADecisionAlone(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	}); err != nil {
		t.Fatalf("Create(explorer): %v", err)
	}
	gate, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: decisionTemplate(t, "approve scope"),
	})
	if err != nil {
		t.Fatalf("Create(decision): %v", err)
	}

	group, ok, err := s.ReadyGroup(t.Context(), workID)
	if err != nil || !ok {
		t.Fatalf("ReadyGroup = ok %v, err %v", ok, err)
	}
	if len(group.Actions) != 1 || group.Actions[0].ID != gate.ID {
		t.Fatalf("group = %+v, want only the decision", group.Actions)
	}
	if group.State != protocol.NextBlocked {
		t.Fatalf("group.State = %q, want blocked", group.State)
	}
	if err := group.Response().Validate(); err != nil {
		t.Fatalf("blocked response: %v", err)
	}
}

func TestReadyGroupReportsUnsatisfiableDependencies(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	explore, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Dependencies: []identity.ActionID{explore.ID},
		Template: assignmentTemplate(t, protocol.RoleReviewer, "review"),
	}); err != nil {
		t.Fatalf("Create(reviewer): %v", err)
	}
	// Invalidating the dependency makes the dependent unreachable; that is
	// an error, never a silent "complete".
	if err := s.Invalidate(t.Context(), explore.ID); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, _, err := s.ReadyGroup(t.Context(), workID); !errors.Is(err, ErrUnsatisfiable) {
		t.Fatalf("ReadyGroup error = %v, want ErrUnsatisfiable", err)
	}
}

func TestSubmitRefusesDuplicateStaleAndWrongRole(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	group, _, err := s.ReadyGroup(t.Context(), workID)
	if err != nil {
		t.Fatalf("ReadyGroup: %v", err)
	}
	act := group.Actions[0]

	t.Run("wrong role", func(t *testing.T) {
		sub := submission(t, s, act)
		sub.Role = protocol.RoleReviewer
		sub.Report = reportPayload(t, protocol.RoleReviewer)
		if _, err := s.Submit(t.Context(), sub); !errors.Is(err, ErrWrongRole) {
			t.Fatalf("Submit error = %v, want ErrWrongRole", err)
		}
	})

	t.Run("stale token", func(t *testing.T) {
		other, err := identity.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		sub := submission(t, s, act)
		sub.FreshnessToken = other
		if _, err := s.Submit(t.Context(), sub); !errors.Is(err, ErrStaleToken) {
			t.Fatalf("Submit error = %v, want ErrStaleToken", err)
		}
	})

	t.Run("unknown action", func(t *testing.T) {
		id, err := identity.NewActionID()
		if err != nil {
			t.Fatalf("NewActionID: %v", err)
		}
		sub := submission(t, s, act)
		sub.ActionID = id
		sub.FreshnessToken = s.Token(id)
		if _, err := s.Submit(t.Context(), sub); !errors.Is(err, ErrUnknownAction) {
			t.Fatalf("Submit error = %v, want ErrUnknownAction", err)
		}
	})

	t.Run("accepted once", func(t *testing.T) {
		if _, err := s.Submit(t.Context(), submission(t, s, act)); err != nil {
			t.Fatalf("Submit: %v", err)
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		if _, err := s.Submit(t.Context(), submission(t, s, act)); !errors.Is(err, ErrDuplicateSubmission) {
			t.Fatalf("second Submit error = %v, want ErrDuplicateSubmission", err)
		}
	})
}

func TestSubmitRefusesUnissuedAndInvalidatedActions(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	act, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Pending, never issued: answering it is not something a host could
	// honestly do, because it never saw the action.
	sub := submission(t, s, act.Spec)
	if _, err := s.Submit(t.Context(), sub); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Submit(pending) error = %v, want ErrNotIssued", err)
	}
	if _, _, err := s.ReadyGroup(t.Context(), workID); err != nil {
		t.Fatalf("ReadyGroup: %v", err)
	}
	if err := s.Invalidate(t.Context(), act.ID); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := s.Submit(t.Context(), sub); !errors.Is(err, ErrNotIssued) {
		t.Fatalf("Submit(invalidated) error = %v, want ErrNotIssued", err)
	}
}

func TestSubmitRefusesAMalformedReportPayload(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	group, _, err := s.ReadyGroup(t.Context(), workID)
	if err != nil {
		t.Fatalf("ReadyGroup: %v", err)
	}
	sub := submission(t, s, group.Actions[0])
	sub.Report = json.RawMessage(`{"facts":["x"],"unexpected":true}`)
	if _, err := s.Submit(t.Context(), sub); err == nil {
		t.Fatal("Submit with an unknown report field = nil error, want rejection")
	}
	// The refusal left the action answerable.
	if _, err := s.Submit(t.Context(), submission(t, s, group.Actions[0])); err != nil {
		t.Fatalf("Submit after a refused payload: %v", err)
	}
}

func TestSubmitAndDecideRefuseEachOthersActions(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: decisionTemplate(t, "approve scope"),
	}); err != nil {
		t.Fatalf("Create(decision): %v", err)
	}
	group, _, err := s.ReadyGroup(t.Context(), workID)
	if err != nil {
		t.Fatalf("ReadyGroup: %v", err)
	}
	gate := group.Actions[0]

	report := protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        gate.ID,
		FreshnessToken:  s.Token(gate.ID),
		Role:            protocol.RoleExplorer,
		Session:         session(t),
		Report:          reportPayload(t, protocol.RoleExplorer),
	}
	if _, err := s.Submit(t.Context(), report); !errors.Is(err, ErrKindMismatch) {
		t.Fatalf("Submit(decision) error = %v, want ErrKindMismatch", err)
	}
}

func TestDecideRefusesSilenceAndAcceptsAChoice(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: decisionTemplate(t, "approve scope"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	group, _, err := s.ReadyGroup(t.Context(), workID)
	if err != nil {
		t.Fatalf("ReadyGroup: %v", err)
	}
	gate := group.Actions[0]
	base := decision.Submission{ActionID: gate.ID, FreshnessToken: s.Token(gate.ID)}

	silent := base
	if _, err := s.Decide(t.Context(), silent); !errors.Is(err, decision.ErrEmptyChoice) {
		t.Fatalf("Decide(silence) error = %v, want ErrEmptyChoice", err)
	}
	unknown := base
	unknown.Choice = "maybe"
	if _, err := s.Decide(t.Context(), unknown); !errors.Is(err, decision.ErrUnknownChoice) {
		t.Fatalf("Decide(unknown choice) error = %v, want ErrUnknownChoice", err)
	}
	noRationale := base
	noRationale.Choice = "reject"
	if _, err := s.Decide(t.Context(), noRationale); !errors.Is(err, decision.ErrMissingRationale) {
		t.Fatalf("Decide(no rationale) error = %v, want ErrMissingRationale", err)
	}

	accepted := base
	accepted.Choice = "approve"
	receipt, err := s.Decide(t.Context(), accepted)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if receipt.ActionID != gate.ID || receipt.Kind != protocol.KindDecision {
		t.Fatalf("receipt = %+v", receipt)
	}
	if _, err := s.Decide(t.Context(), accepted); !errors.Is(err, ErrDuplicateSubmission) {
		t.Fatalf("second Decide error = %v, want ErrDuplicateSubmission", err)
	}
	if _, ok, err := s.ReadyGroup(t.Context(), workID); err != nil || ok {
		t.Fatalf("ReadyGroup after the decision = ok %v, err %v, want complete", ok, err)
	}
}

func TestReceiptRecordsTheInputGeneration(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 7, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	group, _, err := s.ReadyGroup(t.Context(), workID)
	if err != nil {
		t.Fatalf("ReadyGroup: %v", err)
	}
	receipt, err := s.Submit(t.Context(), submission(t, s, group.Actions[0]))
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if receipt.Generation != 7 {
		t.Fatalf("receipt.Generation = %d, want 7", receipt.Generation)
	}
	if receipt.Role != protocol.RoleExplorer || receipt.WorkID != workID {
		t.Fatalf("receipt = %+v", receipt)
	}
}

// TestTokensAreDerivedNotStored proves the freshness contract: a token is
// reproducible from the runtime key and the action id, and a store built
// on a different key rejects it.
func TestTokensAreDerivedNotStored(t *testing.T) {
	s := newStore(t)
	id, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("NewActionID: %v", err)
	}
	if s.Token(id) != s.Token(id) {
		t.Fatal("Token is not deterministic")
	}
	if err := identity.ValidateToken(string(s.Token(id))); err != nil {
		t.Fatalf("derived token is not a canonical token: %v", err)
	}
	other := newStore(t)
	if other.Token(id) == s.Token(id) {
		t.Fatal("two runtimes derived the same token; the key is not per-runtime")
	}
}

// TestActionsAreScopedToTheirWork proves one work's ready group never
// includes another work's actions.
func TestActionsAreScopedToTheirWork(t *testing.T) {
	s := newStore(t)
	a := mustWorkID(t)
	b := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: a, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "a"),
	}); err != nil {
		t.Fatalf("Create(a): %v", err)
	}
	if _, err := s.Create(t.Context(), Spec{
		WorkID: b, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "b"),
	}); err != nil {
		t.Fatalf("Create(b): %v", err)
	}
	group, _, err := s.ReadyGroup(t.Context(), a)
	if err != nil {
		t.Fatalf("ReadyGroup(a): %v", err)
	}
	if len(group.Actions) != 1 || group.Actions[0].Reason != "a" {
		t.Fatalf("group for work a = %+v", group.Actions)
	}
	// b's action is untouched: still pending, not swept into a's group.
	acts, err := s.Actions(t.Context(), b)
	if err != nil {
		t.Fatalf("Actions(b): %v", err)
	}
	if len(acts) != 1 || acts[0].State != StatePending {
		t.Fatalf("work b actions = %+v, want one pending", acts)
	}
}

func TestActionRoundTripsThroughTheDatabase(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	created, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 3, Template: assignmentTemplate(t, protocol.RoleImplementer, "implement"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := s.Action(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("Action: %v", err)
	}
	if got.ID != created.ID || got.WorkID != workID || got.Role != protocol.RoleImplementer ||
		got.Generation != 3 || got.State != StatePending {
		t.Fatalf("round trip = %+v, want %+v", got, created)
	}
	if got.Spec.Prompt != created.Spec.Prompt || got.Spec.FreshnessToken != created.Spec.FreshnessToken {
		t.Fatalf("spec did not round trip: %+v", got.Spec)
	}
	if !got.CreatedAt.Equal(fixedNow) {
		t.Fatalf("CreatedAt = %v, want the injected clock", got.CreatedAt)
	}
}

func TestInvalidateLeavesAnsweredActionsAlone(t *testing.T) {
	s := newStore(t)
	workID := mustWorkID(t)
	if _, err := s.Create(t.Context(), Spec{
		WorkID: workID, Generation: 1, Template: assignmentTemplate(t, protocol.RoleExplorer, "explore"),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	group, _, err := s.ReadyGroup(t.Context(), workID)
	if err != nil {
		t.Fatalf("ReadyGroup: %v", err)
	}
	act := group.Actions[0]
	if _, err := s.Submit(t.Context(), submission(t, s, act)); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if err := s.Invalidate(t.Context(), act.ID); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	got, err := s.Action(t.Context(), act.ID)
	if err != nil {
		t.Fatalf("Action: %v", err)
	}
	if got.State != StateSubmitted {
		t.Fatalf("answered action became %q; a durable report must not be erased", got.State)
	}
	// Invalidate is idempotent on an already-invalidated action.
	if err := s.Invalidate(t.Context()); err != nil {
		t.Fatalf("Invalidate(none): %v", err)
	}
}

func TestSplitRepairActionsKeepsAssignmentsOrderedAndUsesLastDecision(t *testing.T) {
	actions := []Action{
		{Kind: protocol.KindAssignment, Step: "first"},
		{Kind: protocol.KindDecision, Step: "first-decision"},
		{Kind: protocol.KindAssignment, Step: "second"},
		{Kind: protocol.KindDecision, Step: "last-decision"},
	}

	gate, rest := SplitRepairActions(actions)

	if gate != &actions[3] {
		t.Fatalf("gate = %p, want the last decision %p", gate, &actions[3])
	}
	if len(rest) != 2 {
		t.Fatalf("rest has %d actions, want 2", len(rest))
	}
	if rest[0].Step != "first" || rest[1].Step != "second" {
		t.Fatalf("rest steps = %q, %q; want first, second", rest[0].Step, rest[1].Step)
	}

	noDecision := []Action{
		{Kind: protocol.KindAssignment, Step: "third"},
		{Kind: protocol.KindAssignment, Step: "fourth"},
	}
	gate, rest = SplitRepairActions(noDecision)
	if gate != nil {
		t.Fatalf("no-decision gate = %p, want nil", gate)
	}
	if len(rest) != 2 {
		t.Fatalf("no-decision rest has %d actions, want 2", len(rest))
	}
	if rest[0].Step != "third" || rest[1].Step != "fourth" {
		t.Fatalf("no-decision rest steps = %q, %q; want third, fourth", rest[0].Step, rest[1].Step)
	}
}
