package ontocli

// Full-lifecycle conformance suite for the onto binary (N7). These tests
// drive the real CLI through NewRootCmd() — the same in-process pattern as
// the rest of the package — and assert two things the roadmap's "B1 gates
// actually reject bad work" conformance calls for:
//
//  1. the happy path completes: new → set decisions → advance through every
//     phase → close/archive, with `onto state --json` reflecting each move;
//  2. the gates REJECT bad work: a missing required artifact blocks advance,
//     an invalid workflow is refused with no scaffolding, bad enum/guides
//     values write nothing, and malformed/missing state is classified (never
//     silently dropped, F14).
//
// They reuse the existing package helpers (prepWorkspace, seedChange,
// seedDocsLayout, commitAll, runOnto, writeFile) so nothing here forks the
// fixtures the unit tests already trust.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// stateView is the subset of `onto state --json` output the conformance
// tests assert on: the persisted gated core plus the derived phase.
type stateView struct {
	Phase        string `json:"phase"`
	Workflow     string `json:"workflow"`
	Isolation    string `json:"isolation"`
	BuildMode    string `json:"build_mode"`
	TDDMode      string `json:"tdd_mode"`
	DerivedPhase string `json:"derived_phase"`
}

// readStateJSON runs `onto state <name> --json` and decodes it. `state` is
// ungated and prints only JSON to stdout on success, so the combined buffer
// runOnto returns is the JSON document.
func readStateJSON(t *testing.T, root, name string) stateView {
	t.Helper()
	out, err := runOnto(t, "state", name, "--json", "--dir", root)
	if err != nil {
		t.Fatalf("onto state --json %s: %v\n%s", name, err, out)
	}
	var v stateView
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("decoding onto state --json output %q: %v", out, err)
	}
	return v
}

