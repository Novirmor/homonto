package verify

import (
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// staleKinds indexes reasons by kind for assertions.
func staleKinds(reasons []StaleReason) map[StaleKind]bool {
	out := make(map[StaleKind]bool, len(reasons))
	for _, r := range reasons {
		out[r.Kind] = true
	}
	return out
}

// passingSet builds a fresh, green set over inputs.
func passingSet(t *testing.T, in Inputs) Set {
	t.Helper()
	spec := Spec{Name: "unit", Command: []string{"/bin/true"}, Timeout: time.Minute}
	pin, err := spec.Digest()
	if err != nil {
		t.Fatalf("Spec.Digest: %v", err)
	}
	return Set{
		Inputs: in,
		At:     time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC),
		Results: []Result{{
			Spec: spec, SpecPin: pin, Outcome: OutcomePassed,
			Summary: summarize(nil, nil, false),
		}},
	}
}

func TestFreshAcceptsUnchangedInputs(t *testing.T) {
	in := testInputs(t)
	set := passingSet(t, in)
	fresh, reasons := Fresh(set, in)
	if !fresh {
		t.Fatalf("Fresh = false, reasons %+v", reasons)
	}
	// Age is irrelevant: the same set recorded long ago is still fresh.
	set.At = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if fresh, _ := Fresh(set, in); !fresh {
		t.Fatal("an old set against unchanged inputs must still be fresh")
	}
	// Input order and duplication must not matter.
	shuffled := in
	shuffled.Sources = append([]fingerprint.Digest{
		fingerprint.Bytes("test-source", []byte("a")),
	}, in.Sources...)
	if fresh, reasons := Fresh(set, shuffled); !fresh {
		t.Fatalf("duplicate inputs made the set stale: %+v", reasons)
	}
}

func TestFreshDetectsEachMovedInput(t *testing.T) {
	in := testInputs(t)
	in.Artifacts = []fingerprint.Digest{fingerprint.Bytes("test-artifact", []byte("doc"))}
	set := passingSet(t, in)

	t.Run("config", func(t *testing.T) {
		now := in
		now.Config = fingerprint.Bytes("test-config", []byte("v2"))
		fresh, reasons := Fresh(set, now)
		if fresh || !staleKinds(reasons)[StaleConfig] {
			t.Fatalf("fresh=%v reasons=%+v, want a config reason", fresh, reasons)
		}
	})

	t.Run("source", func(t *testing.T) {
		now := in
		now.Sources = []fingerprint.Digest{fingerprint.Bytes("test-source", []byte("b"))}
		fresh, reasons := Fresh(set, now)
		if fresh || !staleKinds(reasons)[StaleSource] {
			t.Fatalf("fresh=%v reasons=%+v, want a source reason", fresh, reasons)
		}
	})

	t.Run("added source", func(t *testing.T) {
		now := in
		now.Sources = append(append([]fingerprint.Digest(nil), in.Sources...),
			fingerprint.Bytes("test-source", []byte("extra")))
		fresh, reasons := Fresh(set, now)
		if fresh || !staleKinds(reasons)[StaleSource] {
			t.Fatalf("fresh=%v reasons=%+v, want a source reason", fresh, reasons)
		}
	})

	t.Run("artifact", func(t *testing.T) {
		now := in
		now.Artifacts = []fingerprint.Digest{fingerprint.Bytes("test-artifact", []byte("edited"))}
		fresh, reasons := Fresh(set, now)
		if fresh || !staleKinds(reasons)[StaleArtifact] {
			t.Fatalf("fresh=%v reasons=%+v, want an artifact reason", fresh, reasons)
		}
	})

	t.Run("repository", func(t *testing.T) {
		now := in
		now.Repository = mustRepoID(t)
		fresh, reasons := Fresh(set, now)
		if fresh || !staleKinds(reasons)[StaleRepository] {
			t.Fatalf("fresh=%v reasons=%+v, want a repository reason", fresh, reasons)
		}
	})

	t.Run("every input at once", func(t *testing.T) {
		now := Inputs{
			Repository: mustRepoID(t),
			Config:     fingerprint.Bytes("test-config", []byte("v9")),
			Sources:    []fingerprint.Digest{fingerprint.Bytes("test-source", []byte("z"))},
			Artifacts:  nil,
		}
		fresh, reasons := Fresh(set, now)
		kinds := staleKinds(reasons)
		if fresh {
			t.Fatal("Fresh = true with every input moved")
		}
		for _, want := range []StaleKind{StaleRepository, StaleConfig, StaleSource, StaleArtifact} {
			if !kinds[want] {
				t.Errorf("missing reason %q in %+v", want, reasons)
			}
		}
	})
}

func TestEmptySetIsNeverFresh(t *testing.T) {
	in := testInputs(t)
	empty := Set{Inputs: in, At: time.Now()}
	fresh, reasons := Fresh(empty, in)
	if fresh || !staleKinds(reasons)[StaleEmpty] {
		t.Fatalf("fresh=%v reasons=%+v, want an empty reason", fresh, reasons)
	}
	if empty.Passed() {
		t.Fatal("an empty set must not report passed; it proved nothing")
	}
}

func TestFreshDetectsAPinnedSpecThatMoved(t *testing.T) {
	in := testInputs(t)
	set := passingSet(t, in)
	// The recorded command changed but the pin still names the old spec:
	// the evidence no longer describes what would run now.
	set.Results[0].Spec.Command = []string{"/bin/false"}
	fresh, reasons := Fresh(set, in)
	if fresh || !staleKinds(reasons)[StaleSpec] {
		t.Fatalf("fresh=%v reasons=%+v, want a spec reason", fresh, reasons)
	}
}

func TestFreshForRequiresFreshAndGreen(t *testing.T) {
	in := testInputs(t)
	set := passingSet(t, in)
	if ok, _ := FreshFor(set, in); !ok {
		t.Fatal("FreshFor = false for a fresh, green set")
	}
	failed := passingSet(t, in)
	failed.Results[0].Outcome = OutcomeFailed
	if ok, reasons := FreshFor(failed, in); ok {
		t.Fatal("FreshFor = true for a fresh but failing set")
	} else if len(reasons) != 0 {
		t.Fatalf("a failing-but-fresh set reported staleness: %+v", reasons)
	}
	moved := in
	moved.Config = fingerprint.Bytes("test-config", []byte("v2"))
	if ok, reasons := FreshFor(set, moved); ok || len(reasons) == 0 {
		t.Fatalf("FreshFor(stale) = %v with reasons %+v", ok, reasons)
	}
}

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
