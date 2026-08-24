package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// referenceDigest recomputes the expected digest the slow way so the test
// does not just call the implementation back.
func referenceDigest(domain string, data []byte) string {
	h := sha256.New()
	h.Write([]byte("homonto.v1." + domain + ":"))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func TestBytesIsDomainSeparatedSHA256(t *testing.T) {
	d := Bytes("config", []byte("payload"))
	if got, want := d.String(), referenceDigest("config", []byte("payload")); got != want {
		t.Fatalf("Bytes = %s, want %s", got, want)
	}
	if len(d.String()) != 64 {
		t.Fatalf("digest length = %d, want 64 hex chars", len(d.String()))
	}
}

// TestBytesSeparatesDomainsAndData: identical data under different domains,
// and different data under the same domain, must never collide.
func TestBytesSeparatesDomainsAndData(t *testing.T) {
	if Bytes("a", []byte("x")) == Bytes("b", []byte("x")) {
		t.Fatal("same data under different domains produced the same digest")
	}
	if Bytes("a", []byte("x")) == Bytes("a", []byte("y")) {
		t.Fatal("different data under the same domain produced the same digest")
	}
	if Bytes("a", []byte("x")) == Bytes("a", nil) {
		t.Fatal("data and empty data produced the same digest")
	}
	// The trailing colon terminates the domain: for colon-free domains,
	// payload bytes that look like a domain continuation must not alias a
	// longer domain. (Domains containing ':' are outside the contract.)
	if Bytes("ab", nil) == Bytes("a", []byte("b:")) {
		t.Fatal("domain/payload boundary is ambiguous")
	}
	if Bytes("ab", nil) == Bytes("a", []byte("b")) {
		t.Fatal("domain terminator lost: longer domain aliased domain plus data")
	}
}

func TestBytesIsDeterministic(t *testing.T) {
	data := []byte("stable input")
	if Bytes("work", data) != Bytes("work", data) {
		t.Fatal("same domain and data produced different digests")
	}
}

func TestReaderMatchesBytes(t *testing.T) {
	data := strings.NewReader("streamed payload")
	d, err := Reader("config", data)
	if err != nil {
		t.Fatalf("Reader: %v", err)
	}
	if want := Bytes("config", []byte("streamed payload")); d != want {
		t.Fatalf("Reader digest = %s, want Bytes digest %s", d, want)
	}
}

// TestReaderPropagatesReadError: a failing reader must surface its error, not
// return a digest of partial data.
func TestReaderPropagatesReadError(t *testing.T) {
	boom := errors.New("boom")
	_, err := Reader("config", failingReader{boom})
	if !errors.Is(err, boom) {
		t.Fatalf("Reader error = %v, want wrapped %v", err, boom)
	}
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

// TestCanonicalJSONIsDeterministicAcrossMapOrder: Go's json.Marshal sorts map
// keys, so equal values must fingerprint identically regardless of insertion
// order, and different values must differ.
func TestCanonicalJSONIsDeterministicAcrossMapOrder(t *testing.T) {
	m1 := map[string]string{"alpha": "1", "beta": "2", "gamma": "3"}
	m2 := map[string]string{"gamma": "3", "alpha": "1", "beta": "2"}
	d1, err := CanonicalJSON("cfg", m1)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	d2, err := CanonicalJSON("cfg", m2)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if d1 != d2 {
		t.Fatal("equal maps with different insertion order produced different digests")
	}
	m3 := map[string]string{"alpha": "1", "beta": "2", "gamma": "4"}
	d3, err := CanonicalJSON("cfg", m3)
	if err != nil {
		t.Fatalf("CanonicalJSON: %v", err)
	}
	if d1 == d3 {
		t.Fatal("different maps produced the same digest")
	}
}

func TestCanonicalJSONRejectsUnmarshalable(t *testing.T) {
	_, err := CanonicalJSON("cfg", func() {})
	if err == nil {
		t.Fatal("CanonicalJSON(func) = nil error, want marshal error")
	}
}

func TestParse(t *testing.T) {
	raw := referenceDigest("config", []byte("payload"))
	d, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%s) = %v, want nil", raw, err)
	}
	if d != Digest(raw) {
		t.Fatalf("Parse returned %s, want %s", d, raw)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Validate on parsed digest = %v", err)
	}

	invalid := map[string]string{
		"empty":       "",
		"odd-length":  strings.Repeat("a", 63),
		"too-long":    strings.Repeat("a", 65),
		"non-hex":     strings.ReplaceAll(raw, string(raw[0]), "z"),
		"uppercase":   strings.ToUpper(raw),
		"with-prefix": "sha256:" + raw,
	}
	for name, s := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(s); err == nil {
				t.Fatalf("Parse(%s) = nil error, want error", abbreviate(s))
			}
		})
	}
}

func TestDigestValidate(t *testing.T) {
	if err := Digest(referenceDigest("x", nil)).Validate(); err != nil {
		t.Fatalf("Validate on valid digest = %v", err)
	}
	if err := Digest(fmt.Sprintf("%063d", 0) + "g").Validate(); err == nil {
		t.Fatal("Validate on non-hex digest = nil, want error")
	}
}

func abbreviate(s string) string {
	if len(s) > 16 {
		return s[:16] + "..."
	}
	return s
}
