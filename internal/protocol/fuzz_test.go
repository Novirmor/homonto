package protocol

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// FuzzDecodeSubmission feeds arbitrary bytes to the strict submission
// decoder. The contract: never panic; every error is one of the package's
// typed decode errors; every accepted envelope re-marshals.
func FuzzDecodeSubmission(f *testing.F) {
	sub := validSubmission(explorerPayload)
	valid, err := json.Marshal(sub)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(valid))
	f.Add(`{"protocol_version":1,"mood":"calm"}`)
	f.Add(`[]`)
	f.Add(string(valid) + " {}")
	f.Add("not json")
	f.Fuzz(func(t *testing.T, doc string) {
		got, err := DecodeSubmission(strings.NewReader(doc))
		if err != nil {
			typed := errors.Is(err, ErrInvalidJSON) ||
				errors.Is(err, ErrUnknownField) ||
				errors.Is(err, ErrTrailingData)
			if !typed {
				t.Fatalf("DecodeSubmission returned untyped error: %v", err)
			}
			return
		}
		if _, err := json.Marshal(got); err != nil {
			t.Fatalf("accepted envelope does not re-marshal: %v", err)
		}
	})
}
