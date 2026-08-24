package checkpoint

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Typed decode errors. Wrap with field context via fmt.Errorf("%w", ...) so
// callers can branch with errors.Is; messages always name the offending value.
var (
	// ErrInvalidJSON wraps syntax and type errors from the JSON decoder.
	ErrInvalidJSON = errors.New("checkpoint: invalid JSON")
	// ErrUnknownField names a JSON key the schema does not define.
	ErrUnknownField = errors.New("checkpoint: unknown field")
	// ErrTrailingData: input continues after the first JSON value.
	ErrTrailingData = errors.New("checkpoint: trailing data after JSON value")
	// ErrUnsupportedSchema: schema_version is not exactly 1.
	ErrUnsupportedSchema = errors.New("checkpoint: unsupported schema_version")
)

// Encode marshals the canonical form of cp: members sorted by repository
// ID, unresolved gates sorted, nil slices as empty arrays. Encode is
// byte-stable — the same value always encodes to identical bytes, and
// values differing only in slice order encode identically. The receiver is
// never mutated.
func Encode(cp Checkpoint) ([]byte, error) {
	b, err := json.Marshal(canonical(cp))
	if err != nil {
		return nil, fmt.Errorf("checkpoint: marshal canonical form: %w", err)
	}
	return b, nil
}

// Decode strictly parses one checkpoint: unknown fields are rejected,
// trailing JSON values are rejected, and schema_version must be exactly
// CurrentSchemaVersion. Slice order in the input is not required to be
// canonical; re-encoding normalizes it.
func Decode(r io.Reader) (Checkpoint, error) {
	var cp Checkpoint
	if err := decodeStrict(r, &cp); err != nil {
		return Checkpoint{}, err
	}
	if cp.SchemaVersion != CurrentSchemaVersion {
		return Checkpoint{}, fmt.Errorf("checkpoint: schema_version %d, want exactly %d: %w",
			cp.SchemaVersion, CurrentSchemaVersion, ErrUnsupportedSchema)
	}
	return cp, nil
}

// decodeStrict decodes exactly one JSON value into v with unknown fields
// disallowed and any trailing content rejected.
func decodeStrict(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if isUnknownFieldError(err) {
			return fmt.Errorf("%w: %w", ErrUnknownField, err)
		}
		return fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	}
	// A following token (including a bare null) means the input did not
	// end with the first value; only io.EOF is a clean end.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %w", ErrTrailingData, errIfNil(err))
	}
	return nil
}

// errIfNil keeps the wrapped error non-nil for %w even when Token returned
// (nil, nil), which happens for a trailing JSON null.
func errIfNil(err error) error {
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
}

// isUnknownFieldError reports whether err is encoding/json's
// DisallowUnknownFields rejection. The decoder returns it as a plain error
// whose message is stable across supported Go toolchains.
func isUnknownFieldError(err error) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte("unknown field"))
}
