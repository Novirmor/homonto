package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/remote"
)

// TestHasRemoteResources covers the small but CLI-critical HasRemoteResources
// flag (it forces apply to re-resolve remote content even when the symlink plan
// is empty). remote_e2e exercises apply indirectly; this asserts the predicate
// directly so a regression here is attributable.
func TestHasRemoteResources(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "homonto.toml")

	// An empty base — model routing is per-agent now, and these cases declare
	// only local/remote subagents (neither is rendered through agentfm, so
	// no per-agent model block is required).
	const base = ""

	cases := []struct {
		name string
		toml string
		want bool
	}{
		{
			name: "no resources",
			toml: base,
			want: false,
		},
		{
			name: "only local subagent",
			toml: base + "[subagents.local]\nsource = \"local:demo\"\n",
			want: false,
		},
		{
			name: "remote subagent present",
			toml: base + "[subagents.r]\nsource = \"remote:https://example.com/r.tar.gz\"\ndigest = \"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"\ntargets = [\"opencode\"]\n",
			want: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(cfgPath, []byte(tc.toml), 0o644); err != nil {
				t.Fatal(err)
			}
			e, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
			if err != nil {
				t.Fatal(err)
			}
			if got := e.HasRemoteResources(); got != tc.want {
				t.Fatalf("HasRemoteResources = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPendingRemoteRepins covers the F6 digest-only repin flow: a declared
// remote subagent whose digest changed but whose name (and thus symlink plan)
// did not. Plan would render nothing for it; PendingRemoteRepins must surface
// it so apply can prompt before mutating remote content.
func TestPendingRemoteRepins(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "homonto.toml")

	const pinA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const pinB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Remote: subagents are not rendered through agentfm (their content is
	// projected verbatim), so no per-agent model block is required for them.
	// First config: declares pinA. There is no lockfile yet, so there is no
	// repin to report (a fresh declaration surfaces in the projection plan as
	// a create, not as a repin).
	if err := os.WriteFile(cfgPath, []byte("[subagents.r]\nsource = \"remote:https://example.com/r.tar.gz\"\ndigest = \""+pinA+"\"\ntargets = [\"opencode\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	if repins, err := e.PendingRemoteRepins(); err != nil {
		t.Fatalf("PendingRemoteRepins (no lock): %v", err)
	} else if len(repins) != 0 {
		t.Fatalf("PendingRemoteRepins (no lock) = %v, want empty (fresh declaration is not a repin)", repins)
	}

	// Seed the lockfile with pinA — the previously-applied digest.
	lock := &remote.Lock{}
	lock.Set(remote.LockEntry{Kind: "subagent", Name: "r", Digest: pinA})
	if err := lock.Save(e.remoteLockPath()); err != nil {
		t.Fatal(err)
	}

	// Same config + same pin → no repin.
	if repins, err := e.PendingRemoteRepins(); err != nil {
		t.Fatalf("PendingRemoteRepins (same pin): %v", err)
	} else if len(repins) != 0 {
		t.Fatalf("PendingRemoteRepins (same pin) = %v, want empty", repins)
	}

	// Change the config to pinB → one repin, named, with the old+new pins.
	if err := os.WriteFile(cfgPath, []byte("[subagents.r]\nsource = \"remote:https://example.com/r.tar.gz\"\ndigest = \""+pinB+"\"\ntargets = [\"opencode\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e2, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	repins, err := e2.PendingRemoteRepins()
	if err != nil {
		t.Fatalf("PendingRemoteRepins (repin): %v", err)
	}
	if len(repins) != 1 || repins[0].Name != "r" || repins[0].Old != pinA || repins[0].New != pinB {
		t.Fatalf("PendingRemoteRepins (repin) = %+v, want one r entry {%s -> %s}", repins, pinA, pinB)
	}
}

// TestRemotePathsAreStateDirAnchored locks in the path helpers: a relative
// stateDir must never produce relative remote paths (the symlink-target bug
// from a similar mistake in catalog-skill paths).
func TestRemotePathsAreStateDirAnchored(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	cfgPath := filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(cfgPath, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), cfgPath, home, filepath.Join(repo, "content"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		e.remoteSubagentDir(),
		e.remoteLockPath(),
		e.remoteRevokedPath(),
	} {
		if !filepath.IsAbs(p) {
			t.Errorf("remote path %q is not absolute (stateDir = %q)", p, e.StateDir)
		}
	}
}

// TestMaterializeRemoteFile covers the atomicity and error shape of the leaf
// copy: a missing <name>.md in the cache dir must surface a clear "archive
// must contain" error; a present file must land at destRoot/<name>.md.
func TestMaterializeRemoteFile(t *testing.T) {
	dest := t.TempDir()

	// Missing source → named error.
	cacheMissing := t.TempDir()
	if err := materializeRemoteFile(cacheMissing, "demo", dest); err == nil {
		t.Fatal("materializeRemoteFile on missing source must error")
	}

	// Present source → lands at destRoot/demo.md.
	cacheOK := t.TempDir()
	src := []byte("# demo agent\n")
	if err := os.WriteFile(filepath.Join(cacheOK, "demo.md"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := materializeRemoteFile(cacheOK, "demo", dest); err != nil {
		t.Fatalf("materializeRemoteFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "demo.md"))
	if err != nil {
		t.Fatalf("read materialized: %v", err)
	}
	if string(got) != string(src) {
		t.Fatalf("materialized content = %q, want %q", got, src)
	}

	// Re-materialize over an existing file is idempotent (atomic rename).
	if err := materializeRemoteFile(cacheOK, "demo", dest); err != nil {
		t.Fatalf("re-materialize: %v", err)
	}
}

// TestFileSize covers the trivial stat helper and its missing-file branch.
func TestFileSize(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x")
	if got := fileSize(p); got != 0 {
		t.Errorf("fileSize on missing file = %d, want 0", got)
	}
	if err := os.WriteFile(p, []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := fileSize(p); got != 5 {
		t.Errorf("fileSize = %d, want 5", got)
	}
}
