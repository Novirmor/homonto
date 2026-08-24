package snapshot

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// Diff compares base against a fresh capture of resultDir and returns the
// deterministic patch that transforms a tree at base.root_digest into the
// result tree. Content blobs of the result tree are written into blobDir
// (the directory holding <digest> files — store/blobs/sha256), so every
// operation's after-content is restorable from the store alone.
//
// The capture of resultDir runs under DefaultLimits with no exclusions:
// exclusions are a Capture-time input, and the journaled assignment flow
// keeps base and result consistent because the work tree was materialized
// from the (already excluded) base snapshot.
//
// Rename inference is deterministic: a pure delete and a pure add with an
// identical content digest, kind, AND mode collapse into one rename only
// when each is the only entry with that (digest, kind, mode) — any
// ambiguity stays delete+add. Kind changes are modify operations that
// carry both the before and after entry shape.
func Diff(ctx context.Context, base Manifest, resultDir, blobDir string) (PatchManifest, error) {
	if err := ValidateManifest(base); err != nil {
		return PatchManifest{}, fmt.Errorf("snapshot: diff base: %w", err)
	}
	if err := validateAbsDir("result dir", resultDir); err != nil {
		return PatchManifest{}, err
	}
	if err := validateAbsDir("blob dir", blobDir); err != nil {
		return PatchManifest{}, err
	}
	result, err := capture(ctx, resultDir, Limits{}.withDefaults(), nil, blobDir)
	if err != nil {
		return PatchManifest{}, err
	}
	result.RepositoryID = base.RepositoryID

	ops := diffEntries(base.Entries, result.Entries)
	patch := PatchManifest{
		SchemaVersion: SchemaVersion,
		RepositoryID:  base.RepositoryID,
		BaseDigest:    base.RootDigest,
		ResultDigest:  result.RootDigest,
		Operations:    ops,
	}
	if err := ValidatePatch(patch); err != nil {
		return PatchManifest{}, fmt.Errorf("snapshot: diff produced an invalid patch: %w", err)
	}
	return patch, nil
}

// diffEntries computes the sorted operation list between two sorted entry
// lists, pairing 1:1 identical delete+add pairs into renames.
func diffEntries(base, result []Entry) []PatchOp {
	baseByPath := make(map[string]Entry, len(base))
	for _, e := range base {
		baseByPath[e.Path] = e
	}
	resultByPath := make(map[string]Entry, len(result))
	for _, e := range result {
		resultByPath[e.Path] = e
	}

	var deletes, adds, modifies []PatchOp
	for _, b := range base {
		r, ok := resultByPath[b.Path]
		if !ok {
			deletes = append(deletes, deleteOp(b))
			continue
		}
		if entryEqual(b, r) {
			continue
		}
		modifies = append(modifies, modifyOp(b, r))
	}
	for _, r := range result {
		if _, ok := baseByPath[r.Path]; !ok {
			adds = append(adds, addOp(r))
		}
	}

	ops := pairRenames(deletes, adds)
	ops = append(ops, modifies...)
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	if ops == nil {
		ops = []PatchOp{}
	}
	return ops
}

func entryEqual(a, b Entry) bool {
	return a.Kind == b.Kind && a.Mode == b.Mode && a.Size == b.Size &&
		a.Digest == b.Digest && a.LinkTarget == b.LinkTarget
}

func addOp(e Entry) PatchOp {
	return PatchOp{Op: OpAdd, Path: e.Path, Kind: e.Kind, Mode: e.Mode, Size: e.Size, Digest: e.Digest, LinkTarget: e.LinkTarget}
}

func deleteOp(e Entry) PatchOp {
	return PatchOp{Op: OpDelete, Path: e.Path, Kind: e.Kind, Mode: e.Mode,
		BeforeKind: e.Kind, BeforeMode: e.Mode, BeforeDigest: e.Digest}
}

func modifyOp(b, r Entry) PatchOp {
	return PatchOp{Op: OpModify, Path: r.Path, Kind: r.Kind, Mode: r.Mode, Size: r.Size,
		Digest: r.Digest, LinkTarget: r.LinkTarget,
		BeforeKind: b.Kind, BeforeMode: b.Mode, BeforeDigest: b.Digest}
}

