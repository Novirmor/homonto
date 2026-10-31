package copyproj

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/copyfile"
	"github.com/Novirmor/homonto/internal/state"
)

func TestName(t *testing.T) {
	if got := Name("/a/b/foo.md"); got != "foo" {
		t.Errorf("Name = %q, want foo", got)
	}
}

func TestPlan_CreateWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	st, _ := state.Load(t.TempDir())
	dst := filepath.Join(dir, "foo.md")
	desired := map[string][]byte{dst: []byte("hello")}
	ops, err := Plan("claude", desired, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || ops[0].Action != copyfile.Create || ops[0].Dst != dst {
		t.Fatalf("want one create, got %+v", ops)
	}
}

func TestApply_CreatesFileAndRecordsState(t *testing.T) {
	dir := t.TempDir()
	st, _ := state.Load(t.TempDir())
	dst := filepath.Join(dir, "foo.md")
	desired := map[string][]byte{dst: []byte("hello")}
	if err := Apply("claude", desired, st, []string{dir}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "hello" {
		t.Fatalf("file not written: %q %v", b, err)
	}
	e, ok := st.Get("claude", "subagentcopy.foo")
	if !ok || e.Desired != dst || e.Applied != copyfile.Hash([]byte("hello")) {
		t.Fatalf("state not recorded: %+v %v", e, ok)
	}
}

func TestApply_ConflictAbortsKeyedByTool(t *testing.T) {
	dir := t.TempDir()
	st, _ := state.Load(t.TempDir())
	dst := filepath.Join(dir, "foo.md")
	// A foreign (unmanaged) file at dst → conflict.
	if err := os.WriteFile(dst, []byte("user's own"), 0o644); err != nil {
		t.Fatal(err)
	}
	desired := map[string][]byte{dst: []byte("hello")}
	err := Apply("opencode", desired, st, []string{dir})
	if err == nil {
		t.Fatal("want conflict error, got nil")
	}
	if got := err.Error(); got[:9] != "opencode:" {
		t.Errorf("error not keyed by tool: %q", got)
	}
	// Foreign file untouched.
	if b, _ := os.ReadFile(dst); string(b) != "user's own" {
		t.Errorf("foreign file clobbered: %q", b)
	}
}

func TestApply_LocalEditBackedUpBeforeOverwrite(t *testing.T) {
	dir := t.TempDir()
	st, _ := state.Load(t.TempDir())
	dst := filepath.Join(dir, "foo.md")
	// Recorded ours with base "v1", but on-disk edited to "edited".
	if err := os.WriteFile(dst, []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.Set("claude", "subagentcopy.foo", dst, copyfile.Hash([]byte("v1")))
	desired := map[string][]byte{dst: []byte("v2")}
	if err := Apply("claude", desired, st, []string{dir}); err != nil {
		t.Fatal(err)
	}
	// Local edit backed up, then overwritten with desired.
	if b, _ := os.ReadFile(dst + ".bak"); string(b) != "edited" {
		t.Errorf("local edit not backed up: %q", b)
	}
	if b, _ := os.ReadFile(dst); string(b) != "v2" {
		t.Errorf("not overwritten with desired: %q", b)
	}
}
