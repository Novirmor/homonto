package ontocli

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestStateJSON_EmitsFullStateAndDerivedPhase(t *testing.T) {
	root := t.TempDir() // read command needs no framework install
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "onto-state.yaml"),
		"schema_version: 1\nchange: c\nworkflow: full\nphase: build\nisolation: worktree\n")
	// Phase-appropriate artifacts: a confirmed design + incomplete tasks are
	// what make the WORKING phase build (derivation reads files, not the claim).
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "proposal.md"), "p")
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "design.md"), "Status: Confirmed\n")
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "tasks.md"), "- [ ] a\n")

	before := treeSnapshot(t, root)

	out, err := runOnto(t, "state", "c", "--json", "--dir", root)
	if err != nil {
		t.Fatalf("state --json: %v", err)
	}

	var got struct {
		Change        string `json:"change"`
		Phase         string `json:"phase"`
		Isolation     string `json:"isolation"`
		DerivedPhase  string `json:"derived_phase"`
		PhaseMismatch bool   `json:"phase_mismatch"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if got.Change != "c" || got.Isolation != "worktree" {
		t.Errorf("state = %+v, want change=c isolation=worktree", got)
	}
	if got.DerivedPhase != "build" {
		t.Errorf("derived_phase = %q, want build", got.DerivedPhase)
	}
	if got.PhaseMismatch {
		t.Errorf("phase_mismatch = true for an agreeing claim")
	}

	after := treeSnapshot(t, root)
	if len(before) != len(after) {
		t.Errorf("state --json mutated the tree: before=%d after=%d files", len(before), len(after))
	}
}

// TestStateJSON_DerivedPhaseIsArtifactBased: the derived_phase field is a real
// derivation from workspace artifacts — a claim the files cannot support is
// reported at the working phase with phase_mismatch set, never echoed.
func TestStateJSON_DerivedPhaseIsArtifactBased(t *testing.T) {
	root := t.TempDir()
	// Claimed verify, but tasks.md has unchecked items and design is a draft:
	// the working phase is design.
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "onto-state.yaml"),
		"schema_version: 1\nchange: c\nworkflow: full\nphase: verify\n")
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "proposal.md"), "p")
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "design.md"), "Status: Draft\n")
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "tasks.md"), "- [ ] a\n")

	out, err := runOnto(t, "state", "c", "--json", "--dir", root)
	if err != nil {
		t.Fatalf("state --json: %v", err)
	}
	var got struct {
		Phase         string `json:"phase"`
		DerivedPhase  string `json:"derived_phase"`
		PhaseMismatch bool   `json:"phase_mismatch"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if got.Phase != "verify" || got.DerivedPhase != "design" || !got.PhaseMismatch {
		t.Errorf("got %+v, want phase=verify derived=design mismatch=true", got)
	}
}
