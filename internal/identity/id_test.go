package identity

import (
	"strings"
	"testing"
)

// TestNewTypedIDsProduceValidUUIDv4 covers every typed constructor: each must
// return a distinct canonical UUIDv4 string that round-trips through
// ValidateUUID.
func TestNewTypedIDsProduceValidUUIDv4(t *testing.T) {
	first, err := NewWorkspaceID()
	if err != nil {
		t.Fatalf("NewWorkspaceID: %v", err)
	}
	second, err := NewWorkspaceID()
	if err != nil {
		t.Fatalf("NewWorkspaceID: %v", err)
	}
	if first == second {
		t.Fatalf("two generated WorkspaceIDs are identical: %s", first)
	}
	if err := ValidateUUID(string(first)); err != nil {
		t.Fatalf("generated WorkspaceID %q invalid: %v", first, err)
	}

	pairs := []struct {
		name string
		gen  func() (string, error)
	}{
		{"WorkspaceID", func() (string, error) { id, err := NewWorkspaceID(); return string(id), err }},
		{"RepositoryID", func() (string, error) { id, err := NewRepositoryID(); return string(id), err }},
		{"WorkID", func() (string, error) { id, err := NewWorkID(); return string(id), err }},
		{"OperationID", func() (string, error) { id, err := NewOperationID(); return string(id), err }},
		{"ActionID", func() (string, error) { id, err := NewActionID(); return string(id), err }},
		{"ParallelGroupID", func() (string, error) { id, err := NewParallelGroupID(); return string(id), err }},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			id, err := p.gen()
			if err != nil {
				t.Fatalf("generate: %v", err)
			}
			if err := ValidateUUID(id); err != nil {
				t.Fatalf("generated id %q invalid: %v", id, err)
			}
		})
	}
}

func TestValidateUUID(t *testing.T) {
	valid := []string{
		"57e7d1d2-26b6-4a68-b3d2-91a4a3a30d63", // variant bits 10
		"00000000-0000-4000-8000-000000000000",
		"ffffffff-ffff-4fff-bfff-ffffffffffff",
	}
	for _, id := range valid {
		if err := ValidateUUID(id); err != nil {
			t.Errorf("ValidateUUID(%q) = %v, want nil", id, err)
		}
	}

	invalid := map[string]string{
		"empty":                "",
		"wrong-length":         "57e7d1d2-26b6-4a68-b3d2-91a4a3a30d6",
		"missing-dash":         "57e7d1d226b6-4a68-b3d2-91a4a3a30d63",
		"non-hex":              "57e7d1d2-26b6-4a6g-b3d2-91a4a3a30d63",
		"uppercase-hex":        "57E7D1D2-26B6-4A68-B3D2-91A4A3A30D63",
		"version-1":            "57e7d1d2-26b6-1a68-b3d2-91a4a3a30d63",
		"variant-0":            "57e7d1d2-26b6-4a68-03d2-91a4a3a30d63",
		"variant-111x":         "57e7d1d2-26b6-4a68-e3d2-91a4a3a30d63",
		"trailing-newline":     "57e7d1d2-26b6-4a68-b3d2-91a4a3a30d63\n",
		"braces-not-canonical": "{57e7d1d2-26b6-4a68-b3d2-91a4a3a30d63}",
		"urn-not-canonical":    "urn:uuid:57e7d1d2-26b6-4a68-b3d2-91a4a3a30d63",
	}
	for name, id := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateUUID(id); err == nil {
				t.Fatalf("ValidateUUID(%q) = nil, want error", id)
			}
		})
	}
}

// TestNewTokenRoundTrip checks the token contract: 32 random bytes in
// unpadded base64url (43 characters), unique per call, accepted by
// ValidateToken, and never containing padded or standard-alphabet characters.
func TestNewTokenRoundTrip(t *testing.T) {
	tok, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	other, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if tok == other {
		t.Fatalf("two generated tokens are identical: %s", tok)
	}
	s := string(tok)
	if len(s) != 43 {
		t.Fatalf("token length = %d, want 43 (32 bytes unpadded base64url)", len(s))
	}
	for _, bad := range []string{"+", "/", "="} {
		if strings.Contains(s, bad) {
			t.Fatalf("token %q contains %q; alphabet must be base64url without padding", s, bad)
		}
	}
	if err := ValidateToken(s); err != nil {
		t.Fatalf("ValidateToken(generated) = %v, want nil", err)
	}
}

func TestValidateToken(t *testing.T) {
	valid := []string{
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // 43 chars, 32 zero bytes
		"__________________________________________w", // 43 chars, all-ones bits
	}
	for _, tok := range valid {
		if err := ValidateToken(tok); err != nil {
			t.Errorf("ValidateToken(%q) = %v, want nil", tok, err)
		}
	}

	invalid := map[string]string{
		"empty":           "",
		"too-short":       "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"too-long":        "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"padded":          "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		"standard-base64": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA+",
		"non-canonical":   "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA_B", // decodes to 32 bytes but re-encodes differently
		"non-alphabet":    "*******************************************",
	}
	for name, tok := range invalid {
		t.Run(name, func(t *testing.T) {
			if err := ValidateToken(tok); err == nil {
				t.Fatalf("ValidateToken(%q) = nil, want error", tok)
			}
		})
	}
}
