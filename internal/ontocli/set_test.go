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

func TestSetCloseMerged_SetsTrueIdempotently(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "close")

	for i := 0; i < 2; i++ { // idempotent: running twice is fine
		if _, err := runOnto(t, "set", "close-merged", "c", "--dir", root); err != nil {
			t.Fatalf("set close-merged (run %d): %v", i, err)
		}
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if !st.Close.Merged {
		t.Errorf("Close.Merged = false, want true")
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

func TestSetBaseRef_HappyPath_WritesField(t *testing.T) {
	root := prepWorkspace(t)
	seedChange(t, root, "c", "open")

	if _, err := runOnto(t, "set", "base-ref", "c", "abc123", "--dir", root); err != nil {
		t.Fatalf("set base-ref: %v", err)
	}
	st, _ := ontostate.LoadChange(filepath.Join(root, "docs", "changes", "c"))
	if st.BaseRef != "abc123" {
		t.Errorf("BaseRef = %q, want abc123", st.BaseRef)
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
