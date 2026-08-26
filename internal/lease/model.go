// Package lease implements all-or-none active-work leases over workspace
// members. Every lease is a strict-JSON file written beside the member's
// registration with securefs (O_EXCL at acquisition, atomic at commit), and
// every acquisition is a journaled operation whose prepared effects carry
// one random recovery token per target — the token store is the operation
// journal itself. Crash recovery either completes the acquisition (roll
// forward, the trusting default) or removes exactly the token-matching
// leases (roll back); once the checkpoint commit marker exists, activation
// is never rolled back. There is no timeout-based reclamation: leases
// outlive a crashed process until the same workspace recovers them, and
// process-liveness checks are diagnostic only (a reused pid can look alive).
package lease

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
)

// CurrentSchemaVersion is the lease and sentinel schema version this binary
// writes and accepts.
const CurrentSchemaVersion = 1

// leaseMode is the permission of every lease and sentinel file. Lease
// content carries a recovery token, so the files are owner-only.
const leaseMode = 0o600

// Operation kinds journaled by this package.
const (
	// OpKindAcquire names the all-or-none acquisition operation.
	OpKindAcquire = "lease.acquire"
	// OpKindRelease names the idempotent release operation.
	OpKindRelease = "lease.release"
)

// Effect kinds registered for recovery.
const (
	kindCreateLease = "lease.create"
	kindSentinel    = "lease.sentinel"
	kindActivate    = "lease.activate"
	kindRemoveLease = "lease.remove"
)

// meta key prefix recording that an acquisition reached its activation
// transaction (the projection of "operation applied").
const metaAppliedPrefix = "lease-op-applied:"

// workActiveState is the works-row state the activation transaction writes.
const workActiveState = "active"

// Typed errors. Wrap with context via fmt.Errorf("%w", ...) so callers can
// branch with errors.Is.
var (
	// ErrInvalidLease: a lease or sentinel value or file is not valid.
	ErrInvalidLease = errors.New("lease: invalid lease")
	// ErrInvalidRequest: an AcquireRequest is not valid.
	ErrInvalidRequest = errors.New("lease: invalid acquire request")
	// ErrLeaseConflict: the lease slot is held by a different owner.
	ErrLeaseConflict = errors.New("lease: conflicting lease held by another owner")
)

// Process is the provenance of the process that holds a lease: which host,
// which binary, when it started. It is diagnostic — recovery never decides
// takeover on liveness, and a reused pid can report alive.
type Process struct {
	// HostID identifies the machine. It is the hostname for now; a durable
	// per-machine runtime identity belongs to the handoff workstream.
	HostID string `json:"host_id"`
	// Hostname is the machine's hostname.
	Hostname string `json:"hostname"`
	// PID is the process id of the holding homonto process.
	PID int `json:"pid"`
	// Executable is the absolute path of the holding binary.
	Executable string `json:"executable"`
	// StartedAt is when the holding process started.
	StartedAt time.Time `json:"started_at"`
}

// Validate checks every process field is present and plausible.
func (p Process) Validate() error {
	if p.HostID == "" {
		return fmt.Errorf("lease: process host_id must not be empty: %w", ErrInvalidLease)
	}
	if p.Hostname == "" {
		return fmt.Errorf("lease: process hostname must not be empty: %w", ErrInvalidLease)
	}
	if p.PID <= 0 {
		return fmt.Errorf("lease: process pid %d must be positive: %w", p.PID, ErrInvalidLease)
	}
	if p.Executable == "" {
		return fmt.Errorf("lease: process executable must not be empty: %w", ErrInvalidLease)
	}
	if p.StartedAt.IsZero() {
		return fmt.Errorf("lease: process started_at must not be zero: %w", ErrInvalidLease)
	}
	return nil
}

