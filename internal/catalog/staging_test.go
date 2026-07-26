package catalog

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMaterialize_CleansLeftoverStagingAndSwaps proves per-skill materialization
// stages then swaps: a leftover <skill>.staging from a prior crashed run is
// cleaned, and the destination ends up complete with no staging sibling left.
func TestMaterialize_CleansLeftoverStagingAndSwaps(t *testing.T) {
	c, err := Load(matFS())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dst := t.TempDir()
	// Simulate a prior crash: a stale staging dir with junk sitting beside dst.
	staging := filepath.Join(dst, "brainstorming.staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "junk.txt"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.Materialize(dst, []string{"brainstorming"}, "none", "none"); err != nil {
		t.Fatalf("materialize: %v", err)
	}

	// Skill materialized correctly (nested content intact).
	if b, _ := os.ReadFile(filepath.Join(dst, "brainstorming", "SKILL.md")); string(b) != "top" {
		t.Errorf("SKILL.md = %q, want top", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "brainstorming", "references", "notes.md")); string(b) != "nested" {
		t.Errorf("notes.md = %q, want nested", b)
	}
	// The stale staging leftover (with its junk) must be gone — not merged in.
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("leftover <skill>.staging not cleaned: stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "brainstorming", "junk.txt")); !os.IsNotExist(err) {
		t.Errorf("stale junk leaked into the materialized skill")
	}
}
