// Package copyproj is the copy-mode projection contract: the content-file
// analogue of internal/adapter/structproj and internal/adapter/fileproj. It owns
// the plan/apply/state-record control flow that Claude and OpenCode otherwise
// each re-implement for their copy-mode subagent content files (subagentcopy.*)
// — reconciling desired bytes against recorded ownership hashes via
// internal/copyfile, backing up a local edit to <dst>.bak before overwrite or
// prune, and refusing to delete a prune destination outside the managed roots
// (F7). An adapter supplies only the desired dst->content map and its prune
// roots; the tool string keys state records and the conflict error.
package copyproj

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Novirmor/homonto/internal/copyfile"
	"github.com/Novirmor/homonto/internal/fsutil"
	"github.com/Novirmor/homonto/internal/state"
)

// keyPrefix is the state namespace for copy-mode subagent content files. Desired
// holds the dst path, Applied the content hash.
const keyPrefix = "subagentcopy."

// Name recovers the subagent name from a managed copy-file dst.
func Name(dst string) string { return strings.TrimSuffix(filepath.Base(dst), ".md") }

// recordedCopyHashes returns dst -> recorded content hash for every keyPrefix key
// in state (Desired holds the dst, Applied the content hash).
func recordedCopyHashes(st *state.State, tool string) map[string]string {
	out := map[string]string{}
	for _, key := range st.Keys(tool) {
		if !strings.HasPrefix(key, keyPrefix) {
			continue
		}
		if e, ok := st.Get(tool, key); ok {
			out[e.Desired] = e.Applied
		}
	}
	return out
}

// Plan computes the reconciler ops for the desired copy files against state.
func Plan(tool string, desired map[string][]byte, st *state.State) ([]copyfile.Op, error) {
	return copyfile.Plan(desired, recordedCopyHashes(st, tool))
}

// Apply reconciles copy-mode content files: it writes created/updated files,
// prunes de-declared ones, and backs up any local edit to <dst>.bak before
// overwriting or pruning (never losing a user's edit) — the pre-merge behavior;
// three-way merge replaces the backup+overwrite later. A destination occupied by
// a foreign file or a symlink is a conflict and aborts (keyed by tool). Records
// subagentcopy.* state for reconciled files and deletes it for pruned ones.
func Apply(tool string, desired map[string][]byte, st *state.State, pruneRoots []string) error {
	ops, err := Plan(tool, desired, st)
	if err != nil {
		return err
	}
	for i, op := range ops {
		switch op.Action {
		case copyfile.Conflict:
			return fmt.Errorf("%s: %s exists and is not a homonto-managed copy-mode subagent; not overwriting", tool, op.Dst)
		case copyfile.LocalEdit:
			if err := fsutil.WriteAtomic(op.Dst+".bak", op.OnDisk); err != nil {
				return err
			}
			if op.Content == nil {
				ops[i].Action = copyfile.Prune // de-declared + edited: backed up, now remove
			} else {
				ops[i].Action = copyfile.Update // declared + edited: backed up, now overwrite
			}
		}
	}
	rec, pruned, _, err := copyfile.Apply(ops, pruneRoots)
	if err != nil {
		return err
	}
	for dst, h := range rec {
		st.Set(tool, keyPrefix+Name(dst), dst, h)
	}
	// Refused prunes (dst outside the managed root — a tampered state entry) are
	// deliberately NOT in `pruned`, so their ownership record is retained rather
	// than dropped and the out-of-root file is never deleted (F7).
	for _, dst := range pruned {
		st.Delete(tool, keyPrefix+Name(dst))
	}
	return nil
}
