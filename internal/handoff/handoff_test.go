package handoff

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func sample() Recovery {
	return Recovery{
		SchemaVersion: SchemaVersion,
		Tool:          "onto",
		Change:        "feat-a",
		OperationID:   "ab12cd34",
		Phase:         "design",
		DerivedPhase:  "design",
		Deps:          []string{"feat-b"},
		RepoAliases:   []string{"service"},
		BaseRef:       "main",
		HeadCommit:    "0123abcd",
		PendingGates:  []GateRef{{ID: "isolation", Header: "Isolation", SetArgv: []string{"onto", "set", "isolation", "feat-a", "<value>"}}},
		Artifacts:     []ArtifactDigest{{Path: "proposal.md", SHA256: "aa"}},
		NextArgv:      []string{"onto", "gate", "feat-a"},
	}
}

func TestValidateSchema(t *testing.T) {
	if err := ValidateSchema(SchemaVersion); err != nil {
		t.Fatalf("current version rejected: %v", err)
	}
	if err := ValidateSchema(SchemaVersion + 1); err == nil {
		t.Fatal("newer major version must be rejected")
	}
	if err := ValidateSchema(0); err == nil {
		t.Fatal("missing version must be rejected")
	}
}

// TestMarkdownOmitsProse: the markdown pack is rendered only from envelope
// fields, so a secret planted in artifact prose cannot reach it (H5 sentinel).
func TestMarkdownOmitsProse(t *testing.T) {
	r := sample()
	md := Markdown(r)
	if strings.Contains(md, "hunter2") {
		t.Fatal("markdown leaked artifact prose")
	}
	for _, want := range []string{"feat-a", "design", "isolation", "proposal.md", "sha256:aa"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
	if !strings.Contains(md, "MISMATCH") {
		r.PhaseMismatch = true
		r.DerivedPhase = "build"
		if !strings.Contains(Markdown(r), "MISMATCH") {
			t.Fatal("phase mismatch not surfaced")
		}
	}
}

func TestJSONRoundTripAndVersion(t *testing.T) {
	r := sample()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var back Recovery
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Change != r.Change || len(back.PendingGates) != 1 || back.PendingGates[0].SetArgv[1] != "set" {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if err := ValidateSchema(back.SchemaVersion); err != nil {
		t.Fatalf("own version rejected: %v", err)
	}
}

func TestWritePackRefusesOverwriteAndSymlinkParents(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs", "changes", "feat-a", ".onto", "handoff")

	r := sample()
	Stamp(&r, time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC))
	jp, mp, err := WritePack(root, dir, r, []byte("{}"), []byte("md"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mp); err != nil {
		t.Fatal(err)
	}

	// Same operation ID again: collision, refused.
	if _, _, err := WritePack(root, dir, r, []byte("{}"), []byte("md")); err == nil {
		t.Fatal("overwrite must be refused")
	}

	// A new operation ID succeeds alongside.
	r.OperationID = "ff99ee88"
	if _, _, err := WritePack(root, dir, r, []byte("{}"), []byte("md")); err != nil {
		t.Fatalf("second pack failed: %v", err)
	}

	// Symlinked parent redirects the write outside root: refused.
	other := t.TempDir()
	linkRoot := t.TempDir()
	if err := os.Symlink(other, filepath.Join(linkRoot, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := WritePack(linkRoot, filepath.Join(linkRoot, "escape", "h"), r, []byte("{}"), []byte("md")); err == nil {
		t.Fatal("symlinked parent must be refused")
	}

	// Directory outside root: refused.
	if _, _, err := WritePack(root, other, r, []byte("{}"), []byte("md")); err == nil {
		t.Fatal("outside-root dir must be refused")
	}
}

func TestStampUTC(t *testing.T) {
	var r Recovery
	Stamp(&r, time.Date(2026, 9, 2, 12, 0, 0, 0, time.FixedZone("X", 3600)))
	if !strings.HasSuffix(r.Generated, "Z") {
		t.Fatalf("stamp not normalized to UTC: %q", r.Generated)
	}
}
