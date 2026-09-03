package ontocli

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

func runOnto(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestSetIsolation_HappyPath_WritesField(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")

	if _, err := runOnto(t, "set", "isolation", "c", "worktree", "--dir", root); err != nil {
		t.Fatalf("set isolation: %v", err)
	}
	st, err := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if err != nil {
		t.Fatalf("LoadChange: %v", err)
	}
	if st.Isolation != "worktree" {
		t.Errorf("Isolation = %q, want worktree", st.Isolation)
	}
}

func TestSetIsolation_BadValue_RejectedNoWrite(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")

	out, err := runOnto(t, "set", "isolation", "c", "vm", "--dir", root)
	if err == nil {
		t.Fatal("set isolation vm succeeded, want rejection")
	}
	if !strings.Contains(out+err.Error(), "isolation") {
		t.Errorf("error = %q, want it to name the field", err)
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.Isolation != "" {
		t.Errorf("Isolation = %q, want unchanged empty after rejected write", st.Isolation)
	}
}

func TestSetEnumSetters_HappyPaths(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")

	cases := []struct {
		field, value string
		read         func(ontostate.State) string
	}{
		{"build-mode", "subagent", func(s ontostate.State) string { return s.BuildMode }},
		{"tdd-mode", "tdd", func(s ontostate.State) string { return s.TDDMode }},
		{"verify-scale", "full", func(s ontostate.State) string { return s.Verify.Scale }},
		{"verify-result", "pass", func(s ontostate.State) string { return s.Verify.Result }},
	}
	for _, tc := range cases {
		if _, err := runOnto(t, "set", tc.field, "c", tc.value, "--dir", root); err != nil {
			t.Fatalf("set %s: %v", tc.field, err)
		}
		st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
		if got := tc.read(st); got != tc.value {
			t.Errorf("after set %s: got %q, want %q", tc.field, got, tc.value)
		}
	}
}

func TestSetCloseMerged_CannotForgeReceipt(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "close")

	if _, err := runOnto(t, "set", "close-merged", "c", "--dir", root); err == nil {
		t.Fatal("set close-merged forged an unverified merge receipt")
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.Close.Merged {
		t.Errorf("Close.Merged = true after refusal")
	}
}

func TestSetDirective_StoresVerbatim(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")

	const text = "ship without re-asking the isolation gate"
	if _, err := runOnto(t, "set", "directive", "c", text, "--dir", root); err != nil {
		t.Fatalf("set directive: %v", err)
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.Directive != text {
		t.Errorf("Directive = %q, want %q", st.Directive, text)
	}
}

func TestSetDirective_EmptyRejected(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")

	if _, err := runOnto(t, "set", "directive", "c", "", "--dir", root); err == nil {
		t.Fatal("empty directive accepted, want rejection")
	}
}

func TestSetBaseRef_HappyPath_WritesCanonicalCommit(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "open")
	head, err := gitOutput(t, root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runOnto(t, "set", "base-ref", "c", head, "--dir", root); err != nil {
		t.Fatalf("set base-ref: %v", err)
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.BaseRef != head {
		t.Errorf("BaseRef = %q, want %q", st.BaseRef, head)
	}
	// An abbreviated spelling of the same commit is canonicalized, not treated
	// as a different ref.
	short := head[:10]
	if _, err := runOnto(t, "set", "base-ref", "c", short, "--dir", root); err != nil {
		t.Fatalf("abbreviated base-ref must canonicalize to the same commit: %v", err)
	}
	st, _ = ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.BaseRef != head {
		t.Errorf("BaseRef after abbreviated replay = %q, want %q", st.BaseRef, head)
	}
	if _, err := runOnto(t, "set", "base-ref", "c", "deadbee", "--dir", root); err == nil {
		t.Fatal("unresolvable base-ref accepted")
	}
	// A different resolvable commit is an overwrite, not a replay.
	runGit(t, root, "commit", "--allow-empty", "-q", "-m", "second")
	next, err := gitOutput(t, root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "set", "base-ref", "c", next, "--dir", root); err == nil {
		t.Fatal("overwriting immutable base-ref succeeded")
	}
}

func TestSetBaseRef_EmptyRejected(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "open")

	if _, err := runOnto(t, "set", "base-ref", "c", "", "--dir", root); err == nil {
		t.Fatal("empty base-ref accepted, want rejection")
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.BaseRef != "" {
		t.Errorf("BaseRef = %q, want unchanged empty", st.BaseRef)
	}
}

func TestSetBaseBranch_HappyPathAndEmptyRejected(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "open")

	if _, err := runOnto(t, "set", "base-branch", "c", "release/v1", "--dir", root); err != nil {
		t.Fatalf("set base-branch: %v", err)
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.BaseBranch != "release/v1" {
		t.Errorf("BaseBranch = %q, want release/v1", st.BaseBranch)
	}
	if _, err := runOnto(t, "set", "base-branch", "c", "release/v1", "--dir", root); err != nil {
		t.Fatalf("replaying the same base-branch must be idempotent: %v", err)
	}
	if _, err := runOnto(t, "set", "base-branch", "c", "main", "--dir", root); err == nil {
		t.Fatal("overwriting immutable base-branch succeeded")
	}
	if _, err := runOnto(t, "set", "base-branch", "c", "", "--dir", root); err == nil {
		t.Fatal("empty base-branch accepted, want rejection")
	}
	for _, invalid := range []string{"-bad", "HEAD", "bad..name", "main@{1}"} {
		root := prepWorkspace(t)
		seedChange(t, root, "c", "open")
		if _, err := runOnto(t, "set", "base-branch", "c", invalid, "--dir", root); err == nil {
			t.Errorf("invalid branch %q accepted", invalid)
		}
	}
}

func TestSetWorkflow_OnlyUpgradesPresets(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "build")
	mutateState(t, root, "c", func(st *ontostate.State) { st.Workflow = "fix" })

	if _, err := runOnto(t, "set", "workflow", "c", "full", "--dir", root); err != nil {
		t.Fatalf("set workflow full: %v", err)
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.Workflow != "full" || !st.Observed.PresetEscalated {
		t.Fatalf("upgraded state = %+v, want full with preset_escalated", st)
	}
	if _, err := runOnto(t, "set", "workflow", "c", "tweak", "--dir", root); err == nil {
		t.Fatal("workflow downgrade accepted, want rejection")
	}
}

func TestSetWorkflow_InvalidatesPresetEvidence(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "close")
	mutateState(t, root, "c", func(st *ontostate.State) {
		st.Workflow = "fix"
		st.BuildMode = "direct"
		st.TDDMode = "tdd"
		st.Verify = ontostate.Verify{Scale: "light", Result: "pass"}
		st.Close = ontostate.Close{Merged: true}
		st.CloseConfirmed = "reviewed"
		st.Guides = "updated"
	})
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "verification.md"), "Result: pass\n")

	if _, err := runOnto(t, "set", "workflow", "c", "full", "--dir", root); err != nil {
		t.Fatalf("set workflow full: %v", err)
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.Phase != "design" || st.ApproachConfirmed != "" || st.BuildMode != "" || st.TDDMode != "" {
		t.Fatalf("upgrade did not return to an unanswered design/build state: %+v", st)
	}
	if st.Verify.Scale != "full" || st.Verify.Result != "pending" || st.Close.Merged || st.CloseConfirmed != "" || st.Guides != "pending" {
		t.Fatalf("upgrade preserved stale downstream evidence: %+v", st)
	}
	if got := ontostate.DeriveWorkingPhase(filepath.Join(root, "docs", "changes", "c"), st); got != "design" {
		t.Fatalf("derived phase after upgrade = %q, want design", got)
	}
}

func TestSetDeps_HappyPath_CollectsRepeatedFlag(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "open")

	if _, err := runOnto(t, "set", "deps", "c", "--dep", "dep-a", "--dep", "dep-b", "--dir", root); err != nil {
		t.Fatalf("set deps: %v", err)
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if !reflect.DeepEqual(st.Deps, []string{"dep-a", "dep-b"}) {
		t.Errorf("Deps = %v, want [dep-a dep-b]", st.Deps)
	}
}

func TestSetGuides_HappyPaths(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "close")

	for _, g := range []string{"pending", "updated", "waived: no user-facing surface"} {
		if _, err := runOnto(t, "set", "guides", "c", g, "--dir", root); err != nil {
			t.Fatalf("set guides %q: %v", g, err)
		}
		st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
		if st.Guides != g {
			t.Errorf("Guides = %q, want %q", st.Guides, g)
		}
	}
}

func TestSetGuides_BadValueRejectedNoWrite(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "close")

	if _, err := runOnto(t, "set", "guides", "c", "done", "--dir", root); err == nil {
		t.Fatal("set guides done accepted, want rejection")
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.Guides != "" {
		t.Errorf("Guides = %q, want unchanged empty", st.Guides)
	}
}

// TestSetEvidenceTokens: the three judgment-gate setters store free-form
// evidence and refuse an empty value.
func TestSetEvidenceTokens(t *testing.T) {
	cases := []struct {
		setter string
		read   func(ontostate.State) string
	}{
		{"proposal-approved", func(s ontostate.State) string { return s.ProposalApproved }},
		{"approach-confirmed", func(s ontostate.State) string { return s.ApproachConfirmed }},
		{"close-confirmed", func(s ontostate.State) string { return s.CloseConfirmed }},
	}
	for _, tc := range cases {
		t.Run(tc.setter, func(t *testing.T) {
			dir := prepWorkspace(t)
			seedChange(t, dir, "feature-x", "open")
			commitAll(t, dir, "seed")

			if _, err := runOnto(t, "set", tc.setter, "feature-x", "2026-07-22 evidence", "--dir", dir); err != nil {
				t.Fatalf("set %s: %v", tc.setter, err)
			}
			st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if got := tc.read(st); got != "2026-07-22 evidence" {
				t.Errorf("%s stored %q", tc.setter, got)
			}

			if _, err := runOnto(t, "set", tc.setter, "feature-x", "", "--dir", dir); err == nil {
				t.Errorf("set %s with empty evidence must refuse", tc.setter)
			}
		})
	}
}
