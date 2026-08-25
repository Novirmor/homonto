package pathclass

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Class is what a path is, for the purpose of counting a preset's scope.
type Class string

const (
	// ClassSource: ordinary source, documentation, or configuration. These
	// are the paths that count.
	ClassSource Class = "source"
	// ClassTest: a test path. Excluded from the count — writing tests is
	// what a well-scoped change does, and counting them would punish it.
	ClassTest Class = "test"
	// ClassGenerated: a generated path. Excluded: it is an output, and one
	// source edit can move hundreds of them.
	ClassGenerated Class = "generated"
	// ClassVendored: a vendored path. Excluded for the same reason.
	ClassVendored Class = "vendored"
)

// Counted reports whether a class counts toward the preset scope warning.
func (c Class) Counted() bool { return c == ClassSource }

// ErrOverlappingPattern reports the same pattern configured in more than
// one class, which would make a path's class depend on evaluation order.
var ErrOverlappingPattern = errors.New("pathclass: pattern is configured in more than one class")

// ErrInvalidPattern reports a pattern this package cannot match.
var ErrInvalidPattern = errors.New("pathclass: invalid pattern")

// precedence is the order classes are tested in when a path matches more
// than one. Vendored wins over generated wins over test: a vendored
// generated test file is, above all, not ours.
var precedence = []Class{ClassVendored, ClassGenerated, ClassTest}

// Matcher classifies member-relative paths against one member's configured
// path classes.
type Matcher struct {
	patterns map[Class][]string
}

// NewMatcher compiles a member's path classes.
//
// A pattern configured in two classes is refused rather than resolved: the
// classes would then depend on the order this package happens to test them
// in, and a path's class decides whether a change pauses for a human.
func NewMatcher(pc *workspacecfg.PathClasses) (*Matcher, error) {
	m := &Matcher{patterns: map[Class][]string{}}
	if pc == nil {
		return m, nil
	}
	sources := []struct {
		class    Class
		patterns []string
	}{
		{ClassTest, pc.Tests},
		{ClassGenerated, pc.Generated},
		{ClassVendored, pc.Vendored},
	}
	seen := map[string]Class{}
	for _, src := range sources {
		for _, p := range src.patterns {
			if err := validatePattern(p); err != nil {
				return nil, err
			}
			if other, dup := seen[p]; dup {
				return nil, fmt.Errorf("pathclass: %q is in both %s and %s: %w",
					p, other, src.class, ErrOverlappingPattern)
			}
			seen[p] = src.class
			m.patterns[src.class] = append(m.patterns[src.class], p)
		}
	}
	return m, nil
}

// Classify returns the class of a normalized path. A path matching several
// classes takes the first in precedence order; a path matching none is
// source, because "not classified" means "ours".
func (m *Matcher) Classify(rel string) Class {
	for _, class := range precedence {
		for _, pattern := range m.patterns[class] {
			if Match(pattern, rel) {
				return class
			}
		}
	}
	return ClassSource
}

// Matches returns every class a path matches, in precedence order. It is
// what an operator needs to see when a classification surprises them.
func (m *Matcher) Matches(rel string) []Class {
	var out []Class
	for _, class := range precedence {
		for _, pattern := range m.patterns[class] {
			if Match(pattern, rel) {
				out = append(out, class)
				break
			}
		}
	}
	return out
}

// Patterns returns the configured patterns of one class, sorted.
func (m *Matcher) Patterns(c Class) []string {
	out := append([]string(nil), m.patterns[c]...)
	sort.Strings(out)
	return out
}

// validatePattern applies the manifest's glob grammar.
func validatePattern(pattern string) error {
	fail := func(reason string) error {
		return fmt.Errorf("pathclass: pattern %q: %s: %w", pattern, reason, ErrInvalidPattern)
	}
	switch {
	case pattern == "":
		return fail("must not be empty")
	case strings.ContainsRune(pattern, '\x00'):
		return fail("must not contain NUL")
	case strings.Contains(pattern, `\`):
		return fail("must use '/' separators only")
	case strings.HasPrefix(pattern, "/"):
		return fail("must not match absolute paths")
	}
	for _, seg := range strings.Split(pattern, "/") {
		if seg == ".." {
			return fail("must not match paths escaping the member root")
		}
		if strings.Contains(seg, "**") && seg != "**" {
			return fail(`"**" must be a whole segment`)
		}
		if _, err := path.Match(strings.ReplaceAll(seg, "**", "*"), ""); err != nil {
			return fail("is not a valid glob")
		}
	}
	return nil
}

// Match reports whether a doublestar pattern matches a normalized path.
//
//   - "*" matches within one segment.
//   - "**" in the middle or at the start matches ZERO or more segments, so
//     "**/*_test.go" matches both "y_test.go" and "a/b/y_test.go".
//   - "**" at the END matches ONE or more segments, so "vendor/**" means
//     "everything under vendor" and does not match "vendor" itself. The
//     bare pattern "**" is the exception: it matches every path.
//
// The implementation is here rather than in a dependency because the
// grammar is small, the semantics have to match the manifest's documented
// ones exactly, and a quiet disagreement about what "**" spans would
// silently change which changes pause for a human.
func Match(pattern, name string) bool {
	if pattern == "" || name == "" {
		return pattern == name
	}
	return matchSegments(strings.Split(pattern, "/"), strings.Split(name, "/"), true)
}

// matchSegments matches pattern segments against name segments. last
// reports whether a trailing "**" is the whole pattern, which is the only
// case where it may match nothing.
func matchSegments(pattern, name []string, whole bool) bool {
	for len(pattern) > 0 {
		if pattern[0] == "**" {
			if len(pattern) == 1 {
				// A trailing "**" consumes the rest of the path, and needs
				// something to consume unless it IS the whole pattern.
				return len(name) > 0 || whole
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(pattern[1:], name[i:], false) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pattern[0], name[0])
		if err != nil || !ok {
			return false
		}
		pattern, name = pattern[1:], name[1:]
		whole = false
	}
	return len(name) == 0
}