// TestConformance_FullLifecycle_HappyPath drives one change end to end
// through the real CLI: new (--workflow full) scaffolds the open-phase
// skeleton, `set` records the isolation/build-mode/tdd-mode decisions, four
// `advance` calls walk open→design→build→verify→close, and `close` archives
// it. `onto state --json` is asserted after each transition so the gate is
// verified through the same read path an agent would use.
func TestConformance_FullLifecycle_HappyPath(t *testing.T) {
	root := prepWorkspace(t)
	const name = "lifecycle"
	changeDir := filepath.Join(root, "docs", "changes", name)

	// new --workflow full → open phase with the base skeleton.
	if _, err := runOnto(t, "new", name, "--workflow", "full", "--dir", root); err != nil {
		t.Fatalf("onto new: %v", err)
	}
	for _, f := range ontostate.RequiredArtifacts("open", "full") {
		if _, err := os.Stat(filepath.Join(changeDir, f)); err != nil {
			t.Fatalf("open-phase required artifact %s missing after new: %v", f, err)
		}
	}
	if v := readStateJSON(t, root, name); v.Phase != "open" || v.Workflow != "full" || v.DerivedPhase != "open" {
		t.Fatalf("after new: %+v, want phase=open workflow=full derived=open", v)
	}

	// Record the gated decisions and confirm they persisted. base-branch must
	// name the branch this test really runs on: close resolves it against git,
	// and base-ref must be a real commit (close and scale resolve it too).
	baseBranch, err := gitOutput(t, root, "branch", "--show-current")
	if err != nil || baseBranch == "" {
		t.Fatalf("determining base branch: %v (%q)", err, baseBranch)
	}
	baseRef, err := gitOutput(t, root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("determining base ref: %v", err)
	}
	for _, d := range [][2]string{{"isolation", "worktree"}, {"build-mode", "subagent"}, {"tdd-mode", "tdd"}, {"integration", "merge"}, {"base-ref", baseRef}, {"base-branch", baseBranch}} {
		if _, err := runOnto(t, "set", d[0], name, d[1], "--dir", root); err != nil {
			t.Fatalf("onto set %s %s: %v", d[0], d[1], err)
		}
	}
	if v := readStateJSON(t, root, name); v.Isolation != "worktree" || v.BuildMode != "subagent" || v.TDDMode != "tdd" {
		t.Fatalf("decisions not recorded in state: %+v", v)
	}

	// Deliverables are written progressively, phase by phase, so the derived
	// WORKING phase (artifact-based since gate-enforcement) agrees with the
	// claimed phase at every assertion below.

	// Walk the phases. Commit before each advance so the worktree is clean:
	// the verify→close transition (and close itself) refuse a dirty worktree,
	// so a committed tree makes the whole lifecycle deterministic. Before
	// leaving verify, record verify.result=pass so the advance evidence gate is
	// satisfied (isolation, needed to enter build, was set above).
	if _, err := runOnto(t, "set", "proposal-approved", name, "2026-07-22 approved", "--dir", root); err != nil {
		t.Fatalf("onto set proposal-approved: %v", err)
	}
	for _, want := range []string{"design", "build", "verify", "close"} {
		switch want {
		case "build":
			// design's deliverables: a confirmed design + the derived (still
			// unchecked) task list. plan.md is build's own deliverable but is
			// required to LEAVE build, so write it now too.
			writeFile(t, filepath.Join(changeDir, "design.md"), "Status: Confirmed\n")
			writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [ ] done\n")
			writeFile(t, filepath.Join(changeDir, "plan.md"), "plan")
		case "verify":
			// build's exit: every task checked.
			writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] done\n")
		case "close":
			// verify's exit: the report with its machine-read Result line.
			writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
		}
		if want == "build" {
			if _, err := runOnto(t, "set", "approach-confirmed", name, "2026-07-22 approach", "--dir", root); err != nil {
				t.Fatalf("onto set approach-confirmed: %v", err)
			}
		}
		if want == "close" {
			if _, err := runOnto(t, "set", "verify-result", name, "pass", "--dir", root); err != nil {
				t.Fatalf("onto set verify-result pass: %v", err)
			}
		}
		commitAll(t, root, "seed before advance to "+want)
		if _, err := runOnto(t, "advance", name, "--dir", root); err != nil {
			t.Fatalf("onto advance to %s: %v", want, err)
		}
		if v := readStateJSON(t, root, name); v.Phase != want || v.DerivedPhase != want {
			t.Fatalf("after advance to %s: %+v, want phase=%s derived=%s", want, v, want, want)
		}
	}

	// Record the remaining close-phase evidence a full workflow requires.
	// verify.result=pass and integration=merge were set above.
	if _, err := runOnto(t, "set", "guides", name, "updated", "--dir", root); err != nil {
		t.Fatalf("onto set guides updated: %v", err)
	}
	if _, err := runOnto(t, "set", "close-confirmed", name, "2026-07-22 confirmed", "--dir", root); err != nil {
		t.Fatalf("onto set close-confirmed: %v", err)
	}
	if _, err := runOnto(t, "merge-deltas", name, "--dir", root); err != nil {
		t.Fatalf("onto merge-deltas: %v", err)
	}

	// close archives the change: the directory moves under archive/ and the
	// archived state is marked Archived, phase unchanged at close. Integration
	// is captured from a real change branch so the receipt below can be a real
	// no-ff merge commit.
	checkoutChangeBranch(t, root, name)
	commitAll(t, root, "seed before close")
	if _, err := runOnto(t, "close", name, "--dir", root); err != nil {
		t.Fatalf("onto close: %v", err)
	}
	if _, err := os.Stat(changeDir); !os.IsNotExist(err) {
		t.Errorf("change dir still present after close, stat err = %v, want IsNotExist", err)
	}
	archiveDir := filepath.Join(root, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-"+name)
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading archived onto-state.yaml: %v", err)
	}
	if !st.Archived {
		t.Errorf("archived state Archived = false, want true")
	}
	if st.Phase != "close" {
		t.Errorf("archived state Phase = %q, want close", st.Phase)
	}
	if st.Integration != "merge" {
		t.Errorf("archived state Integration = %q, want merge", st.Integration)
	}
	if v := readStateJSON(t, root, name); v.DerivedPhase != "close" {
		t.Fatalf("archived change must remain at close while integration is pending: %+v", v)
	}
	// Commit the archive move first: merging a branch whose files the working
	// tree shows as uncommitted deletions would be refused by git.
	commitAll(t, root, "archive "+name)
	mergeSHA := mergeChangeBranch(t, root, baseBranch, "change/"+name)
	if _, err := runOnto(t, "complete-integration", name, "--receipt", "merge:"+mergeSHA, "--dir", root); err != nil {
		t.Fatalf("onto complete-integration: %v", err)
	}
	if v := readStateJSON(t, root, name); v.DerivedPhase != "done" {
		t.Fatalf("completed integration must derive done: %+v", v)
	}
}

// TestConformance_AdvanceRejectsMissingRequiredArtifact confirms the
// advance gate refuses a transition whose current phase is missing one of
// its own cumulative deliverables: a build-phase change without plan.md
// cannot advance to verify, the error names the missing file, and the phase
// is left untouched (no partial write).
func TestConformance_AdvanceRejectsMissingRequiredArtifact(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build") // seeds base + design.md + plan.md
	changeDir := filepath.Join(root, "docs", "changes", "c")
	if err := os.Remove(filepath.Join(changeDir, "plan.md")); err != nil {
		t.Fatalf("removing plan.md: %v", err)
	}
	commitAll(t, root, "seed change without plan.md")

	out, err := runOnto(t, "advance", "c", "--dir", root)
	if err == nil {
		t.Fatal("onto advance succeeded with plan.md missing, want rejection")
	}
	if !strings.Contains(out+err.Error(), "plan.md") {
		t.Errorf("error = %q / out = %q, want it to name plan.md", err, out)
	}

	st, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "build" {
		t.Errorf("Phase = %q, want unchanged build after rejected advance", st.Phase)
	}
}

