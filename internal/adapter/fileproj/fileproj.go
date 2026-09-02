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
	"path/filepath"

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
// link lives, Src the content it points to (relative when Dst and Src share a
// relocation domain, absolute otherwise), Key the full state key (e.g.
// "skill.foo"), Inactive the same-named link path at the OTHER scope (or ""
// when there is nothing to relocate from), and Domain the project root whose
// wholesale move (rename) both Dst and Src survive together — the
// pre-authorization a stale absolute link needs before repair (ADR 0026).
type Link struct {
	Dst      string
	Src      string
	Key      string
	Inactive string
	Domain   string
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
// conflict error unchanged — except where a stale link left by a wholesale
// repository move is repairable: exactly matching its recorded target, with
// recorded destination and source sitting at the same domain-relative
// positions the desired link occupies now (ADR 0026's repair authorization).
func Project(tool string, links []Link, st *state.State, roots []string) ([]adapter.Change, error) {
	byDst := make(map[string]Link, len(links))
	srcs := make(map[string]string, len(links))
	for _, l := range links {
		byDst[l.Dst] = l
		srcs[l.Dst] = l.Src
	}
	ops, err := link.Plan(srcs, roots...)
	if err != nil {
		// A conflict aborts Plan wholesale. Repairs may rescue it: if every
		// conflicting link is an authorized repair, re-plan the remainder and
		// append the repair ops; any genuinely foreign link still fails.
		repairs := planRepairs(tool, links, st, roots)
		if len(repairs) == 0 {
			return nil, err
		}
		repairedDst := map[string]bool{}
		for _, op := range repairs {
			repairedDst[op.Dst] = true
		}
		filtered := make(map[string]string, len(srcs))
		for dst, src := range srcs {
			if !repairedDst[dst] {
				filtered[dst] = src
			}
		}
		rest, err2 := link.Plan(filtered, roots...)
		if err2 != nil {
			return nil, err
		}
		ops = append(rest, repairs...)
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
		if err != nil || !link.SameTarget(l.Dst, l.Src, tgt) {
			continue // not a correct link into content
		}
		if e, ok := st.Get(tool, l.Key); ok && e.Applied == secret.Hash(l.Dst+sep+l.Src) {
			continue // already recorded → a true noop
		}
		changes = append(changes, adapter.Change{Action: "adopt", Key: l.Key, New: l.Dst + sep + l.Src, Cause: adapter.CauseAdopt})
	}
	return changes, nil
}

// planRepairs inspects the links link.Plan rejected as foreign and authorizes
// repair for exactly one case: a wholesale repository move. The on-disk target
// must match the recorded target EXACTLY, and the recorded destination and
// source must sit at the same domain-relative positions the desired link
// occupies now — proving the link is ours, just stranded by a rename, never a
// user's foreign link being repointed.
func planRepairs(tool string, links []Link, st *state.State, roots []string) []link.Op {
	var out []link.Op
	for _, l := range links {
		if op, ok := authorizedRepair(tool, l, st); ok {
			out = append(out, *op)
		}
	}
	return out
}

// authorizedRepair is the single repair rule (ADR 0026), shared by plan,
// conflict prechecks, and apply: a link at l.Dst whose target EXACTLY equals
// the recorded target, where the recorded src sat at the same domain-relative
// position the desired src occupies now — a link stranded by a wholesale
// repository move and nothing else. Two shapes qualify: the in-domain link
// (dst under the domain; recorded dst ends with the same domain-relative
// path) and the user-scope link whose SOURCE moved with the repository while
// its destination (under $HOME) did not (recorded dst equals the current dst
// exactly).
func authorizedRepair(tool string, l Link, st *state.State) (*link.Op, bool) {
	if l.Domain == "" {
		return nil, false
	}
	tgt, err := os.Readlink(l.Dst)
	if err != nil {
		return nil, false // not present or not a link
	}
	e, ok := st.Get(tool, l.Key)
	if !ok {
		return nil, false // unrecorded foreign link stays a conflict
	}
	recordedDst, hasDst := recordedDst(e.Desired)
	_, recordedSrc, hasSrc := strings.Cut(e.Desired, sep)
	if !hasDst || !hasSrc || tgt != recordedSrc {
		return nil, false // not exactly what we recorded
	}
	srcAbs := resolveTarget(l.Src, l.Dst)
	srcRel, err2 := filepath.Rel(l.Domain, srcAbs)
	if err2 != nil || srcRel == "." || strings.HasPrefix(srcRel, "..") {
		return nil, false // desired source outside the domain: nothing anchored the move
	}
	if filepath.Clean(recordedSrc) != filepath.Clean(filepath.Join(trimSuffixDir(recordedSrc, srcRel), srcRel)) {
		return nil, false // recorded source is not domain-relative-shaped; cannot verify
	}
	oldDomain := trimSuffixDir(recordedSrc, srcRel)
	if filepath.Clean(recordedSrc) != filepath.Clean(filepath.Join(oldDomain, srcRel)) {
		return nil, false
	}
	dstRel, err1 := filepath.Rel(l.Domain, l.Dst)
	inDomain := err1 == nil && dstRel != "." && !strings.HasPrefix(dstRel, "..")
	if inDomain {
		if filepath.Clean(recordedDst) != filepath.Clean(filepath.Join(trimSuffixDir(recordedDst, dstRel), dstRel)) {
			return nil, false // recorded dst does not share the domain-relative position
		}
	} else {
		// Out-of-domain destination (user scope): the link's location never
		// moved; only its source did. Require the recorded dst to BE the
		// current dst, so nothing about the destination is being guessed.
		if filepath.Clean(recordedDst) != filepath.Clean(l.Dst) {
			return nil, false
		}
	}
	return &link.Op{Dst: l.Dst, Src: l.Src, Cur: tgt}, true
}

// trimSuffixDir removes exactly one occurrence of rel (a slash-relative path)
// from the tail of abs, returning the prefix. Caller verifies the result by
// re-joining.
func trimSuffixDir(abs, rel string) string {
	relFromSlash := filepath.FromSlash(rel)
	if strings.HasSuffix(filepath.Clean(abs), string(filepath.Separator)+relFromSlash) {
		return filepath.Clean(abs)[:len(filepath.Clean(abs))-len(relFromSlash)]
	}
	if filepath.Clean(abs) == relFromSlash {
		return ""
	}
	return abs // no match; re-join check will fail
}

// resolveTarget makes a link target absolute against the link's directory.
func resolveTarget(target, dst string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(dst), target))
}

