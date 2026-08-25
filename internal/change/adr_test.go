package change

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adr"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
)

func TestParseADRCandidates(t *testing.T) {
	body := []byte(strings.Join([]string{
		"## Design",
		"",
		"- adr-candidate: storage | Adopt the snapshot store | why is non-Git isolation a snapshot",
		"adr-candidate: keys | Key blobs by content | why are blobs content-addressed",
		"- adr-candidate: malformed without enough fields",
		"- a plain bullet",
		"",
	}, "\n"))
	got := ParseADRCandidates(body, fingerprint.Bytes("test", []byte("design")))
	if len(got) != 2 {
		t.Fatalf("ParseADRCandidates found %d candidates, want 2: %+v", len(got), got)
	}
	if got[0].ID != "storage" || got[0].Title != "Adopt the snapshot store" {
		t.Fatalf("first candidate = %+v", got[0])
	}
	if got[1].Question != "why are blobs content-addressed" {
		t.Fatalf("second candidate = %+v", got[1])
	}
	for _, c := range got {
		if err := c.Validate(); err != nil {
			t.Errorf("a parsed candidate is invalid: %v", err)
		}
	}
}

// TestChangeWithNoDecisionsOwesNoADR is the common case: a change that
// decided nothing durable closes without writing anything.
func TestChangeWithNoDecisionsOwesNoADR(t *testing.T) {
	h, st := tweakHarness(t)
	assessment, err := h.engine.AssessADRs(t.Context(), st)
	if err != nil {
		t.Fatalf("AssessADRs: %v", err)
	}
	if assessment.Owed() || assessment.Blocked() {
		t.Fatalf("a fresh tweak owed an ADR: %+v", assessment)
	}
	final := h.driveChange(t, st.WorkID, nil)
	if Step(final.Step) != StepArchived {
		t.Fatalf("final step = %s, want archived", final.Step)
	}
	// No ADR directory entries were created.
	entries, err := os.ReadDir(filepath.Join(h.root, filepath.FromSlash(adr.Dir)))
	if err == nil && len(entries) > 0 {
		t.Fatalf("a change that decided nothing wrote %d ADR(s)", len(entries))
	}
}

