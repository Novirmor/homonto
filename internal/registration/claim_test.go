package registration

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

const otherWorkspaceID = identity.WorkspaceID("2e2e7c3d-4e5f-4071-8bcd-ef0123456789")

// nonGitReg builds a valid non-git registration for memberRoot.
func nonGitReg(memberRoot string) Registration {
	return Registration{
		SchemaVersion: 1,
		WorkspaceID:   testWorkspaceID,
		RepositoryID:  testRepoID,
		ControlRoot:   "/home/u/ws",
		MemberRoot:    memberRoot,
		Kind:          workspacecfg.KindNonGit,
	}
}

func otherWorkspace(mut Registration) Registration {
	mut.WorkspaceID = otherWorkspaceID
	return mut
}

// nonGitSlot derives the claim path for memberRoot under base.
func nonGitSlot(t *testing.T, base, memberRoot string) string {
	t.Helper()
	path, err := NonGitRegistrationPath(base, memberRoot)
	if err != nil {
		t.Fatalf("NonGitRegistrationPath: %v", err)
	}
	return path
}

func TestClaimCreatesRegistration(t *testing.T) {
	base := t.TempDir()
	member := "/home/u/ws/docs"
	path := nonGitSlot(t, base, member)
	reg := nonGitReg(member)

	if err := Claim(path, reg); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != reg {
		t.Errorf("Read = %+v, want %+v", got, reg)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("perm = %o, want 644", perm)
	}
}

func TestClaimRejectsExistingEvenIdle(t *testing.T) {
	base := t.TempDir()
	member := "/home/u/ws/docs"
	path := nonGitSlot(t, base, member)
	first := nonGitReg(member)
	if err := Claim(path, first); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	err = Claim(path, otherWorkspace(first))
	if !errors.Is(err, ErrOwnedByOther) {
		t.Fatalf("Claim error = %v, want ErrOwnedByOther", err)
	}
	if !strings.Contains(err.Error(), string(first.WorkspaceID)) {
		t.Errorf("error %q does not name owning workspace %q", err, first.WorkspaceID)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Error("failed claim modified the registration")
	}
}

func TestClaimRejectsWrongSlot(t *testing.T) {
	base := filepath.Join(t.TempDir(), "state")

	// A correct slot for one member, misused for another member's
	// registration: the hash names a different member.
	wrongMemberSlot := nonGitSlot(t, base, "/home/u/ws/other")

	// A git-family slot.
	gitSlot := filepath.Join(t.TempDir(), "work", ".git", "homonto", registrationName)

	// Wrong file name inside an otherwise correct slot.
	base2 := t.TempDir()
	member := "/home/u/ws/docs"
	leaseNamed, err := NonGitMemberDir(base2, member)
	if err != nil {
		t.Fatalf("NonGitMemberDir: %v", err)
	}
	leaseNamed = filepath.Join(leaseNamed, leaseName)

	tests := []struct {
		name string
		path string
		reg  Registration
	}{
		{"non-git registration in another member's slot", wrongMemberSlot, nonGitReg(member)},
		{"non-git registration in a git slot", gitSlot, nonGitReg("/home/u/ws/api")},
		{"git registration in a members slot", wrongMemberSlot, gitReg("/home/u/ws/api")},
		{"registration content under the lease file name", leaseNamed, nonGitReg(member)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Claim(tt.path, tt.reg)
			if !errors.Is(err, ErrInvalidRegistration) {
				t.Fatalf("Claim error = %v, want ErrInvalidRegistration", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, "slot") {
				t.Errorf("error %q does not explain the slot mismatch", msg)
			}
			if tt.reg.Kind == workspacecfg.KindNonGit && !strings.Contains(msg, hashPath(tt.reg.MemberRoot)) {
				t.Errorf("error %q does not name the expected slot hash for %q", msg, tt.reg.MemberRoot)
			}
			if !strings.Contains(msg, tt.path) {
				t.Errorf("error %q does not name the actual slot %q", msg, tt.path)
			}
		})
	}
}

func TestClaimConcurrentOneWinner(t *testing.T) {
	base := t.TempDir()
	member := "/home/u/ws/docs"
	path := nonGitSlot(t, base, member)
	a := nonGitReg(member)
	b := otherWorkspace(a)

	var wg sync.WaitGroup
	results := make([]error, 2)
	start := make(chan struct{})
	for i, reg := range []Registration{a, b} {
		wg.Add(1)
		go func(i int, reg Registration) {
			defer wg.Done()
			<-start
			results[i] = Claim(path, reg)
		}(i, reg)
	}
	close(start)
	wg.Wait()

	winners := 0
	for i, err := range results {
		if err == nil {
			winners++
			continue
		}
		if !errors.Is(err, ErrOwnedByOther) {
			t.Fatalf("goroutine %d error = %v, want ErrOwnedByOther", i, err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want exactly 1 (errors: %v)", winners, results)
	}

	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	loserErr := results[0]
	if results[0] == nil {
		loserErr = results[1]
	}
	if !strings.Contains(loserErr.Error(), string(got.WorkspaceID)) {
		t.Errorf("loser error %q does not name winner workspace %q", loserErr, got.WorkspaceID)
	}
}

func TestReadMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registration.json")
	if _, err := Read(path); !errors.Is(err, ErrNotRegistered) {
		t.Fatalf("Read error = %v, want ErrNotRegistered", err)
	}
}

func TestReadCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registration.json")
	if err := os.WriteFile(path, []byte("{nope"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := Read(path); !errors.Is(err, ErrInvalidRegistration) {
		t.Fatalf("Read error = %v, want ErrInvalidRegistration", err)
	}
}

func TestDetach(t *testing.T) {
	base := t.TempDir()
	member := "/home/u/ws/docs"
	path := nonGitSlot(t, base, member)
	reg := nonGitReg(member)
	if err := Claim(path, reg); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	err := Detach(path, otherWorkspaceID)
	if !errors.Is(err, ErrOwnedByOther) {
		t.Fatalf("Detach by wrong workspace error = %v, want ErrOwnedByOther", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("wrong-workspace detach removed the file: %v", err)
	}

	if err := Detach(path, reg.WorkspaceID); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("registration still present after detach: %v", err)
	}
	if err := Detach(path, reg.WorkspaceID); !errors.Is(err, ErrNotRegistered) {
		t.Errorf("second Detach error = %v, want ErrNotRegistered", err)
	}
}

func gitReg(member string) Registration {
	return Registration{
		SchemaVersion: 1,
		WorkspaceID:   testWorkspaceID,
		RepositoryID:  testRepoID,
		ControlRoot:   "/home/u/ws",
		MemberRoot:    member,
		Kind:          workspacecfg.KindGit,
	}
}
