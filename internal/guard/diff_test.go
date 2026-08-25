package guard

import (
	"errors"
	"testing"

	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// changed builds a modified-change list from paths.
func changed(paths ...string) []Change {
	out := make([]Change, len(paths))
	for i, p := range paths {
		out[i] = Change{Path: p, Kind: ChangeModified}
	}
	return out
}

func TestValidateAssignmentResultAcceptsAnInScopeDiff(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src", "docs"}, "do"))
	err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{
		Root: "work/impl-1",
		Changes: []Change{
			{Path: "src/login.go", Kind: ChangeModified},
			{Path: "src/login_test.go", Kind: ChangeAdded},
			{Path: "docs/old.md", Kind: ChangeDeleted},
		},
	})
	if err != nil {
		t.Fatalf("ValidateAssignmentResult: %v", err)
	}
}

// TestValidateAssignmentResultCatchesTheHookBypass is the point of the
// whole second gate: a change the write hook never saw — because a shell
// command made it — still fails the final diff.
func TestValidateAssignmentResultCatchesTheHookBypass(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))
	err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{
		Root: "work/impl-1",
		Changes: []Change{
			{Path: "src/login.go", Kind: ChangeModified},
			// Never presented to the guard; written by `sed -i` in a shell.
			{Path: "internal/secret/keys.go", Kind: ChangeModified},
		},
	})
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("error = %v, want ErrOutOfScope", err)
	}
}

func TestValidateAssignmentResultRefusesReadOnlyChanges(t *testing.T) {
	e := newEnv(t)
	for _, role := range []protocol.Role{protocol.RoleExplorer, protocol.RoleReviewer, protocol.RoleSkeptic} {
		act := e.issue(t, e.template(role, "work/read-1", nil, "plan"))
		err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{
			Root:    "work/read-1",
			Changes: changed("notes.md"),
		})
		if !errors.Is(err, ErrReadOnlyResult) {
			t.Errorf("%s: error = %v, want ErrReadOnlyResult", role, err)
		}
		// A read-only assignment that changed nothing is fine.
		if err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{Root: "work/read-1"}); err != nil {
			t.Errorf("%s: a clean read-only result was refused: %v", role, err)
		}
	}
}

func TestValidateAssignmentResultRefusesTheWrongIsolationArea(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))
	err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{
		Root:    "work/impl-2",
		Changes: changed("src/login.go"),
	})
	if !errors.Is(err, ErrWrongIsolation) {
		t.Fatalf("error = %v, want ErrWrongIsolation", err)
	}
}

func TestValidateAssignmentResultRefusesCheckpointEdits(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, ".", []string{".homonto", "src"}, "do"))
	for _, p := range []string{".homonto/checkpoint.json", ".homonto/runtime.db"} {
		err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{
			Root:    ".",
			Changes: changed(p),
		})
		if !errors.Is(err, ErrProtectedChanged) {
			t.Errorf("%s: error = %v, want ErrProtectedChanged", p, err)
		}
	}
}

func TestValidateAssignmentResultRefusesDocumentEdits(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, ".", []string{"docs", "src"}, "build"))
	err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{
		Root:    ".",
		Changes: changed("docs/homonto/changes/rework/tasks.md"),
	})
	if !errors.Is(err, ErrDocumentChanged) {
		t.Fatalf("error = %v, want ErrDocumentChanged", err)
	}
}

// TestValidateAssignmentResultAcceptsGeneratedChanges proves the diff does
// not punish an assignment for what Homonto itself wrote during it: the
// checkbox update the binary made lands in the same diff, in a path the
// assignment could never have written.
func TestValidateAssignmentResultAcceptsGeneratedChanges(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, ".", []string{"src"}, "build"))
	err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{
		Root: ".",
		Changes: []Change{
			{Path: "src/login.go", Kind: ChangeModified},
			{Path: "docs/homonto/changes/rework/tasks.md", Kind: ChangeModified},
			{Path: ".homonto/checkpoint.json", Kind: ChangeModified},
		},
		Generated: []string{"docs/homonto/changes/rework/tasks.md", ".homonto/checkpoint.json"},
	})
	if err != nil {
		t.Fatalf("ValidateAssignmentResult: %v", err)
	}
	// The same paths WITHOUT the generated declaration are refused, so
	// the acceptance is the declaration's doing and not a hole.
	err = e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{
		Root: ".",
		Changes: []Change{
			{Path: "src/login.go", Kind: ChangeModified},
			{Path: "docs/homonto/changes/rework/tasks.md", Kind: ChangeModified},
		},
	})
	if !errors.Is(err, ErrDocumentChanged) {
		t.Fatalf("error = %v, want ErrDocumentChanged", err)
	}
}

