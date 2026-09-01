package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildRemoteSubagentTar writes a tar.gz holding <name>.md and returns its path
// plus the canonical sha256 pin over the single-file tree (matching
// remote.CanonicalDigest so the config digest verifies).
func buildRemoteSubagentTar(t *testing.T, dir, name, body string) (string, string) {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	hdr := &tar.Header{Name: name + ".md", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gzbuf bytes.Buffer
	zw := gzip.NewWriter(&gzbuf)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	p := filepath.Join(dir, name+".tar.gz")
	if err := os.WriteFile(p, gzbuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// Canonical digest over the tree {<name>.md: body} (non-exec).
	h := sha256.New()
	h.Write([]byte(name + ".md"))
	h.Write([]byte{0x00})
	h.Write([]byte{0})
	var lenbuf [8]byte
	binary.BigEndian.PutUint64(lenbuf[:], uint64(len(body)))
	h.Write(lenbuf[:])
	h.Write([]byte(body))
	return p, "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// repinModels was a [models.*.*] baseline that the old tier system required.
// Tiers are gone and remote: subagents are projected verbatim (never rendered
// through agentfm), so an empty baseline is all these tests need.
const repinModels = ""

// TestApplyDigestRepinIsNotSilentlyApplied exercises F6: a config whose only
// change is a remote source's pinned digest must surface as a plan change and
// require confirmation. It must NOT be applied under the "No changes" / silent
// "remote sources verified" path.
func TestApplyDigestRepinIsNotSilentlyApplied(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()

	tarV1, pinV1 := buildRemoteSubagentTar(t, fixtures, "aaa", "# reviewer v1")
	cfg := filepath.Join(repo, "homonto.toml")
	cfg1 := "[subagents.aaa]\nsource=\"remote:file://" + tarV1 + "\"\ndigest=\"" + pinV1 + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" + repinModels
	if err := os.WriteFile(cfg, []byte(cfg1), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runCmd(t, home, "", "apply", "--yes", "--config", cfg); err != nil {
		t.Fatalf("seed apply: %v\n%s", err, out)
	}
	contentFile := filepath.Join(repo, ".homonto", "remote", "subagents", "aaa.md")
	if got, _ := os.ReadFile(contentFile); string(got) != "# reviewer v1" {
		t.Fatalf("seed: content = %q", got)
	}

	// Repin: same subagent, new content and new digest — the ONLY config change.
	tarV2, pinV2 := buildRemoteSubagentTar(t, t.TempDir(), "aaa", "# reviewer v2 changed")
	cfg2 := "[subagents.aaa]\nsource=\"remote:file://" + tarV2 + "\"\ndigest=\"" + pinV2 + "\"\nscope=\"project\"\ntargets=[\"opencode\"]\n" + repinModels
	if err := os.WriteFile(cfg, []byte(cfg2), 0o644); err != nil {
		t.Fatal(err)
	}

	// Apply without --yes and answer "no" (a bare newline). The repin must be
	// surfaced and confirmed, and declining must leave the content at v1.
	out, err := runCmd(t, home, "\n", "apply", "--config", cfg)
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out)
	}
	if strings.Contains(out, "No changes") {
		t.Fatalf("digest repin must not read as 'No changes':\n%s", out)
	}
	if strings.Contains(out, "remote sources verified") {
		t.Fatalf("digest repin must not be applied under the silent verify path:\n%s", out)
	}
	if !strings.Contains(out, "Apply these changes?") {
		t.Fatalf("digest repin must prompt for confirmation:\n%s", out)
	}
	if !strings.Contains(out, "aaa") {
		t.Fatalf("plan output should name the repinned remote:\n%s", out)
	}
	if !strings.Contains(out, "Aborted.") {
		t.Fatalf("declining must abort:\n%s", out)
	}
	if got, _ := os.ReadFile(contentFile); string(got) != "# reviewer v1" {
		t.Fatalf("declined repin mutated content to %q, want v1 unchanged", got)
	}

	// Confirming applies the repin.
	out, err = runCmd(t, home, "y\n", "apply", "--config", cfg)
	if err != nil {
		t.Fatalf("confirm apply: %v\n%s", err, out)
	}
	if got, _ := os.ReadFile(contentFile); string(got) != "# reviewer v2 changed" {
		t.Fatalf("confirmed repin did not update content, got %q", got)
	}
}
