package link

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolve makes target absolute, resolved against the directory the link at
// dst lives in when target is relative (ADR 0026: project links carry
// relative targets, so ownership can no longer be a raw string prefix test).
// A target that escapes above dst's directory tree via ".." is still handled
// honestly: it resolves to wherever it points, and the managed-root check
// decides ownership from the resolved path.
func resolve(target, dst string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(dst), target))
}

// managed reports whether target (raw or relative, at dst) points inside ANY
// of the content roots homonto owns after resolution. A symlink pointing
// into one of them is ours (relinkable/prunable); a symlink pointing outside
// every root is user-owned and must never be touched.
func managed(target string, dst string, roots ...string) bool {
	abs := resolve(target, dst)
	for _, root := range roots {
		if strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

// SameTarget reports whether a link at dst currently points at want,
// comparing resolved targets so a relative recorded target and an on-disk
// target of either spelling agree.
func SameTarget(dst, want, cur string) bool {
	return resolve(want, dst) == resolve(cur, dst)
}

// Link ensures dst is a symlink to src, returning whether it changed. A regular
// file (or dir) at dst is never clobbered — that is a "conflict" error. A
// symlink already pointing at src (either spelling) is a no-op. A symlink
// pointing elsewhere is relinked in place only when it is ours (its target,
// resolved, sits inside one of roots); a symlink pointing outside every root
// is a foreign, user-owned link and is a conflict — homonto must never remove
// or repoint what it does not own.
func Link(src, dst string, roots ...string) (bool, error) {
	if fi, err := os.Lstat(dst); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			return false, fmt.Errorf("conflict: %s exists and is not a symlink", dst)
		}
		cur, err := os.Readlink(dst)
		if err != nil {
			// Symlink at Lstat but unreadable now (vanished/permission race).
			// Surface the real IO error instead of an empty-target conflict.
			return false, fmt.Errorf("read link %s: %w", dst, err)
		}
		if SameTarget(dst, src, cur) {
			return false, nil
		}
		if !managed(cur, dst, roots...) {
			return false, fmt.Errorf("conflict: %s is a symlink to %s, outside managed content %s; not changing", dst, cur, strings.Join(roots, ", "))
		}
		if err := os.Remove(dst); err != nil {
			return false, err
		}
		if err := os.Symlink(src, dst); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	if err := os.Symlink(src, dst); err != nil {
		return false, err
	}
	return true, nil
}

// Remove deletes dst only when it is a symlink pointing into one of roots.
// A user's own file or a foreign link is a conflict error — pruning must never
// destroy anything homonto does not own. A missing dst is fine (already gone).
func Remove(dst string, roots ...string) error {
	fi, err := os.Lstat(dst)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return fmt.Errorf("conflict: %s exists and is not a symlink; not removing", dst)
	}
	target, err := os.Readlink(dst)
	if err != nil {
		return err
	}
	if !managed(target, dst, roots...) {
		return fmt.Errorf("conflict: %s links to %s, outside managed content %s; not removing", dst, target, strings.Join(roots, ", "))
	}
	return os.Remove(dst)
}

// IsManaged reports whether dst is a symlink pointing into one of roots — a
// link homonto created and may therefore safely relocate or prune. A missing
// path, a real file, or a foreign symlink all return false (leave-it-alone),
// so callers can prune only what is unambiguously theirs without risking a
// user's file.
func IsManaged(dst string, roots ...string) bool {
	fi, err := os.Lstat(dst)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(dst)
	if err != nil {
		return false
	}
	return managed(target, dst, roots...)
}

// Op is a pending link change for dst -> src. Cur is the current symlink
// target (raw spelling), empty when dst does not exist yet (a create).
type Op struct {
	Dst, Src, Cur string
}

// Plan returns the link changes (dst->src) that would be made. Links already
// pointing at src (either spelling) are omitted. A non-symlink at dst is a
// conflict error, and so is a symlink pointing outside every root — a
// foreign, user-owned link that Apply must never repoint. Only a symlink
// pointing inside one of roots (one of ours) is planned as a relink.
func Plan(srcs map[string]string, roots ...string) ([]Op, error) {
	var out []Op
	for dst, src := range srcs {
		fi, err := os.Lstat(dst)
		if err != nil {
			out = append(out, Op{Dst: dst, Src: src})
			continue
		}
		if fi.Mode()&os.ModeSymlink == 0 {
			return nil, fmt.Errorf("conflict: %s exists and is not a symlink", dst)
		}
		cur, err := os.Readlink(dst)
		if err != nil {
			// It was a symlink at Lstat but is unreadable now — vanished between
			// the two calls, or a permission race. Treat it as absent (a create)
			// rather than feeding an empty target to managed() and reporting a
			// confusing "symlink to , outside managed content" conflict.
			out = append(out, Op{Dst: dst, Src: src})
			continue
		}
		if SameTarget(dst, src, cur) {
			continue
		}
		if !managed(cur, dst, roots...) {
			return nil, fmt.Errorf("conflict: %s is a symlink to %s, outside managed content %s; not changing", dst, cur, strings.Join(roots, ", "))
		}
		out = append(out, Op{Dst: dst, Src: src, Cur: cur})
	}
	return out, nil
}
