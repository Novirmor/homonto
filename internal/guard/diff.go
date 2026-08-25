package guard

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/protocol"
)

// Typed final-diff errors. Callers branch with errors.Is; the message
// always names the offending path.
var (
	// ErrReadOnlyResult: a read-only assignment changed something.
	ErrReadOnlyResult = errors.New("guard: a read-only assignment changed files")
	// ErrWrongIsolation: the result was observed somewhere other than the
	// isolation area the assignment was issued in.
	ErrWrongIsolation = errors.New("guard: result observed outside the issued isolation area")
	// ErrOutOfScope: a change landed outside the declared scope. This is
	// the hook-bypass catch: a shell command that walked past the write
	// hook still shows up here.
	ErrOutOfScope = errors.New("guard: change outside the assignment's declared scope")
	// ErrProtectedChanged: binary-owned control state was modified.
	ErrProtectedChanged = errors.New("guard: binary-owned control state was modified")
	// ErrDocumentChanged: a workflow document was modified outside the
	// phase or owner that may write it.
	ErrDocumentChanged = errors.New("guard: workflow document modified outside its owning phase")
	// ErrMalformedDiff: the diff itself is unusable.
	ErrMalformedDiff = errors.New("guard: malformed result diff")
)

// ChangeKind grades one changed path.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
)

// known reports whether k is a recognized change kind.
func (k ChangeKind) known() bool {
	switch k {
	case ChangeAdded, ChangeModified, ChangeDeleted:
		return true
	}
	return false
}

// Change is one path the assignment changed, relative to the isolation
// area it was observed in.
type Change struct {
	Path string
	Kind ChangeKind
}

// ResultDiff is what actually changed on disk when an assignment finished.
// It is observed by Homonto — from the worktree's Git status or the
// snapshot's capture — not reported by the host, which is the whole point:
// the process gate can be bypassed, this cannot.
type ResultDiff struct {
	// Root is the isolation area the changes were observed in,
	// workspace-relative. It must be the one the assignment was issued.
	Root string
	// Changes are the changed paths, relative to Root.
	Changes []Change
	// Generated names the paths Homonto itself wrote during the
	// assignment — checkbox updates, appended evidence, generated
	// documents. They are expected in the diff and are not the
	// assignment's changes, so they are accepted even where the
	// assignment could not have written them.
	Generated []string
}

// ValidateAssignmentResult is the advancement gate. It runs on the result,
// independently of whether the host's write hook ever asked permission, and
// refuses to accept a report whose diff contains anything the assignment
// was not issued to change.
//
// It reports the FIRST violation in a stable order — isolation, then
// protected state, then documents, then scope — so a repeated run of the
// same broken result always names the same problem.
func (g *Guard) ValidateAssignmentResult(ctx context.Context, action protocol.Action, diff ResultDiff) error {
	if err := action.Validate(); err != nil {
		return fmt.Errorf("guard: %w", err)
	}
	if action.Kind != protocol.KindAssignment {
		return fmt.Errorf("guard: action %s is a %s, not an assignment: %w", action.ID, action.Kind, ErrMalformedDiff)
	}
	issued, ok := normalize(action.WorkingDirectory)
	if !ok {
		return fmt.Errorf("guard: assignment %s declares an unusable isolation root %q: %w",
			action.ID, action.WorkingDirectory, ErrMalformedDiff)
	}
	observed, ok := normalize(diff.Root)
	if !ok {
		return fmt.Errorf("guard: result root %q is not a clean relative path: %w", diff.Root, ErrMalformedDiff)
	}
	if observed != issued {
		return fmt.Errorf("guard: result observed in %q, assignment %s was issued %q: %w",
			observed, action.ID, issued, ErrWrongIsolation)
	}

	changes, err := cleanChanges(diff.Changes)
	if err != nil {
		return err
	}
	generated := make(map[string]bool, len(diff.Generated))
	for _, p := range diff.Generated {
		if clean, ok := normalize(p); ok {
			generated[clean] = true
		}
	}

	if action.WriteScope.ReadOnly {
		for _, c := range changes {
			if generated[c.Path] {
				continue
			}
			return fmt.Errorf("guard: the %s assignment %s changed %q: %w",
				action.Role, action.ID, c.Path, ErrReadOnlyResult)
		}
		return nil
	}

	for _, c := range changes {
		// A path Homonto wrote itself is accepted wherever it lands: it is
		// Homonto's change, not the assignment's, and the assignment was
		// never in a position to prevent it.
		if generated[c.Path] {
			continue
		}
		if isProtected(c.Path) {
			return fmt.Errorf("guard: %q was %s: %w", c.Path, c.Kind, ErrProtectedChanged)
		}
		if kind, ok := documentKind(c.Path); ok {
			return fmt.Errorf("guard: %q is a %s document changed by assignment %s in phase %s: %w",
				c.Path, kind, action.ID, action.Phase, ErrDocumentChanged)
		}
		if !inScope(action.WriteScope.Paths, c.Path) {
			return fmt.Errorf("guard: %q was %s but assignment %s was issued only %s: %w",
				c.Path, c.Kind, action.ID, strings.Join(action.WriteScope.Paths, ", "), ErrOutOfScope)
		}
	}
	return nil
}

// cleanChanges normalizes and sorts the changed paths so validation is
// deterministic, and refuses a diff it cannot interpret.
func cleanChanges(in []Change) ([]Change, error) {
	out := make([]Change, 0, len(in))
	for _, c := range in {
		if !c.Kind.known() {
			return nil, fmt.Errorf("guard: change %q has unknown kind %q: %w", c.Path, c.Kind, ErrMalformedDiff)
		}
		clean, ok := normalize(c.Path)
		if !ok {
			return nil, fmt.Errorf("guard: changed path %q is not a clean relative path: %w", c.Path, ErrMalformedDiff)
		}
		out = append(out, Change{Path: clean, Kind: c.Kind})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}
