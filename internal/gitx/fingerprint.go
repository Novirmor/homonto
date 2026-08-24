package gitx

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/fingerprint"
)

// treeDomain is the fingerprint domain of a git tree baseline. Combined
// with fingerprint's "homonto.v1." prefix it reads homonto.v1.git.tree:.
const treeDomain = "git.tree"

// SourceFingerprint digests a member repository's clean baseline at commit:
// the commit's tree (mode/type/name/content, recursively, with submodules
// recorded as gitlinks and never recursed into) plus the untracked
// non-ignored files. The executable bit is part of a blob's mode; a symlink
// is a blob whose content is its target. Equal trees yield equal digests —
// a digest changes exactly when the committed tree or the untracked set
// changes.
func (s *Service) SourceFingerprint(ctx context.Context, dir, commit string) (fingerprint.Digest, error) {
	tree, err := revParse(ctx, s.runner, dir, commit, "^{tree}")
	if err != nil {
		return "", err
	}
	root, err := treeRecords(ctx, s.runner, dir, tree)
	if err != nil {
		return "", err
	}
	untracked, err := untrackedRecords(ctx, s.runner, dir)
	if err != nil {
		return "", err
	}
	payload := append(root, untracked...)
	return fingerprint.Bytes(treeDomain, payload), nil
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

// treeRecord is the deterministic record bytes of one tree entry.
func treeRecord(mode, typ, name string, content []byte) []byte {
	var b bytes.Buffer
	b.WriteString(mode)
	b.WriteByte(' ')
	b.WriteString(typ)
	b.WriteByte(' ')
	b.WriteString(name)
	b.WriteByte(' ')
	b.Write(content)
	return b.Bytes()
}

// untrackedRecords hashes the untracked non-ignored files of dir (from git
// ls-files --others --exclude-standard) as blob records, sorted by path:
// regular files carry their bytes, symlinks their target, and anything else
// fails loudly.
func untrackedRecords(ctx context.Context, r Runner, dir string) ([]byte, error) {
	out, err := r.Run(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("gitx: ls-files --others of %s: %w", dir, err)
	}
	var paths []string
	for _, seg := range strings.Split(out, "\x00") {
		if seg != "" {
			paths = append(paths, seg)
		}
	}
	sort.Strings(paths)
	var buf bytes.Buffer
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, fmt.Errorf("gitx: lstat untracked %s: %w", full, err)
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				return nil, fmt.Errorf("gitx: readlink untracked %s: %w", full, err)
			}
			buf.Write(treeRecord("120000", "blob", p, []byte(target)))
		case info.Mode().IsRegular():
			mode := "100644"
			if info.Mode().Perm()&0o111 != 0 {
				mode = "100755"
			}
			content, err := os.ReadFile(full)
			if err != nil {
				return nil, fmt.Errorf("gitx: read untracked %s: %w", full, err)
			}
			buf.Write(treeRecord(mode, "blob", p, content))
		default:
			return nil, fmt.Errorf("gitx: untracked %s is not a regular file or symlink", full)
		}
	}
	return buf.Bytes(), nil
}