// TestConformance_NewRejectsInvalidWorkflow confirms `onto new` refuses an
// unrecognized --workflow and scaffolds nothing: the change directory must
// not exist after the rejection.
func TestConformance_NewRejectsInvalidWorkflow(t *testing.T) {
	root := setUpGatedWorkspace(t)

	out, err := runOnto(t, "new", "c", "--workflow", "bogus", "--dir", root)
	if err == nil {
		t.Fatal("onto new --workflow bogus succeeded, want rejection")
	}
	if !strings.Contains(out+err.Error(), "workflow") {
		t.Errorf("error = %q / out = %q, want it to name the workflow field", err, out)
	}
	if _, statErr := os.Stat(filepath.Join(root, "docs", "changes", "c")); !os.IsNotExist(statErr) {
		t.Errorf("change dir created despite invalid workflow, stat err = %v, want IsNotExist", statErr)
	}
}

// TestConformance_SetRejectsBadEnumValue_NoWrite confirms an enum setter
// refuses an out-of-set value and writes nothing to the state.
func TestConformance_SetRejectsBadEnumValue_NoWrite(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")

	if _, err := runOnto(t, "set", "build-mode", "c", "telepathy", "--dir", root); err == nil {
		t.Fatal("onto set build-mode telepathy succeeded, want rejection")
	}
	st, err := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if err != nil {
		t.Fatalf("LoadChange: %v", err)
	}
	if st.BuildMode != "" {
		t.Errorf("BuildMode = %q, want unchanged empty after rejected write", st.BuildMode)
	}
}

// TestConformance_SetRejectsBadGuides_NoWrite confirms the guides setter —
// which is not a fixed enum because "waived:<reason>" is a prefix — rejects
// a non-member value and a bare/blank waiver with no reason, writing nothing.
func TestConformance_SetRejectsBadGuides_NoWrite(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "close")

	for _, bad := range []string{"done", "waived:", "waived:   "} {
		if _, err := runOnto(t, "set", "guides", "c", bad, "--dir", root); err == nil {
			t.Fatalf("onto set guides %q succeeded, want rejection", bad)
		}
		st, err := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
		if err != nil {
			t.Fatalf("LoadChange after %q: %v", bad, err)
		}
		if st.Guides != "" {
			t.Errorf("after rejected guides %q: Guides = %q, want unchanged empty", bad, st.Guides)
		}
	}
}

// TestConformance_StatusAndDoctorClassifyBadState confirms F14: a change
// directory whose state file was deleted (missing-state) and one whose state
// is malformed are both surfaced — never silently dropped — by the two
// read-only diagnostics. status classifies without failing (exit 0); doctor
// treats them as findings and exits non-zero.
func TestConformance_StatusAndDoctorClassifyBadState(t *testing.T) {
	root := t.TempDir()
	seedDocsLayout(t, root) // so doctor's only findings are the two below

	// missing-state: a change directory that exists but has no state file.
	if err := os.MkdirAll(filepath.Join(root, "docs", "changes", "gone"), 0o755); err != nil {
		t.Fatalf("mkdir gone: %v", err)
	}
	// malformed: a state file that does not parse.
	writeFile(t, filepath.Join(root, "docs", "changes", "broken", "onto-state.yaml"), "a: b: c\n")

	statusOut, err := runOnto(t, "status", "--dir", root)
	if err != nil {
		t.Fatalf("onto status: %v\n%s", err, statusOut)
	}
	assertClassified(t, "status", statusOut)

	doctorOut, err := runOnto(t, "doctor", "--dir", root)
	if err == nil {
		t.Fatalf("onto doctor exited 0 with missing-state + malformed changes, want non-zero\n%s", doctorOut)
	}
	assertClassified(t, "doctor", doctorOut)
}

// assertClassified checks that out names both the missing-state change and
// the malformed change with their classifications.
func assertClassified(t *testing.T, cmd, out string) {
	t.Helper()
	if !strings.Contains(out, "gone") || !strings.Contains(out, "missing-state") {
		t.Errorf("%s output = %q, want a gone missing-state row (F14)", cmd, out)
	}
	if !strings.Contains(out, "broken") || !strings.Contains(out, "malformed") {
		t.Errorf("%s output = %q, want a broken malformed row", cmd, out)
	}
}
