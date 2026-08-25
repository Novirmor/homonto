package finding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
)

var fixedNow = time.Date(2026, 8, 25, 9, 30, 0, 0, time.UTC)

func newService(t *testing.T) *Service {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "homonto.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := NewService(db, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatalf("NewService: %v", err)
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

func mustActionID(t *testing.T) identity.ActionID {
	t.Helper()
	id, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("NewActionID: %v", err)
	}
	return id
}

func fnd(t *testing.T, workID identity.WorkID, id string, sev Severity) Finding {
	t.Helper()
	return Finding{
		WorkID:         workID,
		ActionID:       mustActionID(t),
		ExternalID:     id,
		Role:           protocol.RoleReviewer,
		Severity:       sev,
		Summary:        "something is wrong with " + id,
		Evidence:       []string{"internal/x/y.go:42"},
		Recommendation: "fix it",
		State:          StateOpen,
	}
}

func TestBlockingGradesSeverity(t *testing.T) {
	for _, sev := range []Severity{SeverityCritical, SeverityHigh} {
		if !Blocking(sev) {
			t.Errorf("Blocking(%s) = false, want true", sev)
		}
	}
	for _, sev := range []Severity{SeverityMedium, SeverityLow} {
		if Blocking(sev) {
			t.Errorf("Blocking(%s) = true, want false", sev)
		}
	}
	// An unrecognized grade blocks: a finding Homonto cannot grade is not
	// one it may wave through.
	if !Blocking(Severity("catastrophic")) || !Blocking("") {
		t.Error("an unknown severity must block")
	}
}

func TestRecordAndBlockers(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	findings := []Finding{
		fnd(t, workID, "F-1", SeverityCritical),
		fnd(t, workID, "F-2", SeverityLow),
		fnd(t, workID, "F-3", SeverityHigh),
	}
	if err := s.Record(t.Context(), findings); err != nil {
		t.Fatalf("Record: %v", err)
	}
	all, err := s.All(t.Context(), workID)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("All returned %d findings, want 3", len(all))
	}
	blockers, err := s.Blockers(t.Context(), workID)
	if err != nil {
		t.Fatalf("Blockers: %v", err)
	}
	if len(blockers) != 2 {
		t.Fatalf("Blockers = %d, want the critical and the high", len(blockers))
	}
	if !AnyBlocking(all) {
		t.Fatal("AnyBlocking = false with an open critical finding")
	}
	// Evidence survives the round trip.
	if len(all[0].Evidence) != 1 || all[0].Evidence[0] != "internal/x/y.go:42" {
		t.Fatalf("evidence did not round trip: %+v", all[0].Evidence)
	}
}

func TestRecordUpdatesRatherThanDuplicating(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	first := fnd(t, workID, "F-1", SeverityHigh)
	if err := s.Record(t.Context(), []Finding{first}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	again := fnd(t, workID, "F-1", SeverityCritical)
	again.Summary = "worse than we thought"
	if err := s.Record(t.Context(), []Finding{again}); err != nil {
		t.Fatalf("second Record: %v", err)
	}
	all, err := s.All(t.Context(), workID)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("re-reporting created %d rows, want 1", len(all))
	}
	if all[0].Severity != SeverityCritical || all[0].Summary != "worse than we thought" {
		t.Fatalf("the re-report did not update the row: %+v", all[0])
	}
}

// TestRecordDoesNotResurrectResolvedFindings proves a later report cannot
// silently reopen the gate on something a human already accepted.
func TestRecordDoesNotResurrectResolvedFindings(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), []Finding{fnd(t, workID, "F-1", SeverityCritical)}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	decisionID := mustActionID(t)
	if err := s.Resolve(t.Context(), Resolution{
		WorkID: workID, ExternalID: "F-1", Kind: KindAccepted,
		Rationale: "accepted for the release; tracked as issue 91", DecisionID: decisionID,
	}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := s.Record(t.Context(), []Finding{fnd(t, workID, "F-1", SeverityCritical)}); err != nil {
		t.Fatalf("re-Record: %v", err)
	}
	all, err := s.All(t.Context(), workID)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if all[0].State != StateAccepted {
		t.Fatalf("state = %q, want accepted; a later report must not reopen the gate", all[0].State)
	}
	blockers, err := s.Blockers(t.Context(), workID)
	if err != nil {
		t.Fatalf("Blockers: %v", err)
	}
	if len(blockers) != 0 {
		t.Fatalf("accepted finding still blocks: %+v", blockers)
	}
}

