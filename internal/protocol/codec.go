package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
)

// Typed decode errors. Wrap with context via fmt.Errorf("%w", ...) so
// callers can branch with errors.Is.
var (
	// ErrInvalidJSON wraps syntax and type errors from the JSON decoder.
	ErrInvalidJSON = errors.New("protocol: invalid JSON")
	// ErrUnknownField names a JSON key the schema does not define.
	ErrUnknownField = errors.New("protocol: unknown field")
	// ErrTrailingData: input continues after the first JSON value.
	ErrTrailingData = errors.New("protocol: trailing data after JSON value")
)

// EncodeNextResponse validates resp and renders it deterministically with
// two-space indentation and a trailing newline: the shape `homonto next
// --json` prints and the goldens pin. A nil action list encodes as an
// explicitly empty array, never an omitted key.
func EncodeNextResponse(resp NextResponse) ([]byte, error) {
	if err := resp.Validate(); err != nil {
		return nil, err
	}
	if resp.Actions == nil {
		resp.Actions = []Action{}
	}
	for i := range resp.Actions {
		if resp.Actions[i].Dependencies == nil {
			resp.Actions[i].Dependencies = []identity.ActionID{}
		}
		if resp.Actions[i].InputFingerprints == nil {
			resp.Actions[i].InputFingerprints = []fingerprint.Digest{}
		}
		if resp.Actions[i].WriteScope.Paths == nil {
			resp.Actions[i].WriteScope.Paths = []string{}
		}
	}
	b, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("protocol: marshal response: %w", err)
	}
	return append(b, '\n'), nil
}

// DecodeSubmission strictly parses a report submission envelope. The
// report payload stays raw; DecodeReport selects and validates the role
// schema. Semantic checks against an action live in ValidateSubmission.
func DecodeSubmission(r io.Reader) (ReportSubmission, error) {
	var sub ReportSubmission
	if err := decodeStrict(r, &sub); err != nil {
		return ReportSubmission{}, err
	}
	return sub, nil
}

// DecodeDecisionSubmission strictly parses a decision submission
// envelope. Checks against the answered schema are the engines' work
// against the persisted decision contract.
func DecodeDecisionSubmission(r io.Reader) (DecisionSubmission, error) {
	var sub DecisionSubmission
	if err := decodeStrict(r, &sub); err != nil {
		return DecisionSubmission{}, err
	}
	return sub, nil
}

// DecodeGuardRequest strictly parses and validates a guard request from a
// host write hook.
func DecodeGuardRequest(r io.Reader) (GuardRequest, error) {
	var req GuardRequest
	if err := decodeStrict(r, &req); err != nil {
		return GuardRequest{}, err
	}
	if err := req.Validate(); err != nil {
		return GuardRequest{}, err
	}
	return req, nil
}

// EncodeGuardDecision renders a guard decision deterministically for the
// host hook's stdout.
func EncodeGuardDecision(d GuardDecision) ([]byte, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("protocol: marshal guard decision: %w", err)
	}
	return b, nil
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
	if _, err := dec.Token(); !isEOF(err) {
		return fmt.Errorf("%w: %w", ErrTrailingData, errIfNil(err))
	}
	return nil
}

// isEOF reports a clean end of stream.
func isEOF(err error) bool { return errors.Is(err, io.EOF) }

// errIfNil keeps the wrapped error non-nil for %w even when Token
// returned (nil, nil), which happens for a trailing JSON null.
func errIfNil(err error) error {
	if err == nil {
		return errors.New("extra JSON value")
	}
	return err
}

// isUnknownFieldError reports whether err is encoding/json's
// DisallowUnknownFields rejection. The decoder returns it as a plain
// error whose message is stable across supported Go toolchains.
func isUnknownFieldError(err error) bool {
	return err != nil && bytes.Contains([]byte(err.Error()), []byte("unknown field"))
}

// EncodeProbeResponse renders a probe payload for a host to read.
func EncodeProbeResponse(resp ProbeResponse) ([]byte, error) {
	if err := resp.Validate(); err != nil {
		return nil, err
	}
	encoded, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("protocol: encode probe response: %w", err)
	}
	return encoded, nil
}
