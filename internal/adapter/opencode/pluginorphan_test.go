package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/state"
)

// A disabled plugin recorded in state but already absent on disk (removed out
// of band) used to emit NO change: the state record stayed, the declared loop
// shielded it from the generic prune, ObserveHashes omitted it, and `homonto
// status` reported "plugin.X missing (deleted out of band)" on every run — with
// no apply able to clear it, short of deleting the [plugins] entry entirely.
// The delete must be emitted for any recorded entry, and dropping the orphaned
// record must NOT rewrite opencode.jsonc (a rewrite normalizes the JSONC and
// destroys comments for nothing).
func TestDisabledPluginOrphanedStateEntryClears(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := filepath.Join(dir, "opencode.jsonc")
	original := "{\n  // precious comment\n  \"theme\": \"x\"\n}"
	if err := os.WriteFile(jsonc, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	// The orphan precondition: recorded in state, absent from the doc.
	st.Set("opencode", "plugin.@x/quota", `"@x/quota"`, "hash")

	c := &config.Config{
		Plugins: config.Plugins{OpenCode: map[string]config.Plugin{
			"quota": disabled(),
		}},
	}
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}

	if _, still := st.Get("opencode", "plugin.@x/quota"); still {
		t.Error("orphaned plugin state record must be dropped by apply")
	}
	after, _ := os.ReadFile(jsonc)
	if string(after) != original {
		t.Errorf("dropping an orphaned record must not rewrite opencode.jsonc:\nbefore: %s\nafter:  %s", original, after)
	}

	// Steady state: the next plan emits nothing for the plugin.
	cs2, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range cs2.Changes {
		if ch.Key == "plugin.@x/quota" {
			t.Errorf("settled disabled plugin must plan no change, got %s %s", ch.Action, ch.Key)
		}
	}
}

// The normal disable path (present on disk AND recorded) must still remove the
// array entry and write the doc.
func TestDisabledPluginPresentOnDiskStillRemoved(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := filepath.Join(dir, "opencode.jsonc")
	if err := os.WriteFile(jsonc, []byte(`{"plugin":["@x/quota"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	st.Set("opencode", "plugin.@x/quota", `"@x/quota"`, "hash")

	c := &config.Config{
		Plugins: config.Plugins{OpenCode: map[string]config.Plugin{
			"quota": disabled(),
		}},
	}
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(jsonc)
	if len(after) == 0 || strings.Contains(string(after), "@x/quota") {
		t.Errorf("disabled plugin must be removed from the doc, got: %s", after)
	}
	if _, still := st.Get("opencode", "plugin.@x/quota"); still {
		t.Error("state record must be dropped with the array entry")
	}
}
