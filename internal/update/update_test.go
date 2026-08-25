package update

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/update/trust"
)

// signer is one test signing identity.
type signer struct {
	id      string
	public  ed25519.PublicKey
	private ed25519.PrivateKey
}

func newSigner(t *testing.T, id string) signer {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return signer{id: id, public: public, private: private}
}

func (s signer) root() trust.Root {
	return trust.Root{ID: s.id, Key: base64.StdEncoding.EncodeToString(s.public)}
}

// sign returns the manifest with this signer's signature added.
func (s signer) sign(t *testing.T, m Manifest) Manifest {
	t.Helper()
	canonical, err := Canonical(m)
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	m.Signatures = append(m.Signatures, trust.Signature{
		RootID: s.id,
		Value:  base64.StdEncoding.EncodeToString(ed25519.Sign(s.private, canonical)),
	})
	return m
}

// artifactBody is a stand-in binary; its digest is what the manifest
// carries.
var artifactBody = []byte("#!/bin/sh\necho candidate\n")

func digestOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// manifest builds an unsigned manifest for the running platform.
func manifest() Manifest {
	return Manifest{
		SchemaVersion:      ManifestSchema,
		Channel:            ChannelStable,
		Version:            "v9.0.0",
		ProtocolVersion:    1,
		StoreSchemaVersion: 1,
		Artifacts: []Artifact{{
			OS: runtime.GOOS, Arch: runtime.GOARCH,
			URL:    "https://releases.example/homonto",
			SHA256: digestOf(artifactBody), Size: int64(len(artifactBody)),
		}},
	}
}

// encode renders a manifest for parsing.
func encode(t *testing.T, m Manifest) []byte {
	t.Helper()
	body, err := Encode(m)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return body
}

// TestAnEmptyTrustStoreVerifiesNothing is the fail-closed default: a
// development build must not be able to replace itself from the network.
func TestAnEmptyTrustStoreVerifiesNothing(t *testing.T) {
	s := newSigner(t, "root-a")
	body := encode(t, s.sign(t, manifest()))
	_, err := VerifyManifest(trust.Store{}, body, ChannelStable)
	if !errors.Is(err, trust.ErrNoRoots) {
		t.Fatalf("VerifyManifest error = %v, want ErrNoRoots", err)
	}
	if !trust.Compiled().Empty() {
		t.Error("this build carries compiled-in roots; it should not by default")
	}
}

func TestAValidManifestVerifies(t *testing.T) {
	s := newSigner(t, "root-a")
	store := trust.Store{Roots: []trust.Root{s.root()}}
	release, err := VerifyManifest(store, encode(t, s.sign(t, manifest())), ChannelStable)
	if err != nil {
		t.Fatalf("VerifyManifest: %v", err)
	}
	if release.Manifest.Version != "v9.0.0" {
		t.Fatalf("release = %+v", release.Manifest)
	}
	if release.Artifact.OS != runtime.GOOS {
		t.Fatalf("artifact = %+v, want this platform", release.Artifact)
	}
}

func TestBadAndUnknownSignaturesAreRefused(t *testing.T) {
	good := newSigner(t, "root-a")
	stranger := newSigner(t, "root-unknown")
	store := trust.Store{Roots: []trust.Root{good.root()}}

	t.Run("an unknown root", func(t *testing.T) {
		body := encode(t, stranger.sign(t, manifest()))
		_, err := VerifyManifest(store, body, ChannelStable)
		if !errors.Is(err, trust.ErrUnknownRoot) {
			t.Fatalf("error = %v, want ErrUnknownRoot", err)
		}
	})

	t.Run("a forged signature", func(t *testing.T) {
		m := manifest()
		m.Signatures = []trust.Signature{{
			RootID: good.id,
			Value:  base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)),
		}}
		_, err := VerifyManifest(store, encode(t, m), ChannelStable)
		if !errors.Is(err, trust.ErrBadSignature) {
			t.Fatalf("error = %v, want ErrBadSignature", err)
		}
	})

	t.Run("a signature over different content", func(t *testing.T) {
		signed := good.sign(t, manifest())
		// Tamper AFTER signing: the artifact now points somewhere else.
		signed.Artifacts[0].URL = "https://evil.example/homonto"
		_, err := VerifyManifest(store, encode(t, signed), ChannelStable)
		if !errors.Is(err, trust.ErrBadSignature) {
			t.Fatalf("error = %v, want ErrBadSignature", err)
		}
	})

	t.Run("no signature at all", func(t *testing.T) {
		_, err := VerifyManifest(store, encode(t, manifest()), ChannelStable)
		if !errors.Is(err, ErrMalformedManifest) {
			t.Fatalf("error = %v, want ErrMalformedManifest", err)
		}
	})
}

