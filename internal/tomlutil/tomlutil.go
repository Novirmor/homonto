// Package tomlutil is a TOML codec for the adapter projection core: it reads,
// sets, and deletes a value at a dotted key path in a TOML document while
// preserving unmanaged tables and keys, bridging TOML values to canonical JSON
// so state hashing stays format-independent. Comment preservation is a
// non-goal (a write normalizes the document), matching OpenCode's JSONC limit.
package tomlutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/jsonutil"
	toml "github.com/pelletier/go-toml/v2"
)

// StripBOM drops a leading UTF-8 byte-order mark. Some Windows editors save
// one; TOML parsers read it as a stray key character, so every homonto.toml
// reader strips it before decoding.
func StripBOM(doc []byte) []byte {
	return bytes.TrimPrefix(doc, []byte("\xef\xbb\xbf"))
}

// EnsureRoot normalizes an empty or whitespace-only document to an empty TOML
// object so a first Set has a root to write into.
func EnsureRoot(doc []byte) ([]byte, error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return []byte{}, nil
	}
	var m map[string]any
	if err := toml.Unmarshal(doc, &m); err != nil {
		return nil, fmt.Errorf("tomlutil: parse: %w", err)
	}
	return doc, nil
}

// Get returns the value at a dotted path encoded as canonical JSON, whether it
// is present, and a parse error if the document is malformed TOML. Distinguishing
// "broken" from "absent" matters at callers: a corrupted tool file interpreted
// as "key absent" would emit a destructive plan or a misleading drift report,
// so the parse failure is surfaced rather than collapsed into ok=false.
func Get(doc []byte, path string) (string, bool, error) {
	m, err := load(doc)
	if err != nil {
		return "", false, err
	}
	v, ok := getPath(m, splitPath(path))
	if !ok {
		return "", false, nil
	}
	enc, err := json.Marshal(v)
	if err != nil {
		return "", false, fmt.Errorf("tomlutil: encode value at %q: %w", path, err)
	}
	return Canonical(string(enc)), true, nil
}

// Set assigns jsonValue (a JSON-encoded value) at a dotted path, creating
// intermediate tables and preserving unmanaged content.
func Set(doc []byte, path, jsonValue string) ([]byte, error) {
	m, err := load(doc)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal([]byte(jsonValue), &v); err != nil {
		return nil, fmt.Errorf("tomlutil: decode value: %w", err)
	}
	segs := splitPath(path)
	if len(segs) == 0 {
		return nil, fmt.Errorf("tomlutil: empty path")
	}
	setPath(m, segs, v)
	return marshal(m)
}

// Delete removes the value at a dotted path and prunes any parent tables it
// leaves empty.
func Delete(doc []byte, path string) ([]byte, error) {
	m, err := load(doc)
	if err != nil {
		return nil, err
	}
	deletePath(m, splitPath(path))
	return marshal(m)
}

// Canonical renders a JSON-encoded value in a stable, key-sorted form so equal
// values hash identically regardless of key order or spacing. It reuses the JSON
// codec's canonicalization so TOML and JSON adapters compare/hash identically.
func Canonical(jsonValue string) string { return jsonutil.Canonical(jsonValue) }

func load(doc []byte) (map[string]any, error) {
	m := map[string]any{}
	if len(bytes.TrimSpace(doc)) == 0 {
		return m, nil
	}
	if err := toml.Unmarshal(doc, &m); err != nil {
		return nil, fmt.Errorf("tomlutil: parse: %w", err)
	}
	return m, nil
}

func marshal(m map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	enc.SetIndentTables(true)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("tomlutil: encode: %w", err)
	}
	return buf.Bytes(), nil
}

// splitPath splits a dotted TOML key path, honoring double-quoted segments so a
// key containing a literal dot (e.g. mcp_servers."github.copilot".command) is
// one segment. Quote a segment with QuoteSegment when building a path.
func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	var segs []string
	var cur strings.Builder
	inQuote := false
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == '.' && !inQuote:
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(c)
		}
	}
	segs = append(segs, cur.String())
	return segs
}

// QuoteSegment wraps a path segment in double quotes so a name containing dots
// (or other special characters) is treated as a single key.
func QuoteSegment(name string) string { return `"` + name + `"` }

func getPath(m map[string]any, segs []string) (any, bool) {
	cur := any(m)
	for _, s := range segs {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = mm[s]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func setPath(m map[string]any, segs []string, v any) {
	cur := m
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur[s].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[s] = next
		}
		cur = next
	}
	cur[segs[len(segs)-1]] = v
}

// deletePath removes the leaf and prunes emptied ancestor tables.
func deletePath(m map[string]any, segs []string) {
	if len(segs) == 0 {
		return
	}
	// Walk to the parent, tracking the chain so we can prune empties.
	chain := []map[string]any{m}
	cur := m
	for _, s := range segs[:len(segs)-1] {
		next, ok := cur[s].(map[string]any)
		if !ok {
			return // path does not exist; nothing to delete
		}
		chain = append(chain, next)
		cur = next
	}
	delete(cur, segs[len(segs)-1])
	// Prune empty parents from the leaf upward (but never the root map).
	for i := len(chain) - 1; i >= 1; i-- {
		if len(chain[i]) == 0 {
			delete(chain[i-1], segs[i-1])
		} else {
			break
		}
	}
}
