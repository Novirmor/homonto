// The patch model: operations partitioning a diff, and the path and scope rules they answer to.
package snapshot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// Patch operation kinds.
const (
	OpAdd    = "add"
	OpModify = "modify"
	OpDelete = "delete"
	OpRename = "rename"
)

// PatchError carries the reason a patch manifest is invalid.
type PatchError struct {
	Reason string
}

func (e *PatchError) Error() string {
	return fmt.Sprintf("snapshot: invalid patch manifest: %s", e.Reason)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *PatchError) Unwrap() error { return ErrInvalidPatch }

// InvalidPathError names the rejected path and why.
type InvalidPathError struct {
	Path   string
	Reason string
}

func (e *InvalidPathError) Error() string {
	return fmt.Sprintf("snapshot: path %q rejected: %s", e.Path, e.Reason)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *InvalidPathError) Unwrap() error { return ErrInvalidPath }

// PreimageMismatchError names the path whose stage content diverges from
// the recorded preimage.
type PreimageMismatchError struct {
	Path string
	Want string
	Got  string
}

func (e *PreimageMismatchError) Error() string {
	return fmt.Sprintf("snapshot: preimage mismatch at %q: want %s, got %s", e.Path, digestOrAbsent(e.Want), digestOrAbsent(e.Got))
}

// Unwrap exposes the sentinel for errors.Is.
func (e *PreimageMismatchError) Unwrap() error { return ErrPatchPreimageMismatch }

func digestOrAbsent(d string) string {
	if d == "" {
		return "absent"
	}
	return d
}

// PatchConflictError names the path and the structural conflict.
type PatchConflictError struct {
	Path   string
	Reason string
}

func (e *PatchConflictError) Error() string {
	return fmt.Sprintf("snapshot: conflict at %q: %s", e.Path, e.Reason)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *PatchConflictError) Unwrap() error { return ErrPatchConflict }

// ScopeViolationError lists the offending paths.
type ScopeViolationError struct {
	Paths []string
}

func (e *ScopeViolationError) Error() string {
	return fmt.Sprintf("snapshot: changed paths outside declared scope: %s", strings.Join(e.Paths, ", "))
}

// Unwrap exposes the sentinel for errors.Is.
func (e *ScopeViolationError) Unwrap() error { return ErrScopeViolation }

// CaseCollisionError names two operation paths differing only by case.
type CaseCollisionError struct {
	A, B string
}

func (e *CaseCollisionError) Error() string {
	return fmt.Sprintf("snapshot: case-insensitive collision between %q and %q", e.A, e.B)
}

// Unwrap exposes the sentinel for errors.Is.
func (e *CaseCollisionError) Unwrap() error { return ErrCaseCollision }

// PatchOp is one operation of a PatchManifest. Path is the final
// (after-state) path; for renames OldPath names the source.
//
// The after state of the entry at Path is carried by Kind/Mode/Size/
// Digest/LinkTarget; the before state (the preimage Apply verifies) by
// BeforeKind/BeforeMode/BeforeDigest. A delete carries the entry as it
// was (Kind/Mode/BeforeDigest describe it; the after state is absent).
// A rename carries the shared content in Kind/Mode/Digest and its source
// in OldPath; renames never change content or mode.
type PatchOp struct {
	Op         string `json:"op"`
	Path       string `json:"path"`
	OldPath    string `json:"old_path,omitempty"`
	Kind       string `json:"kind"`
	Mode       uint32 `json:"mode"`
	Size       int64  `json:"size,omitempty"`
	Digest     string `json:"digest,omitempty"`
	LinkTarget string `json:"link_target,omitempty"`

	BeforeKind   string `json:"before_kind,omitempty"`
	BeforeMode   uint32 `json:"before_mode,omitempty"`
	BeforeDigest string `json:"before_digest,omitempty"`
}

// PatchManifest is the schema v1 diff between a base snapshot and a result
// tree: the operations that transform a tree at base_digest into the tree
// at result_digest.
type PatchManifest struct {
	SchemaVersion int                   `json:"schema_version"`
	RepositoryID  identity.RepositoryID `json:"repository_id"`
	BaseDigest    fingerprint.Digest    `json:"base_digest"`
	ResultDigest  fingerprint.Digest    `json:"result_digest"`
	// Operations are sorted by Path; all operation paths (Path and
	// OldPath) are pairwise disjoint.
	Operations []PatchOp `json:"operations"`
}

