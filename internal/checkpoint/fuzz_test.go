package checkpoint

import (
	"errors"
	"strings"
	"testing"
)

// FuzzDecode feeds arbitrary bytes to the strict checkpoint decoder. The
// contract: never panic; every decode error is one of the package's typed
// decode errors; every accepted input re-encodes byte-stably.
func FuzzDecode(f *testing.F) {
	cp := validCheckpoint()
	valid, err := Encode(cp)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(valid))
	f.Add(`{"schema_version":1,"surprise":1}`)
	f.Add(`{"schema_version":2}`)
	f.Add(`not json`)
	f.Add("")
	f.Add(string(valid) + " {}")
	f.Add(`{"schema_version":1,"workspace_id":"0a1b2c3d-4e5f-4a6b-8c7d-0e1f2a3b4c5d","config_fingerprint":"d83306dd5bd697696fba8805fe3c02bbb1d9484cc7748823884484c566e6bfee","members":null,"unresolved_gates":null,"handoff":{"state":"local","generation":1}}`)
	f.Fuzz(func(t *testing.T, doc string) {
		got, err := Decode(strings.NewReader(doc))
		if err != nil {
			typed := errors.Is(err, ErrInvalidJSON) ||
				errors.Is(err, ErrUnknownField) ||
				errors.Is(err, ErrTrailingData) ||
				errors.Is(err, ErrUnsupportedSchema)
			if !typed {
				t.Fatalf("Decode returned untyped error: %v", err)
			}
			return
		}
		b1, err := Encode(got)
		if err != nil {
			t.Fatalf("Encode(Decode(...)) failed on accepted input: %v", err)
		}
		b2, err := Encode(got)
		if err != nil {
			t.Fatalf("second Encode failed: %v", err)
		}
		if string(b1) != string(b2) {
			t.Fatalf("Encode is not byte-stable on fuzzed input:\n %s\n %s", b1, b2)
		}
	})
}
