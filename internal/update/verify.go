package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/update/trust"
)

// Typed verification errors.
var (
	// ErrChecksumMismatch: the downloaded artifact is not the one the
	// manifest describes.
	ErrChecksumMismatch = errors.New("update: artifact checksum does not match the manifest")
	// ErrDowngrade: the candidate is older than the running binary.
	ErrDowngrade = errors.New("update: candidate is not newer than the running binary")
	// ErrIncompatible: the candidate cannot work with this workspace's
	// state.
	ErrIncompatible = errors.New("update: candidate is incompatible with this workspace")
	// ErrCandidateMismatch: the candidate binary does not match what its
	// manifest said it would be.
	ErrCandidateMismatch = errors.New("update: candidate does not match its manifest")
)

// VerifiedRelease is a manifest whose signatures verified, together with
// the artifact this platform would install.
type VerifiedRelease struct {
	Manifest Manifest
	Artifact Artifact
	// Rotation is the root set the candidate must carry, when the manifest
	// proposes one.
	Rotation []trust.Root
}

// VerifyManifest checks a manifest's signatures, channel, and — when it
// proposes new signing roots — that this build's roots authorize the
// rotation.
func VerifyManifest(store trust.Store, raw []byte, want Channel) (VerifiedRelease, error) {
	manifest, canonical, err := ParseManifest(bytesReader(raw))
	if err != nil {
		return VerifiedRelease{}, err
	}
	if err := manifest.RequireChannel(want); err != nil {
		return VerifiedRelease{}, err
	}
	if err := store.Verify(canonical, manifest.Signatures); err != nil {
		return VerifiedRelease{}, err
	}
	if len(manifest.Roots) > 0 && !store.AuthorizesRotation(manifest.Roots) {
		return VerifiedRelease{}, store.RotationError(manifest.Roots)
	}
	artifact, err := manifest.LocalArtifact()
	if err != nil {
		return VerifiedRelease{}, err
	}
	return VerifiedRelease{Manifest: manifest, Artifact: artifact, Rotation: manifest.Roots}, nil
}

