package snapshot

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// Materialize recreates the tree of manifest exactly at destination:
// files byte-identical with their recorded modes (umask defeated), empty
// directories included, symlinks recreated verbatim (their targets are
// data; absolute and ..-escaping targets are recreated as-is — the
// snapshot never follows them). A destination that exists and is not an
// empty directory is refused with ErrDestinationExists; nothing is ever
// written outside destination (parent directories are created only
// through symlink-refusing traversal). Bytes are copied from the store —
// never hardlinked — so materialized trees stay independent of the store.
func Materialize(ctx context.Context, manifest Manifest, store, destination string) error {
	if err := ValidateManifest(manifest); err != nil {
		return fmt.Errorf("snapshot: materialize: %w", err)
	}
	if err := validateAbsDir("store", store); err != nil {
		return err
	}
	if err := validateAbsDir("destination", destination); err != nil {
		return err
	}
	if err := prepareDestination(destination); err != nil {
		return err
	}

	blobDir := BlobDir(store)
	// Directories first (sorted paths put parents before children), then
	// content entries.
	for _, e := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if e.Kind != KindDir {
			continue
		}
		if err := secureMkdirAll(filepath.Join(destination, filepath.FromSlash(e.Path)), fs.FileMode(e.Mode)); err != nil {
			return err
		}
	}
	for _, e := range manifest.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch e.Kind {
		case KindFile:
			if err := materializeFile(destination, e, blobDir); err != nil {
				return err
			}
		case KindSymlink:
			if err := secureMkdirAll(filepath.Dir(filepath.Join(destination, filepath.FromSlash(e.Path))), storeDirPerm); err != nil {
				return err
			}
			target := filepath.Join(destination, filepath.FromSlash(e.Path))
			if err := os.Symlink(e.LinkTarget, target); err != nil {
				return fmt.Errorf("snapshot: symlink %s: %w", e.Path, err)
			}
		}
	}
	return nil
}

// prepareDestination ensures destination exists as an empty directory.
func prepareDestination(destination string) error {
	switch info, err := os.Lstat(destination); {
	case err == nil:
		if !info.IsDir() {
			return &DestinationExistsError{Path: destination}
		}
		empty, err := dirEmpty(destination)
		if err != nil {
			return err
		}
		if !empty {
			return &DestinationExistsError{Path: destination}
		}
		return nil
	case errors.Is(err, fs.ErrNotExist):
		if err := secureMkdirAll(destination, storeDirPerm); err != nil {
			return err
		}
		return nil
	default:
		return fmt.Errorf("snapshot: stat destination %s: %w", destination, err)
	}
}

// DestinationExistsError names the refused materialize destination.
type DestinationExistsError struct {
	Path string
}

func (e *DestinationExistsError) Error() string {
	return fmt.Sprintf("snapshot: destination %s exists and is not empty", e.Path)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *DestinationExistsError) Unwrap() error { return ErrDestinationExists }

func dirEmpty(dir string) (bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false, fmt.Errorf("snapshot: read destination %s: %w", dir, err)
	}
	return len(entries) == 0, nil
}

