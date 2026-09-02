// Package snapshot is the opt-in transactional layer around homonto apply
// (ADR 0030): `homonto apply --snapshot` writes a durable journal and can
// roll back ordinary failures; `homonto recover` finishes an interrupted
// transaction under a process lock the OS releases on death; `homonto undo`
// restores a completed snapshot. Checkpoints are semantic — unresolved
// desired values, applied hashes, link targets, and content-addressed blobs —
// never whole tool files, so resolved secrets never enter the journal.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/state"
)

// SchemaVersion is the journal format version.
const SchemaVersion = 1

// Status is a journal's lifecycle state.
type Status string

const (
	StatusPrepared   Status = "prepared"    // being applied; not yet finished
	StatusCommitted  Status = "committed"   // apply succeeded; undoable
	StatusRolledBack Status = "rolled-back" // failure rolled back; no longer undoable
)

// EntryState tracks one changeset through the transaction.
type EntryState string

const (
	EntryPrepared   EntryState = "prepared"
	EntryCommitted  EntryState = "committed"
	EntryRolledBack EntryState = "rolled-back"
)

// MutationKind names what a journal entry's disk operations touch.
type MutationKind string

const (
	MutStructured MutationKind = "structured" // managed keys inside a doc (mcp./setting./tui./plugin.)
	MutLink       MutationKind = "link"       // managed symlink
	MutCopy       MutationKind = "copy"       // copy-mode subagent content file
	MutCatalog    MutationKind = "catalog"    // materialized catalog content
	MutRemoteLock MutationKind = "remote-lock"
	MutState      MutationKind = "state" // state.json / state.<repo>.json
)

// DiskOp is one before-state disk fact for a mutation kind. For links the
// fact is the current target; for copies and the remote lock it is a blob
// reference; for catalog content it is a blob reference (regenerated on next
// apply, so the journal only pins its identity).
type DiskOp struct {
	Kind MutationKind `json:"kind"`
	Path string       `json:"path"`
	Fact string       `json:"fact,omitempty"` // link target, blob ref, or "" for absent
	Blob string       `json:"blob,omitempty"` // content-addressed blob id
}

// Partition is one state partition's before/after checkpoint: the main
// state.json or a named state.<repo>.json. Desired and Applied are captured
// per key; tombstones ride along.
type Partition struct {
	Path   string                            `json:"path"`
	Before map[string]map[string]state.Entry `json:"before"`
	After  map[string]map[string]state.Entry `json:"after"`
}

// Journal is one apply's transaction record.
type Journal struct {
	SchemaVersion int    `json:"schemaVersion"`
	ApplyID       string `json:"applyId"`
	Status        Status `json:"status"`
	Started       string `json:"started"`
	Finished      string `json:"finished,omitempty"`
	// Tool is the adapting tool's label this journal applies to ("opencode").
	Tool string `json:"tool"`
	// Partitions are the state checkpoints, keyed by state file path.
	Partitions []Partition `json:"partitions,omitempty"`
	// Disk holds the before-state disk facts, keyed by path.
	Disk []DiskOp `json:"disk,omitempty"`
	// Changesets tracks per-adapter progress.
	Changesets []ChangesetState `json:"changesets,omitempty"`
}

// ChangesetState is one adapter's changeset progress through the transaction.
type ChangesetState struct {
	Tool  string     `json:"tool"`
	State EntryState `json:"state"`
}

// BlobStore stores content-addressed blobs under the journal directory.
type BlobStore struct{ dir string }

func NewBlobStore(dir string) *BlobStore { return &BlobStore{dir: dir} }

// Put stores data under its sha256 hex id and returns the id.
func (b *BlobStore) Put(data []byte) (string, error) {
	sum := sha256.Sum256(data)
	id := hex.EncodeToString(sum[:])
	p := filepath.Join(b.dir, id)
	if _, err := os.Stat(p); err == nil {
		return id, nil
	}
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		return "", err
	}
	return id, fsutil.WriteControlPlane(p, data, 0o600)
}

