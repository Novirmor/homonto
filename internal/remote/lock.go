package remote

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/Novirmor/homonto/internal/fsutil"
)

// LockEntry records the provenance of one materialized remote install. It holds
// no wall-clock timestamp so the lockfile is byte-stable across runs of
// unchanged state.
type LockEntry struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Locator   string `json:"locator"`
	Transport string `json:"transport"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

func (e LockEntry) key() string { return e.Kind + "/" + e.Name }

// Lock is the remote lockfile: an auditable, reproducible record of every
// remote install keyed by "kind/name".
type Lock struct {
	Entries map[string]LockEntry
}

// LoadLock reads .homonto/remote.lock.json. A missing file yields an empty lock.
func LoadLock(path string) (Lock, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Lock{Entries: map[string]LockEntry{}}, nil
		}
		return Lock{}, fmt.Errorf("remote: lock: %w", err)
	}
	var doc lockDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return Lock{}, fmt.Errorf("remote: lock %q: %w", path, err)
	}
	entries := make(map[string]LockEntry, len(doc.Remotes))
	for _, e := range doc.Remotes {
		entries[e.key()] = e
	}
	return Lock{Entries: entries}, nil
}

// lockDoc is the on-disk shape: a sorted array so the file is diff-stable.
type lockDoc struct {
	Remotes []LockEntry `json:"remotes"`
}

// Set inserts or replaces an entry. The locator is redacted here so a
// credential embedded in a remote source can never be persisted to the lockfile,
// regardless of the caller.
func (l *Lock) Set(e LockEntry) {
	if l.Entries == nil {
		l.Entries = map[string]LockEntry{}
	}
	e.Locator = RedactLocator(e.Locator)
	l.Entries[e.key()] = e
}

// Get returns an entry by kind/name.
func (l Lock) Get(kind, name string) (LockEntry, bool) {
	e, ok := l.Entries[kind+"/"+name]
	return e, ok
}

// Remove drops an entry by kind/name.
func (l *Lock) Remove(kind, name string) {
	delete(l.Entries, kind+"/"+name)
}

// Digests returns the parsed pins referenced by the lock. Entries whose digest
// does not parse are skipped deliberately: the lockfile is human-editable and a
// typo in one entry must not crash GC. The trade is that a malformed pin's
// cache entry becomes unreferenced and may be reclaimed by `cache gc` — but
// the cache is content-addressed and a correct re-declaration will re-fetch
// cleanly, so the recovery path is "re-apply", not "data loss". Callers that
// need to surface malformed entries to the user should validate the lock
// separately (LoadLock already rejects structurally-broken files).
func (l Lock) Digests() []Digest {
	var out []Digest
	for _, e := range l.sorted() {
		if d, err := ParseDigest(e.Digest); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// Save writes the lock atomically as a stable-sorted array with no timestamps,
// so consecutive saves of unchanged state are byte-identical.
func (l Lock) Save(path string) error {
	doc := lockDoc{Remotes: l.sorted()}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("remote: lock: %w", err)
	}
	// remote.lock.json is one of homonto's own control-plane files: write it
	// no-follow (a planted symlink is refused) rather than through a symlink.
	return fsutil.WriteControlPlane(path, buf.Bytes(), 0o600)
}

func (l Lock) sorted() []LockEntry {
	out := make([]LockEntry, 0, len(l.Entries))
	for _, e := range l.Entries {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}
