// Package pathclass decides which changed files count toward a preset's
// scope, and which signals should stop a preset and ask a human.
//
// # Why counting is fussy
//
// The preset scope rule is one sentence in the spec and every clause of it
// is load-bearing: "unique normalized paths in the integrated workspace
// diff from the immutable baseline captured by the path-confirmed
// transition; a rename counts once; configured generated, vendored, and
// test paths are excluded". A count that double-counts a rename, or that
// counts a regenerated file, turns a legitimate small change into a
// spurious pause — and a pause the human learns to dismiss is worse than
// no pause at all.
//
// # Warning, not verdict
//
// The file count NEVER decides the path by itself. It pauses the preset
// and hands a human the evidence; so do the semantic signals. What the
// human does with that — continue with the broader scope recorded, or
// upgrade to Full — is theirs to decide. Nothing here upgrades anything.
package pathclass

import (
	"fmt"
	"path"
	"strings"
)

// Normalize returns the canonical form of a member-relative path: slash
// separators, cleaned, no leading slash, no escape. Two spellings of the
// same file must normalize identically, because the count is over UNIQUE
// paths and a path counted twice is a path that pauses a preset for
// nothing.
func Normalize(p string) (string, error) {
	fail := func(reason string) error {
		return fmt.Errorf("pathclass: path %q: %s", p, reason)
	}
	switch {
	case p == "":
		return "", fail("must not be empty")
	case strings.ContainsRune(p, '\x00'):
		return "", fail("must not contain NUL")
	case strings.Contains(p, `\`):
		return "", fail("must use '/' separators only")
	case strings.HasPrefix(p, "/"):
		return "", fail("must not be absolute")
	}
	clean := path.Clean(p)
	if clean == "." {
		return "", fail("must name a file, not the root")
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", fail("must not escape the member root")
		}
	}
	return clean, nil
}
