package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/remote"
)

func examplePackage(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "examples", name))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// packDir writes a deterministic tar.gz of root, the shape a remote: package
// is published in.
func packDir(t *testing.T, root, out string) string {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	err := fs.WalkDir(os.DirFS(root), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: p, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return out
}

const exampleModels = `
[workflows.example-workflow.processes.default]
steps = ["read", "label"]

[subagents.homonto.opencode]
model = "test/model"

[subagents.example-reviewer.opencode]
model = "test/model"
`

// TestExamplesInstallAsLocalAndRemotePackages consumes the examples exactly
// as a user's packages: the same config validation, plan, apply and
// conformance check, with the engine once from local: and once from a
// digest-pinned remote: source through the lock/cache trust path.
func TestExamplesInstallAsLocalAndRemotePackages(t *testing.T) {
	tarball := packDir(t, examplePackage(t, "example-engine"), filepath.Join(t.TempDir(), "example-engine.tar.gz"))
	pin := pinFor(t, tarball)
	for _, tc := range []struct{ name, engine string }{
		{"local", "source = \"local:" + examplePackage(t, "example-engine") + "\"\n"},
		{"remote", "source = \"remote:file://" + tarball + "\"\ndigest = \"" + pin.String() + "\"\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := t.TempDir(), t.TempDir()
			cfg := "[engines.example-engine]\n" + tc.engine + "scope = \"project\"\n" +
				"[workflows.example-workflow]\nsource = \"local:" + examplePackage(t, "example-workflow") + "\"\nscope = \"project\"\n" +
				"[services.example-service]\nsource = \"local:" + examplePackage(t, "example-service") + "\"\nscope = \"project\"\n" + exampleModels
			if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			e := buildEngine(t, home, repo)
			cl, err := e.Cfg.FrameworkCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"example-engine", "example-workflow", "example-service"} {
				r, err := cl.Conformance(name)
				if err != nil || !r.Passed() {
					t.Fatalf("%s conformance: %+v %v", name, r, err)
				}
			}
			if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
				t.Fatal(err)
			}
			// The example reviewer renders with the service's worker denies,
			// and the shared coordinator with its coordinator asks.
			reviewer, err := os.ReadFile(filepath.Join(repo, ".homonto", "catalog", "subagents", "example-reviewer.opencode.md"))
			if err != nil || !strings.Contains(string(reviewer), `model: "test/model"`) {
				t.Fatalf("example reviewer not rendered: %v\n%s", err, reviewer)
			}
			coordinator, err := os.ReadFile(filepath.Join(repo, ".homonto", "catalog", "subagents", "homonto.opencode.md"))
			if err != nil || !strings.Contains(string(coordinator), `"tracker-cli issue delete*": ask`) {
				t.Fatalf("coordinator lacks the service's ask: %v", err)
			}
			if tc.name == "remote" {
				// Served from the content-addressed cache under its pin, and
				// the cached tree still verifies against it.
				cache := &remote.Cache{Root: e.RemoteCacheRoot}
				if !cache.Has(pin) {
					t.Fatalf("remote example not cached under %s", pin)
				}
				if err := cache.VerifyContent(pin); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRemoteExampleWithAWrongDigestFailsClosed(t *testing.T) {
	tarball := packDir(t, examplePackage(t, "example-engine"), filepath.Join(t.TempDir(), "example-engine.tar.gz"))
	home, repo := t.TempDir(), t.TempDir()
	cfg := "[engines.example-engine]\nsource = \"remote:file://" + tarball + "\"\ndigest = \"sha256:" + strings.Repeat("0", 64) + "\"\nscope = \"project\"\n[subagents.example-reviewer.opencode]\nmodel = \"test/model\"\n"
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tryBuildEngine(home, repo); err == nil {
		t.Fatal("a mismatched digest must fail before the package loads")
	}
}

// TestRemotePackageTuneOnlyOverridesAreJudgedAfterInjection: load cannot see
// a remote package's agents, so a tune-only block naming one is deferred, and
// one naming no agent the package ships still fails once it is injected.
func TestRemotePackageTuneOnlyOverridesAreJudgedAfterInjection(t *testing.T) {
	tarball := packDir(t, examplePackage(t, "example-engine"), filepath.Join(t.TempDir(), "example-engine.tar.gz"))
	pin := pinFor(t, tarball)
	home, repo := t.TempDir(), t.TempDir()
	cfg := "[engines.example-engine]\nsource = \"remote:file://" + tarball + "\"\ndigest = \"" + pin.String() + "\"\nscope = \"project\"\n" +
		"[subagents.example-reviewer.opencode]\nmodel = \"test/model\"\n[subagents.example-auditor.opencode]\nmodel = \"test/model\"\n"
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tryBuildEngine(home, repo); err == nil || !strings.Contains(err.Error(), "subagents.example-auditor tunes an agent that is not installed") {
		t.Fatalf("err = %v", err)
	}
}
