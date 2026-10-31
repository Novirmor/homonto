package copyfile

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// find returns the single Op for dst, failing if absent.
func find(t *testing.T, ops []Op, dst string) Op {
	t.Helper()
	for _, o := range ops {
		if o.Dst == dst {
			return o
		}
	}
	t.Fatalf("no op for %s in %+v", dst, ops)
	return Op{}
}

func TestPlanCreateNoopUpdate(t *testing.T) {
	dir := t.TempDir()
	create := filepath.Join(dir, "create.md")
	noop := filepath.Join(dir, "noop.md")
	update := filepath.Join(dir, "update.md")

	write(t, noop, []byte("same"))
	write(t, update, []byte("old")) // ours, unchanged since we wrote "old"

	desired := map[string][]byte{
		create: []byte("new"),
		noop:   []byte("same"),
		update: []byte("newer"),
	}
	recorded := map[string]string{
		noop:   Hash([]byte("same")),
		update: Hash([]byte("old")),
	}
	ops, err := Plan(desired, recorded)
	if err != nil {
		t.Fatal(err)
	}
	if got := find(t, ops, create).Action; got != Create {
		t.Fatalf("create: %s", got)
	}
	if got := find(t, ops, noop).Action; got != Noop {
		t.Fatalf("noop: %s", got)
	}
	if got := find(t, ops, update).Action; got != Update {
		t.Fatalf("update: %s", got)
	}
}

func TestPlanLocalEdit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "edited.md")
	write(t, p, []byte("user edited")) // on disk differs from recorded base
	ops, err := Plan(
		map[string][]byte{p: []byte("upstream")},
		map[string]string{p: Hash([]byte("base"))},
	)
	if err != nil {
		t.Fatal(err)
	}
	op := find(t, ops, p)
	if op.Action != LocalEdit {
		t.Fatalf("want local-edit, got %s", op.Action)
	}
	if string(op.OnDisk) != "user edited" || op.Recorded != Hash([]byte("base")) {
		t.Fatalf("local-edit must carry on-disk bytes + recorded base: %+v", op)
	}
}

func TestPlanForeignFileConflict(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "foreign.md")
	write(t, p, []byte("hand written")) // exists, NO record of ownership
	ops, err := Plan(map[string][]byte{p: []byte("ours")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := find(t, ops, p).Action; got != Conflict {
		t.Fatalf("a foreign file must be a conflict, got %s", got)
	}
}

func TestPlanSymlinkConflict(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	write(t, target, []byte("x"))
	p := filepath.Join(dir, "link.md")
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
	// Even if recorded, a symlink at a copy destination is not ours to overwrite.
	ops, err := Plan(map[string][]byte{p: []byte("ours")}, map[string]string{p: Hash([]byte("x"))})
	if err != nil {
		t.Fatal(err)
	}
	if got := find(t, ops, p).Action; got != Conflict {
		t.Fatalf("a symlink at a copy dst must be a conflict, got %s", got)
	}
}

func TestPlanPruneAndPruneLocalEdit(t *testing.T) {
	dir := t.TempDir()
	prune := filepath.Join(dir, "prune.md")
	kept := filepath.Join(dir, "kept.md")
	write(t, prune, []byte("ours unchanged"))
	write(t, kept, []byte("user changed it"))

	recorded := map[string]string{
		prune: Hash([]byte("ours unchanged")),
		kept:  Hash([]byte("ours original")),
	}
	ops, err := Plan(map[string][]byte{}, recorded) // nothing desired → prune both records
	if err != nil {
		t.Fatal(err)
	}
	if got := find(t, ops, prune).Action; got != Prune {
		t.Fatalf("an unchanged de-declared managed file must prune, got %s", got)
	}
	if got := find(t, ops, kept).Action; got != LocalEdit {
		t.Fatalf("a de-declared but user-edited file must NOT prune (local-edit), got %s", got)
	}
}

func TestPlanPruneRejectsReplacedFile(t *testing.T) {
	for _, kind := range []string{"matching symlink", "changed symlink", "dangling symlink", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "agent.md")
			target := filepath.Join(dir, "user-agent.md")
			original := []byte("managed content")
			if kind == "directory" {
				if err := os.Mkdir(p, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				if kind == "matching symlink" {
					write(t, target, original)
				} else if kind == "changed symlink" {
					write(t, target, []byte("user content"))
				}
				if err := os.Symlink(target, p); err != nil {
					t.Fatal(err)
				}
			}
			ops, err := Plan(nil, map[string]string{p: Hash(original)})
			if err != nil {
				t.Fatal(err)
			}
			if got := find(t, ops, p).Action; got != Conflict {
				t.Fatalf("replaced copy-mode file must be a conflict on removal, got %s", got)
			}
			_, pruned, _, err := Apply(ops, []string{dir})
			if err != nil || len(pruned) != 0 {
				t.Fatalf("Apply: pruned = %v, err = %v", pruned, err)
			}
			if _, err := os.Lstat(p); err != nil {
				t.Fatalf("user replacement removed: %v", err)
			}
		})
	}
}

