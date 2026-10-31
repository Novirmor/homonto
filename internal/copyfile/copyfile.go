// Package copyfile is a conflict-safe reconciler for homonto-managed CONTENT
// files — the copy-mode analogue of internal/link (which reconciles symlinks).
// A managed content file is a real file whose bytes homonto owns; ownership is
// proven by a recorded content hash from the last apply, so copyfile never
// clobbers a user's own file or a file another mechanism (e.g. a symlink)
// placed. It computes the plan; the caller performs the atomic writes. Three-way
// merge of local edits is layered on top by the caller (it is not this package's
// concern).
package copyfile

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"

	"github.com/Novirmor/homonto/internal/fsutil"
)

// Action is what a plan entry does to its destination.
type Action string

const (
	// Create writes a brand-new managed file (dst absent).
	Create Action = "create"
	// Update overwrites our own managed file whose content changed.
	Update Action = "update"
	// Noop leaves an already-correct managed file untouched.
	Noop Action = "noop"
	// LocalEdit marks our managed file whose on-disk bytes diverged from the
	// recorded base (a user edit). The caller decides how to reconcile it
	// (three-way merge / backup); copyfile never silently overwrites it.
	LocalEdit Action = "local-edit"
	// Conflict marks a destination occupied by something homonto does not own
	// (a foreign file, or a symlink) — never written or removed.
	Conflict Action = "conflict"
	// Prune removes a managed file no longer desired (recorded ours, absent from
	// desired, still matching the recorded hash on disk).
	Prune Action = "prune"
)

// Op is one planned change to dst.
type Op struct {
	Dst      string
	Action   Action
	Content  []byte // desired bytes for Create/Update (nil otherwise)
	OnDisk   []byte // current bytes for LocalEdit (nil otherwise)
	Recorded string // recorded base hash for LocalEdit (empty otherwise)
}

// Hash is the content hash copyfile records ownership by (sha256 hex), matching
// how callers store the applied hash.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Plan reconciles desired managed files against the recorded ownership hashes
// (dst -> hash homonto last wrote). It returns one Op per affected dst, sorted
// by dst for deterministic output. A recorded dst absent from desired is pruned
// only if the on-disk bytes still match the recorded hash (ours, unchanged); a
// locally-edited or already-gone recorded dst is not force-removed here.
func Plan(desired map[string][]byte, recorded map[string]string) ([]Op, error) {
	var ops []Op

	for dst, content := range desired {
		want := Hash(content)
		fi, err := os.Lstat(dst)
		switch {
		case os.IsNotExist(err):
			ops = append(ops, Op{Dst: dst, Action: Create, Content: content})
			continue
		case err != nil:
			return nil, err
		}
		// A symlink (or any non-regular file) at a copy-mode destination is not
		// ours to overwrite — copy mode owns real files only.
		if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
			ops = append(ops, Op{Dst: dst, Action: Conflict, Content: content})
			continue
		}
		cur, err := os.ReadFile(dst)
		if err != nil {
			return nil, err
		}
		curHash := Hash(cur)
		rec, owned := recorded[dst]
		switch {
		case curHash == want:
			ops = append(ops, Op{Dst: dst, Action: Noop, Content: content})
		case !owned:
			// A real file we have no record of owning — foreign, never clobbered.
			ops = append(ops, Op{Dst: dst, Action: Conflict, Content: content})
		case curHash == rec:
			// Our file, unchanged since we wrote it, but the desired bytes moved.
			ops = append(ops, Op{Dst: dst, Action: Update, Content: content})
		default:
			// Our file, but edited on disk since we wrote it → the caller merges.
			ops = append(ops, Op{Dst: dst, Action: LocalEdit, Content: content, OnDisk: cur, Recorded: rec})
		}
	}

	for dst, rec := range recorded {
		if _, still := desired[dst]; still {
			continue
		}
		fi, err := os.Lstat(dst)
		if os.IsNotExist(err) {
			continue // already gone
		}
		if err != nil {
			return nil, err
		}
		// Ownership is of a regular file, not whatever now occupies its path.
		// In particular, reading through a replacement symlink could match the
		// recorded hash and authorize deleting a link we never created.
		if !fi.Mode().IsRegular() {
			ops = append(ops, Op{Dst: dst, Action: Conflict})
			continue
		}
		cur, err := os.ReadFile(dst)
		if os.IsNotExist(err) {
			continue // already gone
		}
		if err != nil {
			return nil, err
		}
		if Hash(cur) == rec {
			ops = append(ops, Op{Dst: dst, Action: Prune})
		} else {
			// Locally edited since we wrote it — leave it (a backup/merge is the
			// caller's call), never silently delete a user's edit.
			ops = append(ops, Op{Dst: dst, Action: LocalEdit, OnDisk: cur, Recorded: rec})
		}
	}

	sort.Slice(ops, func(i, j int) bool { return ops[i].Dst < ops[j].Dst })
	return ops, nil
}

// Apply executes the writable ops — Create and Update write the desired bytes
// atomically; Prune removes the managed file. It returns the ownership hashes to
// persist in state (dst -> content hash, for Create/Update/Noop so unchanged
// files keep their record) and the pruned destinations. Conflict and LocalEdit
// are the caller's responsibility (a conflict must abort the run before Apply; a
// local edit is merged/backed-up by the caller) and are ignored here.
func Apply(ops []Op, pruneRoots []string) (recorded map[string]string, pruned, refused []string, err error) {
	recorded = map[string]string{}
	for _, op := range ops {
		switch op.Action {
		case Create, Update:
			if err := fsutil.WriteAtomic(op.Dst, op.Content); err != nil {
				return nil, nil, nil, err
			}
			recorded[op.Dst] = Hash(op.Content)
		case Noop:
			recorded[op.Dst] = Hash(op.Content)
		case Prune:
			// The prune destination comes from a recorded state entry, which is
			// untrusted: a tampered state.json could point it at an arbitrary file
			// (F7). Delete only when it resolves under a managed root; otherwise
			// refuse — the file is left intact and the caller retains ownership
			// (the entry is not reported as pruned).
			if !PrunePermitted(op.Dst, pruneRoots) {
				refused = append(refused, op.Dst)
				continue
			}
			if err := os.Remove(op.Dst); err != nil && !os.IsNotExist(err) {
				return nil, nil, nil, err
			}
			pruned = append(pruned, op.Dst)
		case Conflict, LocalEdit:
			// caller's responsibility — never auto-written or removed here
		}
	}
	sort.Strings(pruned)
	sort.Strings(refused)
	return recorded, pruned, refused, nil
}

// PrunePermitted reports whether dst is below a managed provider directory,
// with no symlinked parents inside that root. The root itself may be an alias
// (e.g. a dotfiles-managed agent directory). Callers must check before backing
// up a de-declared local edit as well as before deleting it.
// An empty root set permits nothing; a root itself is never a prune target.
func PrunePermitted(dst string, roots []string) bool {
	clean, err := filepath.Abs(dst)
	if err != nil {
		return false
	}
	for _, root := range roots {
		if root == "" {
			continue
		}
		root, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(root, clean)
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			continue
		}
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			continue
		}
		if fsutil.RequireRealParents(realRoot, filepath.Join(realRoot, filepath.Dir(rel))) == nil {
			return true
		}
	}
	return false
}
