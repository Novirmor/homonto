package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/registration"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

const (
	wsID  = identity.WorkspaceID("3f3f8d4e-5f60-4a71-9cde-0123456789ab")
	ctlID = identity.RepositoryID("4a4a9e5f-6071-4b82-8def-123456789abc")
	memID = identity.RepositoryID("5b5b0f60-7182-4c93-9ef0-23456789abcd")
)

func gitReg(control, member string) registration.Registration {
	return registration.Registration{
		SchemaVersion: 1,
		WorkspaceID:   wsID,
		RepositoryID:  memID,
		ControlRoot:   control,
		MemberRoot:    member,
		Kind:          workspacecfg.KindGit,
	}
}

// setupWorkspace builds a workspace whose control root is a git repository
// with one git member, both registered to control. It returns the roots.
func setupWorkspace(t *testing.T) (control, member string) {
	t.Helper()
	control = CanonicalPathOf(t, t.TempDir())
	initRepo(t, control)
	member = filepath.Join(control, "services", "api")
	initRepo(t, member)
	if err := registration.Claim(registration.GitRegistrationPath(filepath.Join(control, ".git")),
		registration.Registration{SchemaVersion: 1, WorkspaceID: wsID, RepositoryID: ctlID, ControlRoot: control, MemberRoot: control, Kind: workspacecfg.KindGit}); err != nil {
		t.Fatalf("claim control: %v", err)
	}
	if err := registration.Claim(registration.GitRegistrationPath(filepath.Join(member, ".git")), gitReg(control, member)); err != nil {
		t.Fatalf("claim member: %v", err)
	}
	return control, member
}

func TestLocateFromNestedDirectory(t *testing.T) {
	control, member := setupWorkspace(t)
	nested := filepath.Join(member, "cmd", "tool")
	mkdir(t, nested)

	loc, err := Locate(context.Background(), nested, t.TempDir())
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if loc.ControlRoot != control {
		t.Errorf("ControlRoot = %q, want %q", loc.ControlRoot, control)
	}
	if loc.MemberRoot != member {
		t.Errorf("MemberRoot = %q, want %q", loc.MemberRoot, member)
	}
	if loc.Registration.WorkspaceID != wsID {
		t.Errorf("Registration.WorkspaceID = %q, want %q", loc.Registration.WorkspaceID, wsID)
	}
}

func TestLocateNonGitMemberViaStateRoot(t *testing.T) {
	control, _ := setupWorkspace(t)
	stateRoot := t.TempDir()
	docs := filepath.Join(control, "docs")
	mkdir(t, docs)

	reg := registration.Registration{
		SchemaVersion: 1,
		WorkspaceID:   wsID,
		RepositoryID:  memID,
		ControlRoot:   control,
		MemberRoot:    CanonicalPathOf(t, docs),
		Kind:          workspacecfg.KindNonGit,
	}
	slot, err := registration.NonGitRegistrationPath(stateRoot, CanonicalPathOf(t, docs))
	if err != nil {
		t.Fatalf("NonGitRegistrationPath: %v", err)
	}
	if err := registration.Claim(slot, reg); err != nil {
		t.Fatalf("claim: %v", err)
	}

	loc, err := Locate(context.Background(), docs, stateRoot)
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if loc.ControlRoot != control || loc.MemberRoot != CanonicalPathOf(t, docs) {
		t.Errorf("location = %+v, want control %q member %q", loc, control, docs)
	}
	if loc.Registration.Kind != workspacecfg.KindNonGit {
		t.Errorf("kind = %q, want non_git", loc.Registration.Kind)
	}
}

func TestLocateNone(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "a", "b")
	mkdir(t, nested)

	if _, err := Locate(context.Background(), nested, t.TempDir()); !errors.Is(err, ErrNoWorkspace) {
		t.Fatalf("Locate error = %v, want ErrNoWorkspace", err)
	}
}

func TestLocateConflictingControlRoots(t *testing.T) {
	other := CanonicalPathOf(t, t.TempDir())
	initRepo(t, other)
	control, member := setupWorkspace(t)

	// Retarget the member's registration to a different control root by
	// taking ownership within the same workspace.
	next := gitReg(other, member)
	if err := registration.TakeOwnership(registration.GitRegistrationPath(filepath.Join(member, ".git")), next); err != nil {
		t.Fatalf("take ownership: %v", err)
	}

	_, err := Locate(context.Background(), filepath.Join(member, "sub"), t.TempDir())
	if !errors.Is(err, ErrConflictingRegistrations) {
		t.Fatalf("Locate error = %v, want ErrConflictingRegistrations", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, control) || !strings.Contains(msg, other) {
		t.Errorf("error %q does not name both control roots %q and %q", msg, control, other)
	}
}

func TestLocateCorruptRegistrationFailsClosed(t *testing.T) {
	control, _ := setupWorkspace(t)
	bad := filepath.Join(control, ".git", "homonto", "registration.json")
	if err := os.WriteFile(bad, []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Locate(context.Background(), control, t.TempDir()); err == nil {
		t.Fatal("Locate: expected error for corrupt registration, got nil")
	}
}

func TestCanonicalPath(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	mkdir(t, real)
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"resolves symlinks", link, CanonicalPathOf(t, real)},
		{"cleans dot dot", filepath.Join(base, "real", "..", "real"), CanonicalPathOf(t, real)},
		{"missing path stays lexical", filepath.Join(base, "ghost", "..", "real"), CanonicalPathOf(t, real)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalPath(tt.in)
			if err != nil {
				t.Fatalf("CanonicalPath: %v", err)
			}
			if got != tt.want {
				t.Errorf("CanonicalPath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