// ValidateRelPath checks that rel is a slash-clean, root-relative path:
// no leading slash, no empty, ".", or ".." components, no backslashes, no
// NUL bytes.
func ValidateRelPath(rel string) error {
	if rel == "" {
		return &InvalidPathError{Path: rel, Reason: "empty"}
	}
	if strings.HasPrefix(rel, "/") {
		return &InvalidPathError{Path: rel, Reason: "absolute"}
	}
	if strings.ContainsRune(rel, '\\') {
		return &InvalidPathError{Path: rel, Reason: "backslash is not a separator"}
	}
	if strings.ContainsRune(rel, 0) {
		return &InvalidPathError{Path: rel, Reason: "NUL byte"}
	}
	if path.Clean(rel) != rel {
		return &InvalidPathError{Path: rel, Reason: "not slash-clean"}
	}
	for _, comp := range strings.Split(rel, "/") {
		if comp == "." || comp == ".." {
			return &InvalidPathError{Path: rel, Reason: fmt.Sprintf("%q component", comp)}
		}
	}
	return nil
}

// EncodePatch writes the canonical JSON encoding of p (operations sorted
// by path, trailing newline).
func EncodePatch(p PatchManifest) ([]byte, error) {
	ops := make([]PatchOp, len(p.Operations))
	copy(ops, p.Operations)
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	p.Operations = ops
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("snapshot: encode patch: %w", err)
	}
	return buf.Bytes(), nil
}

// DecodePatch strictly decodes one patch manifest document.
func DecodePatch(data []byte) (PatchManifest, error) {
	var p PatchManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return PatchManifest{}, &PatchError{Reason: fmt.Sprintf("decode: %v", err)}
	}
	if dec.More() {
		return PatchManifest{}, &PatchError{Reason: "trailing data after patch manifest"}
	}
	if p.Operations == nil {
		p.Operations = []PatchOp{} // canonical form: "operations":[] not null
	}
	if err := ValidatePatch(p); err != nil {
		return PatchManifest{}, err
	}
	return p, nil
}

// ValidatePatch checks every structural rule of a patch manifest: schema,
// repository id, digests, operation shapes, path safety, pairwise
// disjointness, after-tree shape (no path is both a file and a parent),
// sorting, and case-insensitive collisions among operation paths.
func ValidatePatch(p PatchManifest) error {
	if p.SchemaVersion != SchemaVersion {
		return fmt.Errorf("snapshot: patch schema version %d: %w", p.SchemaVersion, ErrUnsupportedSchema)
	}
	if p.RepositoryID != "" {
		if err := identity.ValidateUUID(string(p.RepositoryID)); err != nil {
			return &PatchError{Reason: fmt.Sprintf("repository_id: %v", err)}
		}
	}
	if err := p.BaseDigest.Validate(); err != nil {
		return &PatchError{Reason: fmt.Sprintf("base_digest: %v", err)}
	}
	if err := p.ResultDigest.Validate(); err != nil {
		return &PatchError{Reason: fmt.Sprintf("result_digest: %v", err)}
	}
	seen := map[string]bool{}
	claim := func(at string, rel string) error {
		if err := ValidateRelPath(rel); err != nil {
			return err
		}
		if seen[rel] {
			return &PatchError{Reason: fmt.Sprintf("%s: operation paths overlap at %s", at, rel)}
		}
		seen[rel] = true
		return nil
	}
	for i, op := range p.Operations {
		if i > 0 && p.Operations[i-1].Path > op.Path {
			return &PatchError{Reason: fmt.Sprintf("operations not sorted: %s before %s", p.Operations[i-1].Path, op.Path)}
		}
		switch op.Op {
		case OpAdd:
			if err := validateAfterEntry(op); err != nil {
				return err
			}
			if op.BeforeKind != "" || op.BeforeMode != 0 || op.BeforeDigest != "" {
				return &PatchError{Reason: fmt.Sprintf("add %s: carries a preimage", op.Path)}
			}
		case OpModify:
			if err := validateAfterEntry(op); err != nil {
				return err
			}
			if err := validateBeforeEntry(op); err != nil {
				return err
			}
		case OpDelete:
			if err := validateBeforeEntry(op); err != nil {
				return err
			}
			// The delete's Kind/Mode describe the removed entry; the
			// after-state content fields must stay empty.
			if op.Size != 0 || op.Digest != "" || op.LinkTarget != "" {
				return &PatchError{Reason: fmt.Sprintf("delete %s: carries after-state content", op.Path)}
			}
			if op.Kind != op.BeforeKind || op.Mode != op.BeforeMode {
				return &PatchError{Reason: fmt.Sprintf("delete %s: kind/mode must restate the removed entry", op.Path)}
			}
		case OpRename:
			if op.OldPath == "" {
				return &PatchError{Reason: fmt.Sprintf("rename %s: missing old_path", op.Path)}
			}
			if err := validateAfterEntry(op); err != nil {
				return err
			}
			if err := ValidateRelPath(op.OldPath); err != nil {
				return err
			}
			if op.BeforeKind != "" && op.BeforeKind != op.Kind {
				return &PatchError{Reason: fmt.Sprintf("rename %s: content cannot change kind", op.Path)}
			}
			if op.BeforeDigest != op.Digest || (op.BeforeMode != 0 && op.BeforeMode != op.Mode) {
				return &PatchError{Reason: fmt.Sprintf("rename %s: content or mode changes", op.Path)}
			}
		default:
			return &PatchError{Reason: fmt.Sprintf("unknown op %q at %s", op.Op, op.Path)}
		}
		if err := claim(op.Op+" "+op.Path, op.Path); err != nil {
			return err
		}
		if op.OldPath != "" {
			if err := claim(op.Op+" old_path", op.OldPath); err != nil {
				return err
			}
		}
	}
	if err := checkAfterTreeShapes(p.Operations); err != nil {
		return err
	}
	if err := checkCaseCollisions(p.Operations); err != nil {
		return err
	}
	return nil
}

