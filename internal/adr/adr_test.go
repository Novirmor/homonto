package adr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

func digest(s string) fingerprint.Digest { return fingerprint.Bytes("test", []byte(s)) }

func mustActionID(t *testing.T) identity.ActionID {
	t.Helper()
	id, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("NewActionID: %v", err)
	}
	return id
}

func candidate(id, title string, design fingerprint.Digest) Candidate {
	return Candidate{
		ID: id, Title: title,
		Question: "why is " + id + " like this",
		Design:   design,
	}
}

func TestCandidateValidate(t *testing.T) {
	if err := candidate("c1", "Adopt X", digest("design")).Validate(); err != nil {
		t.Fatalf("a well-formed candidate was refused: %v", err)
	}
	for _, c := range []Candidate{
		{Title: "Adopt X", Question: "why"},
		{ID: "c1", Question: "why"},
		{ID: "c1", Title: "Adopt X"},
	} {
		if err := c.Validate(); err == nil {
			t.Errorf("an incomplete candidate was accepted: %+v", c)
		}
	}
}

// TestNoDecisionMeansNoADR is the rule that keeps the directory readable:
// Design noticing a question is not the same as the change answering it.
func TestNoDecisionMeansNoADR(t *testing.T) {
	design := digest("design")
	got, err := Assess([]Candidate{
		candidate("c1", "Adopt X", design),
		candidate("c2", "Adopt Y", design),
	}, nil, design)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Owed() || got.Blocked() {
		t.Fatalf("candidates alone owed an ADR: %+v", got)
	}
	// A non-durable decision does not trigger one either.
	got, err = Assess([]Candidate{candidate("c1", "Adopt X", design)}, []Record{{
		CandidateIDs: []string{"c1"}, ActionID: mustActionID(t),
		Kind: decision.KindApproveScope, Choice: "approve", Durable: false,
	}}, design)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Owed() {
		t.Fatalf("a non-durable decision owed an ADR: %+v", got)
	}
}

func TestDurableDecisionAgainstACandidateOwesAnADR(t *testing.T) {
	design := digest("design")
	c := candidate("c1", "Adopt X", design)
	got, err := Assess([]Candidate{c}, []Record{{
		CandidateIDs: []string{"c1"}, ActionID: mustActionID(t),
		Kind: decision.KindApproveDesign, Choice: "approve",
		Rationale: "the alternative could not migrate", Durable: true,
	}}, design)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if !got.Owed() || len(got.Required) != 1 {
		t.Fatalf("Assess = %+v, want one required ADR", got)
	}
	if got.Blocked() {
		t.Fatalf("a designed decision blocked close: %+v", got)
	}
	if got.Required[0].Candidate.ID != "c1" || got.Required[0].Reason == "" {
		t.Fatalf("requirement = %+v", got.Required[0])
	}
	if !strings.Contains(got.Required[0].Reason, c.Question) {
		t.Fatalf("the reason does not name the question: %q", got.Required[0].Reason)
	}
}

// TestUndesignedDecisionBlocksRatherThanProducesAnADR is the inverse rule:
// writing an ADR for a decision nobody designed would document an
// accident.
func TestUndesignedDecisionBlocksRatherThanProducesAnADR(t *testing.T) {
	design := digest("design")
	for _, name := range []string{"no candidate named", "a candidate that is not in the design"} {
		t.Run(name, func(t *testing.T) {
			record := Record{
				ActionID: mustActionID(t), Kind: decision.KindPresetTripwire,
				Choice: "continue", Durable: true,
			}
			if name != "no candidate named" {
				record.CandidateIDs = []string{"ghost"}
			}
			got, err := Assess([]Candidate{candidate("c1", "Adopt X", design)},
				[]Record{record}, design)
			if err != nil {
				t.Fatalf("Assess: %v", err)
			}
			if got.Owed() {
				t.Fatalf("an undesigned decision produced an ADR requirement: %+v", got)
			}
			if !got.Blocked() || len(got.Undesigned) != 1 {
				t.Fatalf("Assess = %+v, want the decision reported as undesigned", got)
			}
		})
	}
}

