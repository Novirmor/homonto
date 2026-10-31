// Package jsoncodec is the shared structproj.Codec for JSON config documents.
// Both JSON adapters (claude, opencode) project their structured-document
// managed keys through it, so the diff/write/observe control flow lives once
// in internal/adapter/structproj rather than being re-implemented per adapter.
// It delegates to internal/jsonutil, matching the adapters' prior direct use of
// those primitives byte-for-byte.
package jsoncodec

import (
	"bytes"
	"encoding/json"

	"github.com/Novirmor/homonto/internal/adapter/structproj"
	"github.com/Novirmor/homonto/internal/jsonutil"
)

// Codec implements the structproj.Codec contract.
var _ structproj.Codec = Codec{}

// Codec implements structproj.Codec over JSON documents. It is a zero-value
// stateless type; construct with `var c Codec` or `Codec{}`.
type Codec struct{}

// EnsureRoot normalizes an empty/whitespace document to an object root and
// refuses a non-object root (a keyed write into an array/scalar would corrupt
// it) — mirroring the adapters' readStandardized + jsonutil.ObjectRoot guard.
func (Codec) EnsureRoot(doc []byte) ([]byte, error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return []byte("{}"), nil
	}
	if err := jsonutil.ObjectRoot(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// Get returns the canonical JSON value at path and whether it is present. The
// value is canonicalized so it compares and hashes identically to the value
// Apply records (the noop identity: Applied == hash(Canonical(disk))). It never
// returns a parse error: callers standardize JSONC → JSON before this codec
// runs (see opencode/claude readStandardized), so by the time bytes reach Get
// the document is valid JSON; gjson returns ok=false for a missing path rather
// than a parse failure, and that distinction is preserved.
func (Codec) Get(doc []byte, path string) (string, bool, error) {
	raw, ok := jsonutil.GetJSON(doc, path)
	if !ok {
		return "", false, nil
	}
	return jsonutil.Canonical(raw), true, nil
}

// Set assigns a JSON-encoded value at path, preserving unmanaged content. The
// value string is parsed back to a Go value before jsonutil.SetJSON so the
// written bytes match the adapters' prior SetJSON(doc, path, resolvedValue).
func (Codec) Set(doc []byte, path, jsonValue string) ([]byte, error) {
	var v any
	if err := json.Unmarshal([]byte(jsonValue), &v); err != nil {
		return nil, err
	}
	return jsonutil.SetJSON(doc, path, v)
}

// Delete removes the value at path, pruning parents it empties.
func (Codec) Delete(doc []byte, path string) ([]byte, error) {
	return jsonutil.DeleteJSON(doc, path)
}

// Canonical renders a JSON-encoded value in a stable, key-order-independent
// form for compare/hash.
func (Codec) Canonical(jsonValue string) string { return jsonutil.Canonical(jsonValue) }
