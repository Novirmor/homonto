// Package structproj is the adapter contract: a format-agnostic managed-key
// projection engine. It owns the plan/apply/observe control flow that Claude and
// OpenCode otherwise each re-implement — diffing desired values against disk and
// recorded state to emit create/update/delete/noop/adopt changes, writing only
// managed keys while preserving unmanaged content, and re-hashing recorded keys
// for drift. A new adapter supplies only a Codec (its file format), a state-key
// prefix, and a key→document-path mapping.
package structproj

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
)

// Codec abstracts a structured config document of some format (JSON, TOML, …).
// Values crossing this boundary are JSON-encoded strings so state hashing stays
// format-independent.
type Codec interface {
	// EnsureRoot normalizes an empty/whitespace document to a writable root.
	EnsureRoot(doc []byte) ([]byte, error)
	// Get returns the canonical JSON value at a path, whether it is present,
	// and a parse error if the document is malformed. A parse failure must NOT
	// be folded into ok=false — callers must be able to tell a missing key
	// (create/adopt path) from a corrupted file (abort).
	Get(doc []byte, path string) (string, bool, error)
	// Set assigns a JSON-encoded value at a path, preserving unmanaged content.
	Set(doc []byte, path, jsonValue string) ([]byte, error)
	// Delete removes the value at a path, pruning parents it empties.
	Delete(doc []byte, path string) ([]byte, error)
	// Canonical renders a JSON-encoded value in a stable form for compare/hash.
	Canonical(jsonValue string) string
}

// PathFor maps a full state key (prefix+name) to the codec document path.
type PathFor func(stateKey string) string

// Project diffs desired (state-key → JSON value) against the on-disk document
// and recorded state, producing the change list for one managed namespace. It
// reproduces the built-in adapters' semantics exactly, including secret-safe
// redaction of Old. A codec parse error aborts the projection rather than being
// folded into "key absent" — emitting a destructive plan against a corrupted
// file would be worse than failing loud.
func Project(tool, prefix string, desired map[string]string, disk []byte, st *state.State, codec Codec, pathFor PathFor) ([]adapter.Change, error) {
	var changes []adapter.Change
	declared := make(map[string]bool, len(desired))
	for key, want := range desired {
		declared[key] = true
		diskVal, hasDisk, err := codec.Get(disk, pathFor(key))
		if err != nil {
			return nil, err
		}
		e, inState := st.Get(tool, key)
		switch {
		case !hasDisk:
			changes = append(changes, adapter.Change{Action: "create", Key: key, New: want, Cause: adapter.CauseDeclare})
		case !secret.ContainsRef(want):
			if diskVal == codec.Canonical(want) {
				if inState && e.Applied == secret.Hash(diskVal) {
					changes = append(changes, adapter.Change{Action: "noop", Key: key})
				} else {
					changes = append(changes, adapter.Change{Action: "adopt", Key: key, New: want, Cause: adapter.CauseAdopt})
				}
			} else {
				old := diskVal
				// Never expose an on-disk value of unknown or secret provenance.
				if !inState || secret.ContainsRef(e.Desired) {
					old = adapter.SecretRedaction
				}
				changes = append(changes, adapter.Change{Action: "update", Key: key, Old: old, New: want, Cause: adapter.CauseUpdate})
			}
		default: // secret-bearing desired value: never read/expose the on-disk value
			if inState && e.Desired == want && e.Applied == secret.Hash(diskVal) {
				changes = append(changes, adapter.Change{Action: "noop", Key: key})
			} else {
				changes = append(changes, adapter.Change{Action: "update", Key: key, Old: adapter.SecretRedaction, New: want, Cause: adapter.CauseDriftFix})
			}
		}
	}
	// Delete recorded keys in this namespace no longer declared.
	for _, k := range st.Keys(tool) {
		if !strings.HasPrefix(k, prefix) || declared[k] {
			continue
		}
		changes = append(changes, adapter.Change{Action: "delete", Key: k, Old: adapter.SecretRedaction, Cause: adapter.CauseRemove})
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Key < changes[j].Key })
	return changes, nil
}

// Apply writes the changes into the document (managed keys only), records state,
// and reports whether the document changed. noop/adopt are state-only and leave
// the document byte-for-byte untouched. Secrets are resolved via res.
func Apply(tool, prefix string, changes []adapter.Change, disk []byte, codec Codec, res *secret.Resolver, st *state.State, pathFor PathFor) ([]byte, bool, error) {
	// EnsureRoot once (not per change) only if there is document-mutating work.
	doc := disk
	rooted := false
	ensure := func() error {
		if rooted {
			return nil
		}
		d, err := codec.EnsureRoot(doc)
		if err != nil {
			return err
		}
		doc = d
		rooted = true
		return nil
	}
	for _, c := range changes {
		switch c.Action {
		case "noop":
			continue
		case "adopt":
			val, err := res.ResolveJSON(c.New)
			if err != nil {
				return nil, false, err
			}
			st.Set(tool, c.Key, c.New, secret.Hash(codec.Canonical(MustJSON(val))))
		case "delete":
			if err := ensure(); err != nil {
				return nil, false, err
			}
			var err error
			if doc, err = codec.Delete(doc, pathFor(c.Key)); err != nil {
				return nil, false, err
			}
			st.Delete(tool, c.Key)
		default: // create | update
			val, err := res.ResolveJSON(c.New)
			if err != nil {
				return nil, false, err
			}
			if err := ensure(); err != nil {
				return nil, false, err
			}
			if doc, err = codec.Set(doc, pathFor(c.Key), MustJSON(val)); err != nil {
				return nil, false, err
			}
			st.Set(tool, c.Key, c.New, secret.Hash(codec.Canonical(MustJSON(val))))
		}
	}
	// Only report a change (and thus a write) when the document actually differs
	// from disk — so a delete against an absent/empty file never recreates it.
	changed := !bytes.Equal(doc, disk)
	return doc, changed, nil
}

// Observe re-hashes each recorded key of this namespace still present on disk,
// the same way Apply stored Entry.Applied, so an unchanged key hashes back to
// its recorded value. Keys absent from disk are omitted. A codec parse error
// aborts observation rather than silently treating every key as absent (which
// would report false drift).
func Observe(tool, prefix string, disk []byte, st *state.State, codec Codec, pathFor PathFor) (map[string]string, error) {
	out := map[string]string{}
	for _, k := range st.Keys(tool) {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		v, ok, err := codec.Get(disk, pathFor(k))
		if err != nil {
			return nil, err
		}
		if ok {
			out[k] = secret.Hash(v)
		}
	}
	return out, nil
}

// MustJSON marshals a value to a JSON string ("null" on error). Exported so
// adapters build desired values without re-implementing it.
func MustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}
