// Package registration owns the on-disk proof that a workspace owns a
// member repository: a small strict-JSON file created atomically with
// create-if-absent semantics (O_EXCL). A registration exists either inside
// the member's git common directory (git members) or under a per-platform
// state root keyed by the member's canonical path (non-git members).
// Ownership is exclusive: a registration held by another workspace is
// rejected even when idle, and takeover is refused while a lease file
// exists.
package registration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// CurrentSchemaVersion is the registration schema version this binary
// writes and accepts.
const CurrentSchemaVersion = 1

// Typed errors, wrapped with context so callers can branch with errors.Is.
var (
	// ErrInvalidRegistration: a registration value or file is not valid.
	ErrInvalidRegistration = errors.New("registration: invalid registration")
	// ErrNotRegistered: no registration file exists at the path.
	ErrNotRegistered = errors.New("registration: not registered")
	// ErrOwnedByOther: the registration names a different workspace.
	ErrOwnedByOther = errors.New("registration: owned by another workspace")
	// ErrLeaseActive: takeover was refused because a lease exists.
	ErrLeaseActive = errors.New("registration: active lease blocks takeover")
	// ErrRegistrationChanged: the registration changed on disk between
	// the read and the write of a takeover.
	ErrRegistrationChanged = errors.New("registration: registration changed during takeover")
)

// Registration is the ownership record stored beside a member repository.
// All fields are required; the encoding is strict JSON with a fixed field
// set.
type Registration struct {
	// SchemaVersion must be CurrentSchemaVersion.
	SchemaVersion int `json:"schema_version"`
	// WorkspaceID identifies the owning workspace.
	WorkspaceID identity.WorkspaceID `json:"workspace_id"`
	// RepositoryID identifies the member within the workspace.
	RepositoryID identity.RepositoryID `json:"repository_id"`
	// ControlRoot is the canonical absolute path of the control repository.
	ControlRoot string `json:"control_root"`
	// MemberRoot is the canonical absolute path of the member repository.
	MemberRoot string `json:"member_root"`
	// Kind is how the member is tracked: git or non_git.
	Kind workspacecfg.MemberKind `json:"kind"`
}

// Validate checks every field in canonical form: schema version exactly 1,
// canonical UUIDv4 identifiers, clean absolute slash-separated roots, and a
// known member kind.
func (r Registration) Validate() error {
	if r.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("registration: schema_version must be %d, got %d: %w",
			CurrentSchemaVersion, r.SchemaVersion, ErrInvalidRegistration)
	}
	if err := identity.ValidateUUID(string(r.WorkspaceID)); err != nil {
		return fmt.Errorf("registration: workspace_id %q: %w: %w", r.WorkspaceID, err, ErrInvalidRegistration)
	}
	if err := identity.ValidateUUID(string(r.RepositoryID)); err != nil {
		return fmt.Errorf("registration: repository_id %q: %w: %w", r.RepositoryID, err, ErrInvalidRegistration)
	}
	if err := validateRoot("control_root", r.ControlRoot); err != nil {
		return err
	}
	if err := validateRoot("member_root", r.MemberRoot); err != nil {
		return err
	}
	switch r.Kind {
	case workspacecfg.KindGit, workspacecfg.KindNonGit:
	default:
		return fmt.Errorf("registration: kind %q must be %q or %q: %w",
			r.Kind, workspacecfg.KindGit, workspacecfg.KindNonGit, ErrInvalidRegistration)
	}
	return nil
}

// validateRoot requires a clean absolute slash-separated path.
func validateRoot(field, path string) error {
	if path == "" {
		return fmt.Errorf("registration: %s must not be empty: %w", field, ErrInvalidRegistration)
	}
	if strings.ContainsRune(path, '\\') {
		return fmt.Errorf("registration: %s %q must use '/' as its only separator: %w", field, path, ErrInvalidRegistration)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("registration: %s %q must be absolute: %w", field, path, ErrInvalidRegistration)
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("registration: %s %q must be clean: %w", field, path, ErrInvalidRegistration)
	}
	return nil
}

// Marshal encodes the registration in its canonical strict-JSON form.
func (r Registration) Marshal() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("registration: marshal: %w", err)
	}
	return append(data, '\n'), nil
}

// ReadBytes strictly decodes registration bytes: unknown fields, trailing
// data, and missing or non-canonical values are errors wrapping
// ErrInvalidRegistration.
func ReadBytes(data []byte) (Registration, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var reg Registration
	if err := dec.Decode(&reg); err != nil {
		return Registration{}, fmt.Errorf("registration: decode: %w: %w", err, ErrInvalidRegistration)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Registration{}, fmt.Errorf("registration: trailing data after JSON object: %w", ErrInvalidRegistration)
	}
	if err := reg.Validate(); err != nil {
		return Registration{}, err
	}
	return reg, nil
}
