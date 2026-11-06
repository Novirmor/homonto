package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Novirmor/homonto/internal/remote"
)

// publishedReferenceSet is examples/reference-set/published.toml: the
// published release the E2E reference cell installs.
type publishedReferenceSet struct {
	Repository string `toml:"repository"`
	Tag        string `toml:"tag"`
	Commit     string `toml:"commit"`
	Packages   map[string]struct {
		URL           string `toml:"url"`
		Digest        string `toml:"digest"`
		ArchiveSHA256 string `toml:"archive_sha256"`
	} `toml:"packages"`
}

func loadPublishedReferenceSet(t *testing.T) publishedReferenceSet {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "examples", "reference-set", "published.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var pub publishedReferenceSet
	if err := toml.Unmarshal(raw, &pub); err != nil {
		t.Fatalf("published.toml: %v", err)
	}
	return pub
}

// TestReferenceSetMatchesItsPublishedPins keeps the in-repo reference set and
// the published release the E2E cell installs from drifting apart: every
// package directory is pinned, each pin is the content digest of the in-repo
// copy, and each URL is the tag's release asset for the manifest's version.
// It runs offline; `task reference-published` checks the live assets.
func TestReferenceSetMatchesItsPublishedPins(t *testing.T) {
	pub := loadPublishedReferenceSet(t)
	if pub.Repository != "https://github.com/Novirmor/homonto-reference-set" {
		t.Errorf("repository = %q", pub.Repository)
	}
	if !strings.HasPrefix(pub.Tag, "v") || len(pub.Commit) != 40 {
		t.Errorf("tag %q / commit %q: want a v-tag and a full commit sha", pub.Tag, pub.Commit)
	}

	entries, err := os.ReadDir(filepath.Join("..", "..", "examples", "reference-set"))
	if err != nil {
		t.Fatal(err)
	}
	var dirs, pinned []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	for name := range pub.Packages {
		pinned = append(pinned, name)
	}
	sort.Strings(pinned)
	if strings.Join(dirs, ",") != strings.Join(pinned, ",") {
		t.Fatalf("packages %v, pinned %v: every package directory needs exactly one pin", dirs, pinned)
	}

	for _, name := range dirs {
		pkg := pub.Packages[name]
		dir := referencePackage(t, name)
		var manifest struct {
			Version string `toml:"version"`
		}
		raw, err := os.ReadFile(filepath.Join(dir, "framework.toml"))
		if err != nil {
			t.Fatal(err)
		}
		if err := toml.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("%s/framework.toml: %v", name, err)
		}
		wantURL := pub.Repository + "/releases/download/" + pub.Tag + "/" + name + "-" + manifest.Version + ".tar.gz"
		if pkg.URL != wantURL {
			t.Errorf("%s url = %q, want %q", name, pkg.URL, wantURL)
		}
		if len(pkg.ArchiveSHA256) != 64 {
			t.Errorf("%s archive_sha256 = %q", name, pkg.ArchiveSHA256)
		}
		src, err := remote.ParseRemoteSource("remote:file://" + dir)
		if err != nil {
			t.Fatal(err)
		}
		tree, _, err := remote.Fetch(context.Background(), src, remote.DefaultLimits)
		if err != nil {
			t.Fatal(err)
		}
		if got := remote.CanonicalDigest(tree).String(); got != pkg.Digest {
			t.Errorf("%s: the in-repo copy pins %s but published.toml pins %s (%s); "+
				"publish the change as a new tag and move published.toml, or revert the copy",
				name, got, pkg.Digest, pub.Tag)
		}
	}
}

// TestPublishedReferenceSetAssets fetches the pinned release assets over
// https and checks them against published.toml: the archive bytes, the
// content pin, and the release's own pins.toml. It needs the network, so it
// runs only with HOMONTO_REFERENCE_ONLINE=1 (`task reference-published`).
func TestPublishedReferenceSetAssets(t *testing.T) {
	if os.Getenv("HOMONTO_REFERENCE_ONLINE") != "1" {
		t.Skip("set HOMONTO_REFERENCE_ONLINE=1 to check the published release assets")
	}
	pub := loadPublishedReferenceSet(t)
	get := func(url string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s: %s", url, resp.Status)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, remote.DefaultLimits.MaxTotalBytes))
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	releasePins := string(get(pub.Repository + "/releases/download/" + pub.Tag + "/pins.toml"))
	for name, pkg := range pub.Packages {
		body := get(pkg.URL)
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != pkg.ArchiveSHA256 {
			t.Errorf("%s archive sha256 = %s, published.toml says %s", name, got, pkg.ArchiveSHA256)
		}
		tree, err := remote.ValidateTarGz(bytes.NewReader(body), remote.DefaultLimits)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := remote.CanonicalDigest(tree).String(); got != pkg.Digest {
			t.Errorf("%s content pin = %s, published.toml says %s", name, got, pkg.Digest)
		}
		if !strings.Contains(releasePins, name+` = "`+pkg.Digest+`"`) {
			t.Errorf("%s: the release's pins.toml does not carry %s:\n%s", name, pkg.Digest, releasePins)
		}
	}
}