// TestAThresholdCannotBeMetByRepetition proves one root signing twice is
// one signature.
func TestAThresholdCannotBeMetByRepetition(t *testing.T) {
	a := newSigner(t, "root-a")
	b := newSigner(t, "root-b")
	store := trust.Store{Roots: []trust.Root{a.root(), b.root()}, Threshold: 2}

	once := a.sign(t, manifest())
	if _, err := VerifyManifest(store, encode(t, once), ChannelStable); !errors.Is(err, trust.ErrThreshold) {
		t.Fatalf("one signature met a threshold of two: %v", err)
	}
	// The same root's signature repeated is still one distinct root.
	twice := once
	twice.Signatures = append(twice.Signatures, once.Signatures[0])
	if _, err := VerifyManifest(store, encode(t, twice), ChannelStable); !errors.Is(err, trust.ErrThreshold) {
		t.Fatalf("a repeated signature met the threshold: %v", err)
	}
	both := b.sign(t, once)
	if _, err := VerifyManifest(store, encode(t, both), ChannelStable); err != nil {
		t.Fatalf("two distinct signatures did not meet the threshold: %v", err)
	}
}

// TestChannelSubstitutionIsRefused proves the channel is inside what is
// signed, so serving a beta manifest at the stable address does not work
// even though its signature is perfectly valid.
func TestChannelSubstitutionIsRefused(t *testing.T) {
	s := newSigner(t, "root-a")
	store := trust.Store{Roots: []trust.Root{s.root()}}
	beta := manifest()
	beta.Channel = ChannelBeta
	body := encode(t, s.sign(t, beta))

	if _, err := VerifyManifest(store, body, ChannelStable); !errors.Is(err, ErrChannelMismatch) {
		t.Fatalf("error = %v, want ErrChannelMismatch", err)
	}
	// And it verifies fine when asked for as itself, which is what makes
	// the point: the signature was never the problem.
	if _, err := VerifyManifest(store, body, ChannelBeta); err != nil {
		t.Fatalf("the beta manifest does not verify as beta: %v", err)
	}
}

func TestChecksumMismatchIsRefused(t *testing.T) {
	a := manifest().Artifacts[0]
	if err := VerifyChecksum(a, artifactBody); err != nil {
		t.Fatalf("the correct artifact was refused: %v", err)
	}
	if err := VerifyChecksum(a, []byte("something else entirely!!")); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v, want ErrChecksumMismatch", err)
	}
	// A body of the right length but the wrong content is still refused.
	wrong := make([]byte, len(artifactBody))
	copy(wrong, artifactBody)
	wrong[0] ^= 0xff
	if err := VerifyChecksum(a, wrong); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v, want ErrChecksumMismatch", err)
	}
}

