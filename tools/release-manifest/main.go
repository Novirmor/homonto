// Command release-manifest writes the unsigned release manifest for a set
// of packaged archives.
//
// It exists so that no part of a release manifest is ever hand-written.
// The document a Homonto binary verifies is produced from the archives
// that were actually built, using the same Go types the binary parses it
// with — so a field renamed in internal/update breaks the release build
// rather than every installed binary.
//
// The protocol and schema versions come from THIS source tree, which is
// the tree the archives were built from. They are still re-checked against
// the candidate binary at update time; stating them here only lets a
// manifest be rejected before anything is downloaded.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/update"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "release-manifest: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	version := flag.String("version", "", "the release version, e.g. v0.9.0")
	channel := flag.String("channel", string(update.ChannelStable), "release channel")
	baseURL := flag.String("base-url", "", "https base url the archives are published under")
	dist := flag.String("dist", "dist", "directory holding the archives")
	out := flag.String("out", "", "where to write the manifest")
	flag.Parse()

	if strings.TrimSpace(*version) == "" || strings.TrimSpace(*baseURL) == "" {
		return fmt.Errorf("needs --version and --base-url")
	}
	if !strings.HasPrefix(*baseURL, "https://") {
		return fmt.Errorf("--base-url must be https, got %q", *baseURL)
	}
	if *out == "" {
		*out = filepath.Join(*dist, "release-manifest.json")
	}

	artifacts, err := describe(*dist, *version, strings.TrimRight(*baseURL, "/"))
	if err != nil {
		return err
	}
	if len(artifacts) == 0 {
		return fmt.Errorf("no homonto_%s_*.tar.gz archives in %s", *version, *dist)
	}

	manifest := update.Manifest{
		SchemaVersion:      update.ManifestSchema,
		Channel:            update.Channel(*channel),
		Version:            *version,
		ProtocolVersion:    protocol.CurrentVersion,
		StoreSchemaVersion: store.SchemaVersion(),
		Artifacts:          artifacts,
	}
	// Canonical rather than Encode: the unsigned document is exactly the
	// bytes the signer will sign, so signing cannot reorder anything.
	body, err := update.Canonical(manifest)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*out, append(body, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *out, err)
	}
	fmt.Printf("%s: %d artifact(s), protocol %d, schema %d\n",
		*out, len(artifacts), manifest.ProtocolVersion, manifest.StoreSchemaVersion)
	return nil
}

// describe hashes every archive the build produced. The digests come from
// the files on disk, never from a build log — a manifest that describes
// what the build was supposed to produce is exactly the failure signing is
// meant to catch.
func describe(dist, version, baseURL string) ([]update.Artifact, error) {
	pattern := filepath.Join(dist, "homonto_"+version+"_*.tar.gz")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)
	var artifacts []update.Artifact
	for _, path := range matches {
		name := filepath.Base(path)
		platform := strings.TrimSuffix(strings.TrimPrefix(name, "homonto_"+version+"_"), ".tar.gz")
		goos, goarch, ok := strings.Cut(platform, "_")
		if !ok {
			return nil, fmt.Errorf("cannot read a platform out of %q", name)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		digest := sha256.Sum256(body)
		artifacts = append(artifacts, update.Artifact{
			OS:     goos,
			Arch:   goarch,
			URL:    baseURL + "/" + name,
			SHA256: hex.EncodeToString(digest[:]),
			Size:   int64(len(body)),
		})
	}
	return artifacts, nil
}
