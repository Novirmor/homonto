package catalog

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func pluginPathSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += target
		} else if !d.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += string(data)
		}
		out[rel] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMaterializePluginsRefusesSymlinkPathsBeforeAnyRemoval(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"root", "ancestor", "name", "dangling-root", "dangling-name", "root-file"} {
		t.Run(kind, func(t *testing.T) {
			base, outside := t.TempDir(), t.TempDir()
			root := filepath.Join(base, "parent", "plugins")
			for _, dir := range []string{outside, filepath.Join(outside, "homonto-workflow"), filepath.Join(outside, "plugins", "homonto-workflow")} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("preserve outside"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
				t.Fatal(err)
			}
			link, target := root, outside
			switch kind {
			case "ancestor":
				link = filepath.Dir(root)
				if err := os.Remove(link); err != nil {
					t.Fatal(err)
				}
			case "name", "dangling-name":
				if err := os.MkdirAll(filepath.Join(root, "permission-observer"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "permission-observer", "sentinel"), []byte("preserve earlier plugin too"), 0o600); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(root, "homonto-workflow")
				target = filepath.Join(outside, "homonto-workflow")
			}
			if strings.HasPrefix(kind, "dangling-") {
				target = filepath.Join(outside, "missing")
			}
			if kind == "root-file" {
				if err := os.WriteFile(link, []byte("not a directory"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			before, external := pluginPathSnapshot(t, base), pluginPathSnapshot(t, outside)
			if err := c.MaterializePlugins(root, []string{"permission-observer", "homonto-workflow"}); err == nil || !strings.Contains(err.Error(), "unsafe plugin") {
				t.Fatalf("unsafe destination accepted: %v", err)
			}
			if !reflect.DeepEqual(before, pluginPathSnapshot(t, base)) {
				t.Fatal("rejection changed or removed a plugin destination")
			}
			if !reflect.DeepEqual(external, pluginPathSnapshot(t, outside)) {
				t.Fatal("rejection deleted, created, or changed outside data")
			}
		})
	}
}

func TestMaterializePluginsRepairsFileLinkWithoutFollowingIt(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []bool{false, true} {
		t.Run(map[bool]string{false: "absolute", true: "relative"}[relative], func(t *testing.T) {
			base, outside := t.TempDir(), t.TempDir()
			root := filepath.Join(base, "missing", "plugins")
			dst := root
			if relative {
				cwd, err := os.Getwd()
				if err != nil {
					t.Fatal(err)
				}
				dst, err = filepath.Rel(cwd, root)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := c.MaterializePlugins(dst, []string{"homonto-workflow"}); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "homonto-workflow", "plugin.ts")
			want, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(outside, "sentinel")
			if err := os.WriteFile(sentinel, []byte("outside bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(source); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(sentinel, source); err != nil {
				t.Fatal(err)
			}
			before := pluginPathSnapshot(t, outside)
			if err := c.MaterializePlugins(dst, []string{"homonto-workflow"}); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Lstat(source); err != nil || !info.Mode().IsRegular() {
				t.Fatalf("entrypoint not repaired: %v", err)
			}
			if data, err := os.ReadFile(source); err != nil || string(data) != string(want) {
				t.Fatalf("incorrect repaired source: %v", err)
			}
			if !reflect.DeepEqual(before, pluginPathSnapshot(t, outside)) {
				t.Fatal("file-link repair modified outside data")
			}
		})
	}
}
