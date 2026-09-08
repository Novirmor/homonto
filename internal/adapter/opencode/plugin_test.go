package opencode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/jsonutil"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/tidwall/gjson"
)

func writeCfg(t *testing.T, home, body string) string {
	t.Helper()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func pluginArray(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, _ := jsonutil.Standardize(raw)
	var out []string
	for _, v := range gjson.GetBytes(doc, "plugin").Array() {
		out = append(out, v.String())
	}
	return out
}

func disabled() config.Plugin {
	off := false
	return config.Plugin{Source: "@x/quota", Enabled: &off}
}

func TestBundledPluginSourcesResolveToEntrypointFiles(t *testing.T) {
	for _, name := range []string{"permission-observer", "homonto-workflow"} {
		t.Run(name, func(t *testing.T) {
			home, catalog := t.TempDir(), t.TempDir()
			cfgPath := writeCfg(t, home, `{"plugin":["foreign"]}`)
			a := New(home, t.TempDir()).WithPluginCatalogRoot(catalog)
			st, err := state.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			// Previously projected directory entries are real persisted state;
			// normal reconciliation must replace just that managed entry.
			old := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{name: {Source: filepath.Join(catalog, name)}}}}
			cs, err := a.Plan(old, st)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Apply(old, cs, noSecret(), st); err != nil {
				t.Fatal(err)
			}
			oldKey := "plugin." + filepath.Join(catalog, name)
			previous, _ := st.Get("opencode", oldKey)
			st.Delete("opencode", oldKey)
			st.Set("opencode", "plugin."+name, previous.Desired, previous.Applied)
			c := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{name: {Source: name}}}}
			cs, err = a.Plan(c, st)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Apply(c, cs, noSecret(), st); err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(catalog, name, "plugin.ts")
			got := pluginArray(t, cfgPath)
			if len(got) != 2 || got[0] != "foreign" || got[1] != want {
				t.Fatalf("entrypoints = %v, want foreign and %q", got, want)
			}
			if entry, ok := st.Get("opencode", "plugin."+name); !ok || entry.Desired != `"`+want+`"` {
				t.Fatal("entrypoint not tracked")
			}
			if _, ok := st.Get("opencode", "plugin."+filepath.Join(catalog, name)); ok {
				t.Fatal("obsolete directory still tracked")
			}
			cs, err = a.Plan(c, st)
			if err != nil {
				t.Fatal(err)
			}
			for _, ch := range cs.Changes {
				if ch.Action != "noop" {
					t.Fatalf("not idempotent: %+v", ch)
				}
			}
		})
	}
}

// An enabled plugin's source is appended to the array without duplicating
// existing entries.
func TestOpenCodeEnabledPluginAppendedNoDup(t *testing.T) {
	home := t.TempDir()
	p := writeCfg(t, home, `{"plugin":["existing"]}`)
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{"quota": {Source: "@x/quota"}}}}

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	got := pluginArray(t, p)
	if len(got) != 2 || got[0] != "existing" || got[1] != "@x/quota" {
		t.Fatalf("plugin array = %v; want [existing @x/quota]", got)
	}
	// Idempotent: a second apply must not duplicate.
	cs2, _ := a.Plan(c, st)
	if err := a.Apply(c, cs2, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	if got := pluginArray(t, p); len(got) != 2 {
		t.Fatalf("plugin array after re-apply = %v; want no duplicate", got)
	}
}

// A plugin that was managed+present and is now declared disabled is removed
// from the array; unmanaged siblings survive.
func TestOpenCodeDisabledManagedPluginRemoved(t *testing.T) {
	home := t.TempDir()
	p := writeCfg(t, home, `{"plugin":["existing"]}`)
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())

	// First, adopt the plugin as enabled (managed, present).
	on := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{"quota": {Source: "@x/quota"}}}}
	cs, err := a.Plan(on, st)
	if err != nil {
		t.Fatalf("plan enabled: %v", err)
	}
	if err := a.Apply(on, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Get("opencode", "plugin.@x/quota"); !ok {
		t.Fatal("precondition: plugin not recorded in state after enable")
	}

	// Now declare it disabled: plan must remove it.
	off := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{"quota": disabled()}}}
	cs2, err := a.Plan(off, st)
	if err != nil {
		t.Fatalf("plan disabled: %v", err)
	}
	if findChange(cs2, "delete", "plugin.@x/quota") == nil {
		t.Fatalf("disabled managed plugin: expected delete, got %+v", cs2.Changes)
	}
	if err := a.Apply(off, cs2, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	got := pluginArray(t, p)
	if len(got) != 1 || got[0] != "existing" {
		t.Fatalf("plugin array after disable = %v; want [existing]", got)
	}
	if _, ok := st.Get("opencode", "plugin.@x/quota"); ok {
		t.Fatal("state still records the disabled plugin")
	}
}

// A disabled plugin whose source is not on disk is a noop: no create, no delete.
func TestOpenCodeDisabledPluginAbsentIsNoop(t *testing.T) {
	home := t.TempDir()
	writeCfg(t, home, `{"plugin":["existing"]}`)
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{"quota": disabled()}}}

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if findChange(cs, "create", "plugin.@x/quota") != nil {
		t.Fatalf("disabled absent plugin must not be created: %+v", cs.Changes)
	}
	if findChange(cs, "delete", "plugin.@x/quota") != nil {
		t.Fatalf("disabled absent plugin must not be deleted: %+v", cs.Changes)
	}
}

// A disabled plugin whose source is present on disk but UNMANAGED (not in
// state) must never be removed.
func TestOpenCodeDisabledUnmanagedEntryPreserved(t *testing.T) {
	home := t.TempDir()
	p := writeCfg(t, home, `{"plugin":["@x/quota","other"]}`)
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir()) // empty state → @x/quota is unmanaged
	c := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{"quota": disabled()}}}

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if findChange(cs, "delete", "plugin.@x/quota") != nil {
		t.Fatalf("unmanaged entry must not be deleted: %+v", cs.Changes)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	got := pluginArray(t, p)
	if len(got) != 2 || got[0] != "@x/quota" || got[1] != "other" {
		t.Fatalf("plugin array = %v; unmanaged entries must survive", got)
	}
}
