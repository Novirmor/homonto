package catalog

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"
	"testing/fstest"

	embedded "github.com/noviopenworks/homonto/catalog"
)

func matFS() fstest.MapFS {
	return fstest.MapFS{
		"version.txt": {Data: []byte("0.1.0")},
		"frameworks/sp/framework.toml": {Data: []byte(`name = "sp"
version = "0.1.0"
[skills]
brainstorming = "skills/brainstorming"
[commands]
demo-cmd = "commands/demo-cmd.md"
`)},
		"skills/brainstorming/SKILL.md":            {Data: []byte("top")},
		"skills/brainstorming/references/notes.md": {Data: []byte("nested")},
		"commands/demo-cmd.md":                     {Data: []byte("command body")},
	}
}

func TestMaterializeWritesNestedContent(t *testing.T) {
	c, err := Load(matFS())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dst := t.TempDir()
	if err := c.Materialize(dst, []string{"brainstorming"}, "none", "none"); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "brainstorming", "SKILL.md")); string(b) != "top" {
		t.Fatalf("SKILL.md = %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "brainstorming", "references", "notes.md")); string(b) != "nested" {
		t.Fatalf("nested references/notes.md = %q", b)
	}
}

func TestMaterializeRemovesStaleOnUpgrade(t *testing.T) {
	c, err := Load(matFS())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dst := t.TempDir()
	// Pre-seed a stale file that the new content does not include.
	os.MkdirAll(filepath.Join(dst, "brainstorming"), 0o755)
	os.WriteFile(filepath.Join(dst, "brainstorming", "STALE.md"), []byte("old"), 0o644)

	if err := c.Materialize(dst, []string{"brainstorming"}, "none", "none"); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "brainstorming", "STALE.md")); !os.IsNotExist(err) {
		t.Fatal("stale file survived materialization")
	}
}

func TestMaterializeUnknownSkillErrors(t *testing.T) {
	c, _ := Load(matFS())
	if err := c.Materialize(t.TempDir(), []string{"nope"}, "none", "none"); err == nil {
		t.Fatal("expected error for unknown skill")
	}
}

func TestMaterializeCommandsWritesFile(t *testing.T) {
	c, err := Load(matFS())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dst := t.TempDir()
	if err := c.MaterializeCommands(dst, []string{"demo-cmd"}); err != nil {
		t.Fatalf("materialize commands: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "demo-cmd.md")); string(b) != "command body" {
		t.Fatalf("demo-cmd.md = %q", b)
	}
}

func TestMaterializeCommandsOverwrites(t *testing.T) {
	c, _ := Load(matFS())
	dst := t.TempDir()
	os.WriteFile(filepath.Join(dst, "demo-cmd.md"), []byte("STALE"), 0o644)
	if err := c.MaterializeCommands(dst, []string{"demo-cmd"}); err != nil {
		t.Fatalf("materialize commands: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "demo-cmd.md")); string(b) != "command body" {
		t.Fatalf("stale command not overwritten: %q", b)
	}
}

func TestMaterializeCommandsUnknownErrors(t *testing.T) {
	c, _ := Load(matFS())
	if err := c.MaterializeCommands(t.TempDir(), []string{"nope"}); err == nil {
		t.Fatal("expected error for unknown command")
	}
}

func TestMaterializeSubagentsWritesFileVerbatim(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dst := t.TempDir()
	if err := c.MaterializeSubagents(dst, []string{"onto-reviewer"}, nil); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "onto-reviewer.md"))
	if err != nil {
		t.Fatalf("read materialized: %v", err)
	}
	sp, _ := c.SubagentPath("onto-reviewer")
	want, err := fs.ReadFile(embedded.FS, sp)
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("materialized subagent is not byte-for-byte identical to catalog source")
	}
}

