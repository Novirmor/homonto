// Package release proves the release actually produces what a Homonto
// binary will accept, and refuses to produce anything when the evidence a
// release rests on is missing.
//
// The interesting failure is not "the build broke" — that is loud. It is a
// release that publishes cleanly while one of the things it claims was
// proven was never run. Everything here is aimed at that.
package release

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/update"
	"github.com/noviopenworks/homonto/internal/update/trust"
)

// repoRoot is the repository root relative to this test's directory.
const repoRoot = "../.."

// script returns an absolute path to a repository script.
func script(t *testing.T, name string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join(repoRoot, "scripts", name))
	if err != nil {
		t.Fatalf("resolve %s: %v", name, err)
	}
	return abs
}

// run executes a command and returns its combined output, failing the test
// on a non-zero exit.
func run(t *testing.T, dir, name string, args ...string) string {
	t.Helper()
	out, err := command(dir, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

// command builds a command with a clean-enough environment.
func command(dir, name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	return cmd
}

// requiredAssets is the exact set the BUILD produces: --list is the
// script's own account of what it writes, and the signature is not the
// build's to produce — the release workflow signs afterwards and composes
// the published set. An extra archive is a platform nobody verified, and a
// missing one is a platform someone will report as broken.
var requiredAssets = []string{
	"SHA256SUMS",
	"homonto_v9.9.9_darwin_amd64.tar.gz",
	"homonto_v9.9.9_darwin_arm64.tar.gz",
	"homonto_v9.9.9_linux_amd64.tar.gz",
	"homonto_v9.9.9_linux_arm64.tar.gz",
	"release-manifest.json",
}

// TestReleaseListsExactlyTheRequiredAssets pins the asset set without
// paying for four cross-compiles. --list is the script's own account of
// what it will produce, so a target added or dropped shows up here.
func TestReleaseListsExactlyTheRequiredAssets(t *testing.T) {
	out := run(t, repoRoot, script(t, "build-release.sh"), "--list", "v9.9.9")
	var got []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			got = append(got, line)
		}
	}
	sort.Strings(got)
	if strings.Join(got, "\n") != strings.Join(requiredAssets, "\n") {
		t.Fatalf("release assets:\n got %v\nwant %v", got, requiredAssets)
	}
}

// TestReleaseNamesNoWindowsOrOtherTarget states the negative directly.
// Windows was a supported target of the product this replaced; the
// workflow product has never run there and must not appear to.
func TestReleaseNamesNoWindowsOrOtherTarget(t *testing.T) {
	body, err := os.ReadFile(script(t, "build-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"windows", ".zip", "cmd/onto", "cmd/to"} {
		if strings.Contains(string(body), absent) {
			t.Errorf("build-release.sh still mentions %q", absent)
		}
	}
}

// TestPackagedReleaseVerifiesEndToEnd builds one real target, signs the
// manifest, and verifies it the way the shipped binary would.
//
// One target rather than four: the packaging code path is identical per
// target, and what is being proven here is that the manifest a signer
// produces is one an updater accepts — not that Go can cross-compile.
func TestPackagedReleaseVerifiesEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("packaging builds a binary")
	}
	dist := t.TempDir()
	target := runtime.GOOS + "/" + runtime.GOARCH
	run(t, repoRoot, script(t, "build-release.sh"), "--dist", dist,
		"--targets", target, "--base-url", "https://example.invalid/r", "v9.9.9")

	// The manifest must describe the archive that was actually written.
	manifestPath := filepath.Join(dist, "release-manifest.json")
	body, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("the build produced no manifest: %v", err)
	}
	var unsigned update.Manifest
	if err := json.Unmarshal(body, &unsigned); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	artifact, err := unsigned.ArtifactFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatalf("manifest carries no artifact for %s: %v", target, err)
	}
	archive := filepath.Join(dist, "homonto_v9.9.9_"+runtime.GOOS+"_"+runtime.GOARCH+".tar.gz")
	packed, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	digest := sha256.Sum256(packed)
	if artifact.SHA256 != hex.EncodeToString(digest[:]) {
		t.Errorf("manifest digest %s does not describe the archive", artifact.SHA256)
	}
	if artifact.Size != int64(len(packed)) {
		t.Errorf("manifest size %d, archive is %d bytes", artifact.Size, len(packed))
	}
	if !strings.HasPrefix(artifact.URL, "https://example.invalid/r/") {
		t.Errorf("artifact url %q is not under the base url", artifact.URL)
	}

	// Sign it as a release engineer would, then verify it as the shipped
	// binary would. The point is that these two agree.
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "root.key")
	genOut := run(t, repoRoot, "go", "run", "./tools/release-sign", "keygen",
		"--id", "test-root", "--out", keyPath)
	public := fieldAfter(t, genOut, "public key:")
	run(t, repoRoot, "go", "run", "./tools/release-sign", "sign",
		"--key", keyPath, "--id", "test-root", "--manifest", manifestPath,
		"--sig", filepath.Join(dist, "release-manifest.sig"))

	signedBody, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	store := trust.Store{
		Roots:     []trust.Root{{ID: "test-root", Key: public}},
		Threshold: 1,
	}
	verified, err := update.VerifyManifest(store, signedBody, update.ChannelStable)
	if err != nil {
		t.Fatalf("the shipped binary would refuse this release: %v", err)
	}
	if verified.Manifest.Version != "v9.9.9" {
		t.Errorf("verified version %q", verified.Manifest.Version)
	}

	// The detached signature must cover the published bytes. An auditor
	// who never parses the manifest still has to be able to check it.
	detached, err := os.ReadFile(filepath.Join(dist, "release-manifest.sig"))
	if err != nil {
		t.Fatalf("no detached signature: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(detached)))
	if err != nil {
		t.Fatalf("detached signature is not base64: %v", err)
	}
	key, err := base64.StdEncoding.DecodeString(public)
	if err != nil {
		t.Fatal(err)
	}
	_, canonical, err := update.ParseManifest(strings.NewReader(string(signedBody)))
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(ed25519.PublicKey(key), canonical, raw) {
		t.Error("the detached signature does not cover the published manifest")
	}

	// SHA256SUMS must cover every archive, so a downloader without the
	// manifest still has something to check.
	sums, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	if err != nil {
		t.Fatalf("no SHA256SUMS: %v", err)
	}
	if !strings.Contains(string(sums), filepath.Base(archive)) {
		t.Error("SHA256SUMS does not list the archive")
	}
}

// fieldAfter pulls the value following a label in tool output.
func fieldAfter(t *testing.T, out, label string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if idx := strings.Index(line, label); idx >= 0 {
			return strings.TrimSpace(line[idx+len(label):])
		}
	}
	t.Fatalf("no %q in:\n%s", label, out)
	return ""
}