// Conflicts is the fail-fast precheck: link.Plan over the desired links, error
// only, no mutation. A stale link stranded by a wholesale repository move
// (authorized repair) is not a conflict. The adapter runs it for every link
// namespace BEFORE any document write or link mutation.
func Conflicts(tool string, links []Link, st *state.State, roots []string) error {
	srcs := make(map[string]string, len(links))
	for _, l := range links {
		srcs[l.Dst] = l.Src
	}
	if _, err := link.Plan(srcs, roots...); err == nil {
		return nil
	}
	// Some link conflicts: every conflicting link must be an authorized
	// repair, else the conflict stands.
	for _, l := range links {
		if _, ok := authorizedRepair(tool, l, st); ok {
			continue
		}
		if _, err := link.Plan(map[string]string{l.Dst: l.Src}, roots...); err != nil {
			return err
		}
	}
	return nil
}

// ApplyState processes the state-only side of one namespace's already-prefix-
// filtered changes: "adopt" records the link into state without touching disk;
// "delete" resolves the on-disk dsts (recorded dst plus the adapter's current
// fallback dsts, both scopes) then link.Remove + st.Delete for each. It
// creates no links. Runs before doc writes.
func ApplyState(tool string, changes []adapter.Change, st *state.State, roots []string, fallbackDst func(key string) []string) error {
	for _, c := range changes {
		switch c.Action {
		case "adopt":
			// A correct-but-unrecorded symlink recorded into state without touching
			// disk; its value is "dst -> src", recorded like a freshly linked one.
			st.Set(tool, c.Key, c.New, secret.Hash(c.New))
		case "delete":
			// Only a symlink into our content dir is removed; anything else is a
			// conflict error inside link.Remove. A de-declared resource's on-disk
			// location is recovered from the recorded dst; the adapter's fallback
			// dsts cover the CURRENT locations at both scopes, so a repository
			// move between the apply that recorded the entry and this prune
			// leaves no orphan (L5): Remove is guarded to our own symlinks.
			var dsts []string
			if e, ok := st.Get(tool, c.Key); ok {
				if d, found := recordedDst(e.Desired); found {
					dsts = append(dsts, d)
				}
			}
			if fallbackDst != nil {
				dsts = append(dsts, fallbackDst(c.Key)...)
			}
			for _, dst := range dsts {
				if err := link.Remove(dst, roots...); err != nil {
					return err
				}
			}
			st.Delete(tool, c.Key)
		}
	}
	return nil
}

// ApplyLinks prunes each link's managed inactive-scope orphan, then creates the
// link and records state. Runs AFTER doc writes (create/update for these keys is
// symlink work, not JSON). noop/adopt/delete are handled by ApplyState. A link
// whose current target is a stale-but-recorded spelling (an authorized repair)
// is relinked in place; a foreign link still fails.
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
			if _, ok := authorizedRepair(tool, l, st); !ok {
				return err
			}
			// Authorized repair: the link is ours, stranded by a wholesale
			// repository move. Replace it with the desired target.
			if err := os.Remove(l.Dst); err != nil {
				return err
			}
			if err := os.Symlink(l.Src, l.Dst); err != nil {
				return err
			}
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
