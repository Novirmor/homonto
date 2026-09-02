// Package fileproj is the file-projection contract: the symlink analogue of
// internal/adapter/structproj. It owns the plan/apply/observe control flow that
// Claude and OpenCode otherwise each re-implement for their skill./command./
// subagent. managed symlinks — create/relocate/relink/adopt planning, fail-fast
// conflict prechecks, inactive-scope pruning, link creation + state recording,
// and drift re-hashing. An adapter supplies only a flat []Link (destination,
// content source, state key, and the same-named other-scope path); the core
// never needs to know about directories, .md suffixes, or scopes.
//
// Unlike structproj, fileproj plans NO deletes: de-declared managed keys are
// pruned by the adapter's existing generic delete loop, so that loop stays the
// single source of file-prefix deletes (no double-delete). fileproj only
// consumes the deletes it produces, in ApplyState.
package fileproj

import (
	"os"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/link"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
	"strings"
)

// sep joins a link's destination and source in the recorded/hashed value
// "dst -> src". recordedDst cuts on it; the adopt, link, and observe hashes all
// use it, so it lives here as the single source of truth.
const sep = " -> "

// Link is one desired managed symlink for a resource type. Dst is where the
// link lives, Src the content it points to, Key the full state key (e.g.
// "skill.foo"), and Inactive the same-named link path at the OTHER scope (or ""
// when there is nothing to relocate from).
type Link struct {
	Dst      string
	Src      string
	Key      string
	Inactive string
}

// recordedDst extracts the destination path from a recorded "dst -> src" value.
// The recorded dst is where the link physically lives, independent of the
// adapter's current scope, so a pending scope switch is read at the right place.
func recordedDst(desired string) (string, bool) {
	dst, _, found := strings.Cut(desired, sep)
	return dst, found
}

// Project emits the create / relocate(update) / relink(update) + adopt-unrecorded
// changes for one link namespace. It plans NO deletes and does not sort (the
// adapter's final sort handles ordering; keys are unique). It returns link.Plan's
// conflict error unchanged.
func Project(tool string, links []Link, st *state.State, roots []string) ([]adapter.Change, error) {
	byDst := make(map[string]Link, len(links))
	srcs := make(map[string]string, len(links))
	for _, l := range links {
		byDst[l.Dst] = l
		srcs[l.Dst] = l.Src
	}
	ops, err := link.Plan(srcs, roots...)
	if err != nil {
		return nil, err
	}
	var changes []adapter.Change
	opDst := make(map[string]bool, len(ops))
	for _, op := range ops {
		opDst[op.Dst] = true
		l := byDst[op.Dst]
		switch {
		case op.Cur == "" && l.Inactive != "" && link.IsManaged(l.Inactive, roots...):
			// Scope switch: the same-named managed link still exists at the other
			// scope. Render as a relocate so the move (and the prune Apply performs)
			// is visible before confirm.
			changes = append(changes, adapter.Change{Action: "update", Key: l.Key, Old: l.Inactive, New: op.Dst + sep + op.Src, Cause: adapter.CauseRelocate})
		case op.Cur == "":
			changes = append(changes, adapter.Change{Action: "create", Key: l.Key, New: op.Dst + sep + op.Src, Cause: adapter.CauseDeclare})
		default:
			changes = append(changes, adapter.Change{Action: "update", Key: l.Key, Old: op.Cur, New: op.Src, Cause: adapter.CauseDriftFix})
		}
	}
	// Adopt a correct-but-unrecorded link — one already on disk pointing at its
	// content but absent from state (or stale). link.Plan omits a correct link, so
	// without this a lost state.json could never be rebuilt (apply short-circuits).
	// State-only: the on-disk link is left untouched.
	for _, l := range links {
		if opDst[l.Dst] {
			continue // a create/relink/relocate already covers it
		}
		tgt, err := os.Readlink(l.Dst)
		if err != nil || tgt != l.Src {
			continue // not a correct link into content
		}
		if e, ok := st.Get(tool, l.Key); ok && e.Applied == secret.Hash(l.Dst+sep+l.Src) {
			continue // already recorded → a true noop
		}
		changes = append(changes, adapter.Change{Action: "adopt", Key: l.Key, New: l.Dst + sep + l.Src, Cause: adapter.CauseAdopt})
	}
	return changes, nil
}

// Conflicts is the fail-fast precheck: link.Plan over the desired links, error
// only, no mutation. The adapter runs it for every link namespace BEFORE any
// document write or link mutation.
func Conflicts(links []Link, roots []string) error {
	srcs := make(map[string]string, len(links))
	for _, l := range links {
		srcs[l.Dst] = l.Src
	}
	_, err := link.Plan(srcs, roots...)
	return err
}

// ApplyState processes the state-only side of one namespace's already-prefix-
// filtered changes: "adopt" records the link into state without touching disk;
// "delete" resolves the on-disk dst (recorded dst, else fallbackDst) then
// link.Remove + st.Delete. It creates no links. Runs before doc writes.
func ApplyState(tool string, changes []adapter.Change, st *state.State, roots []string, fallbackDst func(key string) string) error {
	for _, c := range changes {
		switch c.Action {
		case "adopt":
			// A correct-but-unrecorded symlink recorded into state without touching
			// disk; its value is "dst -> src", recorded like a freshly linked one.
			st.Set(tool, c.Key, c.New, secret.Hash(c.New))
		case "delete":
			// Only a symlink into our content dir is removed; anything else is a
			// conflict error inside link.Remove. A de-declared resource's on-disk
			// location is recovered from the recorded dst; fall back otherwise.
			var dst string
			if e, ok := st.Get(tool, c.Key); ok {
				dst, _ = recordedDst(e.Desired)
			}
			if dst == "" && fallbackDst != nil {
				dst = fallbackDst(c.Key)
			}
			if err := link.Remove(dst, roots...); err != nil {
				return err
			}
			st.Delete(tool, c.Key)
		}
	}
	return nil
}

// ApplyLinks prunes each link's managed inactive-scope orphan, then creates the
// link and records state. Runs AFTER doc writes (create/update for these keys is
// symlink work, not JSON). noop/adopt/delete are handled by ApplyState.
func ApplyLinks(tool string, links []Link, st *state.State, roots []string) error {
	for _, l := range links {
		// Prune the same-named managed link at the other scope (a scope switch),
		// guarded by IsManaged so Remove only ever touches our own symlink.
		if l.Inactive != "" && link.IsManaged(l.Inactive, roots...) {
			if err := link.Remove(l.Inactive, roots...); err != nil {
				return err
			}
		}
		if _, err := link.Link(l.Src, l.Dst, roots...); err != nil {
			return err
		}
		st.Set(tool, l.Key, l.Dst+sep+l.Src, secret.Hash(l.Dst+sep+l.Src))
	}
	return nil
}

// Observe re-hashes each recorded key of prefix still on disk, read at its
// RECORDED dst (not the current scope — a pending scope switch leaves the applied
// link at the old location), the way ApplyLinks stored Entry.Applied. Keys absent
// from disk are omitted (the engine infers "missing").
func Observe(tool, prefix string, st *state.State) map[string]string {
	out := map[string]string{}
	for _, key := range st.Keys(tool) {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		e, ok := st.Get(tool, key)
		if !ok {
			continue
		}
		dst, ok := recordedDst(e.Desired)
		if !ok {
			continue
		}
		target, err := os.Readlink(dst)
		if err != nil {
			continue
		}
		out[key] = secret.Hash(dst + sep + target)
	}
	return out
}
