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

func TestApply_LocalEditBackedUpBeforePrune(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "foo.md")
	if err := os.WriteFile(dst, []byte("local edit"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	st.Set("opencode", "subagentcopy.foo", dst, copyfile.Hash([]byte("old content")))
	if err := Apply("opencode", nil, st, []string{root}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(dst + ".bak"); err != nil || string(got) != "local edit" {
		t.Fatalf("local edit not backed up: %q, %v", got, err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("de-declared file not pruned: %v", err)
	}
	if _, ok := st.Get("opencode", "subagentcopy.foo"); ok {
		t.Fatal("pruned file still recorded in state")
	}
}

func TestApply_RefusedPruneDoesNotWriteBackup(t *testing.T) {
	for _, kind := range []string{"outside path", "symlinked parent"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			victim := filepath.Join(outside, "foo.md")
			if err := os.WriteFile(victim, []byte("user content"), 0o600); err != nil {
				t.Fatal(err)
			}
			dst := victim
			if kind == "symlinked parent" {
				if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
					t.Fatal(err)
				}
				dst = filepath.Join(root, "escape", "foo.md")
			}
			st, err := state.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			st.Set("opencode", "subagentcopy.foo", dst, copyfile.Hash([]byte("old content")))
			if err := Apply("opencode", nil, st, []string{root}); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(victim + ".bak"); !os.IsNotExist(err) {
				t.Fatalf("refused prune wrote outside the managed root: backup error = %v", err)
			}
			if b, err := os.ReadFile(victim); err != nil || string(b) != "user content" {
				t.Fatalf("refused prune changed outside file: %q, %v", b, err)
			}
			if _, ok := st.Get("opencode", "subagentcopy.foo"); !ok {
				t.Fatal("refused prune dropped its state record")
			}
		})
	}
}

func TestApply_RefusesForeignBackupSymlink(t *testing.T) {
	for _, action := range []string{"update", "remove"} {
		t.Run(action, func(t *testing.T) {
			root := t.TempDir()
			dst := filepath.Join(root, "foo.md")
			victim := filepath.Join(t.TempDir(), "user-file")
			for path, content := range map[string]string{dst: "local edit", victim: "user content"} {
				if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(victim, dst+".bak"); err != nil {
				t.Fatal(err)
			}
			st, err := state.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			st.Set("opencode", "subagentcopy.foo", dst, copyfile.Hash([]byte("old content")))
			var desired map[string][]byte
			if action == "update" {
				desired = map[string][]byte{dst: []byte("new content")}
			}
			if err := Apply("opencode", desired, st, []string{root}); err == nil {
				t.Error("want conflict at foreign backup symlink")
			}
			for path, want := range map[string]string{dst: "local edit", victim: "user content"} {
				if got, err := os.ReadFile(path); err != nil || string(got) != want {
					t.Errorf("file changed at %s: %q, %v", path, got, err)
				}
			}
			if got, err := os.Readlink(dst + ".bak"); err != nil || got != victim {
				t.Errorf("backup symlink changed: %q, %v", got, err)
			}
			if entry, ok := st.Get("opencode", "subagentcopy.foo"); !ok || entry.Applied != copyfile.Hash([]byte("old content")) {
				t.Fatalf("failed backup changed state: %+v, present = %v", entry, ok)
			}
		})
	}
}
