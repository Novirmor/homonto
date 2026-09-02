package state

import (
	"fmt"
	"strconv"
	"testing"
)

// TestEnrichPreservesValuesAndSurvivesSet: adapters re-Set entries on every
// apply; provenance recorded by Enrich must survive those value rewrites, and
// Enrich on a missing key is a no-op.
func TestEnrichPreservesValuesAndSurvivesSet(t *testing.T) {
	s := newState()
	s.Set("opencode", "skill.demo", "a -> b", "h1")
	s.Enrich("opencode", "skill.demo", &Origin{Kind: "framework", Framework: "onto", Scope: "project"}, LastEvent{Op: "op1", Action: "create", Cause: "declare", At: "2026-09-02T10:00:00Z"})

	// A value-only re-record keeps provenance.
	s.Set("opencode", "skill.demo", "a -> c", "h2")
	e, ok := s.Get("opencode", "skill.demo")
	if !ok || e.Desired != "a -> c" || e.Applied != "h2" {
		t.Fatalf("value rewrite lost: %+v", e)
	}
	if e.Origin == nil || e.Origin.Framework != "onto" {
		t.Fatalf("origin lost: %+v", e.Origin)
	}
	if e.LastEvent == nil || e.LastEvent.Op != "op1" {
		t.Fatalf("last event lost: %+v", e.LastEvent)
	}

	// Enrich on an unknown key does not create it.
	s.Enrich("opencode", "skill.gone", nil, LastEvent{Op: "op2"})
	if _, ok := s.Get("opencode", "skill.gone"); ok {
		t.Fatal("Enrich created a phantom entry")
	}
}

// TestTombstoneRingIsBounded: the ring keeps the latest TombstoneLimit
// removals in operation order and drops the oldest beyond it.
func TestTombstoneRingIsBounded(t *testing.T) {
	s := newState()
	for i := 0; i < TombstoneLimit+30; i++ {
		s.Set("opencode", keyI(i), "v", "h")
		s.DeleteWithEvent("opencode", keyI(i), Tombstone{Op: opI(i), At: atI(i), Cause: "remove"})
	}
	if len(s.Tombstones) != TombstoneLimit {
		t.Fatalf("ring holds %d, want %d", len(s.Tombstones), TombstoneLimit)
	}
	first, last := s.Tombstones[0], s.Tombstones[len(s.Tombstones)-1]
	if first.Op != opI(30) {
		t.Fatalf("oldest retained is %s, want %s", first.Op, opI(30))
	}
	if last.Op != opI(TombstoneLimit+29) {
		t.Fatalf("newest is %s", last.Op)
	}
	if _, ok := s.Get("opencode", keyI(0)); ok {
		t.Fatal("deleted key still present")
	}
}

// TestDeleteWithoutEventRecordsNothing: a tombstone without an operation is a
// guess, so legacy Delete keeps recording nothing.
func TestDeleteWithoutEventRecordsNothing(t *testing.T) {
	s := newState()
	s.Set("opencode", "skill.x", "v", "h")
	s.Delete("opencode", "skill.x")
	if len(s.Tombstones) != 0 {
		t.Fatalf("legacy delete recorded %d tombstones", len(s.Tombstones))
	}
	if _, ok := s.Get("opencode", "skill.x"); ok {
		t.Fatal("delete left the entry")
	}
}

// TestLegacyEntriesLoadWithUnknownProvenance: schema-3 loading of a schema-2
// file reports nil Origin and nil LastEvent — unknown, never invented.
func TestLegacyEntriesLoadWithUnknownProvenance(t *testing.T) {
	s := mustLoadFixture(t, "legacy_v014_main.json")
	e, _ := s.Get("opencode", "skill.onto")
	if e.Origin != nil || e.LastEvent != nil {
		t.Fatalf("legacy entry gained invented provenance: %+v", e)
	}
	if len(s.Tombstones) != 0 {
		t.Fatalf("legacy state gained tombstones: %d", len(s.Tombstones))
	}
}

func keyI(i int) string { return "skill.k" + strconv.Itoa(i) }
func opI(i int) string  { return "op" + strconv.Itoa(i) }
func atI(i int) string  { return "2026-09-02T10:00:" + fmt.Sprintf("%02d", i%60) + "Z" }
