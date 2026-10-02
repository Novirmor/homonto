package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A remote: subagent source requires a valid sha256 digest pin; the loader must
// fail closed when it is missing or malformed, and accept a well-formed pin.
func TestRemoteSourceRequiresDigest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	load := func(doc string) (*Config, error) {
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		return Load(p)
	}
	const validHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	t.Run("valid pin loads", func(t *testing.T) {
		c, err := load("[subagents.x]\nsource=\"remote:https://h.test/x.tar.gz\"\ndigest=\"sha256:" + validHex + "\"\nscope=\"project\"\n")
		if err != nil {
			t.Fatalf("valid pinned remote source should load: %v", err)
		}
		if got := c.Subagents["x"].Digest; got != "sha256:"+validHex {
			t.Fatalf("digest not preserved: %q", got)
		}
	})

	t.Run("missing digest rejected", func(t *testing.T) {
		_, err := load("[subagents.x]\nsource=\"remote:https://h.test/x.tar.gz\"\nscope=\"project\"\n")
		if err == nil {
			t.Fatal("remote source without digest must be rejected")
		}
		if !strings.Contains(err.Error(), "digest") {
			t.Fatalf("error should mention digest, got: %v", err)
		}
	})

	t.Run("malformed digest rejected", func(t *testing.T) {
		_, err := load("[subagents.x]\nsource=\"remote:https://h.test/x.tar.gz\"\ndigest=\"sha256:nothex\"\nscope=\"project\"\n")
		if err == nil {
			t.Fatal("remote source with malformed digest must be rejected")
		}
	})

	t.Run("plain http rejected", func(t *testing.T) {
		_, err := load("[subagents.x]\nsource=\"remote:http://h.test/x.tar.gz\"\ndigest=\"sha256:" + validHex + "\"\nscope=\"project\"\n")
		if err == nil {
			t.Fatal("plain http remote source must be rejected")
		}
	})

	t.Run("builtin source unaffected by absent digest", func(t *testing.T) {
		_, err := load("[subagents.x]\nsource=\"builtin:architect\"\nscope=\"project\"\n" + modelsFor("x"))
		if err != nil {
			t.Fatalf("builtin source without digest must still load: %v", err)
		}
	})
}

// Remote sources are only supported for subagents today; declaring one for a
// skill/command/framework must be rejected at load, not silently accepted into
// a dangling local path.
func TestRemoteRejectedForNonSubagentKinds(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	const hex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	load := func(doc string) error {
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		return err
	}
	if err := load("[skills.x]\nsource=\"remote:https://h.test/x.tar.gz\"\ndigest=\"sha256:" + hex + "\"\nscope=\"project\"\n"); err == nil {
		t.Fatal("a remote skill must be rejected (remote is subagent-only today)")
	}
	if err := load("[commands.x]\nsource=\"remote:https://h.test/x.tar.gz\"\ndigest=\"sha256:" + hex + "\"\nscope=\"project\"\n"); err == nil {
		t.Fatal("a remote command must be rejected (remote is subagent-only today)")
	}
}

// The claude target is opt-in and currently supports MCPs only; the codex
// pilot was removed in v0.13.0 and stays removed. A resource naming claude
// must fail closed naming the current support boundary, a target naming a
// removed tool keeps its removal message, and a genuine unknown tool keeps its
// typo report.
func TestRemovedToolTargetsRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "homonto.toml")
	load := func(doc string) error {
		if err := os.WriteFile(p, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(p)
		return err
	}
	// An MCP targeting claude is exactly the supported claude surface: it must
	// load, not be rejected.
	if err := load("[mcps.demo]\ncommand=[\"srv\"]\ntargets=[\"claude\"]\n"); err != nil {
		t.Fatalf("an MCP targeting claude must load: %v", err)
	}
	for _, tc := range []struct{ label, doc, want string }{
		{"mcp targets codex", "[mcps.demo]\ncommand=[\"srv\"]\ntargets=[\"codex\"]\n", `targets "codex"`},
		{"subagent targets claude", "[subagents.foo]\nsource=\"builtin:architect\"\nscope=\"project\"\ntargets=[\"claude\"]\n", `targets "claude"`},
		{"skill targets codex", "[skills.foo]\nsource=\"local:foo\"\nscope=\"project\"\ntargets=[\"codex\"]\n", `targets "codex"`},
	} {
		err := load(tc.doc)
		if err == nil {
			t.Fatalf("%s: accepted; want the rejection", tc.label)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error %v does not mention %q", tc.label, err, tc.want)
		}
	}
	if err := load("[mcps.demo]\ncommand=[\"srv\"]\ntargets=[\"nope\"]\n"); err == nil {
		t.Fatal("an unknown MCP target must still be rejected")
	}
}