// Get loads a blob by id.
func (b *BlobStore) Get(id string) ([]byte, error) {
	return os.ReadFile(filepath.Join(b.dir, id))
}

// Dir is the snapshots root: <stateDir>/snapshots.
func Dir(stateDir string) string { return filepath.Join(stateDir, "snapshots") }

// JournalPath is one apply's journal file.
func JournalPath(stateDir, applyID string) string {
	return filepath.Join(Dir(stateDir), applyID, "journal.json")
}

// BlobDir is one apply's blob store directory.
func BlobDir(stateDir, applyID string) string {
	return filepath.Join(Dir(stateDir), applyID, "blobs")
}

// Save writes the journal atomically (0600, no-follow).
func (j *Journal) Save(stateDir string) error {
	if err := os.MkdirAll(filepath.Dir(JournalPath(stateDir, j.ApplyID)), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteControlPlane(JournalPath(stateDir, j.ApplyID), data, 0o600)
}

// Load reads a journal; missing means no such apply.
func Load(stateDir, applyID string) (*Journal, bool, error) {
	data, err := os.ReadFile(JournalPath(stateDir, applyID))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var j Journal
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, false, fmt.Errorf("snapshot: malformed journal %s: %w", applyID, err)
	}
	if j.SchemaVersion != SchemaVersion {
		return nil, false, fmt.Errorf("snapshot: journal %s schema %d is not supported (%d)", applyID, j.SchemaVersion, SchemaVersion)
	}
	return &j, true, nil
}

// PartitionState encodes a partition's managed map for checkpointing.
func PartitionState(st *state.State) map[string]map[string]state.Entry {
	out := map[string]map[string]state.Entry{}
	for tool, entries := range st.Managed {
		out[tool] = map[string]state.Entry{}
		for k, e := range entries {
			out[tool][k] = e
		}
	}
	return out
}

// ApplyPartition restores a partition's managed entries and tombstones.
func ApplyPartition(st *state.State, src map[string]map[string]state.Entry) {
	st.Managed = map[string]map[string]state.Entry{}
	for tool, entries := range src {
		st.Managed[tool] = map[string]state.Entry{}
		for k, e := range entries {
			st.Managed[tool][k] = e
		}
	}
}

// EqualEntries compares two checkpointed partitions for after-image checks.
func EqualEntries(a, b map[string]map[string]state.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for tool, entries := range a {
		other, ok := b[tool]
		if !ok || len(entries) != len(other) {
			return false
		}
		for k, e := range entries {
			o, ok := other[k]
			if !ok || o.Desired != e.Desired || o.Applied != e.Applied {
				return false
			}
		}
	}
	return true
}

// HashEntries digests a checkpointed partition deterministically.
func HashEntries(m map[string]map[string]state.Entry) string {
	h := sha256.New()
	tools := make([]string, 0, len(m))
	for tool := range m {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		keys := make([]string, 0, len(m[tool]))
		for k := range m[tool] {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			e := m[tool][k]
			fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00", tool, k, e.Desired, e.Applied)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// List returns the apply IDs under the snapshots root, newest first.
func List(stateDir string) ([]string, error) {
	entries, err := os.ReadDir(Dir(stateDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

// Retain keeps the latest n committed journals and drops older committed
// ones plus their blobs. Incomplete journals are never dropped; that is
// recovery's job. Unreferenced blobs of removed journals are removed with
// their journal directory.
func Retain(stateDir string, n int) error {
	ids, err := List(stateDir)
	if err != nil {
		return err
	}
	kept := 0
	for _, id := range ids {
		j, ok, err := Load(stateDir, id)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if j.Status == StatusCommitted {
			if kept >= n {
				if err := os.RemoveAll(filepath.Join(Dir(stateDir), id)); err != nil {
					return err
				}
				continue
			}
			kept++
		}
	}
	return nil
}

// Now formats a UTC timestamp.
func Now() string { return time.Now().UTC().Format(time.RFC3339) }
