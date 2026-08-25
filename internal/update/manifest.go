// Package update fetches, verifies, stages, and activates a new Homonto
// binary — and rolls the whole thing back when activation does not
// validate.
//
// # Only update touches the network
//
// Ordinary Homonto processes perform no network access at all. This
// package is the single exception, it runs only when a human types
// `homonto update`, and it never checks for updates on its own.
//
// # Nothing is taken on the release's word
//
// A manifest is signed by roots compiled into THIS binary. The artifact's
// checksum is verified against the manifest. The candidate binary is
// interrogated for its own version, protocol, and schema — by running it,
// with no network — rather than believed from the manifest that shipped
// it. A downgrade, a channel substitution, and a schema the current state
// cannot migrate to are all refused before anything is replaced.
package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/update/trust"
)

// ManifestSchema is the only manifest schema this binary reads.
const ManifestSchema = 1

// Typed manifest errors.
var (
	// ErrMalformedManifest: the manifest is not the canonical shape.
	ErrMalformedManifest = errors.New("update: malformed release manifest")
	// ErrChannelMismatch: the manifest is for a different channel than the
	// one that was asked for.
	ErrChannelMismatch = errors.New("update: manifest is for a different channel")
	// ErrNoArtifact: the manifest carries no artifact for this platform.
	ErrNoArtifact = errors.New("update: manifest carries no artifact for this platform")
)

// Channel is a release channel.
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// Known reports whether c is a release channel.
func (c Channel) Known() bool { return c == ChannelStable || c == ChannelBeta }

// Artifact is one platform's binary.
type Artifact struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// URL must be https. A plain-http artifact is refused rather than
	// fetched-and-verified: the checksum would still catch tampering, but
	// an unencrypted fetch tells a network observer exactly which version
	// of what a machine is about to run.
	URL string `json:"url"`
	// SHA256 is the artifact's digest, lowercase hex.
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is a signed release description.
//
// Signatures are carried in the document but EXCLUDED from what is signed:
// a signature cannot cover itself. Canonical() produces exactly the bytes
// the roots sign, and ParseManifest returns them alongside the parsed
// value so a caller can never verify a different serialization than the
// one it read.
type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	Channel       Channel `json:"channel"`
	// Version is the release's version, e.g. "v0.9.0".
	Version string `json:"version"`
	// ProtocolVersion and StoreSchemaVersion are what the candidate
	// claims. They are checked against the candidate binary itself before
	// activation; here they let a manifest be rejected without a download.
	ProtocolVersion    int   `json:"protocol_version"`
	StoreSchemaVersion int64 `json:"store_schema_version"`
	// Artifacts are the per-platform binaries.
	Artifacts []Artifact `json:"artifacts"`
	// Roots, when present, is the signing-key set the candidate carries.
	// A rotation needs this AND a manifest signed by already-trusted
	// roots AND a candidate that actually carries them.
	Roots []trust.Root `json:"roots,omitempty"`
	// Signatures are over Canonical(), which omits this field.
	Signatures []trust.Signature `json:"signatures"`
}

// Canonical returns the exact bytes the signing roots sign: the manifest
// with its signatures removed, encoded deterministically.
func Canonical(m Manifest) ([]byte, error) {
	m.Signatures = nil
	sort.Slice(m.Artifacts, func(i, j int) bool {
		if m.Artifacts[i].OS != m.Artifacts[j].OS {
			return m.Artifacts[i].OS < m.Artifacts[j].OS
		}
		return m.Artifacts[i].Arch < m.Artifacts[j].Arch
	})
	sort.Slice(m.Roots, func(i, j int) bool { return m.Roots[i].ID < m.Roots[j].ID })
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("update: encode canonical manifest: %w", err)
	}
	return encoded, nil
}

// ParseManifest reads a manifest and returns it with the canonical bytes
// its signatures must be verified against.
//
// The decode is strict: an unknown field is a manifest this binary does
// not fully understand, and verifying a document you only partly parsed
// means signing off on the part you skipped.
func ParseManifest(r io.Reader) (Manifest, []byte, error) {
	var m Manifest
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, nil, fmt.Errorf("update: decode manifest: %w: %w", ErrMalformedManifest, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Manifest{}, nil, fmt.Errorf("update: trailing data after the manifest: %w", ErrMalformedManifest)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, nil, err
	}
	canonical, err := Canonical(m)
	if err != nil {
		return Manifest{}, nil, err
	}
	return m, canonical, nil
}