// VerifyChecksum checks downloaded bytes against the artifact.
func VerifyChecksum(a Artifact, body []byte) error {
	if int64(len(body)) != a.Size {
		return fmt.Errorf("update: artifact is %d bytes, manifest says %d: %w",
			len(body), a.Size, ErrChecksumMismatch)
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if got != a.SHA256 {
		return fmt.Errorf("update: artifact digest %s, manifest says %s: %w",
			got, a.SHA256, ErrChecksumMismatch)
	}
	return nil
}

// CandidateMetadata is what a candidate binary says about itself when
// asked. It comes from RUNNING the candidate rather than from the manifest
// that shipped it: a manifest describes what a release is supposed to be,
// and the point of this check is to catch a release that is not.
type CandidateMetadata struct {
	Version            string `json:"version"`
	ProtocolVersion    int    `json:"protocol_version"`
	StoreSchemaVersion int64  `json:"store_schema_version"`
	// TrustRoots is the signing-key set the candidate itself carries. A
	// rotation is only real when the candidate actually has the new keys.
	TrustRoots []trust.Root `json:"trust_roots,omitempty"`
	// HostAssets is a digest of the host wrappers the candidate would
	// install, so an update that changes them is visible before it lands.
	HostAssets string `json:"host_assets,omitempty"`
}

// LocalMetadata describes the running binary.
func LocalMetadata() CandidateMetadata {
	return CandidateMetadata{
		Version:            buildinfo.Resolve(buildinfo.DevVersion, buildinfo.DevVersion),
		ProtocolVersion:    protocol.CurrentVersion,
		StoreSchemaVersion: store.SchemaVersion(),
		TrustRoots:         trust.Compiled().Roots,
	}
}

// Compatibility is the verdict on whether a candidate may replace this
// binary against this workspace's state.
type Compatibility struct {
	Current   CandidateMetadata
	Candidate CandidateMetadata
	// Reasons explains a refusal.
	Reasons []string
}

// OK reports whether the candidate may be activated.
func (c Compatibility) OK() bool { return len(c.Reasons) == 0 }

// CheckCompatibility decides whether a candidate may replace the running
// binary.
//
// Four rules, and each exists because breaking it strands a workspace:
//
//   - A candidate must be NEWER. `homonto update` moves forward; installing
//     an older binary over a migrated database is how a workspace becomes
//     unopenable by the only binary present.
//   - Its protocol must not go backwards, or a host mid-workflow starts
//     speaking a dialect the binary no longer answers.
//   - Its store schema must not go backwards, because the migration ledger
//     refuses a database recorded at a newer version than the binary knows
//     — which is the correct behaviour and a terrible surprise.
//   - When the manifest proposed a rotation, the candidate must actually
//     carry those roots. A manifest can claim anything; the binary is what
//     will have to verify the NEXT release.
func CheckCompatibility(current, candidate CandidateMetadata, rotation []trust.Root) Compatibility {
	c := Compatibility{Current: current, Candidate: candidate}
	if !newerVersion(current.Version, candidate.Version) {
		c.Reasons = append(c.Reasons, fmt.Sprintf(
			"the candidate is %s and this binary is %s; update only moves forward",
			candidate.Version, current.Version))
	}
	if candidate.ProtocolVersion < current.ProtocolVersion {
		c.Reasons = append(c.Reasons, fmt.Sprintf(
			"the candidate speaks protocol %d and this binary speaks %d; a host mid-workflow "+
				"would be talking to a binary that no longer answers it",
			candidate.ProtocolVersion, current.ProtocolVersion))
	}
	if candidate.StoreSchemaVersion < current.StoreSchemaVersion {
		c.Reasons = append(c.Reasons, fmt.Sprintf(
			"the candidate knows schema v%d and this workspace is at v%d; it would refuse to "+
				"open the database it inherited",
			candidate.StoreSchemaVersion, current.StoreSchemaVersion))
	}
	if len(rotation) > 0 && !carriesRoots(candidate.TrustRoots, rotation) {
		c.Reasons = append(c.Reasons,
			"the manifest proposes new signing roots that the candidate binary does not carry")
	}
	return c
}

// carriesRoots reports whether have contains every root in want.
func carriesRoots(have, want []trust.Root) bool {
	index := make(map[string]string, len(have))
	for _, r := range have {
		index[r.ID] = r.Key
	}
	for _, r := range want {
		if index[r.ID] != r.Key {
			return false
		}
	}
	return true
}

// newerVersion reports whether candidate is strictly newer than current.
// An unparsable current version — a development build — is treated as
// older than any release, so a dev binary can always take a real one.
func newerVersion(current, candidate string) bool {
	cur, curOK := parseVersion(current)
	next, nextOK := parseVersion(candidate)
	if !nextOK {
		return false
	}
	if !curOK {
		return true
	}
	for i := 0; i < 3; i++ {
		if next[i] != cur[i] {
			return next[i] > cur[i]
		}
	}
	return false
}

// parseVersion reads "vMAJOR.MINOR.PATCH", ignoring any pre-release
// suffix.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	s := v
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	if idx := indexAny(s, "-+"); idx >= 0 {
		s = s[:idx]
	}
	parts := splitN(s, '.', 3)
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, ok := atoi(p)
		if !ok {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func indexAny(s, chars string) int {
	for i := 0; i < len(s); i++ {
		for j := 0; j < len(chars); j++ {
			if s[i] == chars[j] {
				return i
			}
		}
	}
	return -1
}

func splitN(s string, sep byte, n int) []string {
	var out []string
	start := 0
	for i := 0; i < len(s) && len(out) < n-1; i++ {
		if s[i] == sep {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, true
}
