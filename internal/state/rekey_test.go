package state

import (
	"strings"
	"testing"
)

func TestRekeyMovesLabelledEntriesToTheHarnessID(t *testing.T) {
	s := newState()
	s.Set("claude@svc", "skill.a", "dst -> src", "h1")
	s.Set("claude", "subagentcopy.b", "/p/b.md", "h2")
	s.Tombstones = []Tombstone{{Tool: "claude@svc", Key: "skill.old", Op: "op-1"}, {Tool: "opencode", Key: "skill.x", Op: "op-2"}}

	if err := s.Rekey("claude@svc", "claude"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Managed["claude@svc"]; ok {
		t.Fatal("the labelled namespace must be gone")
	}
	if got := s.Keys("claude"); strings.Join(got, ",") != "skill.a,subagentcopy.b" {
		t.Fatalf("claude keys = %v", got)
	}
	if e, _ := s.Get("claude", "skill.a"); e.Applied != "h1" {
		t.Fatalf("entry not carried over: %+v", e)
	}
	if s.Tombstones[0].Tool != "claude" || s.Tombstones[1].Tool != "opencode" {
		t.Fatalf("tombstones = %+v", s.Tombstones)
	}
	if err := s.Rekey("claude@svc", "claude"); err != nil {
		t.Fatalf("a second rekey must be a no-op: %v", err)
	}
}

func TestRekeyRefusesAKeyRecordedUnderBothNames(t *testing.T) {
	s := newState()
	s.Set("claude@svc", "skill.a", "x", "h1")
	s.Set("claude", "skill.a", "y", "h2")
	s.Set("claude@svc", "skill.b", "z", "h3")
	if err := s.Rekey("claude@svc", "claude"); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("an ambiguous key must fail closed, got %v", err)
	}
	if len(s.Keys("claude@svc")) != 2 || len(s.Keys("claude")) != 1 {
		t.Fatalf("a refused rekey must move nothing: %v / %v", s.Keys("claude@svc"), s.Keys("claude"))
	}
}