// Alive reports whether a process with p's pid currently exists, using
// signal 0. It is diagnostic only: a recycled pid reports alive, and the
// lease layer never uses liveness to take over a lease.
func (p Process) Alive() bool {
	err := syscall.Kill(p.PID, 0)
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}

// CurrentProcess returns the provenance of the calling process.
func CurrentProcess() (Process, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return Process{}, fmt.Errorf("lease: hostname: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return Process{}, fmt.Errorf("lease: executable: %w", err)
	}
	return Process{
		HostID:     hostname,
		Hostname:   hostname,
		PID:        os.Getpid(),
		Executable: exe,
		StartedAt:  time.Now().Round(0),
	}, nil
}

// normalized returns p with the monotonic clock reading stripped from
// StartedAt, so a lease content round-trips through JSON byte-stably and
// compares equal to its decoded file.
func (p Process) normalized() Process {
	p.StartedAt = p.StartedAt.Round(0)
	return p
}

// LeaseContent is the strict-JSON content of one lease file: the identity of
// the holder, the execution generation, the process provenance, and the
// local recovery token that proves this machine's journal owns the lease.
// The token never leaves the local runtime: it appears only in the lease
// file and the control database, never in a committed artifact.
type LeaseContent struct {
	SchemaVersion int                   `json:"schema_version"`
	WorkspaceID   identity.WorkspaceID  `json:"workspace_id"`
	RepositoryID  identity.RepositoryID `json:"repository_id"`
	WorkID        identity.WorkID       `json:"work_id"`
	Generation    uint64                `json:"generation"`
	Process       Process               `json:"process"`
	RecoveryToken identity.Token        `json:"recovery_token"`
}

// Validate checks every field in canonical form.
func (c LeaseContent) Validate() error {
	if c.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("lease: schema_version must be %d, got %d: %w",
			CurrentSchemaVersion, c.SchemaVersion, ErrInvalidLease)
	}
	if err := identity.ValidateUUID(string(c.WorkspaceID)); err != nil {
		return fmt.Errorf("lease: workspace_id: %v: %w", err, ErrInvalidLease)
	}
	if err := identity.ValidateUUID(string(c.RepositoryID)); err != nil {
		return fmt.Errorf("lease: repository_id: %v: %w", err, ErrInvalidLease)
	}
	if err := identity.ValidateUUID(string(c.WorkID)); err != nil {
		return fmt.Errorf("lease: work_id: %v: %w", err, ErrInvalidLease)
	}
	if c.Generation == 0 {
		return fmt.Errorf("lease: generation must be at least 1: %w", ErrInvalidLease)
	}
	if err := c.Process.Validate(); err != nil {
		return err
	}
	if err := identity.ValidateToken(string(c.RecoveryToken)); err != nil {
		return fmt.Errorf("lease: recovery_token: %v: %w", err, ErrInvalidLease)
	}
	return nil
}

// Marshal encodes the lease in its canonical strict-JSON form.
func (c LeaseContent) Marshal() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("lease: marshal: %w", err)
	}
	return append(data, '\n'), nil
}

// ReadBytes strictly decodes lease bytes: unknown fields, trailing data, and
// missing or non-canonical values are errors wrapping ErrInvalidLease.
func ReadBytes(data []byte) (LeaseContent, error) {
	var c LeaseContent
	if err := strictDecode(data, &c); err != nil {
		return LeaseContent{}, err
	}
	if err := c.Validate(); err != nil {
		return LeaseContent{}, err
	}
	return c, nil
}