func TestApplyWritesPrunesAndRecords(t *testing.T) {
	dir := t.TempDir()
	create := filepath.Join(dir, "create.md")
	update := filepath.Join(dir, "update.md")
	noop := filepath.Join(dir, "noop.md")
	prune := filepath.Join(dir, "prune.md")
	foreign := filepath.Join(dir, "foreign.md")

	write(t, update, []byte("old"))
	write(t, noop, []byte("same"))
	write(t, prune, []byte("gone"))
	write(t, foreign, []byte("user data"))

	ops := []Op{
		{Dst: create, Action: Create, Content: []byte("new")},
		{Dst: update, Action: Update, Content: []byte("newer")},
		{Dst: noop, Action: Noop, Content: []byte("same")},
		{Dst: prune, Action: Prune},
		{Dst: foreign, Action: Conflict, Content: []byte("ours")},
	}
	rec, pruned, _, err := Apply(ops, []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(create); string(b) != "new" {
		t.Fatalf("create not written: %q", b)
	}
	if b, _ := os.ReadFile(update); string(b) != "newer" {
		t.Fatalf("update not written: %q", b)
	}
	if _, err := os.Stat(prune); !os.IsNotExist(err) {
		t.Fatal("prune did not remove the file")
	}
	// A Conflict op must never touch the foreign file.
	if b, _ := os.ReadFile(foreign); string(b) != "user data" {
		t.Fatalf("conflict op clobbered a foreign file: %q", b)
	}
	// Recorded hashes cover create/update/noop; pruned lists the removed dst.
	for _, p := range []string{create, update, noop} {
		if rec[p] == "" {
			t.Fatalf("missing recorded hash for %s", p)
		}
	}
	if rec[create] != Hash([]byte("new")) || rec[update] != Hash([]byte("newer")) {
		t.Fatal("recorded hashes must be of the written content")
	}
	if len(pruned) != 1 || pruned[0] != prune {
		t.Fatalf("pruned = %v, want [%s]", pruned, prune)
	}
}

// TestApplyConfinesPruneToRoots: prune deletes only destinations under a managed
// root (F7). A tampered state entry whose Dst resolves OUTSIDE the root — an
// absolute foreign path or a traversal that escapes — is refused: the file is not
// deleted, it is not listed in pruned (so ownership is retained), and it surfaces
// in refused. An in-root managed file still prunes normally.
func TestApplyConfinesPruneToRoots(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()

	inRoot := filepath.Join(root, "managed.md")
	absOutside := filepath.Join(outsideDir, "victim.md")
	write(t, inRoot, []byte("ours"))
	write(t, absOutside, []byte("victim"))

	// A traversal Dst that cleans to a path outside root (…/root/../victim.md).
	traversal := filepath.Join(root, "..", filepath.Base(outsideDir), "victim.md")

	ops := []Op{
		{Dst: inRoot, Action: Prune},
		{Dst: absOutside, Action: Prune},
		{Dst: traversal, Action: Prune},
	}
	_, pruned, refused, err := Apply(ops, []string{root})
	if err != nil {
		t.Fatal(err)
	}

	// The in-root file is pruned; the out-of-root file survives untouched.
	if _, err := os.Stat(inRoot); !os.IsNotExist(err) {
		t.Fatal("in-root managed file was not pruned")
	}
	if b, _ := os.ReadFile(absOutside); string(b) != "victim" {
		t.Fatalf("out-of-root file was deleted; prune confinement failed: %q", b)
	}

	if len(pruned) != 1 || pruned[0] != inRoot {
		t.Fatalf("pruned = %v, want only [%s]", pruned, inRoot)
	}
	// Both out-of-root destinations (absolute + traversal) are reported refused.
	if len(refused) != 2 {
		t.Fatalf("refused = %v, want the two out-of-root destinations", refused)
	}
}

// TestApplyRefusesAllPrunesWithoutRoots: fail-closed — an empty root set refuses
// every prune rather than deleting unconfined.
func TestApplyRefusesAllPrunesWithoutRoots(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.md")
	write(t, p, []byte("ours"))

	_, pruned, refused, err := Apply([]Op{{Dst: p, Action: Prune}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "ours" {
		t.Fatalf("file deleted with no confinement roots: %q", b)
	}
	if len(pruned) != 0 || len(refused) != 1 {
		t.Fatalf("pruned=%v refused=%v; want no prune, one refusal", pruned, refused)
	}
}

func TestApplyRefusesPruneThroughEscapingParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	victim := filepath.Join(outside, "agent.md")
	write(t, victim, []byte("user content"))
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "escape", "agent.md")
	_, pruned, refused, err := Apply([]Op{{Dst: dst, Action: Prune}}, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(victim); err != nil || string(got) != "user content" {
		t.Fatalf("prune escaped the managed root: content = %q, err = %v", got, err)
	}
	if len(pruned) != 0 || len(refused) != 1 || refused[0] != dst {
		t.Fatalf("pruned = %v, refused = %v; want only a refusal", pruned, refused)
	}
}

func TestApplyRefusesPruningRootItself(t *testing.T) {
	root := t.TempDir()
	_, pruned, refused, err := Apply([]Op{{Dst: root, Action: Prune}}, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("managed root removed: %v", err)
	}
	if len(pruned) != 0 || len(refused) != 1 {
		t.Fatalf("pruned = %v, refused = %v; want root refused", pruned, refused)
	}
}

func TestApplyPrunesWithinSymlinkedRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "agents")
	realRoot := t.TempDir()
	if err := os.Symlink(realRoot, root); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "agent.md")
	write(t, dst, []byte("managed content"))
	_, pruned, refused, err := Apply([]Op{{Dst: dst, Action: Prune}}, []string{root})
	if err != nil || len(pruned) != 1 || len(refused) != 0 {
		t.Fatalf("prune in aliased root: pruned = %v, refused = %v, err = %v", pruned, refused, err)
	}
	if _, err := os.Stat(filepath.Join(realRoot, "agent.md")); !os.IsNotExist(err) {
		t.Fatalf("managed file not removed: %v", err)
	}
	if _, err := os.Readlink(root); err != nil {
		t.Fatalf("root alias not preserved: %v", err)
	}
}

func TestPlanAbsentPrunedRecordIsSkipped(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone.md")
	ops, err := Plan(map[string][]byte{}, map[string]string{gone: Hash([]byte("whatever"))})
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 0 {
		t.Fatalf("an already-gone recorded file needs no op, got %+v", ops)
	}
}
