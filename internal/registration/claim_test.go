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

func TestTakeOwnership(t *testing.T) {
	member := "/home/u/ws/docs"
	setup := func() (string, Registration) {
		base := t.TempDir()
		path := nonGitSlot(t, base, member)
		reg := nonGitReg(member)
		if err := Claim(path, reg); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		return path, reg
	}

	t.Run("refuses while lease exists", func(t *testing.T) {
		path, reg := setup()
		leasePath := filepath.Join(filepath.Dir(path), leaseName)
		if err := os.WriteFile(leasePath, []byte("{}\n"), 0o644); err != nil {
			t.Fatalf("write lease: %v", err)
		}
		next := reg
		next.ControlRoot = "/new/control/root"
		err := TakeOwnership(path, next)
		if !errors.Is(err, ErrLeaseActive) {
			t.Fatalf("TakeOwnership error = %v, want ErrLeaseActive", err)
		}
		if !strings.Contains(err.Error(), leaseName) {
			t.Errorf("error %q does not name %q", err, leaseName)
		}
		got, err := Read(path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.ControlRoot != reg.ControlRoot {
			t.Error("refused takeover still replaced the registration")
		}
	})

	t.Run("replaces within same workspace", func(t *testing.T) {
		path, reg := setup()
		next := reg
		next.ControlRoot = "/new/control/root"
		if err := TakeOwnership(path, next); err != nil {
			t.Fatalf("TakeOwnership: %v", err)
		}
		got, err := Read(path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.ControlRoot != "/new/control/root" {
			t.Errorf("ControlRoot = %q, want replaced value", got.ControlRoot)
		}
	})

	t.Run("refuses foreign workspace", func(t *testing.T) {
		path, reg := setup()
		err := TakeOwnership(path, otherWorkspace(reg))
		if !errors.Is(err, ErrOwnedByOther) {
			t.Fatalf("TakeOwnership error = %v, want ErrOwnedByOther", err)
		}
		got, err := Read(path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.WorkspaceID != reg.WorkspaceID {
			t.Error("refused takeover still replaced the registration")
		}
	})

	t.Run("requires an existing registration", func(t *testing.T) {
		base := t.TempDir()
		path := nonGitSlot(t, base, member)
		err := TakeOwnership(path, nonGitReg(member))
		if !errors.Is(err, ErrNotRegistered) {
			t.Fatalf("TakeOwnership error = %v, want ErrNotRegistered", err)
		}
	})
}

// scriptedReader returns the queued byte slices in order, failing the test
// if the queue runs dry.
type scriptedReader struct {
	t      *testing.T
	script [][]byte
	calls  int
}

func (r *scriptedReader) read(string) ([]byte, error) {
	r.t.Helper()
	if r.calls >= len(r.script) {
		r.t.Fatalf("scriptedReader ran dry after %d calls", r.calls)
	}
	b := r.script[r.calls]
	r.calls++
	return b, nil
}

func TestTakeOwnershipRetriesWhenRegistrationChanges(t *testing.T) {
	base := t.TempDir()
	member := "/home/u/ws/docs"
	path := nonGitSlot(t, base, member)
	original := nonGitReg(member)
	if err := Claim(path, original); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	next := original
	next.ControlRoot = "/moved/control"

	movedBytes, err := movedReg(original).Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	origBytes, err := original.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// First attempt reads two different contents (a concurrent takeover
	// landed between the reads); the second attempt reads a stable pair
	// and the write must go through.
	r := &scriptedReader{t: t, script: [][]byte{origBytes, movedBytes, movedBytes, movedBytes}}
	if err := takeOwnership(path, next, r.read); err != nil {
		t.Fatalf("takeOwnership: %v", err)
	}
	if r.calls != 4 {
		t.Errorf("reads = %d, want 4 (two per attempt)", r.calls)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.ControlRoot != next.ControlRoot {
		t.Errorf("ControlRoot = %q, want %q", got.ControlRoot, next.ControlRoot)
	}
}

func movedReg(base Registration) Registration {
	mut := base
	mut.ControlRoot = "/moved/control"
	return mut
}

func TestTakeOwnershipFailsWhenRegistrationKeepsChanging(t *testing.T) {
	base := t.TempDir()
	member := "/home/u/ws/docs"
	path := nonGitSlot(t, base, member)
	original := nonGitReg(member)
	if err := Claim(path, original); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	next := original
	next.ControlRoot = "/moved/control"

	a, _ := original.Marshal()
	b, _ := movedReg(original).Marshal()

	// Every attempt sees a change; exactly two attempts run, then the
	// typed error, and the on-disk registration is untouched.
	r := &scriptedReader{t: t, script: [][]byte{a, b, b, a}}
	err := takeOwnership(path, next, r.read)
	if !errors.Is(err, ErrRegistrationChanged) {
		t.Fatalf("takeOwnership error = %v, want ErrRegistrationChanged", err)
	}
	if r.calls != 4 {
		t.Errorf("reads = %d, want exactly 4 (one retry)", r.calls)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got != original {
		t.Errorf("registration = %+v, want original untouched", got)
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
