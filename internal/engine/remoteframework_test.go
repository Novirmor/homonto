package engine

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/secret"
)

// tryBuildEngine is buildEngine without t.Fatal — returns Build's error so a
// fail-closed remote resolution can be asserted.
func tryBuildEngine(home, repo string) (*Engine, error) {
	e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, filepath.Join(repo, "content"))
	if err != nil {
		return nil, err
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	return e, nil
}

// buildFrameworkTarGz writes a tar.gz of a framework root (framework.toml +
// skills/myskill/SKILL.md) and returns its path.
func buildFrameworkTarGz(t *testing.T, dir string) string {
	t.Helper()
	files := map[string]string{
		"framework.toml":          "name = \"myfw\"\nversion = \"0.1.0\"\n[skills]\nmyskill = \"skills/myskill\"\n",
		"skills/myskill/SKILL.md": "remote framework skill",
	}
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	// deterministic order
	for _, name := range []string{"framework.toml", "skills/myskill/SKILL.md"} {
		body := files[name]
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw.Bytes())
	zw.Close()
	p := filepath.Join(dir, "myfw.tar.gz")
	if err := os.WriteFile(p, gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestApply_RemoteFrameworkSkillMaterialized(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()
	tarPath := buildFrameworkTarGz(t, fixtures)
	pin := pinFor(t, tarPath)

	cfg := "[engines.myfw]\nsource = \"remote:file://" + tarPath + "\"\ndigest = \"" + pin.String() + "\"\nscope = \"user\"\n"
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}

	e := buildEngine(t, home, repo)
	sets, err := e.Plan()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := filepath.Join(repo, ".homonto", "catalog", "skills", "myskill", "SKILL.md")
	b, err := os.ReadFile(got)
	if err != nil || string(b) != "remote framework skill" {
		t.Fatalf("remote framework skill not materialized: %q %v", b, err)
	}
}

func TestApply_RemoteFrameworkWrongDigestAborts(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	fixtures := t.TempDir()
	tarPath := buildFrameworkTarGz(t, fixtures)

	wrong := "sha256:" + "00000000000000000000000000000000000000000000000000000000000000ff"
	cfg := "[engines.myfw]\nsource = \"remote:file://" + tarPath + "\"\ndigest = \"" + wrong + "\"\nscope = \"user\"\n"
	if err := os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	// A wrong digest must abort fail-closed somewhere in build/plan/apply, and
	// nothing must be installed.
	failed := false
	if e, err := tryBuildEngine(home, repo); err != nil {
		failed = true
	} else if sets, err := e.Plan(); err != nil {
		failed = true
	} else if err := e.Apply(context.Background(), sets); err != nil {
		failed = true
	}
	if !failed {
		t.Fatal("a wrong digest must abort fail-closed")
	}
	if _, err := os.Stat(filepath.Join(repo, ".homonto", "catalog", "skills", "myskill")); !os.IsNotExist(err) {
		t.Errorf("nothing should be installed on a digest mismatch")
	}
}
