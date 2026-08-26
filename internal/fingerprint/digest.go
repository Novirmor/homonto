// Package fingerprint produces domain-separated SHA-256 digests that
// identify configuration, artifacts, and evidence. Every digest hashes a
// "homonto.v1.<domain>:" prefix before the payload, so equal bytes under
// different domains never alias. Domains are internal constants and must not
// contain ':' — the terminating colon is what separates a colon-free domain
// from its payload unambiguously.
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// Digest is a SHA-256 digest in canonical lowercase hex (64 characters).
type Digest string

// prefixFormat is written before the payload; the trailing colon ends the
// domain so "a" + ":" + "b:c" cannot alias domain "a:b" with payload "c".
const prefixFormat = "homonto.v1.%s:"

// Bytes returns the domain-separated digest of data. The domain must not
// contain ':'.
func Bytes(domain string, data []byte) Digest {
	h := sha256.New()
	fmt.Fprintf(h, prefixFormat, domain)
	h.Write(data)
	return Digest(hex.EncodeToString(h.Sum(nil)))
}

// CanonicalJSON marshals v with encoding/json — deterministic for a given
// value because struct fields keep declaration order and map keys are
// sorted — and digests the encoding under domain.
func CanonicalJSON(domain string, v any) (Digest, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("fingerprint: marshal canonical JSON: %w", err)
	}
	return Bytes(domain, b), nil
}

// Parse validates s as a canonical digest (64 lowercase hex characters) and
// returns it as a Digest. Uppercase or otherwise non-canonical spellings are
// rejected so digests compare equal only in their generated form.
func Parse(s string) (Digest, error) {
	d := Digest(s)
	if err := d.Validate(); err != nil {
		return "", err
	}
	return d, nil
}

// Validate reports whether d is a canonical 64-character lowercase hex
// SHA-256 digest.
func (d Digest) Validate() error {
	if len(d) != sha256.Size*2 {
		return fmt.Errorf("fingerprint: digest must be %d hex characters, got %d", sha256.Size*2, len(d))
	}
	for i := 0; i < len(d); i++ {
		if !isLowerHex(d[i]) {
			return fmt.Errorf("fingerprint: digest character %q at position %d is not lowercase hex", string(d[i]), i)
		}
	}
	return nil
}

// String returns the raw lowercase hex digest.
func (d Digest) String() string { return string(d) }

func isLowerHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f'
}
