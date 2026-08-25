package main

import (
	"bytes"
	"encoding/json"
)

// newStrictDecoder rejects unknown fields, so a manifest with a typo in a
// key is caught at signing time rather than at every client that fetches
// it.
func newStrictDecoder(body []byte) *json.Decoder {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	return dec
}