// Validate checks a manifest's shape.
func (m Manifest) Validate() error {
	if m.SchemaVersion != ManifestSchema {
		return fmt.Errorf("update: manifest schema %d, want exactly %d: %w",
			m.SchemaVersion, ManifestSchema, ErrMalformedManifest)
	}
	if !m.Channel.Known() {
		return fmt.Errorf("update: channel %q is not known: %w", m.Channel, ErrMalformedManifest)
	}
	if !strings.HasPrefix(m.Version, "v") {
		return fmt.Errorf("update: version %q must start with %q: %w", m.Version, "v", ErrMalformedManifest)
	}
	if m.ProtocolVersion < 1 {
		return fmt.Errorf("update: protocol_version %d must be at least 1: %w",
			m.ProtocolVersion, ErrMalformedManifest)
	}
	if m.StoreSchemaVersion < 1 {
		return fmt.Errorf("update: store_schema_version %d must be at least 1: %w",
			m.StoreSchemaVersion, ErrMalformedManifest)
	}
	if len(m.Artifacts) == 0 {
		return fmt.Errorf("update: manifest lists no artifact: %w", ErrMalformedManifest)
	}
	seen := map[string]bool{}
	for i, a := range m.Artifacts {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("update: artifacts[%d]: %w", i, err)
		}
		key := a.OS + "/" + a.Arch
		if seen[key] {
			return fmt.Errorf("update: two artifacts for %s: %w", key, ErrMalformedManifest)
		}
		seen[key] = true
	}
	for i, r := range m.Roots {
		if _, err := r.PublicKey(); err != nil {
			return fmt.Errorf("update: roots[%d]: %w", i, err)
		}
	}
	if len(m.Signatures) == 0 {
		return fmt.Errorf("update: manifest carries no signature: %w", ErrMalformedManifest)
	}
	return nil
}

// Validate checks one artifact.
func (a Artifact) Validate() error {
	if strings.TrimSpace(a.OS) == "" || strings.TrimSpace(a.Arch) == "" {
		return fmt.Errorf("update: an artifact must name its os and arch: %w", ErrMalformedManifest)
	}
	if !strings.HasPrefix(a.URL, "https://") {
		return fmt.Errorf("update: artifact url %q must be https: %w", a.URL, ErrMalformedManifest)
	}
	if len(a.SHA256) != 64 {
		return fmt.Errorf("update: artifact sha256 %q must be 64 hex characters: %w",
			a.SHA256, ErrMalformedManifest)
	}
	for i := 0; i < len(a.SHA256); i++ {
		c := a.SHA256[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return fmt.Errorf("update: artifact sha256 %q is not lowercase hex: %w",
				a.SHA256, ErrMalformedManifest)
		}
	}
	if a.Size <= 0 {
		return fmt.Errorf("update: artifact size %d must be positive: %w", a.Size, ErrMalformedManifest)
	}
	return nil
}

// ArtifactFor returns this platform's artifact.
func (m Manifest) ArtifactFor(goos, goarch string) (Artifact, error) {
	for _, a := range m.Artifacts {
		if a.OS == goos && a.Arch == goarch {
			return a, nil
		}
	}
	return Artifact{}, fmt.Errorf("update: %s/%s: %w", goos, goarch, ErrNoArtifact)
}

// LocalArtifact returns the artifact for the running platform.
func (m Manifest) LocalArtifact() (Artifact, error) {
	return m.ArtifactFor(runtime.GOOS, runtime.GOARCH)
}

// RequireChannel refuses a manifest served for a different channel than
// the one asked for.
//
// The channel is inside the SIGNED document, so substituting one is not a
// matter of swapping a URL: a beta manifest served at the stable address
// is caught here even though its signature is perfectly valid, because
// the signature attests to what it says rather than to where it was
// found.
func (m Manifest) RequireChannel(want Channel) error {
	if m.Channel == want {
		return nil
	}
	return fmt.Errorf("update: manifest is for the %s channel, not %s: %w",
		m.Channel, want, ErrChannelMismatch)
}

// Encode renders a manifest for publication.
func Encode(m Manifest) ([]byte, error) {
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("update: encode manifest: %w", err)
	}
	return append(encoded, '\n'), nil
}

// bytesReader is a tiny helper so callers can verify bytes they already
// hold without importing bytes at every call site.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// SameCanonical reports whether two manifests sign identically. It exists
// so a signing tool can prove the bytes it signed are the bytes it
// publishes.
func SameCanonical(a, b Manifest) (bool, error) {
	left, err := Canonical(a)
	if err != nil {
		return false, err
	}
	right, err := Canonical(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(left, right), nil
}
