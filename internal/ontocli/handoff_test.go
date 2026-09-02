package ontocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

func TestHandoff_ContentAndWrite(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"),
		ontostate.State{Change: "c", ID: "abcd1234", Workflow: "full", Phase: "design", Deps: []string{"dep-a"}})
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "# Proposal\n\nBuild the thing.\n")

	// stdout form carries identity, the pending gate, the artifact excerpt + hash.
	out, err := runOnto(t, "handoff", "c", "--dir", root)
	if err != nil {
		t.Fatalf("handoff: %v", err)
	}
	for _, want := range []string{"onto handoff: c", "abcd1234", "phase**: design", "deps**: dep-a", "Pending decision", "Isolation", "Build the thing.", "artifacts-hash**: sha256:"} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff missing %q:\n%s", want, out)
		}
	}

	// --write persists the metadata-only recovery view under the workspace:
	// unique operation-ID filenames, JSON envelope + markdown, no prose.
	if _, err := runOnto(t, "handoff", "c", "--dir", root, "--write"); err != nil {
		t.Fatalf("handoff --write: %v", err)
	}
	hd := filepath.Join(changeDir, ".onto", "handoff")
	matches, err := filepath.Glob(filepath.Join(hd, "*-context.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("handoff --write did not persist the markdown pack: %v %v", matches, err)
	}
	persisted, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), "Build the thing.") {
		t.Errorf("persisted pack leaked artifact prose:\n%s", persisted)
	}
	if !strings.Contains(string(persisted), "design") || !strings.Contains(string(persisted), "dep-a") {
		t.Errorf("persisted pack lost identity:\n%s", persisted)
	}
	jmatches, err := filepath.Glob(filepath.Join(hd, "*-context.json"))
	if err != nil || len(jmatches) != 1 {
		t.Fatalf("handoff --write did not persist the JSON envelope: %v %v", jmatches, err)
	}
	jb, err := os.ReadFile(jmatches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jb), `"schemaVersion"`) || strings.Contains(string(jb), "Build the thing.") {
		t.Errorf("persisted envelope wrong:\n%s", jb)
	}

	// --json prints the interactive view: envelope fields plus full state.
	out, err = runOnto(t, "handoff", "c", "--dir", root, "--json")
	if err != nil {
		t.Fatalf("handoff --json: %v", err)
	}
	for _, want := range []string{`"schemaVersion"`, `"change": "c"`, `"state"`, `"derivedPhase"`} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff --json missing %q:\n%s", want, out)
		}
	}
}

// handoff --write must never escape the workspace via a malformed phase, nor
// follow a planted symlink at the destination. See F6.
func TestHandoff_RejectsTraversalPhase(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	// A malicious state whose phase is a path-escape attempt.
	writeFile(t, filepath.Join(changeDir, "onto-state.yaml"),
		"change: c\nphase: ../../escape\nworkflow: full\n")
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "# Proposal\n")

	target := filepath.Join(root, "escape-context.md")

	if _, err := runOnto(t, "handoff", "c", "--dir", root, "--write"); err == nil {
		t.Fatalf("handoff --write must refuse a traversal phase")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatalf("handoff --write escaped the workspace via a traversal phase: %s exists", target)
	}
}

// An unknown phase value must also be rejected rather than baked into a path,
// since the phase is the only unvalidated field feeding the output filename.
func TestHandoff_RejectsUnknownPhase(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(root, "docs", "changes", "c")
	writeFile(t, filepath.Join(changeDir, "onto-state.yaml"),
		"change: c\nphase: bogus-phase\nworkflow: full\nschema_version: 1\n")
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "# Proposal\n")

	if _, err := runOnto(t, "handoff", "c", "--dir", root, "--write"); err == nil {
		t.Fatalf("handoff --write must refuse an unknown phase")
	}
}
