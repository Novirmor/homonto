package change

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/archive"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
)

func TestParseReproduction(t *testing.T) {
	tests := []struct {
		name string
		body string
		want Reproduction
	}{
		{"a reproducible command", "## Fix\n\nreproduce: go test ./internal/catalog\n",
			Reproduction{Command: "go test ./internal/catalog"}},
		{"a failing test", "- failing test: TestCacheKeyCollides\n",
			Reproduction{Test: "TestCacheKeyCollides"}},
		{"a recorded reason", "not automatable: it needs a live provider\n",
			Reproduction{Reason: "it needs a live provider"}},
		{"case and bullets are tolerated", "* Reproduce: make repro\n",
			Reproduction{Command: "make repro"}},
		{"nothing at all", "## Fix\n\nThe cache key collides.\n", Reproduction{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseReproduction([]byte(tt.body))
			if got != tt.want {
				t.Fatalf("ParseReproduction = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReproductionValidate(t *testing.T) {
	if err := (Reproduction{Command: "go test ./..."}).Validate(); err != nil {
		t.Errorf("a reproducible command was refused: %v", err)
	}
	if err := (Reproduction{Test: "TestX"}).Validate(); err != nil {
		t.Errorf("a failing test was refused: %v", err)
	}
	// A reason is not a reproduction. It is what a human approves an
	// EXCEPTION against, and treating it as satisfaction of the gate would
	// make the gate meaningless.
	if err := (Reproduction{Reason: "it needs a live provider"}).Validate(); err == nil {
		t.Error("a recorded reason satisfied the reproduction gate on its own")
	}
	if err := (Reproduction{}).Validate(); err == nil {
		t.Error("an empty reproduction satisfied the gate")
	}
}

// fixHarness confirms a Fix change and returns it.
func fixHarness(t *testing.T) (*harness, State) {
	t.Helper()
	h := newHarness(t)
	h.enableWork(t)
	st := h.confirm(t, "fix-cache", "The catalog cache returns stale rows after a restart.", PathFix)
	return h, st
}

// writeFix replaces the fix document's body.
func (h *harness) writeFix(t *testing.T, st State, body string) {
	t.Helper()
	path, err := st.DocumentPath(artifact.KindFix)
	if err != nil {
		t.Fatalf("DocumentPath: %v", err)
	}
	abs := filepath.Join(h.root, filepath.FromSlash(path))
	raw, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("read fix.md: %v", err)
	}
	doc, err := artifact.Parse(raw)
	if err != nil {
		t.Fatalf("parse fix.md: %v", err)
	}
	for i := range doc.Regions {
		if doc.Regions[i].Region == artifact.RegionWholeDocument {
			doc.Regions[i].Content = []byte(body)
		}
	}
	rendered, err := artifact.Render(doc)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := os.WriteFile(abs, rendered, 0o600); err != nil {
		t.Fatalf("write fix.md: %v", err)
	}
}

// walkTo drives a change until it offers something the predicate matches,
// answering everything else on the way.
func (h *harness) walkTo(t *testing.T, st State, match func(protocol.Action) bool) protocol.Action {
	t.Helper()
	for i := 0; i < 40; i++ {
		resp := h.next(t, st.WorkID)
		if resp.State == protocol.NextComplete {
			t.Fatal("the change finished without offering what was looked for")
		}
		for _, act := range resp.Actions {
			if match(act) {
				return act
			}
		}
		for _, act := range resp.Actions {
			h.answerChange(t, act, nil)
		}
	}
	t.Fatal("the change never offered what was looked for")
	return protocol.Action{}
}

// isDecision matches a decision gate of one kind.
func isDecision(kind decision.Kind) func(protocol.Action) bool {
	return func(act protocol.Action) bool {
		return act.Decision != nil && act.Decision.Kind == protocol.DecisionKind(kind)
	}
}

// TestFixCreatesItsOwnDocuments proves a Fix gets fix.md and tasks.md and
// nothing else: no proposal, no design, no plan.
func TestFixCreatesItsOwnDocuments(t *testing.T) {
	h, st := fixHarness(t)
	present := func(kind artifact.Kind) bool {
		path, err := st.DocumentPath(kind)
		if err != nil {
			t.Fatalf("DocumentPath(%s): %v", kind, err)
		}
		_, err = os.Stat(filepath.Join(h.root, filepath.FromSlash(path)))
		return err == nil
	}
	for _, kind := range []artifact.Kind{artifact.KindFix, artifact.KindTasks} {
		if !present(kind) {
			t.Errorf("a fix has no %s", kind)
		}
	}
	for _, kind := range []artifact.Kind{artifact.KindProposal, artifact.KindDesign, artifact.KindPlan} {
		if present(kind) {
			t.Errorf("a fix was given a %s; presets skip deep design and the full plan", kind)
		}
	}
}

// TestFixWithoutAReproductionAsksAHuman is the Fix gate: a defect is not
// implemented against until it has been shown to exist.
func TestFixWithoutAReproductionAsksAHuman(t *testing.T) {
	h, st := fixHarness(t)
	// The host writes a fix document that states no reproduction.
	draft := h.walkTo(t, st, func(act protocol.Action) bool {
		return act.Kind == protocol.KindEdit &&
			artifact.Kind(act.Edit.Kind) == artifact.KindFix
	})
	h.writeFix(t, st, "## Fix\n\nThe cache returns stale rows. Root cause: the cache key.\n")
	if _, err := h.engine.AcceptEdit(t.Context(), draft.ID, draft.Edit.GrantToken); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}

	gate := h.walkTo(t, st, isDecision(decision.KindReproductionException))
	if !strings.Contains(gate.Decision.Prompt, "no reason was recorded") {
		t.Fatalf("the gate does not say what is missing:\n%s", gate.Decision.Prompt)
	}
	// Accepting the exception requires a rationale; sending it back does
	// not, because asking for a reproduction needs no justification.
	choices := map[string]bool{}
	for _, c := range gate.Decision.Choices {
		choices[c.Value] = c.RequiresRationale
	}
	if !choices["accept"] {
		t.Error("accepting a fix with no reproduction needs a rationale")
	}
	if choices["reproduce"] {
		t.Error("asking for a reproduction should not need a rationale")
	}
	if _, err := h.engine.Decide(t.Context(), decision.Submission{
		ActionID: gate.ID, FreshnessToken: gate.FreshnessToken, Choice: "accept",
	}); err == nil {
		t.Fatal("the exception was accepted with no rationale")
	}
}

// TestFixWithAReproductionSkipsTheGate proves the gate is about evidence,
// not ceremony.
func TestFixWithAReproductionSkipsTheGate(t *testing.T) {
	h, st := fixHarness(t)
	draft := h.walkTo(t, st, func(act protocol.Action) bool {
		return act.Kind == protocol.KindEdit &&
			artifact.Kind(act.Edit.Kind) == artifact.KindFix
	})
	h.writeFix(t, st,
		"## Fix\n\nreproduce: go test ./internal/catalog -run TestStaleRows\n\n"+
			"Expected: fresh rows. Actual: stale rows. Root cause: the cache key.\n")
	if _, err := h.engine.AcceptEdit(t.Context(), draft.ID, draft.Edit.GrantToken); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
	// Nothing asks about a reproduction from here on.
	final := h.driveChange(t, st.WorkID, func(resp protocol.NextResponse) bool {
		for _, act := range resp.Actions {
			if act.Decision != nil &&
				act.Decision.Kind == protocol.DecisionKind(decision.KindReproductionException) {
				t.Fatal("a fix with a reproduction was still asked to justify one")
			}
		}
		return false
	})
	if Step(final.Step) != StepArchived {
		t.Fatalf("final step = %s, want archived", final.Step)
	}
}

// TestSendingAFixBackForAReproduction proves the other answer works.
func TestSendingAFixBackForAReproduction(t *testing.T) {
	h, st := fixHarness(t)
	draft := h.walkTo(t, st, func(act protocol.Action) bool {
		return act.Kind == protocol.KindEdit &&
			artifact.Kind(act.Edit.Kind) == artifact.KindFix
	})
	h.writeFix(t, st, "## Fix\n\nnot automatable: it needs a live provider\n")
	if _, err := h.engine.AcceptEdit(t.Context(), draft.ID, draft.Edit.GrantToken); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
	gate := h.walkTo(t, st, isDecision(decision.KindReproductionException))
	if !strings.Contains(gate.Decision.Prompt, "it needs a live provider") {
		t.Fatalf("the gate does not show the stated reason:\n%s", gate.Decision.Prompt)
	}
	if _, err := h.engine.Decide(t.Context(), decision.Submission{
		ActionID: gate.ID, FreshnessToken: gate.FreshnessToken, Choice: "reproduce",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if _, err := h.engine.Next(t.Context(), st.WorkID); err != nil {
		t.Fatalf("Next: %v", err)
	}
	after, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if Step(after.Step) != StepPresetOpenDraft && Step(after.Step) != StepPresetReproduce {
		t.Fatalf("step = %s, want the fix back in drafting", after.Step)
	}
}

// TestFixReachesTheArchive walks a whole Fix: all four roles, the
// reproduction gate, implementation, integration, checks, review, and one
// archived change directory with the preset's own documents.
func TestFixReachesTheArchive(t *testing.T) {
	h, st := fixHarness(t)
	roles := map[protocol.Role]bool{}
	for i := 0; i < 50; i++ {
		resp := h.next(t, st.WorkID)
		if resp.State == protocol.NextComplete {
			break
		}
		for _, act := range resp.Actions {
			if act.Role != "" {
				roles[act.Role] = true
			}
			h.answerChange(t, act, nil)
		}
	}
	final, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if Step(final.Step) != StepArchived {
		t.Fatalf("final step = %s, want archived", final.Step)
	}
	for _, role := range []protocol.Role{
		protocol.RoleExplorer, protocol.RoleImplementer,
		protocol.RoleReviewer, protocol.RoleSkeptic,
	} {
		if !roles[role] {
			t.Errorf("the %s role was never assigned", role)
		}
	}
	entries, err := os.ReadDir(filepath.Join(h.root, filepath.FromSlash(archive.ChangesArchiveDir)))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("archive holds %d entries, want 1", len(entries))
	}
	dir := filepath.Join(h.root, filepath.FromSlash(archive.ChangesArchiveDir), entries[0].Name())
	for _, file := range []string{"fix.md", "tasks.md", "verification.md", "record.md"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Errorf("the archived fix has no %s: %v", file, err)
		}
	}
	for _, file := range []string{"proposal.md", "design.md", "plan.md"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
			t.Errorf("the archived fix has a %s; presets use lightweight documents", file)
		}
	}
}

// TestPresetTripwireOffersContinueOrUpgrade proves the preset pause is a
// question, not an upgrade.
func TestPresetTripwireOffersContinueOrUpgrade(t *testing.T) {
	h, st := fixHarness(t)
	// Six counted source files: over the warning threshold.
	for i := 0; i < 6; i++ {
		h.env.diff = append(h.env.diff, pathclass.DiffEntry{
			Member: ".", Path: "src/" + string(rune('a'+i)) + ".go", Op: pathclass.OpModified,
		})
	}
	gate := h.walkTo(t, st, isDecision(decision.KindPresetTripwire))
	if !strings.Contains(gate.Decision.Prompt, "not an automatic upgrade") {
		t.Fatalf("the gate does not say the count is a warning:\n%s", gate.Decision.Prompt)
	}
	choices := map[string]bool{}
	for _, c := range gate.Decision.Choices {
		choices[c.Value] = c.RequiresRationale
	}
	for _, want := range []string{"continue", "upgrade"} {
		rationale, offered := choices[want]
		if !offered {
			t.Errorf("the tripwire gate offers no %q choice", want)
			continue
		}
		if !rationale {
			t.Errorf("choosing %q needs a rationale: it is a decision a maintainer may question", want)
		}
	}
	// Continuing keeps the preset and records the broader scope.
	if _, err := h.engine.Decide(t.Context(), decision.Submission{
		ActionID: gate.ID, FreshnessToken: gate.FreshnessToken,
		Choice: "continue", Rationale: "the six files are one mechanical rename",
	}); err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if _, err := h.engine.Next(t.Context(), st.WorkID); err != nil {
		t.Fatalf("Next: %v", err)
	}
	after, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if after.Path != PathFix {
		t.Fatalf("continuing changed the path to %s", after.Path)
	}
	if index(after.Path, Step(after.Step)) < index(after.Path, StepPresetImplement) {
		t.Fatalf("step = %s, want the preset to have continued into implementation", after.Step)
	}
}
