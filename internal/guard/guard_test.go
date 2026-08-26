package guard

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/assignment"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

var fixedNow = time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

// env is one guard fixture: a real assignment store and grant ledger over
// a migrated database, plus the control root the documents live in.
type env struct {
	guard       *Guard
	assignments *assignment.Store
	artifacts   *artifact.Service
	grants      *artifact.StoreJournal
	root        string
	workID      identity.WorkID
	repoID      identity.RepositoryID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(root, "homonto.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clock := func() time.Time { return fixedNow }
	assignments, err := assignment.NewStore(context.Background(), db, clock)
	if err != nil {
		t.Fatalf("assignment.NewStore: %v", err)
	}
	grants, err := artifact.NewStoreJournal(db)
	if err != nil {
		t.Fatalf("artifact.NewStoreJournal: %v", err)
	}
	artifacts, err := artifact.NewService(root, grants, clock)
	if err != nil {
		t.Fatalf("artifact.NewService: %v", err)
	}
	g, err := New(assignments, grants)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewWorkID: %v", err)
	}
	repoID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("NewRepositoryID: %v", err)
	}
	return &env{
		guard: g, assignments: assignments, artifacts: artifacts, grants: grants,
		root: root, workID: workID, repoID: repoID,
	}
}

func mustSessionID(t *testing.T) identity.SessionID {
	t.Helper()
	id, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("NewSessionID: %v", err)
	}
	return identity.SessionID(id)
}

// template builds an assignment spec for a role with an isolation root and
// a write scope.
func (e *env) template(role protocol.Role, root string, scope []string, phase string) protocol.Action {
	return protocol.Action{
		Kind:             protocol.KindAssignment,
		Workflow:         workspacecfg.WorkflowTask,
		Path:             "task",
		Phase:            phase,
		Reason:           "work",
		Role:             role,
		Prompt:           "do the thing",
		Repository:       protocol.RepositoryRef{ID: e.repoID, Path: "repo"},
		WorkingDirectory: root,
		WriteScope: protocol.WriteScope{
			ReadOnly: len(scope) == 0,
			Paths:    scope,
		},
		InputFingerprints: []fingerprint.Digest{fingerprint.Bytes("test", []byte(role))},
		ExpectedReport:    &protocol.ExpectedReport{Kind: role, SchemaVersion: protocol.CurrentVersion},
	}
}

// issue creates and releases an assignment, returning its wire form.
func (e *env) issue(t *testing.T, tmpl protocol.Action) protocol.Action {
	t.Helper()
	if _, err := e.assignments.Create(t.Context(), assignment.Spec{
		WorkID: e.workID, Generation: 1, Template: tmpl,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	group, ok, err := e.assignments.ReadyGroup(t.Context(), e.workID)
	if err != nil || !ok {
		t.Fatalf("ReadyGroup = ok %v, err %v", ok, err)
	}
	return group.Actions[len(group.Actions)-1]
}

// request builds a write request for an assignment.
func (e *env) request(t *testing.T, act protocol.Action, workingDir string, paths ...string) Request {
	t.Helper()
	return Request{
		Wire: protocol.GuardRequest{
			Host:             protocol.HostClaude,
			SessionID:        mustSessionID(t),
			Tool:             "Write",
			WorkingDirectory: workingDir,
			WritePaths:       paths,
		},
		ActionID: act.ID,
		Token:    act.FreshnessToken,
	}
}

func decide(t *testing.T, g *Guard, req Request) protocol.GuardDecision {
	t.Helper()
	d, err := g.Authorize(t.Context(), req)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("decision does not explain itself: %v", err)
	}
	return d
}

func TestNewRequiresBothStores(t *testing.T) {
	e := newEnv(t)
	if _, err := New(nil, e.grants); err == nil {
		t.Error("New(nil assignments) = nil error, want rejection")
	}
	if _, err := New(e.assignments, nil); err == nil {
		t.Error("New(nil grants) = nil error, want rejection")
	}
}

func TestAuthorizeAllowsAWriteInsideScope(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))
	d := decide(t, e.guard, e.request(t, act, "work/impl-1", "src/login.go"))
	if !d.Allow {
		t.Fatalf("write inside scope refused: %s (%s)", d.Reason, d.Code)
	}
}