// checkAfterTreeShapes rejects after-tree prefix conflicts: a path the
// patch leaves as a file or symlink cannot also carry a path below it —
// git's "not a tree" rule, caught at validation instead of mid-apply.
// Only after-state paths participate (adds, modifies, rename
// destinations): a directory add with children is the normal
// nested-create shape, and a delete frees its subtree.
func checkAfterTreeShapes(ops []PatchOp) error {
	after := make([]Entry, 0, len(ops))
	for _, op := range ops {
		switch op.Op {
		case OpAdd, OpModify, OpRename:
			after = append(after, Entry{Path: op.Path, Kind: op.Kind})
		}
	}
	sort.Slice(after, func(i, j int) bool { return after[i].Path < after[j].Path })
	for i := 1; i < len(after); i++ {
		prev, cur := after[i-1], after[i]
		if prev.Kind != KindDir && strings.HasPrefix(cur.Path, prev.Path+"/") {
			return &PatchError{Reason: fmt.Sprintf(
				"paths %s and %s overlap: %s is not a directory in the result", prev.Path, cur.Path, prev.Path)}
		}
	}
	return nil
}

// validateAfterEntry checks the after-state fields of an operation as an
// entry of the result tree; entry-shape problems surface as patch
// errors, not manifest errors.
func validateAfterEntry(op PatchOp) error {
	e := Entry{Path: op.Path, Kind: op.Kind, Mode: op.Mode, Size: op.Size, Digest: op.Digest, LinkTarget: op.LinkTarget}
	if err := validateEntry(e); err != nil {
		var me *ManifestError
		if errors.As(err, &me) {
			return &PatchError{Reason: me.Reason}
		}
		return err
	}
	if op.Op == OpRename && op.Kind == KindDir {
		return &PatchError{Reason: fmt.Sprintf("rename %s: directories have no content to pair on", op.Path)}
	}
	return nil
}

// validateBeforeEntry checks the preimage fields of an operation.
func validateBeforeEntry(op PatchOp) error {
	if op.BeforeKind != KindFile && op.BeforeKind != KindSymlink && op.BeforeKind != KindDir {
		return &PatchError{Reason: fmt.Sprintf("op %s: before_kind %q invalid", op.Path, op.BeforeKind)}
	}
	if op.BeforeMode > 0o777 {
		return &PatchError{Reason: fmt.Sprintf("op %s: before_mode %#o exceeds 0777", op.Path, op.BeforeMode)}
	}
	switch op.BeforeKind {
	case KindDir:
		if op.BeforeDigest != "" {
			return &PatchError{Reason: fmt.Sprintf("op %s: dir preimage carries a digest", op.Path)}
		}
	default:
		if err := mustDigest(op.BeforeDigest, "before "+op.Path); err != nil {
			var me *ManifestError
			if errors.As(err, &me) {
				return &PatchError{Reason: me.Reason}
			}
			return err
		}
	}
	return nil
}

// opPathSet lists every path an operation set touches (Path and OldPath).
func opPaths(ops []PatchOp) []string {
	out := make([]string, 0, 2*len(ops))
	for _, op := range ops {
		out = append(out, op.Path)
		if op.OldPath != "" {
			out = append(out, op.OldPath)
		}
	}
	return out
}

// checkCaseCollisionPaths fails closed when two paths differ only by
// case — a tree or patch holding both would clobber one of them on a
// case-insensitive filesystem (macOS, Windows).
func checkCaseCollisionPaths(paths []string) error {
	lower := make(map[string]string, len(paths))
	for _, p := range paths {
		key := strings.ToLower(p)
		if prev, ok := lower[key]; ok && prev != p {
			return &CaseCollisionError{A: prev, B: p}
		}
		lower[key] = p
	}
	return nil
}

// checkCaseCollisions is checkCaseCollisionPaths over every path an
// operation set touches (Path and OldPath).
func checkCaseCollisions(ops []PatchOp) error {
	return checkCaseCollisionPaths(opPaths(ops))
}