func TestValidateAssignmentResultRefusesMalformedDiffs(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))
	tests := []struct {
		name string
		diff ResultDiff
	}{
		{"unclean root", ResultDiff{Root: "../elsewhere", Changes: changed("src/a.go")}},
		{"absolute path", ResultDiff{Root: "work/impl-1", Changes: changed("/etc/passwd")}},
		{"traversal path", ResultDiff{Root: "work/impl-1", Changes: changed("src/../../a.go")}},
		{"unknown change kind", ResultDiff{Root: "work/impl-1", Changes: []Change{{Path: "src/a.go", Kind: "renamed"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.guard.ValidateAssignmentResult(t.Context(), act, tt.diff)
			if err == nil {
				t.Fatal("ValidateAssignmentResult = nil error, want refusal")
			}
			if !errors.Is(err, ErrMalformedDiff) && !errors.Is(err, ErrWrongIsolation) {
				t.Fatalf("error = %v, want a malformed or isolation refusal", err)
			}
		})
	}
}

func TestValidateAssignmentResultRefusesADecisionAction(t *testing.T) {
	e := newEnv(t)
	act := e.issue(t, e.template(protocol.RoleImplementer, "work/impl-1", []string{"src"}, "do"))
	act.Kind = protocol.KindDecision
	act.Role = ""
	act.ExpectedReport = nil
	act.Decision = &protocol.DecisionSchema{
		Kind: "approve_scope", Prompt: "ok?",
		Choices: []protocol.Choice{{Value: "yes", Label: "Yes"}},
	}
	act.WriteScope = protocol.WriteScope{ReadOnly: true}
	err := e.guard.ValidateAssignmentResult(t.Context(), act, ResultDiff{Root: "work/impl-1"})
	if !errors.Is(err, ErrMalformedDiff) {
		t.Fatalf("error = %v, want ErrMalformedDiff", err)
	}
}

// TestGrantWritesAreAuthorized proves the other half of the write
// boundary: a host session editing a document under an issued grant is
// allowed exactly that document, once.
func TestGrantWritesAreAuthorized(t *testing.T) {
	e := newEnv(t)
	name := "fix-login"
	path, err := artifact.Path(name, artifact.KindTaskDocument)
	if err != nil {
		t.Fatalf("artifact.Path: %v", err)
	}
	if _, err := e.artifacts.Create(t.Context(), path, artifact.Metadata{
		Schema: artifact.MetadataSchema, WorkID: e.workID, Name: name, Kind: artifact.KindTaskDocument,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ref := artifact.Ref{WorkID: e.workID, Kind: artifact.KindTaskDocument, Path: path}
	grant, err := e.artifacts.GrantEdit(t.Context(), artifact.GrantRequest{
		Ref: ref, Phase: artifact.PhasePlan, Regions: []artifact.Region{artifact.RegionTaskGoal},
	})
	if err != nil {
		t.Fatalf("GrantEdit: %v", err)
	}

	req := Request{
		Wire: protocol.GuardRequest{
			Host: protocol.HostClaude, SessionID: mustSessionID(t),
			Tool: "Edit", WorkingDirectory: ".", WritePaths: []string{path},
		},
		GrantID:    grant.ID,
		GrantToken: grant.FreshnessToken,
	}
	if d := decide(t, e.guard, req); !d.Allow {
		t.Fatalf("a granted document edit was refused: %s (%s)", d.Reason, d.Code)
	}

	t.Run("a different document is refused", func(t *testing.T) {
		other := req
		other.Wire.WritePaths = []string{"docs/homonto/changes/fix-login/plan.md"}
		d := decide(t, e.guard, other)
		if d.Allow || d.Code != CodeWrongDocument {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeWrongDocument)
		}
	})

	t.Run("an unknown grant is refused", func(t *testing.T) {
		id, err := identity.NewActionID()
		if err != nil {
			t.Fatalf("NewActionID: %v", err)
		}
		unknown := req
		unknown.GrantID = id
		d := decide(t, e.guard, unknown)
		if d.Allow || d.Code != CodeUnknownGrant {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeUnknownGrant)
		}
	})

	t.Run("a forged grant token is refused", func(t *testing.T) {
		other, err := identity.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		forged := req
		forged.GrantToken = other
		d := decide(t, e.guard, forged)
		if d.Allow || d.Code != CodeStaleToken {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeStaleToken)
		}
	})

	t.Run("a consumed grant opens nothing", func(t *testing.T) {
		if _, err := e.artifacts.AcceptEdit(t.Context(), grant); err != nil {
			t.Fatalf("AcceptEdit: %v", err)
		}
		d := decide(t, e.guard, req)
		if d.Allow || d.Code != CodeStaleGrant {
			t.Fatalf("decision = %+v, want a %q refusal", d, CodeStaleGrant)
		}
	})
}
