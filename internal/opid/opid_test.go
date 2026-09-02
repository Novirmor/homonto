package opid

import (
	"strings"
	"testing"
	"time"
)

func TestNewIDsUnique(t *testing.T) {
	s := New()
	seen := map[string]bool{}
	for i := 0; i < 10000; i++ {
		id := s.NewID()
		if len(id) != 8 {
			t.Fatalf("id %q is not 8 hex chars", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
	if _, err := time.Parse(time.RFC3339, s.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("Now is not usable as UTC time: %v", err)
	}
}

func TestFixedDeterministic(t *testing.T) {
	base := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	a, b := Fixed(base), Fixed(base)
	var idsA, idsB []string
	for i := 0; i < 3; i++ {
		idsA = append(idsA, a.NewID())
		idsB = append(idsB, b.NewID())
	}
	if strings.Join(idsA, ",") != strings.Join(idsB, ",") {
		t.Fatalf("fixed supplier not deterministic: %v vs %v", idsA, idsB)
	}
	if !a.Now().Equal(base) {
		t.Fatalf("fixed clock moved: %v", a.Now())
	}
}
