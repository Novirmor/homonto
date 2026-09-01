package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/schema"
)

// Entry is the last-applied record for one managed key. Desired holds the
// unresolved value (may contain ${...} tokens); Applied holds a non-secret
// sha256 of the resolved value that was written to disk. Neither field ever
// contains a plaintext secret, so state.json is safe to share.
type Entry struct {
	Desired string `json:"desired"`
	Applied string `json:"applied"`
}

// State is the last-applied snapshot, keyed tool -> managed key -> Entry.
// CatalogVersion is the embedded-catalog version last successfully materialized;
// it is global (not per-tool) and omitted when empty so pre-catalog state.json
// files stay backward-compatible (absent = "force materialize").
type State struct {
	// SchemaVersion is the state.json format version. Absent/0 means a legacy
	// (pre-versioning) file and is treated as the current version; a value greater
	// than CurrentStateSchemaVersion is rejected fail-closed at load.
	SchemaVersion  int                         `json:"schemaVersion,omitempty"`
	Managed        map[string]map[string]Entry `json:"managed"`
	CatalogVersion string                      `json:"catalogVersion,omitempty"`
	// HomontoVersion is the homonto binary version that performed the last apply.
	// It lets `onto` detect a binary/framework version skew (its own version vs
	// the homonto that projected the framework) and lets `homonto update` report
	// the transition. Absent on legacy state files.
	HomontoVersion string `json:"homontoVersion,omitempty"`
	// FrameworkVersions records the version of each builtin framework at the last
	// apply (framework name -> version), so a version history is written down.
	FrameworkVersions map[string]string `json:"frameworkVersions,omitempty"`
	// RenderFingerprint digests every config-derived input behind the last
	// materialization: the per-tool subagent model routes, the source content of
	// each declared resource, and the resolved [tooling] providers. Rendered
	// output depends on config, not only on the catalog, so the catalog version
	// alone cannot gate materialization: editing a model route or a tooling
	// provider leaves the version untouched and would otherwise freeze the
	// rendered content forever. Absent = force re-render.
	//
	// Named SubagentRenderFingerprint (json "subagentRenderFingerprint")
	// through schema 1, when it held only the model routes. It has been a
	// composite since v0.2.1; schema 2 renames it to match.
	RenderFingerprint string `json:"renderFingerprint,omitempty"`
}

// CurrentStateSchemaVersion is the state.json schema version this binary writes.
//
// 2 renamed "subagentRenderFingerprint" to "renderFingerprint". No value-preserving
// migration exists on purpose: the field is a cache whose absent value already
// means "force re-render", and the catalog version bump shipped alongside the
// rename forces a re-materialize for every user regardless — so carrying the
// old value across would buy nothing while adding a code path that could be
// wrong. A schema-1 file loads with the field empty and re-renders once.
const CurrentStateSchemaVersion = 2

// CatalogVersionRecorded returns the catalog version last materialized, or "".
func (s *State) CatalogVersionRecorded() string { return s.CatalogVersion }

// SetCatalogVersion records the catalog version after a successful materialize.
func (s *State) SetCatalogVersion(v string) { s.CatalogVersion = v }

// RenderFingerprintRecorded returns the render fingerprint behind the last
// materialization, or "" (never materialized / pre-rename state).
func (s *State) RenderFingerprintRecorded() string { return s.RenderFingerprint }

// SetRenderFingerprint records the render fingerprint after a successful
// materialize.
func (s *State) SetRenderFingerprint(v string) { s.RenderFingerprint = v }

// HomontoVersionRecorded returns the homonto binary version that last applied,
// or "" for a legacy/never-applied state.
func (s *State) HomontoVersionRecorded() string { return s.HomontoVersion }

// SetHomontoVersion records the homonto binary version at apply time. An empty
// value (unstamped/dev build) is ignored so a dev run never overwrites a real
// recorded version with a placeholder.
func (s *State) SetHomontoVersion(v string) {
	if v != "" {
		s.HomontoVersion = v
	}
}

// SetFrameworkVersion records one framework's version at apply time.
func (s *State) SetFrameworkVersion(name, version string) {
	if s.FrameworkVersions == nil {
		s.FrameworkVersions = map[string]string{}
	}
	s.FrameworkVersions[name] = version
}

func newState() *State { return &State{Managed: map[string]map[string]Entry{}} }

func file(dir string) string { return filepath.Join(dir, "state.json") }

// namedFile is the per-repo state partition (ADR 0024 stage 2): a repo's
// adapter records its keys in <dir>/state.<name>.json so pruning and adoption
// stay scoped to that repository, never crossing into the config repo's
// state.json or another repo's partition.
func namedFile(dir, name string) string {
	return filepath.Join(dir, "state."+name+".json")
}

// LoadNamed reads <dir>/state.<name>.json, returning an empty State if the
// file is absent — the partition shape of Load.
func LoadNamed(dir, name string) (*State, error) {
	return loadAt(namedFile(dir, name))
}

// SaveNamed writes the state partition atomically, creating dir if needed —
// the partition shape of Save.
func (s *State) SaveNamed(dir, name string) error {
	return s.saveAt(namedFile(dir, name))
}

// Load reads <dir>/state.json, returning an empty State if the file is absent.
func Load(dir string) (*State, error) {
	return loadAt(file(dir))
}

func loadAt(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newState(), nil
	}
	if err != nil {
		return nil, err
	}
	s := newState()
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	if s.SchemaVersion > CurrentStateSchemaVersion {
		return nil, fmt.Errorf("state: unknown state schema version %d (this binary supports up to %d) — upgrade homonto: %w", s.SchemaVersion, CurrentStateSchemaVersion, schema.ErrTooNew)
	}
	if s.Managed == nil {
		s.Managed = map[string]map[string]Entry{}
	}
	return s, nil
}

// Save writes the state atomically (temp + fsync + rename), creating dir if
// needed. state.json is one of homonto's own control-plane files, so it is
// written no-follow (a symlinked target is refused, never followed) at 0600.
func (s *State) Save(dir string) error {
	return s.saveAt(file(dir))
}

func (s *State) saveAt(path string) error {
	s.SchemaVersion = CurrentStateSchemaVersion
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteControlPlane(path, data, 0o600)
}

// Set records the unresolved desired value and the applied-value hash for a key.
func (s *State) Set(tool, key, desired, appliedHash string) {
	if s.Managed[tool] == nil {
		s.Managed[tool] = map[string]Entry{}
	}
	s.Managed[tool][key] = Entry{Desired: desired, Applied: appliedHash}
}

// Get returns the recorded Entry for a key and whether it exists.
func (s *State) Get(tool, key string) (Entry, bool) {
	e, ok := s.Managed[tool][key]
	return e, ok
}

// Keys returns the sorted managed keys recorded for a tool.
func (s *State) Keys(tool string) []string {
	keys := make([]string, 0, len(s.Managed[tool]))
	for k := range s.Managed[tool] {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Delete drops the record for a key (after its on-disk value was pruned).
func (s *State) Delete(tool, key string) {
	delete(s.Managed[tool], key)
}