// materializeFile copies one blob into a tree with the recorded mode:
// content lands in a temp file (0600), is fsynced, renamed over the final
// name, then chmod'ed to the exact recorded permission. The copy is
// hashed as it goes — bytes that do not hash to the entry's digest (a
// corrupted same-length blob) abort before the rename, so corrupt
// content never lands. Parent directories are created only through
// symlink-refusing traversal; the blob itself is opened O_NOFOLLOW.
func materializeFile(destination string, e Entry, blobDir string) error {
	full := filepath.Join(destination, filepath.FromSlash(e.Path))
	parent := filepath.Dir(full)
	if err := secureMkdirAll(parent, storeDirPerm); err != nil {
		return err
	}
	src, err := os.OpenFile(filepath.Join(blobDir, e.Digest), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return &BlobMissingError{Digest: e.Digest}
	}
	defer src.Close()
	tmp, err := os.CreateTemp(parent, snapshotTempPrefix)
	if err != nil {
		return fmt.Errorf("snapshot: create temp for %s: %w", e.Path, err)
	}
	name := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			tmp.Close()
			os.Remove(name)
		}
	}()
	h := blobHasher()
	if _, err := io.Copy(io.MultiWriter(tmp, h), src); err != nil {
		return fmt.Errorf("snapshot: copy blob for %s: %w", e.Path, err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != e.Digest {
		return &BlobCorruptError{Digest: e.Digest}
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("snapshot: fsync %s: %w", e.Path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("snapshot: close temp for %s: %w", e.Path, err)
	}
	if err := os.Rename(name, full); err != nil {
		return fmt.Errorf("snapshot: place %s: %w", e.Path, err)
	}
	committed = true
	if err := os.Chmod(full, fs.FileMode(e.Mode)); err != nil {
		return fmt.Errorf("snapshot: chmod %s: %w", e.Path, err)
	}
	return nil
}

// Verify recomputes the tree at root and compares it entry by entry
// against manifest. Any divergence — missing entry, extra path, changed
// content, mode, or kind — is a typed VerifyError naming the first
// offending path. The walk is digest-only (no blobs are written). A tree
// captured with exclusions does not Verify against the raw source;
// verify materialized trees (work, stage) or re-apply the same exclusions
// by capturing.
func Verify(ctx context.Context, root string, manifest Manifest) error {
	if err := ValidateManifest(manifest); err != nil {
		return fmt.Errorf("snapshot: verify: %w", err)
	}
	if err := validateAbsDir("root", root); err != nil {
		return err
	}
	got, err := capture(ctx, root, Limits{}.withDefaults(), nil, "")
	if err != nil {
		return err
	}
	return compareEntries(manifest.Entries, got.Entries)
}

// compareEntries walks two sorted entry lists in lockstep.
func compareEntries(want, got []Entry) error {
	i, j := 0, 0
	for i < len(want) || j < len(got) {
		switch {
		case j >= len(got) || (i < len(want) && want[i].Path < got[j].Path):
			return &VerifyError{Path: want[i].Path, Reason: "missing from tree"}
		case i >= len(want) || got[j].Path < want[i].Path:
			return &VerifyError{Path: got[j].Path, Reason: "not in manifest"}
		}
		w, g := want[i], got[j]
		if w.Kind != g.Kind {
			return &VerifyError{Path: w.Path, Reason: fmt.Sprintf("kind %s, want %s", g.Kind, w.Kind)}
		}
		if w.Mode != g.Mode {
			return &VerifyError{Path: w.Path, Reason: fmt.Sprintf("mode %#o, want %#o", g.Mode, w.Mode)}
		}
		if w.Digest != g.Digest {
			return &VerifyError{Path: w.Path, Reason: fmt.Sprintf("digest %s, want %s", g.Digest, w.Digest)}
		}
		if w.LinkTarget != g.LinkTarget {
			return &VerifyError{Path: w.Path, Reason: "symlink target changed"}
		}
		if w.Size != g.Size {
			return &VerifyError{Path: w.Path, Reason: fmt.Sprintf("size %d, want %d", g.Size, w.Size)}
		}
		i++
		j++
	}
	return nil
}

// Apply transforms stageDir from the tree named by patch.BaseDigest into
// the tree named by patch.ResultDigest. It is idempotent and
// conflict-safe in three phases:
//
//  1. Verify every operation's preimage — a path the operation expects
//     absent must be absent, a path it expects present must hold the
//     recorded before-digest, kind, and mode — treating operations whose
//     after-state is already present as applied and skipped (a delete of
//     an already-absent path counts as applied: ADR 0025 roll-forward
//     re-apply must converge, so absence is indistinguishable from and
//     equal to done). Any divergence aborts before anything is mutated:
//     content mismatches are ErrPatchPreimageMismatch, structural
//     mismatches ErrPatchConflict, and divergent content is never
//     overwritten. Missing after-content blobs are ErrBlobMissing, also
//     before any mutation.
//  2. Apply the pending operations in path order. Content always lands
//     through temp files and renames inside stageDir — staged outputs
//     never reach the source — and parent directories are created only
//     through symlink-refusing traversal, so a crafted symlink in the op
//     set cannot redirect writes outside the stage.
//  3. Recompute the stage tree and require its root digest to equal
//     ResultDigest (ErrResultMismatch otherwise) — the backstop that
//     catches a stage that never was at BaseDigest (for example a delete
//     whose path was already missing because the stage never held it).
//
// A crash mid-apply converges on re-apply: applied operations skip, the
// rest run — the property the journal's roll-forward recovery leans on.
func Apply(ctx context.Context, stageDir, blobDir string, patch PatchManifest) error {
	return applyPatch(ctx, stageDir, blobDir, patch, true)
}

// applyPatch is Apply without the final result verification when verify
// is false — the revert path applies inverse patches whose "result" tree
// has no manifest to digest against.
func applyPatch(ctx context.Context, stageDir, blobDir string, patch PatchManifest, verify bool) error {
	if err := ValidatePatch(patch); err != nil {
		return fmt.Errorf("snapshot: apply patch: %w", err)
	}
	if err := validateAbsDir("stage dir", stageDir); err != nil {
		return err
	}
	if err := validateAbsDir("blob dir", blobDir); err != nil {
		return err
	}
	if err := os.MkdirAll(stageDir, storeDirPerm); err != nil {
		return fmt.Errorf("snapshot: mkdir stage %s: %w", stageDir, err)
	}

	st := &stageState{root: stageDir, blobDir: blobDir}
	pending, err := st.verifyPreimages(patch.Operations)
	if err != nil {
		return err
	}
	ops := make([]PatchOp, len(patch.Operations))
	copy(ops, patch.Operations)
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	for _, op := range ops {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !pending[op.Path] {
			continue // already applied
		}
		if err := st.applyOp(op); err != nil {
			return err
		}
	}
	if verify {
		got, err := capture(ctx, stageDir, Limits{}.withDefaults(), nil, "")
		if err != nil {
			return err
		}
		if got.RootDigest != patch.ResultDigest {
			return &ResultMismatchError{Want: patch.ResultDigest, Got: got.RootDigest}
		}
	}
	return nil
}

// ResultMismatchError names the digests that disagreed after an apply.
type ResultMismatchError struct {
	Want, Got fingerprint.Digest
}

func (e *ResultMismatchError) Error() string {
	return fmt.Sprintf("snapshot: stage digest %s, patch result digest %s", e.Got, e.Want)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *ResultMismatchError) Unwrap() error { return ErrResultMismatch }

// stageState caches stage lookups for one apply run.
type stageState struct {
	root    string
	blobDir string
}

// entryState describes what currently sits at one stage path.
type entryState struct {
	kind       string // KindFile, KindSymlink, KindDir, or "" when absent
	mode       uint32
	digest     string
	linkTarget string
}

// lookup lstats path under the stage root and digests content when the
// entry is a file or symlink (symlink content = target bytes).
func (s *stageState) lookup(rel string) (entryState, error) {
	full := filepath.Join(s.root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) {
		return entryState{}, nil
	}
	if err != nil {
		return entryState{}, fmt.Errorf("snapshot: lstat stage %s: %w", rel, err)
	}
	st := entryState{mode: uint32(info.Mode().Perm())}
	switch {
	case info.Mode().IsRegular():
		st.kind = KindFile
		d, err := digestFileNoFollow(full)
		if err != nil {
			return entryState{}, err
		}
		st.digest = d
	case info.Mode()&fs.ModeSymlink != 0:
		st.kind = KindSymlink
		target, err := os.Readlink(full)
		if err != nil {
			return entryState{}, fmt.Errorf("snapshot: readlink stage %s: %w", rel, err)
		}
		st.linkTarget = target
		st.digest = digestBytes([]byte(target))
	case info.IsDir():
		st.kind = KindDir
	default:
		st.kind = "special"
	}
	return st, nil
}

// verifyPreimages classifies every operation against the current stage:
// operations already holding their after-state are skipped (a rename
// additionally requires its source gone — a destination match with a
// lingering source is an orphan, not a completed rename), operations
// whose preimage holds are returned as pending, and anything divergent
// aborts with a typed error naming the path. Blob existence for pending
// after-content is checked here too — before any mutation.
func (s *stageState) verifyPreimages(ops []PatchOp) (map[string]bool, error) {
	pending := map[string]bool{}
	needBlob := map[string]bool{}
	for _, op := range ops {
		state, err := s.lookup(op.Path)
		if err != nil {
			return nil, err
		}
		if opApplied(op, state) {
			if op.Op == OpRename {
				src, err := s.lookup(op.OldPath)
				if err != nil {
					return nil, err
				}
				if src.kind != "" {
					return nil, &PatchConflictError{Path: op.OldPath,
						Reason: fmt.Sprintf("rename result present at %s but source still exists (orphan)", op.Path)}
				}
			}
			continue
		}
		if err := s.checkPreimage(op, state); err != nil {
			return nil, err
		}
		pending[op.Path] = true
		if (op.Op == OpAdd || op.Op == OpModify) && op.Kind == KindFile {
			needBlob[op.Digest] = true
		}
	}
	for digest := range needBlob {
		if err := checkBlob(s.blobDir, digest); err != nil {
			return nil, err
		}
	}
	return pending, nil
}

// opApplied reports whether the stage already holds the operation's
// after-state at its path — an operation re-run after a crash skips.
func opApplied(op PatchOp, state entryState) bool {
	return state == afterState(op)
}

// afterState is the entryState an operation leaves at its Path.
func afterState(op PatchOp) entryState {
	switch op.Op {
	case OpDelete:
		return entryState{}
	case OpRename, OpAdd, OpModify:
		return entryState{kind: op.Kind, mode: op.Mode, digest: op.Digest, linkTarget: op.LinkTarget}
	}
	return entryState{}
}

// checkPreimage verifies the operation's before-state against what sits
// at its path (and, for renames, the source path). Adds expect absence at
// their path; renames expect absence at the destination and their content
// preimage at the source; deletes and modifies expect their recorded
// preimage at the path.
func (s *stageState) checkPreimage(op PatchOp, state entryState) error {
	switch op.Op {
	case OpAdd:
		if state.kind != "" {
			return &PatchConflictError{Path: op.Path, Reason: fmt.Sprintf("%s exists where the patch adds a %s", kindName(state.kind), op.Kind)}
		}
		return nil
	case OpRename:
		if state.kind != "" {
			return &PatchConflictError{Path: op.Path, Reason: fmt.Sprintf("%s exists at the rename destination", kindName(state.kind))}
		}
		src, err := s.lookup(op.OldPath)
		if err != nil {
			return err
		}
		if src.kind == "" {
			return &PatchConflictError{Path: op.OldPath, Reason: "rename source absent"}
		}
		if src.kind != op.Kind || src.digest != op.BeforeDigest || src.mode != op.Mode {
			return &PreimageMismatchError{Path: op.OldPath, Want: op.BeforeDigest, Got: src.digest}
		}
		return nil
	}
	if op.Op == OpDelete && op.Kind == KindDir {
		if state.kind == KindDir {
			return nil
		}
		if state.kind == "" {
			return &PatchConflictError{Path: op.Path, Reason: "directory expected, path absent"}
		}
		return &PatchConflictError{Path: op.Path, Reason: fmt.Sprintf("directory expected, found %s", kindName(state.kind))}
	}
	// Content-bearing preimage (modify, and delete of files/symlinks):
	// kind, mode, and digest must all match.
	if state.kind == "" {
		return &PatchConflictError{Path: op.Path, Reason: fmt.Sprintf("%s expected, path absent", op.BeforeKind)}
	}
	if state.kind != op.BeforeKind {
		return &PatchConflictError{Path: op.Path, Reason: fmt.Sprintf("%s expected, found %s", op.BeforeKind, kindName(state.kind))}
	}
	if state.digest != op.BeforeDigest {
		return &PreimageMismatchError{Path: op.Path, Want: op.BeforeDigest, Got: state.digest}
	}
	if state.mode != op.BeforeMode {
		return &PreimageMismatchError{Path: op.Path, Want: fmt.Sprintf("mode %#o", op.BeforeMode), Got: fmt.Sprintf("mode %#o", state.mode)}
	}
	return nil
}
func kindName(kind string) string {
	if kind == "" {
		return "absent entry"
	}
	return kind
}

// checkBlob fails closed unless the blob exists as a regular file.
func checkBlob(blobDir, digest string) error {
	info, err := os.Lstat(filepath.Join(blobDir, digest))
	if errors.Is(err, fs.ErrNotExist) {
		return &BlobMissingError{Digest: digest}
	}
	if err != nil {
		return fmt.Errorf("snapshot: stat blob %s: %w", digest, err)
	}
	if !info.Mode().IsRegular() {
		return &BlobMissingError{Digest: digest}
	}
	return nil
}

// applyOp performs one pending operation. Deletes use RemoveAll (op paths
// are disjoint and children of a deleted directory carry their own
// operations, which skip as already-applied); a dir-to-dir modify is a
// chmod; everything else lands through temp files and renames inside the
// stage.
func (s *stageState) applyOp(op PatchOp) error {
	full := filepath.Join(s.root, filepath.FromSlash(op.Path))
	switch {
	case op.Op == OpDelete:
		if err := os.RemoveAll(full); err != nil {
			return fmt.Errorf("snapshot: delete %s: %w", op.Path, err)
		}
		return nil
	case op.Op == OpRename:
		src := filepath.Join(s.root, filepath.FromSlash(op.OldPath))
		if err := secureMkdirAll(filepath.Dir(full), storeDirPerm); err != nil {
			return err
		}
		if err := os.Rename(src, full); err != nil {
			return fmt.Errorf("snapshot: rename %s -> %s: %w", op.OldPath, op.Path, err)
		}
		return nil
	case op.Op == OpModify && op.Kind == KindDir && op.BeforeKind == KindDir:
		// A directory mode change: the children are untouched by the
		// patch, so only the mode moves.
		if err := os.Chmod(full, fs.FileMode(op.Mode)); err != nil {
			return fmt.Errorf("snapshot: chmod %s: %w", op.Path, err)
		}
		return nil
	case op.Op == OpModify:
		// Replace the preimage with the after-state; a kind change
		// (file <-> symlink) is exactly this replacement.
		if err := os.RemoveAll(full); err != nil {
			return fmt.Errorf("snapshot: modify-clear %s: %w", op.Path, err)
		}
	}
	// Add (and the create half of modify).
	if op.Kind == KindDir {
		if err := secureMkdirAll(full, fs.FileMode(op.Mode)); err != nil {
			return err
		}
		return nil
	}
	if op.Kind == KindSymlink {
		if err := secureMkdirAll(filepath.Dir(full), storeDirPerm); err != nil {
			return err
		}
		if err := os.Symlink(op.LinkTarget, full); err != nil {
			return fmt.Errorf("snapshot: symlink %s: %w", op.Path, err)
		}
		return nil
	}
	e := Entry{Path: op.Path, Kind: op.Kind, Mode: op.Mode, Size: op.Size, Digest: op.Digest}
	if err := materializeFile(s.root, e, s.blobDir); err != nil {
		return fmt.Errorf("snapshot: stage %s: %w", op.Path, err)
	}
	return nil
}

// secureMkdirAll creates the full path with the given leaf mode, walking
// components from the root and refusing symlinked components. Existing
// directories are accepted as-is; the final component is chmod'ed to
// mode so the umask never leaks in.
func secureMkdirAll(full string, mode fs.FileMode) error {
	if err := validateAbsDir("dir", filepath.Clean(full)); err != nil {
		return err
	}
	abs := filepath.Clean(full)
	vol := filepath.VolumeName(abs)
	rest := strings.TrimPrefix(strings.TrimPrefix(abs, vol), string(filepath.Separator))
	comps := strings.Split(rest, string(filepath.Separator))
	cur := filepath.Join(vol, string(filepath.Separator))
	if err := mkdirComponent(cur, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	for i, comp := range comps {
		if comp == "" {
			continue
		}
		cur = filepath.Join(cur, comp)
		info, err := os.Lstat(cur)
		switch {
		case err == nil:
			if !info.IsDir() {
				return &PatchConflictError{Path: cur, Reason: fmt.Sprintf("%s on the path is not a directory", kindName(fileKind(info)))}
			}
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(cur, 0o700); err != nil {
				return fmt.Errorf("snapshot: mkdir %s: %w", cur, err)
			}
			if i == len(comps)-1 {
				if err := os.Chmod(cur, mode); err != nil {
					return fmt.Errorf("snapshot: chmod %s: %w", cur, err)
				}
			}
		default:
			return fmt.Errorf("snapshot: stat %s: %w", cur, err)
		}
	}
	// The leaf may have existed already; enforce the requested mode for
	// explicit dir operations by the caller when needed.
	return nil
}

func mkdirComponent(path string, mode fs.FileMode) error {
	info, err := os.Lstat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("snapshot: %s is not a directory: %w", path, fs.ErrExist)
		}
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return os.Mkdir(path, mode)
	}
	return err
}

func fileKind(info fs.FileInfo) string {
	switch {
	case info.IsDir():
		return KindDir
	case info.Mode()&fs.ModeSymlink != 0:
		return KindSymlink
	case info.Mode().IsRegular():
		return KindFile
	}
	return "special"
}

// digestFileNoFollow digests a stage file under the blob domain, opening
// with O_NOFOLLOW so a symlink swapped in after lstat cannot redirect the
// read.
func digestFileNoFollow(full string) (string, error) {
	f, err := os.OpenFile(full, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("snapshot: open stage file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("snapshot: fstat stage file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("snapshot: stage file is no longer regular")
	}
	return digestStream(f)
}

// EnsureMaterialized guarantees destination holds exactly manifest's
// tree. It is the journal effects' idempotent materialize: a missing
// destination is materialized, a verifying one is accepted, an empty one
// is filled, and a partial or divergent one — possible only after a crash
// mid-materialize, never after the operation finalized — is removed and
// rebuilt. Blobs are copied from the store; nothing is hardlinked.
func EnsureMaterialized(ctx context.Context, manifest Manifest, store, destination string) error {
	if err := validateAbsDir("destination", destination); err != nil {
		return err
	}
	switch info, err := os.Lstat(destination); {
	case err == nil && info.IsDir():
		if err := Verify(ctx, destination, manifest); err == nil {
			return nil
		}
		empty, err := dirEmpty(destination)
		if err != nil {
			return err
		}
		if !empty {
			// Crash residue from this still-pending operation: the tree
			// is homonto-owned and disposable, so rebuild it.
			if err := os.RemoveAll(destination); err != nil {
				return fmt.Errorf("snapshot: reset partial tree %s: %w", destination, err)
			}
		}
	case err == nil:
		return &DestinationExistsError{Path: destination}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("snapshot: stat destination %s: %w", destination, err)
	}
	return Materialize(ctx, manifest, store, destination)
}
