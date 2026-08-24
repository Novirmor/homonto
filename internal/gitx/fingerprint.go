package gitx

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// treeDomain is the fingerprint domain of a git tree baseline. Combined
// with fingerprint's "homonto.v1." prefix it reads homonto.v1.git.tree:.
const treeDomain = "git.tree"

// SourceFingerprint digests a member repository's clean baseline at commit:
// the commit's tree (mode/type/name/content, recursively, with submodules
// recorded as gitlinks and never recursed into). The executable bit is part
// of a blob's mode; a symlink is a blob whose content is its target. Equal
// trees yield equal digests — a digest changes exactly when the committed
// tree changes.
//
// A dirty worktree is refused with a typed DirtyWorktreeError naming the
// dirty paths (ADR 0024: dirty trees are rejected, never tidied, never
// folded into a baseline): a digest of an in-progress tree would silently
// drift from every later digest of the same checkout.
func (s *Service) SourceFingerprint(ctx context.Context, dir, commit string) (fingerprint.Digest, error) {
	files, err := dirtyPaths(ctx, s.runner, dir)
	if err != nil {
		return "", err
	}
	if len(files) > 0 {
		return "", &DirtyWorktreeError{Files: files}
	}
	tree, err := revParse(ctx, s.runner, dir, commit, "^{tree}")
	if err != nil {
		return "", err
	}
	root, err := treeRecords(ctx, s.runner, dir, tree)
	if err != nil {
		return "", err
	}
	return fingerprint.Bytes(treeDomain, root), nil
}

// treeEntry is one git ls-tree record.
type treeEntry struct {
	mode string
	typ  string
	sha  string
	path string
}

// treeRecords hashes the tree rooted at tree into the record bytes of the
// root entry: every entry contributes mode, type, name, and content, where
// a tree's content is the sorted concatenation of its direct children's
// records, a blob's content is its bytes, and a gitlink's content is its
// commit hash. Submodules are therefore recorded by hash, never recursed
// into.
func treeRecords(ctx context.Context, r Runner, dir, tree string) ([]byte, error) {
	out, err := r.Run(ctx, dir, "ls-tree", "-r", "-t", "-z", tree)
	if err != nil {
		return nil, fmt.Errorf("gitx: ls-tree %s in %s: %w", tree, dir, err)
	}
	entries, err := parseTreeEntries(out)
	if err != nil {
		return nil, err
	}
	// ls-tree -r -t lists parents before children (DFS pre-order) but never
	// the root tree itself; walking backwards means every child's record
	// exists when its parent is hashed, and the root is aggregated last
	// over its direct children.
	digestByPath := map[string][]byte{}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		switch e.typ {
		case "tree":
			children := directChildren(entries, digestByPath, e.path)
			digestByPath[e.path] = treeRecord(e.mode, e.typ, e.path, children)
		case "blob":
			content, err := r.Run(ctx, dir, "cat-file", "blob", e.sha)
			if err != nil {
				return nil, fmt.Errorf("gitx: cat-file blob %s: %w", e.sha, err)
			}
			digestByPath[e.path] = treeRecord(e.mode, e.typ, e.path, []byte(content))
		case "commit": // submodule gitlink: recorded, never recursed
			digestByPath[e.path] = treeRecord(e.mode, e.typ, e.path, []byte(e.sha))
		default:
			return nil, fmt.Errorf("gitx: ls-tree %s: unsupported entry type %q", tree, e.typ)
		}
	}
	return treeRecord("040000", "tree", "", directChildren(entries, digestByPath, "")), nil
}

// directChildren returns the sorted concatenation of the direct children's
// record bytes of the tree at path: children are the entries whose path
// starts with path+"/" and has no further component. The root tree (path
// "") is a special case: its children are the top-level entries.
func directChildren(entries []treeEntry, digestByPath map[string][]byte, path string) []byte {
	prefix := path + "/"
	var children []string
	for _, c := range entries {
		if c.path == path {
			continue
		}
		if path == "" {
			if !strings.Contains(c.path, "/") {
				children = append(children, c.path)
			}
			continue
		}
		if strings.HasPrefix(c.path, prefix) && !strings.Contains(strings.TrimPrefix(c.path, prefix), "/") {
			children = append(children, c.path)
		}
	}
	sort.Strings(children)
	var buf bytes.Buffer
	for _, c := range children {
		buf.Write(digestByPath[c])
	}
	return buf.Bytes()
}

// parseTreeEntries parses git ls-tree -r -t -z output.
func parseTreeEntries(out string) ([]treeEntry, error) {
	var entries []treeEntry
	for _, seg := range strings.Split(out, "\x00") {
		if seg == "" {
			continue
		}
		meta, path, ok := strings.Cut(seg, "\t")
		if !ok {
			return nil, fmt.Errorf("gitx: unparseable ls-tree record %q", seg)
		}
		parts := strings.Split(meta, " ")
		if len(parts) != 3 {
			return nil, fmt.Errorf("gitx: unparseable ls-tree metadata %q", meta)
		}
		entries = append(entries, treeEntry{mode: parts[0], typ: parts[1], sha: parts[2], path: path})
	}
	return entries, nil
}

// treeRecord is the deterministic record bytes of one tree entry. Every
// field is length-framed ("<len>:<bytes>") so adjacent fields can never
// merge: without framing, a file named "a" containing "x y" and a file
// named "a x" containing "y" join to identical bytes.
func treeRecord(mode, typ, name string, content []byte) []byte {
	var b bytes.Buffer
	for _, f := range []string{mode, typ, name} {
		b.WriteString(strconv.Itoa(len(f)))
		b.WriteByte(':')
		b.WriteString(f)
	}
	b.WriteString(strconv.Itoa(len(content)))
	b.WriteByte(':')
	b.Write(content)
	return b.Bytes()
}
