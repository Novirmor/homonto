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

func otherWorkspaceReg(control string) Registration {
	return Registration{
		SchemaVersion: 1,
		WorkspaceID:   identity.WorkspaceID("2e2e7c3d-4e5f-4071-8bcd-ef0123456789"),
		RepositoryID:  testRepoID,
		ControlRoot:   control,
		MemberRoot:    "/home/u/ws/services/api",
		Kind:          workspacecfg.KindGit,
	}
}

func TestClaimCreatesRegistration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x", "homonto", "registration.json")
	reg := validReg()

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
	path := filepath.Join(t.TempDir(), "registration.json")
	first := validReg()
	if err := Claim(path, first); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	err = Claim(path, otherWorkspaceReg("/other/control"))
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

func TestClaimConcurrentOneWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "members", "abc", "registration.json")
	a, b := validReg(), otherWorkspaceReg("/other/control")

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
	path := filepath.Join(t.TempDir(), "registration.json")
	reg := validReg()
	if err := Claim(path, reg); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	err := Detach(path, otherWorkspaceReg("/other").WorkspaceID)
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
	setup := func() string {
		path := filepath.Join(t.TempDir(), "registration.json")
		if err := Claim(path, validReg()); err != nil {
			t.Fatalf("Claim: %v", err)
		}
		return path
	}
	t.Run("refuses while lease exists", func(t *testing.T) {
		path := setup()
		err := TakeOwnership(path, validReg(), true)
		if !errors.Is(err, ErrLeaseActive) {
			t.Fatalf("TakeOwnership error = %v, want ErrLeaseActive", err)
		}
		got, err := Read(path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.WorkspaceID != testWorkspaceID {
			t.Error("refused takeover still replaced the registration")
		}
	})

	t.Run("replaces within same workspace", func(t *testing.T) {
		path := setup()
		next := validReg()
		next.ControlRoot = "/new/control/root"
		if err := TakeOwnership(path, next, false); err != nil {
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
		path := setup()
		err := TakeOwnership(path, otherWorkspaceReg("/other"), false)
		if !errors.Is(err, ErrOwnedByOther) {
			t.Fatalf("TakeOwnership error = %v, want ErrOwnedByOther", err)
		}
		got, err := Read(path)
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got.WorkspaceID != testWorkspaceID {
			t.Error("refused takeover still replaced the registration")
		}
	})

	t.Run("requires an existing registration", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "registration.json")
		err := TakeOwnership(path, validReg(), false)
		if !errors.Is(err, ErrNotRegistered) {
			t.Fatalf("TakeOwnership error = %v, want ErrNotRegistered", err)
		}
	})
}
