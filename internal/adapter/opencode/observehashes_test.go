package opencode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/jsonutil"
	"github.com/noviopenworks/homonto/internal/state"
)

// applyObserveCfg applies a config touching every recorded prefix
// (setting./plugin./skill.) and returns the adapter + populated state.
func applyObserveCfg(t *testing.T, home, content string) (*Adapter, *state.State) {
	t.Helper()
	os.MkdirAll(filepath.Join(content, "skills", "onto"), 0o755)

	a := New(home, content)
	st, _ := state.Load(t.TempDir())
	c := cfgWithSkills("user", "onto")
	c.Settings = config.Settings{OpenCode: map[string]any{"theme": "dark"}}
	c.Plugins = config.Plugins{OpenCode: map[string]config.Plugin{"quota": {Source: "@slkiser/opencode-quota"}}}
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return a, st
}

// TestOpenCodeObserveHashesUnchangedEqualsApplied is the load-bearing
// correctness test: every recorded key still on disk must reproduce the exact
// hash apply stored in Entry.Applied — including plugin membership.
func TestOpenCodeObserveHashesUnchangedEqualsApplied(t *testing.T) {
	home, content := t.TempDir(), t.TempDir()
	a, st := applyObserveCfg(t, home, content)

	obs, err := a.ObserveHashes(st)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	keys := st.Keys("opencode")
	sawSetting, sawPlugin, sawSkill := false, false, false
	for _, key := range keys {
		e, _ := st.Get("opencode", key)
		h, ok := obs[key]
		if !ok {
			t.Fatalf("recorded key %q missing from ObserveHashes", key)
		}
		if h != e.Applied {
			t.Fatalf("key %q: observed %q != Applied %q", key, h, e.Applied)
		}
		switch key {
		case "setting.theme":
			sawSetting = true
		case "plugin.@slkiser/opencode-quota":
			sawPlugin = true
		case "skill.onto":
			sawSkill = true
		}
	}
	if !(sawSetting && sawPlugin && sawSkill) {
		t.Fatalf("expected setting/plugin/skill keys exercised, got %v", keys)
	}
}

func TestOpenCodeObserveHashesEditedDiffers(t *testing.T) {
	home, content := t.TempDir(), t.TempDir()
	a, st := applyObserveCfg(t, home, content)
	before, _ := a.ObserveHashes(st)

	// Out-of-band edit of the managed setting value, keeping other keys.
	doc, _ := readStandardized(a.cfgFile())
	newDoc, err := jsonutil.SetJSON(doc, "theme", "light")
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	os.WriteFile(a.cfgFile(), newDoc, 0o644)

	after, err := a.ObserveHashes(st)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	if after["setting.theme"] == before["setting.theme"] {
		t.Fatalf("edited setting.theme hash unchanged: %q", after["setting.theme"])
	}
	e, _ := st.Get("opencode", "setting.theme")
	if after["setting.theme"] == e.Applied {
		t.Fatalf("edited setting.theme still equals Applied")
	}
}

func TestOpenCodeObserveHashesAbsentOmitted(t *testing.T) {
	home, content := t.TempDir(), t.TempDir()
	a, st := applyObserveCfg(t, home, content)

	// Wipe the config file: every JSON-backed key vanishes from disk.
	os.WriteFile(a.cfgFile(), []byte(`{}`), 0o644)

	obs, err := a.ObserveHashes(st)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	for _, key := range []string{"setting.theme", "plugin.@slkiser/opencode-quota"} {
		if _, ok := obs[key]; ok {
			t.Fatalf("deleted %q must be omitted, got %q", key, obs[key])
		}
	}
	// Remove the skill link too.
	os.Remove(filepath.Join(home, ".config", "opencode", "skills", "onto"))
	obs2, _ := a.ObserveHashes(st)
	if _, ok := obs2["skill.onto"]; ok {
		t.Fatal("removed skill.onto link must be omitted")
	}
}