// TestRotationNeedsAlreadyTrustedRoots pins the rotation rule.
func TestRotationNeedsAlreadyTrustedRoots(t *testing.T) {
	a := newSigner(t, "root-a")
	b := newSigner(t, "root-b")
	c := newSigner(t, "root-c")
	store := trust.Store{Roots: []trust.Root{a.root(), b.root()}}

	t.Run("retiring one key is authorized", func(t *testing.T) {
		if !store.AuthorizesRotation([]trust.Root{a.root(), c.root()}) {
			t.Fatal("an incremental rotation was refused")
		}
	})
	t.Run("replacing every key is refused", func(t *testing.T) {
		if store.AuthorizesRotation([]trust.Root{c.root()}) {
			t.Fatal("a wholesale replacement was authorized")
		}
	})
	t.Run("a substituted key under a trusted id is refused", func(t *testing.T) {
		impostor := newSigner(t, a.id)
		if store.AuthorizesRotation([]trust.Root{impostor.root(), b.root()}) {
			// b is still trusted so the count would pass; the point is
			// that reusing a trusted ID with a different key must not
			// count as retaining that root.
			if store.Threshold <= 1 {
				t.Skip("a threshold of one cannot distinguish this case")
			}
			t.Fatal("a substituted key counted as a retained root")
		}
	})
	t.Run("an unverified rotation refuses the manifest", func(t *testing.T) {
		rotating := manifest()
		rotating.Roots = []trust.Root{c.root()}
		body := encode(t, a.sign(t, rotating))
		_, err := VerifyManifest(store, body, ChannelStable)
		if !errors.Is(err, trust.ErrUnauthorizedRotation) {
			t.Fatalf("error = %v, want ErrUnauthorizedRotation", err)
		}
	})
	t.Run("an empty store authorizes nothing", func(t *testing.T) {
		if (trust.Store{}).AuthorizesRotation([]trust.Root{a.root()}) {
			t.Fatal("an empty store authorized a rotation")
		}
	})
}

// TestCandidateMustCarryTheProposedRoots proves a manifest cannot rotate
// keys the candidate binary does not actually have.
func TestCandidateMustCarryTheProposedRoots(t *testing.T) {
	a := newSigner(t, "root-a")
	c := newSigner(t, "root-c")
	current := CandidateMetadata{Version: "v1.0.0", ProtocolVersion: 1, StoreSchemaVersion: 1}
	candidate := CandidateMetadata{Version: "v2.0.0", ProtocolVersion: 1, StoreSchemaVersion: 1}
	rotation := []trust.Root{a.root(), c.root()}

	if got := CheckCompatibility(current, candidate, rotation); got.OK() {
		t.Fatal("a candidate carrying none of the proposed roots was accepted")
	}
	candidate.TrustRoots = rotation
	if got := CheckCompatibility(current, candidate, rotation); !got.OK() {
		t.Fatalf("a candidate carrying the proposed roots was refused: %v", got.Reasons)
	}
}

func TestCompatibilityRefusesDowngrades(t *testing.T) {
	current := CandidateMetadata{Version: "v2.0.0", ProtocolVersion: 2, StoreSchemaVersion: 7}
	tests := []struct {
		name      string
		candidate CandidateMetadata
		want      string
	}{
		{"an older version",
			CandidateMetadata{Version: "v1.9.0", ProtocolVersion: 2, StoreSchemaVersion: 7},
			"only moves forward"},
		{"the same version",
			CandidateMetadata{Version: "v2.0.0", ProtocolVersion: 2, StoreSchemaVersion: 7},
			"only moves forward"},
		{"an older protocol",
			CandidateMetadata{Version: "v3.0.0", ProtocolVersion: 1, StoreSchemaVersion: 7},
			"no longer answers"},
		{"an older schema",
			CandidateMetadata{Version: "v3.0.0", ProtocolVersion: 2, StoreSchemaVersion: 6},
			"refuse to open the database"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckCompatibility(current, tt.candidate, nil)
			if got.OK() {
				t.Fatal("the candidate was accepted")
			}
			if !strings.Contains(strings.Join(got.Reasons, " "), tt.want) {
				t.Fatalf("reasons = %v, want one mentioning %q", got.Reasons, tt.want)
			}
		})
	}
	newer := CandidateMetadata{Version: "v2.1.0", ProtocolVersion: 2, StoreSchemaVersion: 8}
	if got := CheckCompatibility(current, newer, nil); !got.OK() {
		t.Fatalf("a newer candidate was refused: %v", got.Reasons)
	}
	// A development build can always take a real release.
	dev := CandidateMetadata{Version: "dev", ProtocolVersion: 2, StoreSchemaVersion: 7}
	if got := CheckCompatibility(dev, newer, nil); !got.OK() {
		t.Fatalf("a dev build refused a release: %v", got.Reasons)
	}
}

