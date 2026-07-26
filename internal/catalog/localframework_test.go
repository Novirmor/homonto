package catalog

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// localFrameworkFS is a local single-framework ROOT: framework.toml at the root
// (not under frameworks/<name>/) with framework-root-relative resource paths.
func localFrameworkFS() fstest.MapFS {
	return fstest.MapFS{
		"framework.toml":          {Data: []byte("name = \"myfw\"\nversion = \"0.1.0\"\n[skills]\nmyskill = \"skills/myskill\"\n")},
		"skills/myskill/SKILL.md": {Data: []byte("local skill body")},
		"commands/mycmd.md":       {Data: []byte("local cmd body")},
	}
}

func TestLoadWithLocal_MergesAndMaterializesFromLocalFS(t *testing.T) {
	local := fstest.MapFS{
		"framework.toml":          {Data: []byte("name = \"myfw\"\nversion = \"0.1.0\"\n[skills]\nmyskill = \"skills/myskill\"\n")},
		"skills/myskill/SKILL.md": {Data: []byte("local skill body")},
	}
	c, err := LoadWithLocal(baseFS(), map[string]fs.FS{"myfw": local})
	if err != nil {
		t.Fatalf("LoadWithLocal: %v", err)
	}
	if _, ok := c.Framework("base"); !ok {
		t.Error("base framework missing")
	}
	if _, ok := c.Framework("myfw"); !ok {
		t.Error("local framework myfw missing")
	}
	sk, err := c.Expand([]string{"myfw"})
	if err != nil || len(sk) != 1 || sk[0].Name != "myskill" {
		t.Fatalf("Expand(myfw) = %v, %v; want myskill", sk, err)
	}

	// The local framework's skill materializes from its own FS, not the base.
	dst := t.TempDir()
	if err := c.Materialize(dst, []string{"myskill"}, "none", "none"); err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "myskill", "SKILL.md"))
	if err != nil {
		t.Fatalf("materialized skill missing: %v", err)
	}
	if string(got) != "local skill body" {
		t.Errorf("materialized content = %q, want %q", got, "local skill body")
	}
}

func TestLoadWithLocal_NoLocalsIdenticalToLoad(t *testing.T) {
	b := baseFS()
	c, err := LoadWithLocal(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Base skill still resolves and materializes from the base FS unchanged.
	if _, ok := c.Framework("base"); !ok {
		t.Fatal("base framework should load with no locals")
	}
	dst := t.TempDir()
	if err := c.Materialize(dst, []string{"baseskill"}, "none", "none"); err != nil {
		t.Fatalf("Materialize base skill: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "baseskill", "SKILL.md"))
	if err != nil || string(got) != "base" {
		t.Fatalf("base skill materialize = %q, %v; want \"base\"", got, err)
	}
}

func TestLoadWithLocal_LocalNameMismatchErrors(t *testing.T) {
	local := localFrameworkFS()
	// Key it under a different name than framework.toml declares.
	_, err := LoadWithLocal(baseFS(), map[string]fs.FS{"other": local})
	if err == nil {
		t.Fatal("a local framework whose declared name != key should error")
	}
}