func TestResolveRequiresRationaleAndDecisionForBlockers(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), []Finding{
		fnd(t, workID, "F-block", SeverityCritical),
		fnd(t, workID, "F-minor", SeverityLow),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	decisionID := mustActionID(t)

	t.Run("no rationale", func(t *testing.T) {
		err := s.Resolve(t.Context(), Resolution{
			WorkID: workID, ExternalID: "F-block", Kind: KindAccepted, DecisionID: decisionID,
		})
		if !errors.Is(err, ErrRationaleRequired) {
			t.Fatalf("Resolve error = %v, want ErrRationaleRequired", err)
		}
	})

	t.Run("no decision", func(t *testing.T) {
		err := s.Resolve(t.Context(), Resolution{
			WorkID: workID, ExternalID: "F-block", Kind: KindAccepted, Rationale: "we accept this",
		})
		if !errors.Is(err, ErrDecisionRequired) {
			t.Fatalf("Resolve error = %v, want ErrDecisionRequired", err)
		}
	})

	t.Run("still blocking after refused acceptances", func(t *testing.T) {
		blockers, err := s.Blockers(t.Context(), workID)
		if err != nil {
			t.Fatalf("Blockers: %v", err)
		}
		if len(blockers) != 1 {
			t.Fatalf("Blockers = %+v, want the critical finding still gating", blockers)
		}
	})

	t.Run("a low finding needs no ceremony", func(t *testing.T) {
		if err := s.Resolve(t.Context(), Resolution{
			WorkID: workID, ExternalID: "F-minor", Kind: KindAccepted,
		}); err != nil {
			t.Fatalf("Resolve(low, no rationale): %v", err)
		}
	})

	t.Run("accepted with both", func(t *testing.T) {
		if err := s.Resolve(t.Context(), Resolution{
			WorkID: workID, ExternalID: "F-block", Kind: KindAccepted,
			Rationale: "shipping behind a flag; issue 91", DecisionID: decisionID,
		}); err != nil {
			t.Fatalf("Resolve: %v", err)
		}
	})
}

// TestResolveReadsSeverityFromTheStore proves a caller cannot downgrade a
// blocker in its own resolution to skip the human decision.
func TestResolveReadsSeverityFromTheStore(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), []Finding{fnd(t, workID, "F-1", SeverityCritical)}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	// The resolution says nothing about severity, and the service must
	// still demand the blocker's ceremony.
	err := s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "F-1", Kind: KindAccepted})
	if !errors.Is(err, ErrRationaleRequired) {
		t.Fatalf("Resolve error = %v, want ErrRationaleRequired", err)
	}
}

func TestResolveFixedAndWithdrawn(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), []Finding{
		fnd(t, workID, "F-1", SeverityHigh),
		fnd(t, workID, "F-2", SeverityHigh),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "F-1", Kind: KindFixed}); err != nil {
		t.Fatalf("Resolve(fixed): %v", err)
	}
	if err := s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "F-2", Kind: KindWithdrawn}); err != nil {
		t.Fatalf("Resolve(withdrawn): %v", err)
	}
	blockers, err := s.Blockers(t.Context(), workID)
	if err != nil {
		t.Fatalf("Blockers: %v", err)
	}
	if len(blockers) != 0 {
		t.Fatalf("Blockers = %+v, want none", blockers)
	}
	// A second resolution would overwrite the first one's record.
	err = s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "F-1", Kind: KindWithdrawn})
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("second Resolve error = %v, want ErrAlreadyResolved", err)
	}
}

func TestResolveRejectsMalformedResolutions(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), []Finding{fnd(t, workID, "F-1", SeverityLow)}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "F-1", Kind: "handwaved"}); !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("Resolve(bad kind) error = %v, want ErrInvalidResolution", err)
	}
	// A rationale on a non-acceptance is a contradiction: nothing was
	// decided, so nothing needs justifying.
	err := s.Resolve(t.Context(), Resolution{
		WorkID: workID, ExternalID: "F-1", Kind: KindFixed, Rationale: "because",
	})
	if !errors.Is(err, ErrInvalidResolution) {
		t.Fatalf("Resolve(fixed with rationale) error = %v, want ErrInvalidResolution", err)
	}
	if err := s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "nope", Kind: KindFixed}); !errors.Is(err, ErrUnknownFinding) {
		t.Fatalf("Resolve(unknown) error = %v, want ErrUnknownFinding", err)
	}
}

func TestReopenRestoresFixedButNotAccepted(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), []Finding{
		fnd(t, workID, "F-fixed", SeverityHigh),
		fnd(t, workID, "F-accepted", SeverityHigh),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "F-fixed", Kind: KindFixed}); err != nil {
		t.Fatalf("Resolve(fixed): %v", err)
	}
	if err := s.Resolve(t.Context(), Resolution{
		WorkID: workID, ExternalID: "F-accepted", Kind: KindAccepted,
		Rationale: "documented deviation", DecisionID: mustActionID(t),
	}); err != nil {
		t.Fatalf("Resolve(accepted): %v", err)
	}
	if err := s.Reopen(t.Context(), workID, "F-fixed", "F-accepted"); err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	all, err := s.All(t.Context(), workID)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	states := map[string]State{}
	for _, f := range all {
		states[f.ExternalID] = f.State
	}
	if states["F-fixed"] != StateOpen {
		t.Fatalf("F-fixed = %q, want open after a repair that did not hold", states["F-fixed"])
	}
	if states["F-accepted"] != StateAccepted {
		t.Fatalf("F-accepted = %q; only a human undoes a human acceptance", states["F-accepted"])
	}
}

