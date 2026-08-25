package pathclass

import (
	"fmt"
	"sort"
)

// Op is what happened to a path in the diff.
type Op string

const (
	OpAdded    Op = "added"
	OpModified Op = "modified"
	OpDeleted  Op = "deleted"
	OpRenamed  Op = "renamed"
)

// known reports whether o is a recognized operation.
func (o Op) known() bool {
	switch o {
	case OpAdded, OpModified, OpDeleted, OpRenamed:
		return true
	}
	return false
}

// DiffEntry is one changed path in the integrated workspace diff, taken
// from the immutable baseline. A rename carries both endpoints.
type DiffEntry struct {
	// Member is the repository the path belongs to. Two members may
	// legitimately hold the same relative path, and they are different
	// files.
	Member string
	Path   string
	// OldPath is the pre-rename path; it is set only for OpRenamed.
	OldPath string
	Op      Op
}

// Count is the result of counting a diff against a preset's scope rule.
type Count struct {
	// Total is the number of unique counted paths — the number the
	// threshold is compared against.
	Total int
	// Counted lists them, sorted, as "<member>:<path>".
	Counted []string
	// Excluded lists the paths that did not count, sorted, each with the
	// class that excluded it. An operator arguing with the count needs to
	// see what was left out, not just the number.
	Excluded []string
}

// Matchers resolves the path-class matcher of one member. A member with no
// configured classes still classifies — everything in it is source.
type Matchers func(member string) (*Matcher, error)

// CountPresetChanges counts the unique changed source paths in a diff.
//
// Three rules, and each one exists because getting it wrong pauses a
// preset that should not pause:
//
//   - Unique paths. The same file touched by two entries counts once.
//   - A rename counts ONCE, and it counts unless BOTH of its endpoints are
//     excluded. Moving a source file into vendor/ is a real change to the
//     source tree even though where it landed does not count; counting the
//     endpoints separately would make one move count twice.
//   - Generated, vendored, and test paths are excluded. Everything else —
//     source, documentation, configuration — counts.
func CountPresetChanges(entries []DiffEntry, matchers Matchers) (Count, error) {
	if matchers == nil {
		return Count{}, fmt.Errorf("pathclass: a matcher resolver is required")
	}
	counted := map[string]bool{}
	excluded := map[string]string{}

	for i, e := range entries {
		if !e.Op.known() {
			return Count{}, fmt.Errorf("pathclass: entries[%d] has unknown operation %q", i, e.Op)
		}
		matcher, err := matchers(e.Member)
		if err != nil {
			return Count{}, err
		}
		if matcher == nil {
			return Count{}, fmt.Errorf("pathclass: no path classes for member %q", e.Member)
		}
		endpoints, err := endpointsOf(e)
		if err != nil {
			return Count{}, fmt.Errorf("pathclass: entries[%d]: %w", i, err)
		}

		// The entry counts when ANY endpoint is a counted class. The key
		// is the entry's own identity — for a rename, its new path — so
		// one move is one change.
		countsNow := false
		for _, ep := range endpoints {
			if matcher.Classify(ep).Counted() {
				countsNow = true
			}
		}
		key := e.Member + ":" + endpoints[0]
		if countsNow {
			counted[key] = true
			// A rename out of an excluded class is one change, recorded
			// under its new path; drop any earlier exclusion of it.
			for _, ep := range endpoints {
				delete(excluded, e.Member+":"+ep)
			}
			continue
		}
		for _, ep := range endpoints {
			if counted[e.Member+":"+ep] {
				continue
			}
			excluded[e.Member+":"+ep] = string(matcher.Classify(ep))
		}
	}

	out := Count{Total: len(counted)}
	for k := range counted {
		out.Counted = append(out.Counted, k)
	}
	for k, class := range excluded {
		out.Excluded = append(out.Excluded, k+" ("+class+")")
	}
	sort.Strings(out.Counted)
	sort.Strings(out.Excluded)
	return out, nil
}

// endpointsOf returns a diff entry's normalized paths: the new path first,
// then the old one for a rename.
func endpointsOf(e DiffEntry) ([]string, error) {
	newPath, err := Normalize(e.Path)
	if err != nil {
		return nil, err
	}
	if e.Op != OpRenamed {
		if e.OldPath != "" {
			return nil, fmt.Errorf("only a rename carries an old path, %s does not", e.Op)
		}
		return []string{newPath}, nil
	}
	if e.OldPath == "" {
		return nil, fmt.Errorf("a rename must carry the path it came from")
	}
	oldPath, err := Normalize(e.OldPath)
	if err != nil {
		return nil, err
	}
	if oldPath == newPath {
		return nil, fmt.Errorf("a rename from %q to itself is not a rename", newPath)
	}
	return []string{newPath, oldPath}, nil
}
