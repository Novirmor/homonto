// Package guard is Homonto's write boundary. It has two halves, and the
// second is the one that actually holds.
//
// # The process gate
//
// Authorize answers `homonto guard --json` for a cooperating host's write
// hook. It is a process gate, not an operating-system sandbox and not a
// complete security boundary: it blocks operations a host PRESENTS for
// approval, and a shell command or a child process can walk straight past
// it. Every refusal is a decision with a reason and a machine-readable
// code, and everything unrecognized fails closed.
//
// # The final-diff gate
//
// ValidateAssignmentResult is the half that does not depend on the host
// cooperating. Before a report is accepted or a workflow advances,
// Homonto looks at what actually changed on disk and refuses anything the
// assignment was not issued to change — including changes the write hook
// never saw. Homonto does not claim to have PREVENTED those writes; it
// claims to refuse to build on them.
//
// The two halves are deliberately independent. The gate can be bypassed;
// the diff cannot, because it runs on the result rather than on a request.
package guard

import (
	"crypto/subtle"
	"fmt"
	"path"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
)

// ControlDir is the control-plane directory whose contents are
// binary-owned in every phase.
const ControlDir = ".homonto"

// protectedPaths are the binary-owned state files. They are never written
// by an assignment, in any phase, by any role: they carry the runtime
// projection and the portable checkpoint, and a hand edit to either is
// corruption rather than input.
var protectedPaths = []string{
	ControlDir + "/runtime.db",
	ControlDir + "/runtime.db-wal",
	ControlDir + "/runtime.db-shm",
	ControlDir + "/checkpoint.json",
}

// Request is one guarded operation: the wire request a cooperating host
// presented, plus the permission the host claims to be acting under.
//
// There are exactly two permissions, and a request carries at most one.
// An ASSIGNMENT lets an implementer write source inside its issued
// isolation area and scope. An EDIT GRANT lets a host session write one
// workflow document, in the phase the ownership table opened it in. A
// request that claims neither is a session with no write permission,
// which is the correct answer for a host that is not doing either thing.
//
// Neither id nor token is part of the wire request: the runtime hands
// them to the host when it issues the assignment or the grant, and the
// host's hook passes them back here.
type Request struct {
	Wire protocol.GuardRequest

	// ActionID and Token name the assignment the session is executing.
	ActionID identity.ActionID
	Token    identity.Token

	// GrantID and GrantToken name the edit grant the session is writing
	// under.
	GrantID    identity.ActionID
	GrantToken identity.Token
}

// Validate checks the request's shape. A malformed request is refused
// rather than interpreted.
func (r Request) Validate() error {
	if err := r.Wire.Validate(); err != nil {
		return err
	}
	if r.ActionID != "" && r.GrantID != "" {
		return fmt.Errorf("guard: a request claims both an assignment and an edit grant; it may claim at most one")
	}
	if err := validatePermission("action", r.ActionID, r.Token); err != nil {
		return err
	}
	return validatePermission("grant", r.GrantID, r.GrantToken)
}

// validatePermission checks one id/token pair: both present and
// well-formed, or both absent.
func validatePermission(what string, id identity.ActionID, token identity.Token) error {
	if id == "" {
		if token != "" {
			return fmt.Errorf("guard: a %s token was presented without a %s id", what, what)
		}
		return nil
	}
	if err := identity.ValidateUUID(string(id)); err != nil {
		return fmt.Errorf("guard: %s id: %w", what, err)
	}
	if err := identity.ValidateToken(string(token)); err != nil {
		return fmt.Errorf("guard: %s token: %w", what, err)
	}
	return nil
}

// Decision codes. They are the runtime's vocabulary for why a write was
// allowed or refused, stable enough for a host to branch on.
const (
	// CodeAllowed: the write is inside the assignment's isolation and
	// scope, in a phase that permits it.
	CodeAllowed = "allowed"
	// CodeMalformed: the request itself did not parse.
	CodeMalformed = "malformed_request"
	// CodeNoPermission: the session claims neither an assignment nor an
	// edit grant, so it has no write permission at all.
	CodeNoPermission = "no_permission"
	// CodeUnknownAssignment: the claimed assignment does not exist.
	CodeUnknownAssignment = "unknown_assignment"
	// CodeStaleAssignment: the assignment is not awaiting a result — it
	// was answered, invalidated, or never issued.
	CodeStaleAssignment = "stale_assignment"
	// CodeStaleToken: the token was not issued for that assignment under
	// this runtime key.
	CodeStaleToken = "stale_token"
	// CodeUnknownGrant: the claimed edit grant was never issued.
	CodeUnknownGrant = "unknown_grant"
	// CodeStaleGrant: the edit grant was already accepted; grants are
	// single-use.
	CodeStaleGrant = "stale_grant"
	// CodeWrongDocument: the edit grant covers a different document.
	CodeWrongDocument = "wrong_document"
	// CodeReadOnlyRole: explorer, reviewer, and skeptic write nothing.
	CodeReadOnlyRole = "read_only_role"
	// CodeOutsideIsolation: the write is outside the isolation area the
	// assignment was issued in.
	CodeOutsideIsolation = "outside_isolation"
	// CodeOutsideScope: the write is inside the isolation area but
	// outside the declared scope.
	CodeOutsideScope = "outside_scope"
	// CodeWrongPhase: a workflow document written outside the phase that
	// owns it.
	CodeWrongPhase = "wrong_phase"
	// CodeBinaryOwned: a document region only Homonto writes.
	CodeBinaryOwned = "binary_owned"
	// CodeProtectedPath: the runtime database or the checkpoint.
	CodeProtectedPath = "protected_path"
)

// allow and refuse build the two kinds of decision. Every decision
// explains itself: a host that cannot tell the user WHY a write was
// refused turns a boundary into a mystery.
func allow(reason string) protocol.GuardDecision {
	return protocol.GuardDecision{Allow: true, Reason: reason, Code: CodeAllowed}
}

func refuse(code, format string, args ...any) protocol.GuardDecision {
	return protocol.GuardDecision{Allow: false, Reason: fmt.Sprintf(format, args...), Code: code}
}

// normalize returns a slash path in clean form, or ok=false when the path
// is not a usable relative path.
func normalize(p string) (string, bool) {
	if p == "" || strings.ContainsRune(p, '\x00') || strings.Contains(p, `\`) {
		return "", false
	}
	if strings.HasPrefix(p, "/") {
		return "", false
	}
	clean := path.Clean(p)
	if clean == "." {
		return ".", true
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." {
			return "", false
		}
	}
	return clean, true
}

// within reports whether rel is at or under base, comparing whole path
// segments so "srcs/x" is not treated as being inside "src".
func within(base, rel string) bool {
	if base == "." || base == "" {
		return true
	}
	return rel == base || strings.HasPrefix(rel, base+"/")
}

// isProtected reports whether a control-root-relative path is binary-owned
// state. Everything under the control directory that is not an artifact is
// protected; the named state files are protected by name so the check
// survives a future layout change inside the directory.
func isProtected(rel string) bool {
	for _, p := range protectedPaths {
		if rel == p {
			return true
		}
	}
	return within(ControlDir, rel)
}

// tokenMatches compares an issued token against a presented one without
// leaking the comparison through timing.
func tokenMatches(issued, presented identity.Token) bool {
	return subtle.ConstantTimeCompare([]byte(issued), []byte(presented)) == 1
}