func TestDeviationsAreTheAcceptedBlockers(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	if err := s.Record(t.Context(), []Finding{
		fnd(t, workID, "F-block", SeverityCritical),
		fnd(t, workID, "F-fixed", SeverityHigh),
		fnd(t, workID, "F-minor", SeverityLow),
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	decisionID := mustActionID(t)
	if err := s.Resolve(t.Context(), Resolution{
		WorkID: workID, ExternalID: "F-block", Kind: KindAccepted,
		Rationale: "accepted for the release", DecisionID: decisionID,
	}); err != nil {
		t.Fatalf("Resolve(accepted): %v", err)
	}
	if err := s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "F-fixed", Kind: KindFixed}); err != nil {
		t.Fatalf("Resolve(fixed): %v", err)
	}
	if err := s.Resolve(t.Context(), Resolution{WorkID: workID, ExternalID: "F-minor", Kind: KindAccepted}); err != nil {
		t.Fatalf("Resolve(minor): %v", err)
	}
	devs, err := s.Deviations(t.Context(), workID)
	if err != nil {
		t.Fatalf("Deviations: %v", err)
	}
	if len(devs) != 1 {
		t.Fatalf("Deviations = %+v, want only the accepted blocker", devs)
	}
	if devs[0].ExternalID != "F-block" || devs[0].Rationale == "" || devs[0].DecisionID != string(decisionID) {
		t.Fatalf("deviation = %+v, want the rationale and decision recorded", devs[0])
	}
}

func TestRepairRoundsReachTheLimitAtThree(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	if n, err := s.RepairRounds(t.Context(), workID); err != nil || n != 0 {
		t.Fatalf("RepairRounds = %d, %v, want 0", n, err)
	}
	for round := 1; round <= 3; round++ {
		n, limit, err := s.FailRepair(t.Context(), workID)
		if err != nil {
			t.Fatalf("FailRepair(%d): %v", round, err)
		}
		if n != round {
			t.Fatalf("FailRepair(%d) counted %d", round, n)
		}
		if want := round >= RepairLimit; limit != want {
			t.Fatalf("round %d: limit reached = %v, want %v", round, limit, want)
		}
	}
	// A successful repair resets: the limit counts CONSECUTIVE failures.
	if err := s.ResetRepairs(t.Context(), workID); err != nil {
		t.Fatalf("ResetRepairs: %v", err)
	}
	if n, err := s.RepairRounds(t.Context(), workID); err != nil || n != 0 {
		t.Fatalf("RepairRounds after reset = %d, %v, want 0", n, err)
	}
	n, limit, err := s.FailRepair(t.Context(), workID)
	if err != nil || n != 1 || limit {
		t.Fatalf("FailRepair after reset = %d, %v, %v, want 1, false, nil", n, limit, err)
	}
}

func TestRepairRoundsAreScopedToTheirWork(t *testing.T) {
	s := newService(t)
	a := mustWorkID(t)
	b := mustWorkID(t)
	for i := 0; i < 3; i++ {
		if _, _, err := s.FailRepair(t.Context(), a); err != nil {
			t.Fatalf("FailRepair(a): %v", err)
		}
	}
	n, err := s.RepairRounds(t.Context(), b)
	if err != nil {
		t.Fatalf("RepairRounds(b): %v", err)
	}
	if n != 0 {
		t.Fatalf("work b inherited %d repair rounds from work a", n)
	}
}

func TestFromReportConvertsAndValidates(t *testing.T) {
	workID := mustWorkID(t)
	actionID := mustActionID(t)
	got, err := FromReport(workID, actionID, protocol.RoleSkeptic, []protocol.Finding{{
		ID: "S-1", Severity: protocol.SeverityHigh, Summary: "assumption unchecked",
		Evidence: []string{"design.md:12"}, Recommendation: "check it",
	}})
	if err != nil {
		t.Fatalf("FromReport: %v", err)
	}
	if len(got) != 1 || got[0].Role != protocol.RoleSkeptic || got[0].State != StateOpen {
		t.Fatalf("FromReport = %+v", got)
	}
	// An implementer does not raise findings.
	if _, err := FromReport(workID, actionID, protocol.RoleImplementer, []protocol.Finding{{
		ID: "X", Severity: protocol.SeverityLow, Summary: "s", Recommendation: "r",
	}}); err == nil {
		t.Fatal("FromReport(implementer) = nil error, want rejection")
	}
	// A malformed finding never reaches the store.
	if _, err := FromReport(workID, actionID, protocol.RoleReviewer, []protocol.Finding{{
		ID: "", Severity: protocol.SeverityLow, Summary: "s", Recommendation: "r",
	}}); err == nil {
		t.Fatal("FromReport with a blank id = nil error, want rejection")
	}
}

func TestRecordRejectsMalformedFindings(t *testing.T) {
	s := newService(t)
	workID := mustWorkID(t)
	bad := fnd(t, workID, "F-1", Severity("severe"))
	if err := s.Record(t.Context(), []Finding{bad}); err == nil {
		t.Fatal("Record with an unknown severity = nil error, want rejection")
	}
	all, err := s.All(t.Context(), workID)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("a refused Record left %d rows behind", len(all))
	}
}