// TestStaleCandidateBlocks proves an ADR cannot be written from a design
// that has since been rewritten.
func TestStaleCandidateBlocks(t *testing.T) {
	got, err := Assess([]Candidate{candidate("c1", "Adopt X", digest("old design"))},
		[]Record{{
			CandidateIDs: []string{"c1"}, ActionID: mustActionID(t),
			Kind: decision.KindApproveDesign, Choice: "approve", Durable: true,
		}}, digest("new design"))
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if got.Owed() {
		t.Fatalf("a stale candidate produced a requirement: %+v", got)
	}
	if !got.Blocked() || len(got.Stale) != 1 || got.Stale[0].ID != "c1" {
		t.Fatalf("Assess = %+v, want the candidate reported as stale", got)
	}
}

// TestContinuedPresetStillOwesAnADR pins the spec's explicit carve-out:
// choosing less ceremony for the work does not choose less record for the
// decision.
func TestContinuedPresetStillOwesAnADR(t *testing.T) {
	design := digest("preset intent")
	got, err := Assess([]Candidate{candidate("c1", "Keep the flag public", design)},
		[]Record{{
			CandidateIDs: []string{"c1"}, ActionID: mustActionID(t),
			Kind: decision.KindPresetTripwire, Choice: "continue",
			Rationale: "the rename is mechanical", Durable: true,
		}}, design)
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if !got.Owed() {
		t.Fatalf("a continued preset owed no ADR for the decision it made: %+v", got)
	}
	if !IsDurableKind(decision.KindPresetTripwire) {
		t.Error("a preset tripwire is not treated as durable by nature")
	}
}

func TestAssessRefusesMalformedCandidates(t *testing.T) {
	design := digest("design")
	if _, err := Assess([]Candidate{{ID: "c1"}}, nil, design); err == nil {
		t.Error("an incomplete candidate was accepted")
	}
	if _, err := Assess([]Candidate{
		candidate("c1", "A", design), candidate("c1", "B", design),
	}, nil, design); err == nil {
		t.Error("a duplicate candidate id was accepted")
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{
		"Adopt the snapshot store":       "adopt-the-snapshot-store",
		"Stop committing generated code": "stop-committing-generated-code",
		"Use SQLite (WAL) for state":     "use-sqlite-wal-for-state",
		"  Trim   the  spaces  ":         "trim-the-spaces",
	} {
		got, err := Slug(in)
		if err != nil {
			t.Errorf("Slug(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "   ", "!!!", "---"} {
		if _, err := Slug(bad); err == nil {
			t.Errorf("Slug(%q) = nil error, want rejection", bad)
		}
	}
}

func TestAllocatePathNumbersFourDigitsAndNeverReuses(t *testing.T) {
	root := t.TempDir()
	first, err := AllocatePath(root, "Adopt the snapshot store")
	if err != nil {
		t.Fatalf("AllocatePath: %v", err)
	}
	if first != Dir+"/0001-adopt-the-snapshot-store.md" {
		t.Fatalf("first = %q", first)
	}
	second, err := AllocatePath(root, "Stop committing generated code")
	if err != nil {
		t.Fatalf("AllocatePath: %v", err)
	}
	if second != Dir+"/0002-stop-committing-generated-code.md" {
		t.Fatalf("second = %q", second)
	}
	// Numbers are never reused: deleting one leaves a gap.
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(first))); err != nil {
		t.Fatalf("remove: %v", err)
	}
	third, err := AllocatePath(root, "Adopt something else")
	if err != nil {
		t.Fatalf("AllocatePath: %v", err)
	}
	if n, ok := Number(third); !ok || n != 3 {
		t.Fatalf("third = %q, want 0003: a deleted number is never reused", third)
	}
	// An existing repository's numbering is continued, not restarted.
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(Dir), "0042-existing.md"),
		[]byte("# Existing\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	next, err := AllocatePath(root, "After the gap")
	if err != nil {
		t.Fatalf("AllocatePath: %v", err)
	}
	if n, _ := Number(next); n != 43 {
		t.Fatalf("next = %q, want 0043", next)
	}
}

// TestAllocatePathIsAtomicUnderRace is why the create is O_EXCL: two
// allocations racing for one number would silently merge two decisions
// into one record.
func TestAllocatePathIsAtomicUnderRace(t *testing.T) {
	root := t.TempDir()
	const n = 16
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		paths []string
		errs  []error
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			p, err := AllocatePath(root, fmt.Sprintf("Decide thing %d", i))
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			paths = append(paths, p)
		}(i)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("concurrent allocation failed: %v", errs[0])
	}
	seen := map[int]string{}
	for _, p := range paths {
		num, ok := Number(p)
		if !ok {
			t.Fatalf("allocated path %q carries no number", p)
		}
		if other, dup := seen[num]; dup {
			t.Fatalf("number %04d was allocated twice: %q and %q", num, other, p)
		}
		seen[num] = p
	}
	if len(seen) != n {
		t.Fatalf("allocated %d distinct numbers, want %d", len(seen), n)
	}
}