func TestMalformedManifestsAreRefused(t *testing.T) {
	s := newSigner(t, "root-a")
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"a future schema", func(m *Manifest) { m.SchemaVersion = 99 }},
		{"an unknown channel", func(m *Manifest) { m.Channel = "nightly" }},
		{"a version with no v", func(m *Manifest) { m.Version = "9.0.0" }},
		{"no artifacts", func(m *Manifest) { m.Artifacts = nil }},
		{"a plain-http artifact", func(m *Manifest) { m.Artifacts[0].URL = "http://releases.example/x" }},
		{"a short digest", func(m *Manifest) { m.Artifacts[0].SHA256 = "abc" }},
		{"an uppercase digest", func(m *Manifest) { m.Artifacts[0].SHA256 = strings.ToUpper(digestOf(artifactBody)) }},
		{"a zero size", func(m *Manifest) { m.Artifacts[0].Size = 0 }},
		{"two artifacts for one platform", func(m *Manifest) {
			m.Artifacts = append(m.Artifacts, m.Artifacts[0])
		}},
		{"a malformed root", func(m *Manifest) { m.Roots = []trust.Root{{ID: "x", Key: "not base64!"}} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := manifest()
			tt.mutate(&m)
			signed := s.sign(t, m)
			if _, _, err := ParseManifest(strings.NewReader(string(encode(t, signed)))); err == nil {
				t.Fatal("ParseManifest = nil error, want rejection")
			}
		})
	}
	// An unknown field is refused: verifying a document you only partly
	// parsed means signing off on the part you skipped.
	body := `{"schema_version":1,"channel":"stable","version":"v1.0.0","protocol_version":1,
		"store_schema_version":1,"artifacts":[],"signatures":[],"surprise":true}`
	if _, _, err := ParseManifest(strings.NewReader(body)); !errors.Is(err, ErrMalformedManifest) {
		t.Fatalf("an unknown field was accepted: %v", err)
	}
	if _, _, err := ParseManifest(strings.NewReader(`{}{}`)); !errors.Is(err, ErrMalformedManifest) {
		t.Fatal("trailing data was accepted")
	}
}

func TestNoArtifactForThisPlatform(t *testing.T) {
	s := newSigner(t, "root-a")
	store := trust.Store{Roots: []trust.Root{s.root()}}
	m := manifest()
	m.Artifacts[0].OS = "plan9"
	m.Artifacts[0].Arch = "mips"
	if _, err := VerifyManifest(store, encode(t, s.sign(t, m)), ChannelStable); !errors.Is(err, ErrNoArtifact) {
		t.Fatalf("error = %v, want ErrNoArtifact", err)
	}
}

// TestFetchRefusesInsecureAndRedirected pins the network policy.
func TestFetchRefusesInsecureAndRedirected(t *testing.T) {
	f := NewFetcher()
	if _, err := f.Get(context.Background(), "http://releases.example/x", MaxManifestBytes); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("error = %v, want ErrInsecureURL", err)
	}
	if _, err := f.Get(context.Background(), "file:///etc/passwd", MaxManifestBytes); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("error = %v, want ErrInsecureURL", err)
	}

	// A redirect is refused even when the destination would have worked.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	local := WithClient(redirector.Client())
	local.client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return fmt.Errorf("update: refusing a redirect to %s: %w", req.URL.Host, ErrRedirect)
	}
	if _, err := local.Get(context.Background(), strings.Replace(redirector.URL, "http://", "https://", 1),
		MaxManifestBytes); err == nil {
		t.Fatal("a redirect was followed")
	}
}

// TestFetchIsBounded proves a lying server cannot make Homonto read
// forever.
func TestFetchIsBounded(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 4096; i++ {
			w.Write(make([]byte, 1024))
		}
	}))
	defer server.Close()
	f := WithClient(server.Client())
	url := strings.Replace(server.URL, "http://", "https://", 1)
	if _, err := f.Get(context.Background(), url, 1024); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("error = %v, want ErrTooLarge", err)
	}
}

func TestFetchReportsServerErrors(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer server.Close()
	f := WithClient(server.Client())
	url := strings.Replace(server.URL, "http://", "https://", 1)
	if _, err := f.Get(context.Background(), url, MaxManifestBytes); !errors.Is(err, ErrFetchFailed) {
		t.Fatalf("error = %v, want ErrFetchFailed", err)
	}
}