// pairRenames consumes 1:1 delete+add pairs with identical (digest, kind,
// mode) content and rewrites them as renames; ambiguous multiplicities
// stay delete+add. Kind is part of the key — a deleted file and an added
// symlink can share bytes and mode bits but are not a rename — and
// directories carry no content and never pair.
func pairRenames(deletes, adds []PatchOp) []PatchOp {
	type key struct {
		digest string
		kind   string
		mode   uint32
	}
	delCount := map[key]int{}
	addCount := map[key]int{}
	for _, op := range deletes {
		if op.Kind != KindDir {
			delCount[key{op.BeforeDigest, op.Kind, op.BeforeMode}]++
		}
	}
	for _, op := range adds {
		if op.Kind != KindDir {
			addCount[key{op.Digest, op.Kind, op.Mode}]++
		}
	}

	var ops []PatchOp
	pairedAdds := map[string]bool{}
	for _, del := range deletes {
		if del.Kind == KindDir {
			ops = append(ops, del)
			continue
		}
		k := key{del.BeforeDigest, del.Kind, del.BeforeMode}
		if delCount[k] == 1 && addCount[k] == 1 {
			// Find the unique add with this content and pair it.
			for _, add := range adds {
				if add.Kind != KindDir && add.Digest == k.digest && add.Kind == k.kind && add.Mode == k.mode {
					pairedAdds[add.Path] = true
					ops = append(ops, PatchOp{
						Op: OpRename, Path: add.Path, OldPath: del.Path,
						Kind: add.Kind, Mode: add.Mode, Size: add.Size, Digest: add.Digest,
						LinkTarget: add.LinkTarget,
						BeforeKind: del.BeforeKind, BeforeMode: del.BeforeMode,
						BeforeDigest: del.BeforeDigest,
					})
					break
				}
			}
			continue
		}
		ops = append(ops, del)
	}
	for _, add := range adds {
		if !pairedAdds[add.Path] {
			ops = append(ops, add)
		}
	}
	return ops
}

