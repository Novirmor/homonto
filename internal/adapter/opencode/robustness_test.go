package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/jsonutil"
	"github.com/noviopenworks/homonto/internal/plan"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/tidwall/gjson"
)

// TestForeignSkillSymlinkAborts is the adapter/apply-level guard for the
// release blocker: a symlink in the skills dir pointing OUTSIDE managed content
// is user-owned. Plan must report a conflict, leaving the foreign symlink
// untouched — homonto never repoints or removes what it does not own.
func TestForeignSkillSymlinkAborts(t *testing.T) {
	home := t.TempDir()
	content := t.TempDir()
	os.MkdirAll(filepath.Join(content, "skills", "onto"), 0o755)

	foreign := filepath.Join(home, "dotfiles", "onto")
	os.MkdirAll(foreign, 0o755)
	dst := filepath.Join(home, ".config", "opencode", "skills", "onto")
	os.MkdirAll(filepath.Dir(dst), 0o755)
	if err := os.Symlink(foreign, dst); err != nil {
		t.Fatal(err)
	}

	a := New(home, content)
	st, _ := state.Load(t.TempDir())
	c := cfgWithSkills("user", "onto")

	if _, err := a.Plan(c, st); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("plan must conflict on a foreign skill symlink, got %v", err)
	}
	if got, _ := os.Readlink(dst); got != foreign {
		t.Fatalf("plan changed the foreign symlink: now points to %q, want %q", got, foreign)
	}
}

// objKeys returns the immediate member names of the object at root.
func objKeys(t *testing.T, path, root string) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	doc, err := jsonutil.Standardize(raw)
	if err != nil {
		t.Fatalf("standardize %s: %v", path, err)
	}
	out := map[string]bool{}
	gjson.GetBytes(doc, root).ForEach(func(k, v gjson.Result) bool {
		out[k.String()] = true
		return true
	})
	return out
}

// TestOpenCodeDottedNamesLandAsLiteralKeysAndConverge reproduces the review's
// path-injection finding for opencode: a setting key "a.b" must land as a
// literal key, not a nested object — and a
// second plan after apply must be all noop.
func TestOpenCodeDottedNamesLandAsLiteralKeysAndConverge(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"a.b": "v"}},
	}

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}

	cfgPath := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	root := objKeys(t, cfgPath, "@this")
	if !root["a.b"] {
		t.Fatalf("literal setting key %q missing; got %v", "a.b", root)
	}
	if root["a"] {
		t.Fatalf("dotted setting key nested into an %q object: %v", "a", root)
	}

	cs2, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("second plan: %v", err)
	}
	for _, ch := range cs2.Changes {
		if ch.Action != "noop" {
			t.Fatalf("second plan must be all noop (convergence), got %s %s", ch.Action, ch.Key)
		}
	}
}

// TestOpenCodePlanRenderIsDeterministic reproduces the review's
// map-iteration finding: two plans over the same input must render
// identically.
func TestOpenCodePlanRenderIsDeterministic(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := cfgWithSkills("user", "one", "two", "three")
	c.Settings = config.Settings{OpenCode: map[string]any{"model": "x", "theme": "dark", "alpha": "a", "bravo": "b", "charlie": "c", "delta": "d"}}
	var first string
	for i := 0; i < 20; i++ {
		cs, err := a.Plan(c, st)
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		out := plan.Render([]adapter.ChangeSet{cs})
		if i == 0 {
			first = out
			continue
		}
		if out != first {
			t.Fatalf("plan render differs between runs:\n--- run 0 ---\n%s--- run %d ---\n%s", first, i, out)
		}
	}
}

// TestOpenCodeNonObjectRootIsAnError reproduces the review's finding: a
// managed file whose root is valid JSON but not an object (here an array)
// must be a clear error naming the file, not silent downstream corruption.
func TestOpenCodeNonObjectRootIsAnError(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(p, []byte(`[]`), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{Settings: config.Settings{OpenCode: map[string]any{"model": "x"}}}
	_, err := a.Plan(c, st)
	if err == nil {
		t.Fatal("plan accepted an array-rooted opencode.jsonc; want error")
	}
	if !strings.Contains(err.Error(), p) {
		t.Fatalf("error does not name the file %s: %v", p, err)
	}
	if !strings.Contains(err.Error(), "array") {
		t.Fatalf("error does not name the root kind: %v", err)
	}
}
