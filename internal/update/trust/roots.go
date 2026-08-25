// Package trust decides whether a Homonto release is one this binary is
// willing to become.
//
// # Fail closed, always
//
// A build with no compiled-in signing root verifies nothing and therefore
// trusts nothing: every verification fails, and `homonto update` refuses
// to run. That is deliberate and it is the safe default — a development
// build must not be able to replace itself with something off the
// network, and an empty trust store that "allowed everything" would be
// exactly the bug you cannot notice until it matters.
//
// # Rotation is authorized by the roots you already trust
//
// Signing keys change. A manifest may carry the NEXT set of roots, but a
// binary only accepts them when the manifest itself is signed by roots it
// already trusts, and when the candidate binary carries the same next set.
// Nothing about a rotation is taken on the manifest's word alone.
package trust

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Typed trust errors. Callers branch with errors.Is.
var (
	// ErrNoRoots: this build carries no signing root, so it can verify
	// nothing.
	ErrNoRoots = errors.New("trust: this build carries no signing root")
	// ErrUnknownRoot: a signature names a root this build does not trust.
	ErrUnknownRoot = errors.New("trust: signature names an untrusted root")
	// ErrBadSignature: a signature does not verify against the root it
	// names.
	ErrBadSignature = errors.New("trust: signature does not verify")
	// ErrThreshold: fewer distinct trusted roots signed than required.
	ErrThreshold = errors.New("trust: too few trusted signatures")
	// ErrMalformedRoot: a root's key is not a usable Ed25519 public key.
	ErrMalformedRoot = errors.New("trust: malformed signing root")
	// ErrUnauthorizedRotation: a proposed root set is not authorized by
	// the roots this build already trusts.
	ErrUnauthorizedRotation = errors.New("trust: unauthorized signing-key rotation")
)

// Root is one signing identity.
type Root struct {
	// ID names the root in signatures. It is opaque and stable.
	ID string `json:"id"`
	// Key is the Ed25519 public key, standard base64.
	Key string `json:"key"`
}

// PublicKey decodes the root's key.
func (r Root) PublicKey() (ed25519.PublicKey, error) {
	if strings.TrimSpace(r.ID) == "" {
		return nil, fmt.Errorf("trust: a root must have an id: %w", ErrMalformedRoot)
	}
	raw, err := base64.StdEncoding.DecodeString(r.Key)
	if err != nil {
		return nil, fmt.Errorf("trust: root %q key is not base64: %w: %w", r.ID, ErrMalformedRoot, err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("trust: root %q key is %d bytes, want %d: %w",
			r.ID, len(raw), ed25519.PublicKeySize, ErrMalformedRoot)
	}
	return ed25519.PublicKey(raw), nil
}

// Signature is one root's signature over a canonical manifest.
type Signature struct {
	RootID string `json:"root_id"`
	// Value is the signature, standard base64.
	Value string `json:"value"`
}

// bytes decodes the signature.
func (s Signature) bytes() ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(s.Value)
	if err != nil {
		return nil, fmt.Errorf("trust: signature by %q is not base64: %w: %w", s.RootID, ErrBadSignature, err)
	}
	if len(raw) != ed25519.SignatureSize {
		return nil, fmt.Errorf("trust: signature by %q is %d bytes, want %d: %w",
			s.RootID, len(raw), ed25519.SignatureSize, ErrBadSignature)
	}
	return raw, nil
}

// Store is the set of roots a binary trusts and how many must sign.
type Store struct {
	Roots []Root `json:"roots"`
	// Threshold is how many DISTINCT trusted roots must sign. Zero means
	// one — but never zero: a threshold of zero would accept an unsigned
	// manifest, which is the same as trusting nothing to mean trusting
	// everything.
	Threshold int `json:"threshold"`
}

// threshold returns the effective threshold.
func (s Store) threshold() int {
	if s.Threshold < 1 {
		return 1
	}
	return s.Threshold
}

// compiledRoots is the trust store baked into a release build. It is a
// variable rather than a constant so the release pipeline can stamp it
// with -X-style generation; an unstamped build leaves it empty, which
// makes update refuse to run rather than silently trusting nothing to
// mean trusting anything.
var compiledRoots = Store{}

// Compiled returns this build's trust store.
func Compiled() Store { return compiledRoots }

// SetCompiled installs a trust store. It exists for the release build and
// for tests; a shipped binary calls it once, at init, with generated
// content.
func SetCompiled(s Store) { compiledRoots = s }

// Empty reports whether the store can verify anything at all.
func (s Store) Empty() bool { return len(s.Roots) == 0 }

// rootByID indexes the store's roots.
func (s Store) rootByID(id string) (Root, bool) {
	for _, r := range s.Roots {
		if r.ID == id {
			return r, true
		}
	}
	return Root{}, false
}

// Verify checks that enough distinct trusted roots signed the canonical
// manifest bytes.
//
// Distinct is the operative word: the same root signing twice is one
// signature, not two, so a threshold cannot be met by repetition. An
// unknown root is refused rather than ignored — a manifest carrying a
// signature this build cannot evaluate is a manifest making a claim it
// cannot check, and quietly skipping it would let an attacker pad the
// list.
func (s Store) Verify(canonical []byte, signatures []Signature) error {
	if s.Empty() {
		return fmt.Errorf("trust: %w; `homonto update` is unavailable in this build", ErrNoRoots)
	}
	if len(canonical) == 0 {
		return fmt.Errorf("trust: nothing to verify: %w", ErrBadSignature)
	}
	verified := map[string]bool{}
	for _, sig := range signatures {
		root, known := s.rootByID(sig.RootID)
		if !known {
			return fmt.Errorf("trust: root %q: %w", sig.RootID, ErrUnknownRoot)
		}
		key, err := root.PublicKey()
		if err != nil {
			return err
		}
		raw, err := sig.bytes()
		if err != nil {
			return err
		}
		if !ed25519.Verify(key, canonical, raw) {
			return fmt.Errorf("trust: root %q: %w", sig.RootID, ErrBadSignature)
		}
		verified[sig.RootID] = true
	}
	if len(verified) < s.threshold() {
		return fmt.Errorf("trust: %d distinct trusted signature(s), need %d: %w",
			len(verified), s.threshold(), ErrThreshold)
	}
	return nil
}

// AuthorizesRotation reports whether this store may hand over to next.
//
// A rotation is authorized when the NEW set still contains at least the
// threshold number of roots this build already trusts. That keeps a
// rotation incremental: a release can retire one key and introduce
// another, but it cannot replace the entire set in one step, because a
// manifest that replaced every root at once would be indistinguishable
// from a manifest that had captured the update channel.
func (s Store) AuthorizesRotation(next []Root) bool {
	if s.Empty() || len(next) == 0 {
		return false
	}
	overlap := 0
	for _, candidate := range next {
		if _, err := candidate.PublicKey(); err != nil {
			return false
		}
		existing, known := s.rootByID(candidate.ID)
		if known && existing.Key == candidate.Key {
			overlap++
		}
	}
	return overlap >= s.threshold()
}

// RotationError explains a refused rotation.
func (s Store) RotationError(next []Root) error {
	return fmt.Errorf(
		"trust: the proposed %d root(s) retain too few of this build's %d trusted root(s): %w",
		len(next), len(s.Roots), ErrUnauthorizedRotation)
}

// Fingerprint renders the store's roots for a human, sorted by id.
func (s Store) Fingerprint() string {
	ids := make([]string, 0, len(s.Roots))
	for _, r := range s.Roots {
		ids = append(ids, r.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, ",")
}