// ValidateScope checks that every path a patch touches falls inside the
// declared scope (member-root-relative, mirroring gitx): an empty scope —
// or a "" or "." entry — covers the whole tree; otherwise a path is
// in scope when it equals an entry or lives under "entry/". Both the
// operation path and a rename's source are checked. Violations return a
// typed ScopeViolationError listing every offender.
func ValidateScope(patch PatchManifest, scope []string) error {
	normalized, err := normalizeScope(scope)
	if err != nil {
		return err
	}
	var offenders []string
	for _, op := range patch.Operations {
		if !inScope(op.Path, normalized) {
			offenders = append(offenders, op.Path)
		}
		if op.OldPath != "" && !inScope(op.OldPath, normalized) {
			offenders = append(offenders, op.OldPath)
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		return &ScopeViolationError{Paths: offenders}
	}
	return nil
}

// normalizeScope cleans scope entries the way gitx does: backslashes fold
// to slashes, "./" prefixes and trailing slashes drop, "" and "." mean
// unrestricted, absolute paths and ".." components are rejected.
func normalizeScope(scope []string) ([]string, error) {
	out := make([]string, 0, len(scope))
	for _, s := range scope {
		s = strings.TrimSpace(s)
		if s == "" || s == "." {
			out = append(out, "")
			continue
		}
		s = strings.ReplaceAll(s, "\\", "/")
		s = strings.TrimPrefix(s, "./")
		s = strings.TrimSuffix(s, "/")
		if strings.HasPrefix(s, "/") {
			return nil, &InvalidPathError{Path: s, Reason: "scope entries must be relative"}
		}
		for _, part := range strings.Split(s, "/") {
			if part == ".." {
				return nil, &InvalidPathError{Path: s, Reason: "scope entries must not escape the root"}
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func inScope(rel string, scope []string) bool {
	if len(scope) == 0 {
		return true
	}
	for _, s := range scope {
		if s == "" || rel == s || strings.HasPrefix(rel, s+"/") {
			return true
		}
	}
	return false
}

// InvertPatch returns the patch that undoes p operation by operation:
// adds become deletes, deletes become adds (content restored from the
// before blobs), modifies swap their before/after states, and renames
// reverse direction. The inverse's digests name the two trees it walks
// between, but no full result manifest exists for it, so callers apply it
// without a final result-digest verification (applyPatch's verify flag).
func InvertPatch(p PatchManifest) (PatchManifest, error) {
	if err := ValidatePatch(p); err != nil {
		return PatchManifest{}, err
	}
	inv := PatchManifest{
		SchemaVersion: p.SchemaVersion,
		RepositoryID:  p.RepositoryID,
		BaseDigest:    p.ResultDigest,
		ResultDigest:  p.BaseDigest,
		Operations:    make([]PatchOp, 0, len(p.Operations)),
	}
	for _, op := range p.Operations {
		switch op.Op {
		case OpAdd:
			inv.Operations = append(inv.Operations, PatchOp{
				Op: OpDelete, Path: op.Path, Kind: op.Kind, Mode: op.Mode,
				BeforeKind: op.Kind, BeforeMode: op.Mode, BeforeDigest: op.Digest,
			})
		case OpDelete:
			inv.Operations = append(inv.Operations, PatchOp{
				Op: OpAdd, Path: op.Path, Kind: op.Kind, Mode: op.Mode,
				Size: contentSize(p, op.BeforeDigest), Digest: op.BeforeDigest,
			})
		case OpModify:
			inv.Operations = append(inv.Operations, PatchOp{
				Op: OpModify, Path: op.Path,
				Kind: op.BeforeKind, Mode: op.BeforeMode,
				Size: contentSize(p, op.BeforeDigest), Digest: op.BeforeDigest,
				BeforeKind: op.Kind, BeforeMode: op.Mode, BeforeDigest: op.Digest,
			})
		case OpRename:
			inv.Operations = append(inv.Operations, PatchOp{
				Op: OpRename, Path: op.OldPath, OldPath: op.Path,
				Kind: op.Kind, Mode: op.Mode, Size: op.Size, Digest: op.Digest,
				LinkTarget: op.LinkTarget,
				BeforeKind: op.Kind, BeforeMode: op.Mode,
				BeforeDigest: op.BeforeDigest,
			})
		}
	}
	sort.Slice(inv.Operations, func(i, j int) bool { return inv.Operations[i].Path < inv.Operations[j].Path })
	if err := ValidatePatch(inv); err != nil {
		return PatchManifest{}, fmt.Errorf("snapshot: inverted patch invalid: %w", err)
	}
	return inv, nil
}

// contentSize recovers an entry size from a digest by searching the
// patch's operations; unknown digests yield 0 (sizes are informational —
// digests decide identity).
func contentSize(p PatchManifest, digest string) int64 {
	if digest == "" {
		return 0
	}
	for _, op := range p.Operations {
		if op.Digest == digest && op.Size > 0 {
			return op.Size
		}
	}
	return 0
}

// VerifyStage verifies a sequentially-integrated stage: applying patches
// in the given order to base must produce exactly the tree at stageDir.
// This is the terminal verification for a multi-material integration —
// no single patch's result digest can cover a stage carrying earlier
// materials, so the engine calls this after the LAST ApplyToStage. It is
// read-only: a failure names the offending path (typed VerifyError) and
// reverts nothing; the journaled applies are already final.
func VerifyStage(ctx context.Context, base Manifest, patches []PatchManifest, stageDir string) error {
	if err := ValidateManifest(base); err != nil {
		return fmt.Errorf("snapshot: verify stage: %w", err)
	}
	for _, p := range patches {
		if err := ValidatePatch(p); err != nil {
			return fmt.Errorf("snapshot: verify stage: %w", err)
		}
	}
	if err := validateAbsDir("stage dir", stageDir); err != nil {
		return err
	}
	expected := expectedStageEntries(base, patches)
	got, err := capture(ctx, stageDir, Limits{}.withDefaults(), nil, "")
	if err != nil {
		return err
	}
	return compareEntries(expected, got.Entries)
}

// expectedStageEntries folds the operations of patches (in order) into
// base's entry set. Well-formed (Diff-produced) patches are explicit —
// every directory is an entry, every removal an operation — so the fold
// is literal, with no implicit pruning or creation.
func expectedStageEntries(base Manifest, patches []PatchManifest) []Entry {
	m := make(map[string]Entry, len(base.Entries))
	for _, e := range base.Entries {
		m[e.Path] = e
	}
	for _, p := range patches {
		for _, op := range p.Operations {
			after := Entry{Path: op.Path, Kind: op.Kind, Mode: op.Mode, Size: op.Size, Digest: op.Digest, LinkTarget: op.LinkTarget}
			switch op.Op {
			case OpDelete:
				delete(m, op.Path)
			case OpAdd, OpModify:
				m[op.Path] = after
			case OpRename:
				delete(m, op.OldPath)
				m[op.Path] = after
			}
		}
	}
	out := make([]Entry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
