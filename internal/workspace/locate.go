package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/registration"
)

// Typed location errors.
var (
	// ErrNoWorkspace: no registration was found walking up from start.
	ErrNoWorkspace = errors.New("workspace: no workspace registration found")
	// ErrConflictingRegistrations: registrations on the walk-up name
	// different control roots.
	ErrConflictingRegistrations = errors.New("workspace: conflicting workspace registrations")
)

// Location is the workspace that owns the directory Locate started from.
type Location struct {
	// ControlRoot is the canonical absolute path of the control repository.
	ControlRoot string
	// MemberRoot is the directory whose registration was found (the
	// deepest registration on the walk up from start).
	MemberRoot string
	// Registration is that member's registration.
	Registration registration.Registration
}

// Locate walks up from start and reads the registrations that claim
// ownership of each ancestor: a git member's registration lives in its git
// common directory, a non-git member's under stateRoot keyed by canonical
// path. Every registration found must lead to the same control root: zero
// is ErrNoWorkspace, more than one distinct control root is
// ErrConflictingRegistrations naming both.
func Locate(ctx context.Context, start, stateRoot string) (Location, error) {
	cur, err := CanonicalPath(start)
	if err != nil {
		return Location{}, err
	}

	type hit struct {
		memberRoot string
		reg        registration.Registration
	}
	var hits []hit
	for {
		memberReg, found, err := readRegistration(ctx, cur, stateRoot)
		if err != nil {
			return Location{}, err
		}
		if found {
			hits = append(hits, hit{memberRoot: cur, reg: memberReg})
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}

	if len(hits) == 0 {
		return Location{}, fmt.Errorf("workspace: from %s: %w", start, ErrNoWorkspace)
	}
	first := hits[0]
	for _, h := range hits[1:] {
		if h.reg.ControlRoot != first.reg.ControlRoot {
			return Location{}, fmt.Errorf("workspace: from %s: %s is owned by control %s but %s by control %s: %w",
				start, first.memberRoot, first.reg.ControlRoot, h.memberRoot, h.reg.ControlRoot, ErrConflictingRegistrations)
		}
	}
	return Location{ControlRoot: first.reg.ControlRoot, MemberRoot: first.memberRoot, Registration: first.reg}, nil
}

// readRegistration reads cur's ownership record from either registration
// side. A directory may carry at most one: a .git entry makes it a git
// member. found is false when nothing is registered.
func readRegistration(ctx context.Context, cur, stateRoot string) (registration.Registration, bool, error) {
	path := registration.NonGitRegistrationPath(stateRoot, cur)
	if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
		repo, isGit, err := gitx.Inspect(ctx, gitx.ExecRunner{}, cur)
		if err != nil {
			return registration.Registration{}, false, fmt.Errorf("workspace: %s: %w", cur, err)
		}
		if !isGit {
			return registration.Registration{}, false, fmt.Errorf("workspace: %s: .git exists but is not a usable repository", cur)
		}
		path = registration.GitRegistrationPath(repo.CommonDir)
	}
	reg, err := registration.Read(path)
	if err != nil {
		if errors.Is(err, registration.ErrNotRegistered) {
			return registration.Registration{}, false, nil
		}
		return registration.Registration{}, false, err
	}
	return reg, true, nil
}

// CanonicalPath returns the absolute, symlink-resolved, cleaned form of
// path. A path that does not exist is returned in its lexical absolute
// form — canonicalization must remain usable for lookups of paths the
// caller is about to create.
func CanonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("workspace: canonicalize %s: %w", path, err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return filepath.Clean(abs), nil
		}
		return "", fmt.Errorf("workspace: canonicalize %s: %w", path, err)
	}
	return resolved, nil
}
