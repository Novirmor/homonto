package opencode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/jsonutil"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/tidwall/gjson"
)

func findChange(cs adapter.ChangeSet, action adapter.Action, key string) *adapter.Change {
	for i, c := range cs.Changes {
		if c.Action == action && c.Key == key {
			return &cs.Changes[i]
		}
	}
	return nil
}

// Deep review CRITICAL: a de-declared structured key stayed in opencode.jsonc
// forever — no delete action existed. It must be planned as a delete, pruned
// from disk, and garbage-collected from state.
func TestOpenCodeRemovedSettingIsPruned(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"share": "manual"}},
	}

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}

	cs2, err := a.Plan(&config.Config{}, st)
	if err != nil {
		t.Fatal(err)
	}
	del := findChange(cs2, "delete", "setting.share")
	if del == nil {
		t.Fatalf("plan lacks a delete for the de-declared setting: %+v", cs2.Changes)
	}
	if del.Old != adapter.SecretRedaction {
		t.Fatalf("delete Old must always be redacted (stale provenance), got %q", del.Old)
	}
	if err := a.Apply(&config.Config{}, cs2, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".config", "opencode", "opencode.jsonc"))
	doc, _ := jsonutil.Standardize(raw)
	if gjson.GetBytes(doc, "share").Exists() {
		t.Fatalf("de-declared setting still on disk: %s", doc)
	}
	if _, ok := st.Get("opencode", "setting.share"); ok {
		t.Fatal("state still records the removed setting")
	}
}

// A de-declared plugin must be removed from the plugin array while unmanaged
// elements survive.
func TestOpenCodeRemovedPluginIsRemovedFromArray(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte(`{"plugin":["existing"]}`), 0o644)

	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{"quota": {Source: "@x/quota"}}}}

	cs, _ := a.Plan(c, st)
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}

	cs2, err := a.Plan(&config.Config{}, st)
	if err != nil {
		t.Fatal(err)
	}
	if findChange(cs2, "delete", "plugin.@x/quota") == nil {
		t.Fatalf("plan lacks a delete for the de-declared plugin: %+v", cs2.Changes)
	}
	if err := a.Apply(&config.Config{}, cs2, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "opencode.jsonc"))
	doc, _ := jsonutil.Standardize(raw)
	arr := gjson.GetBytes(doc, "plugin").Array()
	if len(arr) != 1 || arr[0].String() != "existing" {
		t.Fatalf("plugin array after prune = %s", gjson.GetBytes(doc, "plugin").Raw)
	}
	if _, ok := st.Get("opencode", "plugin.@x/quota"); ok {
		t.Fatal("state still records the removed plugin")
	}
}

// Removing a skill from skills.own must prune its symlink here too.
func TestOpenCodeRemovedSkillLinkIsPruned(t *testing.T) {
	home := t.TempDir()
	catalogRoot := t.TempDir()
	os.MkdirAll(filepath.Join(catalogRoot, "foo"), 0o755)
	a := New(home, t.TempDir()).WithCatalogRoot(catalogRoot)
	st, _ := state.Load(t.TempDir())
	c := &config.Config{Skills: map[string]config.Resource{"foo": {Source: "builtin:foo", Scope: "user"}}}

	cs, _ := a.Plan(c, st)
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(home, ".config", "opencode", "skills", "foo")
	if _, err := os.Lstat(dst); err != nil {
		t.Fatalf("precondition: skill link missing: %v", err)
	}

	cs2, err := a.Plan(&config.Config{}, st)
	if err != nil {
		t.Fatal(err)
	}
	if findChange(cs2, "delete", "skill.foo") == nil {
		t.Fatalf("plan lacks a delete for the removed skill: %+v", cs2.Changes)
	}
	if err := a.Apply(&config.Config{}, cs2, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatal("dangling skill symlink not removed")
	}
	if _, ok := st.Get("opencode", "skill.foo"); ok {
		t.Fatal("state still records the removed skill")
	}
}

// TestOpenCodeReleasesOwnSkillLink covers the bring-your-own handover
// (ADR 0085): a link to the user's own content (an earlier local: skill or
// command) leaves state, and the symlink stays exactly where it was.
func TestOpenCodeReleasesOwnSkillLink(t *testing.T) {
	// User scope records absolute targets; project scope records relative
	// ones (ADR 0026). Both must be recognised as the user's own content.
	for _, scope := range []string{"user", "project"} {
		t.Run(scope, func(t *testing.T) {
			home, proj := t.TempDir(), t.TempDir()
			content := filepath.Join(proj, "homonto")
			os.MkdirAll(filepath.Join(content, "skills", "foo"), 0o755)
			os.MkdirAll(filepath.Join(content, "commands"), 0o755)
			os.WriteFile(filepath.Join(content, "commands", "bar.md"), []byte("body\n"), 0o644)
			a := New(home, content).WithProjectRoot(proj)
			st, _ := state.Load(t.TempDir())
			c := cfgWithSkills(scope, "foo")
			c.Commands = map[string]config.Resource{"bar": {Source: "local:bar", Scope: scope}}
			cs, _ := a.Plan(c, st)
			if err := a.Apply(c, cs, noSecret(), st); err != nil {
				t.Fatal(err)
			}
			links := map[string]string{
				"skill.foo":   filepath.Join(a.SkillsDir(scope), "foo"),
				"command.bar": filepath.Join(a.CommandsDir(scope), "bar.md"),
			}
			before := map[string]string{}
			for key, dst := range links {
				target, err := os.Readlink(dst)
				if err != nil {
					t.Fatalf("precondition: %s link missing: %v", key, err)
				}
				before[key] = target
			}

			cs2, err := a.Plan(&config.Config{}, st)
			if err != nil {
				t.Fatal(err)
			}
			for key := range links {
				if ch := findChange(cs2, "delete", key); ch == nil || ch.Cause != adapter.CauseRelease {
					t.Fatalf("%s not released: %+v", key, cs2.Changes)
				}
			}
			if err := a.Apply(&config.Config{}, cs2, noSecret(), st); err != nil {
				t.Fatal(err)
			}
			for key, dst := range links {
				if target, err := os.Readlink(dst); err != nil || target != before[key] {
					t.Fatalf("released link %s changed: %q, %v", dst, target, err)
				}
				if _, ok := st.Get("opencode", key); ok {
					t.Fatalf("state still records released %s", key)
				}
			}
			if cs3, err := a.Plan(&config.Config{}, st); err != nil || len(cs3.Changes) != 0 {
				t.Fatalf("released links still planned: %+v, %v", cs3.Changes, err)
			}
		})
	}
}
