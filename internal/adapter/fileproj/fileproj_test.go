package fileproj

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
)

// setup returns a root dir holding a content source "foo", and a dst path where
// a managed link to it should live.
func setup(t *testing.T) (root, src, dst string) {
	t.Helper()
	root = t.TempDir()
	src = filepath.Join(root, "content", "foo")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	dst = filepath.Join(root, "dest", "foo")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, src, dst
}

func TestProject_CreateWhenAbsent(t *testing.T) {
	root, src, dst := setup(t)
	st, _ := state.Load(t.TempDir())
	links := []Link{{Dst: dst, Src: src, Key: "skill.foo"}}
	changes, err := Project("claude", links, st, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != "create" || changes[0].Key != "skill.foo" {
		t.Fatalf("want one create for skill.foo, got %+v", changes)
	}
	if changes[0].New != dst+" -> "+src {
		t.Errorf("New = %q, want %q", changes[0].New, dst+" -> "+src)
	}
}

func TestProject_AdoptCorrectUnrecorded(t *testing.T) {
	root, src, dst := setup(t)
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(t.TempDir()) // link on disk, absent from state
	links := []Link{{Dst: dst, Src: src, Key: "skill.foo"}}
	changes, err := Project("claude", links, st, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Action != "adopt" {
		t.Fatalf("want one adopt, got %+v", changes)
	}
}

func TestProject_PlansNoDeletes(t *testing.T) {
	root, src, dst := setup(t)
	st, _ := state.Load(t.TempDir())
	// A recorded key that is NOT in the desired links must NOT be deleted here.
	st.Set("claude", "skill.gone", "somewhere -> else", secret.Hash("x"))
	links := []Link{{Dst: dst, Src: src, Key: "skill.foo"}}
	changes, err := Project("claude", links, st, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range changes {
		if c.Action == "delete" {
			t.Fatalf("fileproj must plan no deletes, got %+v", c)
		}
	}
}

func TestApplyLinks_CreatesLinkAndRecords_ThenObserve(t *testing.T) {
	root, src, dst := setup(t)
	st, _ := state.Load(t.TempDir())
	links := []Link{{Dst: dst, Src: src, Key: "skill.foo"}}
	if err := ApplyLinks("claude", links, st, []string{root}); err != nil {
		t.Fatal(err)
	}
	tgt, err := os.Readlink(dst)
	if err != nil || tgt != src {
		t.Fatalf("link not created: %q %v", tgt, err)
	}
	e, ok := st.Get("claude", "skill.foo")
	if !ok || e.Applied != secret.Hash(dst+" -> "+src) {
		t.Fatalf("state not recorded: %+v %v", e, ok)
	}
	// Observe re-hashes the recorded link back to Applied.
	obs := Observe("claude", "skill.", st)
	if obs["skill.foo"] != secret.Hash(dst+" -> "+src) {
		t.Errorf("Observe = %v, want match Applied", obs)
	}
}

func TestApplyState_DeleteRemovesLinkAndState(t *testing.T) {
	root, src, dst := setup(t)
	st, _ := state.Load(t.TempDir())
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}
	st.Set("claude", "skill.foo", dst+" -> "+src, secret.Hash(dst+" -> "+src))
	del := []adapter.Change{{Action: "delete", Key: "skill.foo", Old: adapter.SecretRedaction}}
	fallback := func(k string) []string { return []string{dst} }
	if err := ApplyState("claude", del, st, []string{root}, fallback); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Errorf("link not removed: %v", err)
	}
	if _, ok := st.Get("claude", "skill.foo"); ok {
		t.Errorf("state not deleted")
	}
}

// A fallback location holding the user's own same-named content, or another
// install's link, is not this prune's conflict: it stays, and the prune completes.
func TestApplyState_DeleteLeavesForeignFallbacks(t *testing.T) {
	root, src, dst := setup(t)
	st, _ := state.Load(t.TempDir())
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}
	st.Set("claude", "skill.foo", dst+" -> "+src, secret.Hash(dst+" -> "+src))
	own := filepath.Join(root, "user", "foo")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(root, "other", "foo")
	if err := os.MkdirAll(filepath.Dir(foreign), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), foreign); err != nil {
		t.Fatal(err)
	}
	del := []adapter.Change{{Action: "delete", Key: "skill.foo", Old: adapter.SecretRedaction}}
	fallback := func(k string) []string { return []string{own, foreign, dst} }
	if err := ApplyState("claude", del, st, []string{filepath.Join(root, "content")}, fallback); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Errorf("managed link not removed: %v", err)
	}
	for _, kept := range []string{own, foreign} {
		if _, err := os.Lstat(kept); err != nil {
			t.Errorf("%s removed: %v", kept, err)
		}
	}
	if _, ok := st.Get("claude", "skill.foo"); ok {
		t.Errorf("state not deleted")
	}
}

// When repairs rescue some conflicts but a foreign destination still blocks,
// the error names that foreign destination, never a link the repair covers.
func TestProject_RepairLeavesForeignConflictNamed(t *testing.T) {
	base := t.TempDir()
	oldDomain, domain := filepath.Join(base, "old"), filepath.Join(base, "new")
	var links []Link
	st, _ := state.Load(t.TempDir())
	for _, name := range []string{"a", "b", "c", "d"} {
		src := filepath.Join(domain, "content", name)
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(domain, "dest", name)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		oldSrc := filepath.Join(oldDomain, "content", name)
		if err := os.Symlink(oldSrc, dst); err != nil {
			t.Fatal(err)
		}
		recorded := filepath.Join(oldDomain, "dest", name) + sep + oldSrc
		st.Set("claude", "skill."+name, recorded, secret.Hash(recorded))
		links = append(links, Link{Dst: dst, Src: src, Key: "skill." + name, Domain: domain})
	}
	foreign := filepath.Join(domain, "dest", "zz")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	links = append(links, Link{Dst: foreign, Src: filepath.Join(domain, "content", "a"), Key: "skill.zz", Domain: domain})
	_, err := Project("claude", links, st, []string{filepath.Join(domain, "content")})
	if err == nil || !strings.Contains(err.Error(), foreign) {
		t.Fatalf("want conflict naming %s, got: %v", foreign, err)
	}
}
