package workspace

import (
	"path/filepath"
	"testing"
)

func TestSourceDirsForLayoutExactConfig(t *testing.T) {
	for _, schema := range []string{"", "schema_version = 2\n"} {
		t.Run(schema, func(t *testing.T) {
			root := t.TempDir()
			repo := wtRepo(t, root, "source", "main")
			cfg := filepath.Join(root, "selected.toml")
			wtWrite(t, cfg, schema+"[workflow]\nroot = 'records'\n[repos]\napp = 'source'\n")
			wtWrite(t, filepath.Join(root, "homonto.toml"), "invalid = [")
			l, err := Load(cfg)
			if err != nil {
				t.Fatal(err)
			}
			dirs, err := SourceDirsForLayout(l, "to", "demo", []string{"app"})
			if err != nil {
				t.Fatal(err)
			}
			if dirs["app"] != repo {
				t.Fatalf("sources = %v", dirs)
			}
			_, implicit := dirs[""]
			if implicit != (schema == "") {
				t.Fatalf("implicit scope = %v", dirs)
			}
			if _, err := SourceDirs(root, "to", "demo", []string{"app"}); err == nil {
				t.Fatal("wrapper stopped loading homonto.toml")
			}
			for _, name := range []string{"../demo", "archive", "demo/x"} {
				if _, err := SourceDirsForLayout(l, "to", name, nil); err == nil {
					t.Fatalf("accepted %q", name)
				}
			}
		})
	}
}
