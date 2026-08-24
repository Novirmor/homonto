package checkpoint

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/securefs"
)

// digestDomain separates checkpoint digests from every other fingerprint
// domain. Internal constant; colon-free per the fingerprint contract.
const digestDomain = "checkpoint"

// Store writes checkpoints through a securefs root: every write lands via
// an atomic, fsynced, symlink-refusing replacement at mode 0600, so after
// a crash the slot holds either the old or the new canonical bytes.
type Store struct {
	root *securefs.Root
	rel  string
}

// NewStore binds a Store to the checkpoint slot at rel (root-relative)
// inside root. The parent directory of rel must already exist: securefs
// never creates directories, and workspace scaffolding owns their
// creation.
func NewStore(root *securefs.Root, rel string) (Store, error) {
	if root == nil {
		return Store{}, fmt.Errorf("checkpoint: store root must not be nil")
	}
	if rel == "" {
		return Store{}, fmt.Errorf("checkpoint: store path must not be empty")
	}
	return Store{root: root, rel: rel}, nil
}

// Write encodes cp canonically, atomically replaces the checkpoint slot,
// and returns the digest of the written bytes under the checkpoint domain.
// Write does not validate cp; callers run Validate before persisting.
func (s Store) Write(cp Checkpoint) (fingerprint.Digest, error) {
	return s.persist(cp)
}

// Repair rewrites the slot with expected unconditionally — the recovery
// path for a torn, corrupted, or hand-edited checkpoint. It exists apart
// from Write so recovery call-sites read explicitly and so future
// safety checks added to the normal write path (for example refusing to
// clobber a different workspace's checkpoint) never silently change
// repair semantics.
func (s Store) Repair(expected Checkpoint) (fingerprint.Digest, error) {
	return s.persist(expected)
}

// persist is the shared write path of Write and Repair.
func (s Store) persist(cp Checkpoint) (fingerprint.Digest, error) {
	b, err := Encode(cp)
	if err != nil {
		return "", err
	}
	if err := s.root.WriteAtomic(s.rel, b, fs.FileMode(0o600)); err != nil {
		return "", fmt.Errorf("checkpoint: write %s: %w", s.rel, err)
	}
	return fingerprint.Bytes(digestDomain, b), nil
}

// Load reads the checkpoint at path (any absolute or relative filesystem
// path — typically a fresh clone being attached), strictly decodes it, and
// returns it together with the digest of the exact bytes read, so callers
// can compare against a recorded fingerprint. Load never writes.
func Load(path string) (Checkpoint, fingerprint.Digest, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Checkpoint{}, "", fmt.Errorf("checkpoint: read %s: %w", path, err)
	}
	cp, err := Decode(bytes.NewReader(b))
	if err != nil {
		return Checkpoint{}, "", err
	}
	return cp, fingerprint.Bytes(digestDomain, b), nil
}