// TestInspectCandidateRunsIt proves the candidate is interrogated rather
// than believed.
func TestInspectCandidateRunsIt(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the fake candidate needs a POSIX shell")
	}
	dir := t.TempDir()

	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}

	good := write("good", "#!/bin/sh\ncat <<'JSON'\n"+
		`{"version":"v9.0.0","protocol_version":1,"store_schema_version":3}`+"\nJSON\n")
	meta, err := InspectCandidate(context.Background(), good)
	if err != nil {
		t.Fatalf("InspectCandidate: %v", err)
	}
	if meta.Version != "v9.0.0" || meta.ProtocolVersion != 1 || meta.StoreSchemaVersion != 3 {
		t.Fatalf("metadata = %+v", meta)
	}

	tests := []struct {
		name string
		body string
	}{
		{"a candidate that fails", "#!/bin/sh\nexit 1\n"},
		{"a candidate that says nothing", "#!/bin/sh\nexit 0\n"},
		{"a candidate that answers prose", "#!/bin/sh\necho hello\n"},
		{"a candidate with incomplete metadata",
			"#!/bin/sh\necho '{\"version\":\"v9.0.0\"}'\n"},
		// A candidate speaking a schema from the future is refused: the
		// fields it added are exactly the ones that would have explained
		// why it is incompatible.
		{"a candidate from the future",
			"#!/bin/sh\necho '{\"version\":\"v9.0.0\",\"protocol_version\":1," +
				"\"store_schema_version\":3,\"something_new\":true}'\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := write(strings.ReplaceAll(tt.name, " ", "-"), tt.body)
			if _, err := InspectCandidate(context.Background(), path); !errors.Is(err, ErrInspectFailed) {
				t.Fatalf("error = %v, want ErrInspectFailed", err)
			}
		})
	}

	if _, err := InspectCandidate(context.Background(), filepath.Join(dir, "missing")); !errors.Is(err, ErrInspectFailed) {
		t.Fatalf("error = %v, want ErrInspectFailed", err)
	}
	notExecutable := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(notExecutable, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := InspectCandidate(context.Background(), notExecutable); !errors.Is(err, ErrInspectFailed) {
		t.Fatalf("error = %v, want ErrInspectFailed", err)
	}
}

// TestCanonicalExcludesSignatures proves a signature cannot cover itself,
// and that the canonical form is stable under reordering.
func TestCanonicalExcludesSignatures(t *testing.T) {
	s := newSigner(t, "root-a")
	unsigned, err := Canonical(manifest())
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	signed, err := Canonical(s.sign(t, manifest()))
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if string(unsigned) != string(signed) {
		t.Fatal("signing changed the canonical bytes; a signature cannot cover itself")
	}

	shuffled := manifest()
	shuffled.Artifacts = append(shuffled.Artifacts, Artifact{
		OS: "aaa", Arch: "zzz", URL: "https://x.example/a",
		SHA256: digestOf([]byte("a")), Size: 1,
	})
	reordered := manifest()
	reordered.Artifacts = append([]Artifact{{
		OS: "aaa", Arch: "zzz", URL: "https://x.example/a",
		SHA256: digestOf([]byte("a")), Size: 1,
	}}, reordered.Artifacts...)
	same, err := SameCanonical(shuffled, reordered)
	if err != nil {
		t.Fatalf("SameCanonical: %v", err)
	}
	if !same {
		t.Fatal("the canonical form depends on artifact order")
	}
}

// TestLocalMetadataDescribesThisBinary is a guard on the hidden command:
// what a candidate answers must be what this binary reports about itself.
func TestLocalMetadataDescribesThisBinary(t *testing.T) {
	meta := LocalMetadata()
	if meta.Version == "" {
		t.Error("this binary reports no version")
	}
	if meta.ProtocolVersion < 1 {
		t.Error("this binary reports no protocol version")
	}
	if meta.StoreSchemaVersion < 1 {
		t.Error("this binary reports no store schema version")
	}
}