func TestValidateDocument(t *testing.T) {
	root := t.TempDir()
	c := candidate("c1", "Adopt the snapshot store", digest("design"))
	rel, err := AllocatePath(root, c.Title)
	if err != nil {
		t.Fatalf("AllocatePath: %v", err)
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	_ = abs

	// The reservation is empty and is not yet a record.
	if err := ValidateDocument(root, rel, c); !errors.Is(err, ErrMissingDocument) {
		t.Fatalf("ValidateDocument(empty) error = %v, want ErrMissingDocument", err)
	}

	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	// The template it is filled in from validates.
	write(Template(c, Record{Choice: "adopt the snapshot store"}, "2026-08-25"))
	if err := ValidateDocument(root, rel, c); err != nil {
		t.Fatalf("the template does not validate: %v", err)
	}

	tests := []struct {
		name string
		body string
		want error
	}{
		{"no consequences", "# Adopt X\n\n## Context\n\nwhy is c1 like this\n\n## Decision\n\nWe will X.\n", ErrMissingHeading},
		{"no decision", "# Adopt X\n\n## Context\n\nwhy is c1 like this\n\n## Consequences\n\nCosts.\n", ErrMissingHeading},
		{"no context", "# Adopt X\n\n## Decision\n\nWe will X.\n\n## Consequences\n\nCosts.\n", ErrMissingHeading},
		{"no title", "## Context\n\nwhy is c1 like this\n\n## Decision\n\nX\n\n## Consequences\n\nCosts.\n", ErrMissingHeading},
		{"answers a different question",
			"# Adopt X\n\n## Context\n\nsomething else entirely\n\n## Decision\n\nX\n\n## Consequences\n\nCosts.\n",
			ErrWrongCandidate},
		{"a heading only mentioned in prose",
			"# Adopt X\n\nThis has ## Context in a sentence.\n\n## Decision\n\nX\n\n## Consequences\n\nCosts.\n",
			ErrMissingHeading},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			write(tt.body)
			if err := ValidateDocument(root, rel, c); !errors.Is(err, tt.want) {
				t.Fatalf("ValidateDocument error = %v, want %v", err, tt.want)
			}
		})
	}

	if err := ValidateDocument(root, Dir+"/9999-missing.md", c); !errors.Is(err, ErrMissingDocument) {
		t.Fatalf("ValidateDocument(missing) error = %v, want ErrMissingDocument", err)
	}
}

func TestTemplateCarriesTheQuestionAndTheDecision(t *testing.T) {
	c := candidate("c1", "Adopt the snapshot store", digest("design"))
	body := Template(c, Record{
		Choice: "adopt the snapshot store", Rationale: "git is not available everywhere",
	}, "2026-08-25")
	for _, want := range []string{
		"# Adopt the snapshot store", "**Status:** Accepted", "**Date:** 2026-08-25",
		c.Question, "adopt the snapshot store", "git is not available everywhere",
		"## Context", "## Decision", "## Consequences", "Include the bad parts",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the template does not carry %q:\n%s", want, body)
		}
	}
}
