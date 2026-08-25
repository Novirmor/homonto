package change

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/archive"
	"github.com/noviopenworks/homonto/internal/artifact"
	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/pathclass"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// tweakHarness confirms a Tweak change and returns it.
func tweakHarness(t *testing.T) (*harness, State) {
	t.Helper()
	h := newHarness(t)
	h.enableWork(t)
	st := h.confirm(t, "rename-flag", "Rename the --verbose flag to --debug.", PathTweak)
	return h, st
}

// TestTweakCreatesItsOwnDocuments proves a Tweak gets tweak.md and
// tasks.md and nothing heavier.
func TestTweakCreatesItsOwnDocuments(t *testing.T) {
	h, st := tweakHarness(t)
	present := func(kind artifact.Kind) bool {
		path, err := st.DocumentPath(kind)
		if err != nil {
			t.Fatalf("DocumentPath(%s): %v", kind, err)
		}
		_, err = os.Stat(filepath.Join(h.root, filepath.FromSlash(path)))
		return err == nil
	}
	for _, kind := range []artifact.Kind{artifact.KindTweak, artifact.KindTasks} {
		if !present(kind) {
			t.Errorf("a tweak has no %s", kind)
		}
	}
	for _, kind := range []artifact.Kind{artifact.KindFix, artifact.KindProposal, artifact.KindDesign, artifact.KindPlan} {
		if present(kind) {
			t.Errorf("a tweak was given a %s", kind)
		}
	}
}

