// Package identity defines the typed identifiers and shared secrets that name
// workflow entities (workspaces, repositories, works, operations, actions,
// sessions). All identifiers are canonical UUIDv4 strings; tokens are 32
// random bytes in unpadded base64url. Generation uses crypto/rand only.
package identity

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// WorkspaceID identifies a Homonto workspace across machines and handoffs.
type WorkspaceID string

// RepositoryID identifies a member repository within one workspace.
type RepositoryID string

// WorkID identifies a Task or Change work unit.
type WorkID string

// OperationID identifies one journaled state-machine operation.
type OperationID string

// ActionID identifies one guarded action performed during an operation.
type ActionID string

// ParallelGroupID identifies one issued parallel action group: the maximal
// set of actions released together by the assignment scheduler.
type ParallelGroupID string

// SessionID identifies one host-integration session.
type SessionID string

// Token is a random shared secret (assignment, lease recovery, handoff) in
// unpadded base64url, carrying 256 bits of entropy.
type Token string

// NewWorkspaceID returns a fresh WorkspaceID.
func NewWorkspaceID() (WorkspaceID, error) { return newID[WorkspaceID]() }

// NewRepositoryID returns a fresh RepositoryID.
func NewRepositoryID() (RepositoryID, error) { return newID[RepositoryID]() }

// NewWorkID returns a fresh WorkID.
func NewWorkID() (WorkID, error) { return newID[WorkID]() }

// NewOperationID returns a fresh OperationID.
func NewOperationID() (OperationID, error) { return newID[OperationID]() }

// NewActionID returns a fresh ActionID.
func NewActionID() (ActionID, error) { return newID[ActionID]() }

// NewParallelGroupID returns a fresh ParallelGroupID.
func NewParallelGroupID() (ParallelGroupID, error) { return newID[ParallelGroupID]() }

// NewSessionID returns a fresh SessionID.
func NewSessionID() (SessionID, error) { return newID[SessionID]() }

// newID is the shared generator wrapped by every typed constructor.
func newID[T ~string]() (T, error) {
	id, err := NewUUID()
	if err != nil {
		return "", err
	}
	return T(id), nil
}

// NewUUID returns a random UUIDv4 in canonical lowercase string form.
func NewUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("identity: read random bytes: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// NewToken returns a fresh 32-byte random token in unpadded base64url.
func NewToken() (Token, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("identity: read random bytes: %w", err)
	}
	return Token(base64.RawURLEncoding.EncodeToString(b[:])), nil
}

// ValidateUUID reports whether s is a canonical lowercase UUIDv4 string
// (8-4-4-4-12 hex digits, version nibble 4, variant bits 10). It rejects
// equivalent spellings such as uppercase, braces, or URNs so identifiers
// compare equal only in their generated form.
func ValidateUUID(s string) error {
	if len(s) != 36 {
		return fmt.Errorf("identity: UUID must be 36 characters, got %d", len(s))
	}
	for i := 0; i < len(s); i++ {
		switch i {
		case 8, 13, 18, 23:
			if s[i] != '-' {
				return fmt.Errorf("identity: UUID dash at position %d missing", i)
			}
		default:
			if !isLowerHex(s[i]) {
				return fmt.Errorf("identity: UUID character %q at position %d is not lowercase hex", s[i], i)
			}
		}
	}
	if s[14] != '4' {
		return fmt.Errorf("identity: UUID version %q is not 4", s[14])
	}
	switch s[19] {
	case '8', '9', 'a', 'b':
	default:
		return fmt.Errorf("identity: UUID variant nibble %q is not 10xx", s[19])
	}
	return nil
}

// ValidateToken reports whether s is a token in generated form: 43
// characters of unpadded base64url encoding exactly 32 bytes. Re-encoding
// the decoded bytes must reproduce s, so trailing-bit aliases fail.
func ValidateToken(s string) error {
	if len(s) != base64.RawURLEncoding.EncodedLen(32) {
		return fmt.Errorf("identity: token must be %d characters, got %d", base64.RawURLEncoding.EncodedLen(32), len(s))
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return fmt.Errorf("identity: token is not unpadded base64url: %w", err)
	}
	if len(b) != 32 {
		return fmt.Errorf("identity: token decodes to %d bytes, want 32", len(b))
	}
	if base64.RawURLEncoding.EncodeToString(b) != s {
		return fmt.Errorf("identity: token is not in canonical base64url form")
	}
	return nil
}

func isLowerHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}