// onto-reviewer ships a neutral homonto: access block, so materialize must
// also emit the rendered OpenCode variant: a permission: map spelling the
// block's denials, which cannot share the anchor file with the neutral source.
func TestMaterializeSubagentsWritesOpenCodeVariant(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	dst := t.TempDir()
	if err := c.MaterializeSubagents(dst, []string{"onto-reviewer"}, nil); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	oc, err := os.ReadFile(filepath.Join(dst, "onto-reviewer.opencode.md"))
	if err != nil {
		t.Fatalf("opencode variant not written: %v", err)
	}
	if !bytes.Contains(oc, []byte("permission:")) || !bytes.Contains(oc, []byte("edit: deny")) || bytes.Contains(oc, []byte("tools:")) {
		t.Errorf("opencode variant should carry an edit-deny permission block and no tools string:\n%s", oc)
	}
	// The neutral block must not leak into the rendered variant.
	if bytes.Contains(oc, []byte("homonto:")) {
		t.Error("homonto: block leaked into a rendered variant")
	}
}

func TestMaterializeSubagentsUnknownErrors(t *testing.T) {
	c, _ := New()
	if err := c.MaterializeSubagents(t.TempDir(), []string{"nope"}, nil); err == nil {
		t.Fatal("expected error for unknown subagent")
	}
}

// A catalog upgrade can turn a rendered agent verbatim (homonto: block
// removed). Materialize used to remove a stale variant only in the render
// path — a verbatim transition removed nothing and left the old
// <name>.<tool>.md behind, and OpenCode PREFERS the variant when it exists,
// so the stale render silently won forever, invisible to the gate and doctor.
func TestMaterializeSubagentsRemovesStaleVariantsOnVerbatimTransition(t *testing.T) {
	// Every shipped subagent carries a homonto: block, so the verbatim path is
	// pinned with a fixture framework whose agent has none.
	m := matFS()
	m["frameworks/sp/framework.toml"] = &fstest.MapFile{Data: []byte(`name = "sp"
version = "0.1.0"
[subagents]
nav = "subagents/nav.md"
`)}
	m["subagents/nav.md"] = &fstest.MapFile{Data: []byte("---\ndescription: verbatim agent\n---\nbody\n")}
	c, err := Load(m)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	dst := t.TempDir()
	// Simulate the previous version's render output for an agent whose new
	// content is verbatim (no homonto: block).
	if err := os.WriteFile(filepath.Join(dst, "nav.opencode.md"), []byte("stale render"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.MaterializeSubagents(dst, []string{"nav"}, nil); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "nav.opencode.md")); !os.IsNotExist(err) {
		t.Errorf("stale variant survived a verbatim transition: nav.opencode.md")
	}
	if _, err := os.Stat(filepath.Join(dst, "nav.md")); err != nil {
		t.Errorf("the verbatim anchor must be written: %v", err)
	}
}

// TestSubagentFilesMatchesWhatMaterializeWrites keeps the engine's version gate
// honest: it skips materializing when every file SubagentFiles names is already
// present, so any drift between the two would either re-materialize forever or
// (worse) leave a variant the gate never checks unrepaired.
func TestSubagentFilesMatchesWhatMaterializeWrites(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, name := range []string{"onto-reviewer", "onto"} {
		dst := t.TempDir()
		if err := c.MaterializeSubagents(dst, []string{name}, nil); err != nil {
			t.Fatalf("materialize %s: %v", name, err)
		}
		want, err := c.SubagentFiles(name, nil)
		if err != nil {
			t.Fatalf("SubagentFiles %s: %v", name, err)
		}
		entries, err := os.ReadDir(dst)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]string, 0, len(entries))
		for _, e := range entries {
			got = append(got, e.Name())
		}
		sort.Strings(got)
		sorted := append([]string(nil), want...)
		sort.Strings(sorted)
		if !slices.Equal(got, sorted) {
			t.Errorf("%s: SubagentFiles = %v, materialize actually wrote %v", name, sorted, got)
		}
	}
}