// ReadLease reads and strictly decodes the lease file at path.
func ReadLease(path string) (LeaseContent, error) {
	data, err := readControlFile(path)
	if err != nil {
		return LeaseContent{}, err
	}
	c, err := ReadBytes(data)
	if err != nil {
		return LeaseContent{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Target is one member whose lease the acquisition must hold.
type Target struct {
	// RepositoryID is the member's workspace-assigned repository id.
	RepositoryID identity.RepositoryID
	// Path is the absolute lease file path (the registration's sibling,
	// derived by the caller from the git/non-git slot layout).
	Path string
}

// AcquireRequest names the work and every member the acquisition must lease
// before the work may become active.
type AcquireRequest struct {
	WorkspaceID identity.WorkspaceID
	WorkID      identity.WorkID
	// Generation is the work's execution generation (uint64; Task 5
	// increments it on handoff).
	Generation uint64
	// Provenance is the acquiring process.
	Provenance Process
	// ControlRoot is the canonical control-repository path; the checkpoint
	// commit marker lives under its .homonto/leases/ directory.
	ControlRoot string
	// WorkKind and Title seed the works row the activation transaction
	// writes. WorkKind defaults to "task".
	WorkKind string
	Title    string
	// Targets is the full member set; duplicates of repository id or path
	// are rejected.
	Targets []Target
}

// Validate checks the request in canonical form.
func (r AcquireRequest) Validate() error {
	if err := identity.ValidateUUID(string(r.WorkspaceID)); err != nil {
		return fmt.Errorf("lease: workspace_id: %v: %w", err, ErrInvalidRequest)
	}
	if err := identity.ValidateUUID(string(r.WorkID)); err != nil {
		return fmt.Errorf("lease: work_id: %v: %w", err, ErrInvalidRequest)
	}
	if r.Generation == 0 {
		return fmt.Errorf("lease: generation must be at least 1: %w", ErrInvalidRequest)
	}
	if err := r.Provenance.Validate(); err != nil {
		return fmt.Errorf("lease: provenance: %v: %w", err, ErrInvalidRequest)
	}
	if err := validateRoot("control_root", r.ControlRoot); err != nil {
		return err
	}
	if len(r.Targets) == 0 {
		return fmt.Errorf("lease: at least one target is required: %w", ErrInvalidRequest)
	}
	seenRepo := map[identity.RepositoryID]bool{}
	seenPath := map[string]bool{}
	for _, t := range r.Targets {
		if err := identity.ValidateUUID(string(t.RepositoryID)); err != nil {
			return fmt.Errorf("lease: target repository_id: %v: %w", err, ErrInvalidRequest)
		}
		if err := validateRoot("target path", t.Path); err != nil {
			return err
		}
		if seenRepo[t.RepositoryID] {
			return fmt.Errorf("lease: duplicate target repository %s: %w", t.RepositoryID, ErrInvalidRequest)
		}
		if seenPath[t.Path] {
			return fmt.Errorf("lease: duplicate target path %s: %w", t.Path, ErrInvalidRequest)
		}
		seenRepo[t.RepositoryID] = true
		seenPath[t.Path] = true
	}
	return nil
}

// Lease is one held lease as returned by AcquireAll. OpID and Seq link it to
// the journal row whose payload carries the recovery token.
type Lease struct {
	Path    string
	OpID    identity.OperationID
	Seq     int64
	Content LeaseContent
}

// SentinelLease is one acquired lease as listed in the checkpoint commit
// marker. Tokens deliberately never appear here.
type SentinelLease struct {
	RepositoryID identity.RepositoryID `json:"repository_id"`
	Path         string                `json:"path"`
}

// SentinelContent is the checkpoint commit marker: a strict-JSON file
// written last in an acquisition, after every lease. Its presence — not its
// journal row — is what recovery reads to decide that activation must never
// be rolled back. Task 5 replaces this file with the real checkpoint; until
// then it is the durable "work became active" commit point.
type SentinelContent struct {
	SchemaVersion int                  `json:"schema_version"`
	WorkspaceID   identity.WorkspaceID `json:"workspace_id"`
	WorkID        identity.WorkID      `json:"work_id"`
	Generation    uint64               `json:"generation"`
	// Version is bumped on every membership change during active work; it
	// marks the marker stale so downstream evidence is re-derived (engine
	// side invalidation is the workflow workstreams' job).
	Version uint64 `json:"version"`
	// OperationID is the acquisition operation whose commit this marker
	// finalizes.
	OperationID identity.OperationID `json:"operation_id"`
	Leases      []SentinelLease      `json:"leases"`
}

// Validate checks the marker in canonical form.
func (c SentinelContent) Validate() error {
	if c.SchemaVersion != CurrentSchemaVersion {
		return fmt.Errorf("lease: sentinel schema_version must be %d, got %d: %w",
			CurrentSchemaVersion, c.SchemaVersion, ErrInvalidLease)
	}
	if err := identity.ValidateUUID(string(c.WorkspaceID)); err != nil {
		return fmt.Errorf("lease: sentinel workspace_id: %v: %w", err, ErrInvalidLease)
	}
	if err := identity.ValidateUUID(string(c.WorkID)); err != nil {
		return fmt.Errorf("lease: sentinel work_id: %v: %w", err, ErrInvalidLease)
	}
	if c.Generation == 0 {
		return fmt.Errorf("lease: sentinel generation must be at least 1: %w", ErrInvalidLease)
	}
	if c.Version == 0 {
		return fmt.Errorf("lease: sentinel version must be at least 1: %w", ErrInvalidLease)
	}
	if err := identity.ValidateUUID(string(c.OperationID)); err != nil {
		return fmt.Errorf("lease: sentinel operation_id: %v: %w", err, ErrInvalidLease)
	}
	for _, l := range c.Leases {
		if err := identity.ValidateUUID(string(l.RepositoryID)); err != nil {
			return fmt.Errorf("lease: sentinel repository_id: %v: %w", err, ErrInvalidLease)
		}
		if err := validateRoot("sentinel lease path", l.Path); err != nil {
			return err
		}
	}
	return nil
}

// Marshal encodes the sentinel in its canonical strict-JSON form.
func (c SentinelContent) Marshal() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("lease: sentinel marshal: %w", err)
	}
	return append(data, '\n'), nil
}

// ReadSentinelBytes strictly decodes sentinel bytes.
func ReadSentinelBytes(data []byte) (SentinelContent, error) {
	var c SentinelContent
	if err := strictDecode(data, &c); err != nil {
		return SentinelContent{}, err
	}
	if err := c.Validate(); err != nil {
		return SentinelContent{}, err
	}
	return c, nil
}

// ReadSentinel reads and strictly decodes the checkpoint commit marker at
// path.
func ReadSentinel(path string) (SentinelContent, error) {
	data, err := readControlFile(path)
	if err != nil {
		return SentinelContent{}, err
	}
	c, err := ReadSentinelBytes(data)
	if err != nil {
		return SentinelContent{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// SentinelPath returns the checkpoint commit marker path for one work under
// a control root: <control-root>/.homonto/leases/<work-id>.active.
func SentinelPath(controlRoot string, workID identity.WorkID) string {
	return filepath.Join(controlRoot, ".homonto", "leases", string(workID)+".active")
}

// strictDecode decodes strict JSON: unknown fields and trailing data fail.
func strictDecode(data []byte, into any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("lease: decode: %w: %w", err, ErrInvalidLease)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("lease: trailing data after JSON object: %w", ErrInvalidLease)
	}
	return nil
}

// validateRoot requires a clean absolute slash-separated path.
func validateRoot(field, path string) error {
	if path == "" {
		return fmt.Errorf("lease: %s must not be empty: %w", field, ErrInvalidRequest)
	}
	if strings.ContainsRune(path, '\\') {
		return fmt.Errorf("lease: %s %q must use '/' as its only separator: %w", field, path, ErrInvalidRequest)
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("lease: %s %q must be absolute: %w", field, path, ErrInvalidRequest)
	}
	if filepath.Clean(path) != path {
		return fmt.Errorf("lease: %s %q must be clean: %w", field, path, ErrInvalidRequest)
	}
	return nil
}
