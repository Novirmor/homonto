// Command release-sign signs a Homonto release manifest.
//
// It is a release-engineering tool, not part of the shipped binary. It
// does two things and refuses to do anything else:
//
//   - keygen writes an Ed25519 keypair. The private half never leaves the
//     file it is written to, and this tool never uploads anything.
//   - sign reads a manifest, signs its CANONICAL form, and writes the
//     manifest back with the signature added. It then re-parses what it
//     wrote and verifies it, so the tool cannot publish a document whose
//     signature does not cover the bytes that were published.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/noviopenworks/homonto/internal/update"
	"github.com/noviopenworks/homonto/internal/update/trust"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen(os.Args[2:])
	case "sign":
		err = sign(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-sign: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `release-sign keygen --id <root-id> --out <private-key-file>
release-sign sign --key <private-key-file> --id <root-id> --manifest <file>
`)
}

// keygen writes a new signing keypair.
func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	id := fs.String("id", "", "the root id this key signs as")
	out := fs.String("out", "", "where to write the private key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" || strings.TrimSpace(*out) == "" {
		return fmt.Errorf("keygen needs --id and --out")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	// 0600 and O_EXCL: a signing key is not something to overwrite by
	// accident, and not something to leave world-readable.
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	defer f.Close()
	if _, err := fmt.Fprintln(f, base64.StdEncoding.EncodeToString(private)); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Printf("root id:     %s\npublic key:  %s\nprivate key: %s\n",
		*id, base64.StdEncoding.EncodeToString(public), *out)
	return nil
}

// sign adds a signature to a manifest and proves the result verifies.
func sign(args []string) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyPath := fs.String("key", "", "the private key file")
	id := fs.String("id", "", "the root id to sign as")
	manifestPath := fs.String("manifest", "", "the manifest to sign in place")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*keyPath) == "" || strings.TrimSpace(*id) == "" ||
		strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("sign needs --key, --id, and --manifest")
	}
	private, err := readPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", *manifestPath, err)
	}
	manifest, err := parseUnsigned(body)
	if err != nil {
		return err
	}
	canonical, err := update.Canonical(manifest)
	if err != nil {
		return err
	}
	signature := ed25519.Sign(private, canonical)

	manifest.Signatures = append(withoutRoot(manifest.Signatures, *id), trust.Signature{
		RootID: *id, Value: base64.StdEncoding.EncodeToString(signature),
	})
	encoded, err := update.Encode(manifest)
	if err != nil {
		return err
	}

	// Prove the published bytes verify before writing them. A signing tool
	// that can emit a manifest whose signature covers a different
	// serialization is a tool that will do it exactly once, in the release
	// nobody re-checked.
	reparsed, republished, err := update.ParseManifest(strings.NewReader(string(encoded)))
	if err != nil {
		return fmt.Errorf("the signed manifest does not re-parse: %w", err)
	}
	store := trust.Store{Roots: []trust.Root{{
		ID:  *id,
		Key: base64.StdEncoding.EncodeToString(private.Public().(ed25519.PublicKey)),
	}}}
	if err := store.Verify(republished, reparsed.Signatures); err != nil {
		return fmt.Errorf("the signed manifest does not verify: %w", err)
	}
	if err := os.WriteFile(*manifestPath, encoded, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *manifestPath, err)
	}
	fmt.Printf("signed %s as %s\n", *manifestPath, *id)
	return nil
}

// readPrivateKey loads a base64 Ed25519 private key.
func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, fmt.Errorf("%s is not a base64 private key: %w", path, err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%s is %d bytes, want an %d-byte Ed25519 private key",
			path, len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}

// parseUnsigned reads a manifest that may not have a signature yet.
// ParseManifest rightly refuses one; signing is the moment it gains its
// first.
func parseUnsigned(body []byte) (update.Manifest, error) {
	var m update.Manifest
	dec := newStrictDecoder(body)
	if err := dec.Decode(&m); err != nil {
		return update.Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	return m, nil
}

// withoutRoot drops any existing signature by a root, so re-signing
// replaces rather than accumulates.
func withoutRoot(signatures []trust.Signature, id string) []trust.Signature {
	var out []trust.Signature
	for _, s := range signatures {
		if s.RootID == id {
			continue
		}
		out = append(out, s)
	}
	return out
}
