package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "homonto.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewStore(db, func() time.Time { return time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC) })
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

// runFixture executes two real checks and returns the resulting set.
func runFixture(t *testing.T) (Set, string) {
	t.Helper()
	requireUnix(t)
	root := t.TempDir()
	pass := script(t, root, "pass.sh", "echo hello\n")
	fail := script(t, root, "fail.sh", "echo nope >&2\nexit 3\n")
	r := newRunner(t, root, nil)
	set, err := r.Run(t.Context(), testInputs(t), []Spec{pass, fail})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return set, root
}

func TestStoreRoundTripsEvidence(t *testing.T) {
	s := newStore(t)
	set, _ := runFixture(t)
	workID := mustWorkID(t)

	if err := s.Record(t.Context(), workID, set); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := s.Latest(t.Context(), workID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("read back %d results, want 2", len(got.Results))
	}
	if got.Results[0].Outcome != OutcomePassed || got.Results[1].Outcome != OutcomeFailed {
		t.Fatalf("outcomes did not round trip: %+v", got.Results)
	}
	if got.Results[1].ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", got.Results[1].ExitCode)
	}
	if !strings.Contains(string(got.Results[0].Stdout), "hello") {
		t.Fatalf("raw stdout did not round trip: %q", got.Results[0].Stdout)
	}
	if !strings.Contains(string(got.Results[1].Stderr), "nope") {
		t.Fatalf("raw stderr did not round trip: %q", got.Results[1].Stderr)
	}
	if got.Inputs.Repository != set.Inputs.Repository || got.Inputs.Config != set.Inputs.Config {
		t.Fatalf("inputs did not round trip: %+v", got.Inputs)
	}
	// The spec — argv, working dir, environment NAMES, timeout — is the
	// evidence of what actually ran and must survive intact.
	if got.Results[0].Spec.Command[0] != set.Results[0].Spec.Command[0] {
		t.Fatalf("command did not round trip: %+v", got.Results[0].Spec)
	}
	if got.Results[0].SpecPin != set.Results[0].SpecPin {
		t.Fatal("the spec pin did not round trip")
	}
}

// TestStoreRecordsOnlyEnvironmentNames proves values never reach the
// evidence, even for variables the check was allowed to read.
func TestStoreRecordsOnlyEnvironmentNames(t *testing.T) {
	requireUnix(t)
	s := newStore(t)
	root := t.TempDir()
	spec := script(t, root, "quiet.sh", "exit 0\n", "TOKEN")
	r := newRunner(t, root, map[string]string{"TOKEN": "value-must-not-be-stored"})
	set, err := r.Run(t.Context(), testInputs(t), []Spec{spec})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), workID, set); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := s.Latest(t.Context(), workID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(got.Results[0].Spec.Environment) != 1 || got.Results[0].Spec.Environment[0] != "TOKEN" {
		t.Fatalf("environment names = %+v, want [TOKEN]", got.Results[0].Spec.Environment)
	}
	// Nothing anywhere on disk carries the value — the main database file
	// or the write-ahead log it may still be sitting in.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		raw, err := os.ReadFile(s.db.Path() + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatalf("read %s%s: %v", s.db.Path(), suffix, err)
		}
		if strings.Contains(string(raw), "value-must-not-be-stored") {
			t.Fatalf("the forwarded value was written to %s%s", s.db.Path(), suffix)
		}
	}
}

func TestRecordSupersedesThePreviousPass(t *testing.T) {
	s := newStore(t)
	set, root := runFixture(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), workID, set); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// A second pass with a single check must replace, not accumulate:
	// readable stale evidence is evidence someone eventually trusts.
	only := script(t, root, "solo.sh", "exit 0\n")
	r := newRunner(t, root, nil)
	second, err := r.Run(t.Context(), set.Inputs, []Spec{only})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if err := s.Record(t.Context(), workID, second); err != nil {
		t.Fatalf("second Record: %v", err)
	}
	got, err := s.Latest(t.Context(), workID)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Spec.Name != "solo.sh" {
		t.Fatalf("read back %+v, want only the second pass", got.Results)
	}
}

func TestLatestReportsNoEvidence(t *testing.T) {
	s := newStore(t)
	if _, err := s.Latest(t.Context(), mustWorkID(t)); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("Latest error = %v, want ErrNoEvidence", err)
	}
}

func TestClearRemovesEvidence(t *testing.T) {
	s := newStore(t)
	set, _ := runFixture(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), workID, set); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Clear(t.Context(), workID); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := s.Latest(t.Context(), workID); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("Latest after Clear error = %v, want ErrNoEvidence", err)
	}
}

func TestRecordRejectsBadWorkAndInputs(t *testing.T) {
	s := newStore(t)
	set, _ := runFixture(t)
	if err := s.Record(t.Context(), "not-a-uuid", set); err == nil {
		t.Fatal("Record with a malformed work id = nil error, want rejection")
	}
	bad := set
	bad.Inputs.Config = "not-a-digest"
	if err := s.Record(t.Context(), mustWorkID(t), bad); err == nil {
		t.Fatal("Record with malformed inputs = nil error, want rejection")
	}
}

// TestInputsRoundTripCanonically pins the storage encoding of inputs:
// canonical form regardless of the order the caller collected them in.
func TestInputsRoundTripCanonically(t *testing.T) {
	in := testInputs(t)
	in.Sources = append(in.Sources, in.Sources[0]) // duplicate
	b, err := MarshalInputs(in)
	if err != nil {
		t.Fatalf("MarshalInputs: %v", err)
	}
	back, err := UnmarshalInputs(b)
	if err != nil {
		t.Fatalf("UnmarshalInputs: %v", err)
	}
	if len(back.Sources) != 1 {
		t.Fatalf("stored inputs kept the duplicate: %+v", back.Sources)
	}
	d1, err := in.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	d2, err := back.Digest()
	if err != nil {
		t.Fatalf("Digest(back): %v", err)
	}
	if d1 != d2 {
		t.Fatal("canonicalization is not stable across a round trip")
	}
}