func TestAuthorizeAllowsReadsWithoutAnyPermission(t *testing.T) {
	e := newEnv(t)
	req := Request{Wire: protocol.GuardRequest{
		Host: protocol.HostOpenCode, SessionID: mustSessionID(t),
		Tool: "Read", WorkingDirectory: ".",
	}}
	d := decide(t, e.guard, req)
	if !d.Allow {
		t.Fatalf("a read with no write paths was refused: %s", d.Reason)
	}
}

func TestAuthorizeRefusesReadOnlyRoleWrites(t *testing.T) {
	for _, role := range []protocol.Role{protocol.RoleExplorer, protocol.RoleReviewer, protocol.RoleSkeptic} {
		t.Run(string(role), func(t *testing.T) {
			e := newEnv(t)
			act := e.issue(t, e.template(role, "work/read-1", nil, "plan"))
			d := decide(t, e.guard, e.request(t, act, "work/read-1", "src/login.go"))
			if d.Allow {
				t.Fatalf("a %s assignment was allowed to write", role)
			}
			if d.Code != CodeReadOnlyRole {
				t.Fatalf("code = %q, want %q (%s)", d.Code, CodeReadOnlyRole, d.Reason)
			}
		})
	}
}

func TestAuthorizeRefusesWritesOutsideTheIsolationArea(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))
	// The host's working directory is the sibling isolation area of
	// another implementer running in parallel.
	d := decide(t, e.guard, e.request(t, act, "work/impl-2", "src/login.go"))
	if d.Allow {
		t.Fatal("a write into another implementer's isolation area was allowed")
	}
	if d.Code != CodeOutsideIsolation {
		t.Fatalf("code = %q, want %q (%s)", d.Code, CodeOutsideIsolation, d.Reason)
	}
}

func TestAuthorizeRefusesWritesOutsideTheDeclaredScope(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))
	for _, p := range []string{"docs/readme.md", "srcs/other.go", "vendor/x.go"} {
		d := decide(t, e.guard, e.request(t, act, "work/impl-1", p))
		if d.Allow {
			t.Errorf("write to %q outside the scope was allowed", p)
			continue
		}
		if d.Code != CodeOutsideScope {
			t.Errorf("write to %q: code = %q, want %q (%s)", p, d.Code, CodeOutsideScope, d.Reason)
		}
	}
	// One bad path in a batch refuses the whole operation.
	d := decide(t, e.guard, e.request(t, act, "work/impl-1", "src/ok.go", "docs/bad.md"))
	if d.Allow {
		t.Fatal("a batch containing an out-of-scope path was allowed")
	}
}

func TestAuthorizeRefusesPathTraversal(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))
	for _, p := range []string{"../../etc/passwd", "src/../../escape.go", "/etc/passwd", `src\win.go`} {
		d := decide(t, e.guard, e.request(t, act, "work/impl-1", p))
		if d.Allow {
			t.Errorf("traversal path %q was allowed", p)
		}
	}
}

func TestAuthorizeRefusesControlStateEdits(t *testing.T) {
	e := newEnv(t)
	// The scope deliberately includes the control directory: even an
	// explicitly scoped assignment does not get to write the runtime
	// database or the checkpoint.
	act := e.issue(t, e.template(protocol.RoleImplementer, ".", []string{".homonto", "src"}, "do"))
	for _, p := range []string{
		".homonto/runtime.db",
		".homonto/runtime.db-wal",
		".homonto/checkpoint.json",
		".homonto/config.toml",
	} {
		d := decide(t, e.guard, e.request(t, act, ".", p))
		if d.Allow {
			t.Errorf("write to control state %q was allowed", p)
			continue
		}
		if d.Code != CodeProtectedPath {
			t.Errorf("write to %q: code = %q, want %q (%s)", p, d.Code, CodeProtectedPath, d.Reason)
		}
	}
}