// TestContinuedPresetOwesAnADRForItsDecision pins the spec's carve-out
// end to end: a preset continued past its tripwire must record why.
func TestContinuedPresetOwesAnADRForItsDecision(t *testing.T) {
	h, st := tweakHarness(t)
	// The tweak document declares the candidate the tripwire will settle.
	draft := h.walkTo(t, st, func(act protocol.Action) bool {
		return act.Kind == protocol.KindEdit &&
			artifact.Kind(act.Edit.Kind) == artifact.KindTweak
	})
	h.writeDocument(t, st, artifact.KindTweak,
		"## Tweak\n\nRename the flag.\n\n"+
			"- adr-candidate: flag-scope | Keep the flag public | why does the rename keep the old flag working\n")
	if _, err := h.engine.AcceptEdit(t.Context(), draft.ID, draft.Edit.GrantToken); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
	// Trip the file-count warning so the human has a tripwire to continue
	// past, and point the gate at the candidate.
	for i := 0; i < 6; i++ {
		h.env.diff = append(h.env.diff, pathclass.DiffEntry{
			Member: ".", Path: "src/" + string(rune('a'+i)) + ".go", Op: pathclass.OpModified,
		})
	}
	h.env.tripwireCandidate = "flag-scope"

	gate := h.walkTo(t, st, isDecision(decision.KindPresetTripwire))
	if _, err := h.engine.Decide(t.Context(), decision.Submission{
		ActionID: gate.ID, FreshnessToken: gate.FreshnessToken,
		Choice: "continue", Rationale: "the old flag stays as an alias",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}

	// The ADR assignment is issued, with an allocated numbered path.
	write := h.walkTo(t, st, func(act protocol.Action) bool {
		return act.Role == protocol.RoleImplementer && strings.HasPrefix(act.Reason, "write the decision record")
	})
	if len(write.WriteScope.Paths) != 1 {
		t.Fatalf("the ADR assignment scope = %v, want one allocated path", write.WriteScope.Paths)
	}
	rel := write.WriteScope.Paths[0]
	if n, ok := adr.Number(rel); !ok || n != 1 {
		t.Fatalf("the ADR path %q is not the first four-digit number", rel)
	}
	if !strings.Contains(write.Prompt, "## Consequences") {
		t.Fatalf("the ADR prompt carries no skeleton:\n%s", write.Prompt)
	}

	// An empty reservation is not a record: closing over it is refused.
	if _, err := h.engine.SubmitReport(t.Context(), reportFor(t, write)); err != nil {
		t.Fatalf("SubmitReport: %v", err)
	}
	if _, err := h.engine.Next(t.Context(), st.WorkID); err == nil {
		t.Fatal("the change closed over an empty ADR reservation")
	}

	// Writing a real record lets it close.
	abs := filepath.Join(h.root, filepath.FromSlash(rel))
	if err := os.WriteFile(abs, []byte(adr.Template(adr.Candidate{
		ID: "flag-scope", Title: "Keep the flag public",
		Question: "why does the rename keep the old flag working",
	}, adr.Record{Choice: "keep the old flag as an alias"}, "2026-08-25")), 0o644); err != nil {
		t.Fatalf("write ADR: %v", err)
	}
	final := h.driveChange(t, st.WorkID, nil)
	if Step(final.Step) != StepArchived {
		t.Fatalf("final step = %s, want archived", final.Step)
	}
}

// reportFor builds a canned implementer submission for one action.
func reportFor(t *testing.T, act protocol.Action) protocol.ReportSubmission {
	t.Helper()
	raw := mustJSON(t, implementerReport(act))
	return protocol.ReportSubmission{
		ProtocolVersion: protocol.CurrentVersion,
		ActionID:        act.ID, FreshnessToken: act.FreshnessToken,
		Role: act.Role, Session: session(t), Report: raw,
	}
}

// TestUndesignedDecisionReturnsAFullChangeToDesign proves Close does not
// write an ADR for a decision nobody designed.
//
// The case is an UPGRADED preset. A Fix accepted without a reproduction
// carries a durable decision into its new Full life; if the design it then
// wrote identifies no ADR candidate, the design does not contain the
// question that decision answered, and Close sends the change back rather
// than documenting an accident.
func TestUndesignedDecisionReturnsAFullChangeToDesign(t *testing.T) {
	h := newHarness(t)
	h.enableWork(t)
	st := h.confirm(t, "fix-cache", "The cache returns stale rows.", PathFix)

	// A fix that records no reproduction, accepted by a human.
	draft := h.walkTo(t, st, func(act protocol.Action) bool {
		return act.Kind == protocol.KindEdit &&
			artifact.Kind(act.Edit.Kind) == artifact.KindFix
	})
	h.writeDocument(t, st, artifact.KindFix, "## Fix\n\nThe cache returns stale rows.\n")
	if _, err := h.engine.AcceptEdit(t.Context(), draft.ID, draft.Edit.GrantToken); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
	gate := h.walkTo(t, st, isDecision(decision.KindReproductionException))
	if _, err := h.engine.Decide(t.Context(), decision.Submission{
		ActionID: gate.ID, FreshnessToken: gate.FreshnessToken,
		Choice: "accept", Rationale: "it needs a live provider",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	cur, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	// Upgrading carries the decision into a Full change whose design has
	// not yet identified anything.
	upgraded, err := h.engine.UpgradePreset(t.Context(), cur, UpgradeDecision{
		Rationale: "the cache is part of the public contract",
	})
	if err != nil {
		t.Fatalf("UpgradePreset: %v", err)
	}
	assessment, err := h.engine.AssessADRs(t.Context(), upgraded)
	if err != nil {
		t.Fatalf("AssessADRs: %v", err)
	}
	if !assessment.Blocked() {
		t.Fatalf("a decision the new design never identified did not block: %+v", assessment)
	}
	if len(assessment.Undesigned) == 0 {
		t.Fatalf("assessment = %+v, want the exception reported as undesigned", assessment)
	}
	if assessment.Owed() {
		t.Fatalf("an undesigned decision produced an ADR requirement: %+v", assessment)
	}
}
