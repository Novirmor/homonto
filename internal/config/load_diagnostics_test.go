package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/schema"
)

func TestLoadDiagnostics(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		doc          string
		missing      bool
		danglingRoot bool
		want         string
		cause        error
	}{
		{name: "missing file", missing: true, want: "read config:", cause: fs.ErrNotExist},
		{name: "syntax", doc: "[workflow", want: "parse config:"},
		{name: "semantic key", doc: "[workflow]\nroot = '../outside'\n", want: "workflow.root"},
		{name: "future schema", doc: fmt.Sprintf("schema_version = %d\n", CurrentConfigSchemaVersion+1), want: "unknown config schema version", cause: schema.ErrTooNew},
		{name: "repository resolution", doc: "[repos]\nservice = 'missing-repository'\n", want: "repos.service"},
		{name: "root resolution", danglingRoot: true, want: "resolving workflow.root symlink", cause: fs.ErrNotExist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "selected config.toml")
			if tc.danglingRoot {
				if err := os.Symlink("missing-root", filepath.Join(filepath.Dir(path), "docs")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.doc), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			relative, err := filepath.Rel(cwd, path)
			if err != nil {
				t.Fatal(err)
			}
			for _, selected := range []string{relative, path} {
				c, err := Load(selected)
				if c != nil || err == nil {
					t.Fatalf("Load(%q) = %v, %v, want nil config and error", selected, c, err)
				}
				prefix := fmt.Sprintf("config %q: ", path)
				if !strings.HasPrefix(err.Error(), prefix) || strings.Count(err.Error(), prefix) != 1 {
					t.Errorf("Load(%q) = %v, want exactly one absolute filename prefix %q", selected, err, prefix)
				}
				cause := errors.Unwrap(err)
				if cause == nil || !strings.Contains(cause.Error(), tc.want) {
					t.Errorf("Load(%q) cause = %v, want diagnostic containing %q", selected, cause, tc.want)
				}
				if tc.cause != nil && !errors.Is(err, tc.cause) {
					t.Errorf("Load(%q) = %v, want errors.Is(_, %v)", selected, err, tc.cause)
				}
			}
		})
	}
}

func TestLoadDiagnosticsAbsolutePathFailure(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
	})
	if err := os.Remove(dir); err != nil {
		t.Skipf("cannot remove current directory: %v", err)
	}
	c, err := Load("selected config.toml")
	if c != nil || err == nil || !strings.HasPrefix(err.Error(), `resolve config path "selected config.toml": `) {
		t.Fatalf("Load = %v, %v, want config path resolution diagnostic", c, err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Load = %v, want underlying missing-directory error", err)
	}
}