func TestAuthorizeRefusesWorkflowDocumentsFromAssignments(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, ".", []string{"docs", "src"}, "build"))
	tests := []struct {
		path string
		code string
	}{
		// Binary-owned in Build: Homonto updates the checkboxes.
		{"docs/homonto/changes/rework/tasks.md", CodeBinaryOwned},
		// Host-owned in Build: written through an edit grant, not here.
		{"docs/homonto/changes/rework/plan.md", CodeWrongPhase},
		// Nobody writes a proposal in Build.
		{"docs/homonto/changes/rework/proposal.md", CodeWrongPhase},
		// Generated documents belong to other phases entirely.
		{"docs/homonto/changes/rework/verification.md", CodeWrongPhase},
		{"docs/homonto/changes/rework/record.md", CodeWrongPhase},
	}
	for _, tt := range tests {
		d := decide(t, e.guard, e.request(t, act, ".", tt.path))
		if d.Allow {
			t.Errorf("write to %q was allowed", tt.path)
			continue
		}
		if d.Code != tt.code {
			t.Errorf("write to %q: code = %q, want %q (%s)", tt.path, d.Code, tt.code, d.Reason)
		}
	}
	// A file that merely shares a name with a document, outside the active
	// tree, is ordinary source.
	d := decide(t, e.guard, e.request(t, act, ".", "src/proposal.md"))
	if !d.Allow {
		t.Fatalf("a source file named proposal.md was refused: %s (%s)", d.Reason, d.Code)
	}
}

func TestAuthorizeFailsClosedOnBadPermission(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))

	t.Run("no permission claimed", func(t *testing.T) {
		req := e.request(t, act, "work/impl-1", "src/login.go")
		req.ActionID = ""
		req.Token = ""
		d := decide(t, e.guard, req)
		if d.Allow || d.Code != CodeNoPermission {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeNoPermission)
		}
	})

	t.Run("unknown assignment", func(t *testing.T) {
		id, err := identity.NewActionID()
		if err != nil {
			t.Fatalf("NewActionID: %v", err)
		}
		req := e.request(t, act, "work/impl-1", "src/login.go")
		req.ActionID = id
		req.Token = e.assignments.Token(id)
		d := decide(t, e.guard, req)
		if d.Allow || d.Code != CodeUnknownAssignment {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeUnknownAssignment)
		}
	})

	t.Run("stale token", func(t *testing.T) {
		other, err := identity.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		req := e.request(t, act, "work/impl-1", "src/login.go")
		req.Token = other
		d := decide(t, e.guard, req)
		if d.Allow || d.Code != CodeStaleToken {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeStaleToken)
		}
	})

	t.Run("invalidated assignment", func(t *testing.T) {
		if err := e.assignments.Invalidate(t.Context(), act.ID); err != nil {
			t.Fatalf("Invalidate: %v", err)
		}
		d := decide(t, e.guard, e.request(t, act, "work/impl-1", "src/login.go"))
		if d.Allow || d.Code != CodeStaleAssignment {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeStaleAssignment)
		}
	})

	t.Run("malformed request", func(t *testing.T) {
		req := e.request(t, act, "work/impl-1", "src/login.go")
		req.Wire.Host = "notepad"
		d := decide(t, e.guard, req)
		if d.Allow || d.Code != CodeMalformed {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeMalformed)
		}
	})

	t.Run("both permissions claimed", func(t *testing.T) {
		id, err := identity.NewActionID()
		if err != nil {
			t.Fatalf("NewActionID: %v", err)
		}
		req := e.request(t, act, "work/impl-1", "src/login.go")
		req.GrantID = id
		req.GrantToken = e.assignments.Token(id)
		d := decide(t, e.guard, req)
		if d.Allow || d.Code != CodeMalformed {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeMalformed)
		}
	})
}