// TestTweakReachesTheArchive walks a whole Tweak.
func TestTweakReachesTheArchive(t *testing.T) {
	h, st := tweakHarness(t)
	roles := map[protocol.Role]bool{}
	steps := map[Step]bool{}
	for i := 0; i < 50; i++ {
		resp := h.next(t, st.WorkID)
		if resp.State == protocol.NextComplete {
			break
		}
		cur, err := h.engine.State(t.Context(), st.WorkID)
		if err != nil {
			t.Fatalf("State: %v", err)
		}
		steps[Step(cur.Step)] = true
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
	// A tweak never reproduces: it has no defect.
	if steps[StepPresetReproduce] {
		t.Error("a tweak was routed through the reproduction step")
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
	for _, file := range []string{"tweak.md", "tasks.md", "verification.md", "record.md"} {
		if _, err := os.Stat(filepath.Join(dir, file)); err != nil {
			t.Errorf("the archived tweak has no %s: %v", file, err)
		}
	}
}

// TestEverySemanticSignalTripsAPreset proves each of the spec's signals
// pauses a preset, not just the file count.
func TestEverySemanticSignalTripsAPreset(t *testing.T) {
	for _, signal := range pathclass.SemanticSignals() {
		t.Run(string(signal), func(t *testing.T) {
			h := newHarness(t)
			h.enableWork(t)
			// The signal is reported during preflight, so the human is
			// choosing the preset against Homonto's own suggestion.
			pre, _, err := h.engine.StartPreflight(t.Context(), PreflightInput{
				Name: "rename-flag", Request: "Rename the --verbose flag.",
			})
			if err != nil {
				t.Fatalf("StartPreflight: %v", err)
			}
			h.assess(t, pre, signal)
			st, err := h.engine.ConfirmPreflight(t.Context(), ConfirmInput{
				WorkID: pre.WorkID, Path: PathTweak,
				Rationale: "it really is just a rename",
			})
			if err != nil {
				t.Fatalf("ConfirmPreflight: %v", err)
			}
			gate := h.walkTo(t, st, isDecision(decision.KindPresetTripwire))
			if !strings.Contains(gate.Decision.Prompt, string(signal)) &&
				!strings.Contains(gate.Decision.Prompt, evidenceFragment(signal)) {
				t.Fatalf("the tripwire gate does not explain %s:\n%s", signal, gate.Decision.Prompt)
			}
		})
	}
}

// evidenceFragment is a phrase the assessment's evidence uses for a
// signal, so the test asserts the human can read what fired.
func evidenceFragment(s pathclass.Signal) string {
	switch s {
	case pathclass.SignalNewCapability:
		return "new capability"
	case pathclass.SignalPublicAPI:
		return "public API"
	case pathclass.SignalStorageSchema:
		return "storage schema"
	case pathclass.SignalCrossModule:
		return "across modules"
	case pathclass.SignalArchitecture:
		return "architectural"
	case pathclass.SignalShouldSplit:
		return "split"
	case pathclass.SignalIntentExpansion:
		return "outgrown"
	}
	return string(s)
}

// TestUpgradeConvertsTheArtifactsExactly is the heart of the upgrade: what
// survives, what freezes, and what is created.
func TestUpgradeConvertsTheArtifactsExactly(t *testing.T) {
	h, st := tweakHarness(t)
	// Walk to the draft and write a task list, so there is something to
	// freeze.
	draft := h.walkTo(t, st, func(act protocol.Action) bool {
		return act.Kind == protocol.KindEdit &&
			artifact.Kind(act.Edit.Kind) == artifact.KindTasks
	})
	h.writeDocument(t, st, artifact.KindTasks, "## Tasks\n\n- [ ] rename the flag\n- [ ] update the docs\n")
	if _, err := h.engine.AcceptEdit(t.Context(), draft.ID, draft.Edit.GrantToken); err != nil {
		t.Fatalf("AcceptEdit: %v", err)
	}
	before, err := h.engine.State(t.Context(), st.WorkID)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	workBaseline := append([]fingerprint.Digest(nil), before.Baseline.Work...)

	upgraded, err := h.engine.UpgradePreset(t.Context(), before, UpgradeDecision{
		Rationale: "it turns out the flag is part of the public API",
	})
	if err != nil {
		t.Fatalf("UpgradePreset: %v", err)
	}

	if upgraded.Path != PathFull {
		t.Fatalf("path = %s, want full", upgraded.Path)
	}
	if upgraded.UpgradedFrom != PathTweak {
		t.Fatalf("upgraded_from = %q, want tweak", upgraded.UpgradedFrom)
	}
	if Step(upgraded.Step) != StepDesignDraft {
		t.Fatalf("step = %s, want design_draft: the upgrade rewinds to Design", upgraded.Step)
	}
	if upgraded.Generation <= before.Generation {
		t.Fatal("the upgrade did not open a new generation")
	}

	// tweak.md survives as a read-only input.
	body := h.readDocument(t, upgraded, artifact.KindTweak)
	if body == "" {
		t.Fatal("tweak.md was removed; it is the record of what the change was before it grew")
	}
	// The old task list is frozen under its own kind.
	frozen := h.readDocument(t, upgraded, artifact.KindPresetTasks)
	if !strings.Contains(frozen, "rename the flag") {
		t.Fatalf("preset-tasks.md does not carry the frozen list:\n%s", frozen)
	}
	if !strings.Contains(frozen, "read-only") {
		t.Fatalf("preset-tasks.md does not say it is frozen:\n%s", frozen)
	}
	// Nobody may edit a frozen preset task list.
	if _, _, ok := artifact.Ownership(artifact.KindPresetTasks, artifact.PhaseDesign); ok {
		t.Error("a frozen preset task list is editable in Design")
	}
	// The live task list is cleared so Design writes a new one.
	live := h.readDocument(t, upgraded, artifact.KindTasks)
	if strings.Contains(live, "rename the flag") {
		t.Fatalf("the preset's task list survived as the live one:\n%s", live)
	}
	// proposal.md is created from the confirmed intent and the reason.
	proposal := h.readDocument(t, upgraded, artifact.KindProposal)
	for _, want := range []string{
		"Confirmed intent", "Why this became a full change",
		"part of the public API", "preset-tasks.md",
	} {
		if !strings.Contains(proposal, want) {
			t.Errorf("proposal.md does not carry %q:\n%s", want, proposal)
		}
	}
	// The immutable work baseline is carried across untouched: upgrading
	// is not a way to reset the ruler.
	if len(upgraded.Baseline.Work) != len(workBaseline) {
		t.Fatalf("the work baseline changed length: %v, want %v", upgraded.Baseline.Work, workBaseline)
	}
	for i := range workBaseline {
		if upgraded.Baseline.Work[i] != workBaseline[i] {
			t.Fatalf("the work baseline moved: %v, want %v", upgraded.Baseline.Work, workBaseline)
		}
	}
	// Preset evidence is stale and must not carry over.
	if upgraded.Baseline.Verification != "" {
		t.Fatal("the preset's verification digest survived the upgrade")
	}
}

// TestUpgradeRequiresARationaleAndAPreset proves the two refusals.
func TestUpgradeRequiresARationaleAndAPreset(t *testing.T) {
	h, st := tweakHarness(t)
	if _, err := h.engine.UpgradePreset(t.Context(), st, UpgradeDecision{}); err == nil {
		t.Fatal("an upgrade with no rationale was accepted")
	}
	full := h.confirm(t, "rework-catalog", "Replace the storage layer.",
		PathFull, pathclass.SignalArchitecture)
	if _, err := h.engine.UpgradePreset(t.Context(), full, UpgradeDecision{
		Rationale: "why not",
	}); !errors.Is(err, ErrNotAPreset) {
		t.Fatalf("UpgradePreset(full) error = %v, want ErrNotAPreset", err)
	}
}

// TestUpgradeThroughTheTripwireGate proves the human's choice at the
// tripwire actually upgrades, and that Design approval is required after.
func TestUpgradeThroughTheTripwireGate(t *testing.T) {
	h, st := tweakHarness(t)
	for i := 0; i < 6; i++ {
		h.env.diff = append(h.env.diff, pathclass.DiffEntry{
			Member: ".", Path: "src/" + string(rune('a'+i)) + ".go", Op: pathclass.OpModified,
		})
	}
	gate := h.walkTo(t, st, isDecision(decision.KindPresetTripwire))
	if _, err := h.engine.Decide(t.Context(), decision.Submission{
		ActionID: gate.ID, FreshnessToken: gate.FreshnessToken,
		Choice: "upgrade", Rationale: "six files is a different change",
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
	if after.Path != PathFull || after.UpgradedFrom != PathTweak {
		t.Fatalf("state = %+v, want an upgraded full change", after)
	}

	// Design approval is required before implementation continues.
	approval := h.walkTo(t, st, isDecision(decision.KindApproveDesign))
	if approval.ID == "" {
		t.Fatal("the upgraded change reached implementation without a design approval")
	}
	// And the record says where it came from.
	final := h.driveChange(t, st.WorkID, nil)
	if Step(final.Step) != StepArchived {
		t.Fatalf("final step = %s, want archived", final.Step)
	}
	entries, err := os.ReadDir(filepath.Join(h.root, filepath.FromSlash(archive.ChangesArchiveDir)))
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	record, err := os.ReadFile(filepath.Join(h.root,
		filepath.FromSlash(archive.ChangesArchiveDir), entries[0].Name(), "record.md"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if !strings.Contains(string(record), "began as a tweak preset") {
		t.Fatalf("the record does not say the change was upgraded:\n%s", record)
	}
}
