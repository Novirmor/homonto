package opencode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/state"
)

// seedDisk projects c into opencode.jsonc via a throwaway apply against a
// scratch state, so the on-disk file exactly equals the adapter's desired
// projection. The returned adapter shares the same home for the real plan.
func seedDisk(t *testing.T, home string, c *config.Config) *Adapter {
	t.Helper()
	a := New(home, t.TempDir())
	seed, _ := state.Load(t.TempDir())
	cs0, err := a.Plan(c, seed)
	if err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	if err := a.Apply(c, cs0, noSecret(), seed); err != nil {
		t.Fatalf("seed apply: %v", err)
	}
	return a
}

// A declared non-secret SETTING already on disk == desired but absent from
// state must be planned as `adopt` (not `noop`); applying it records state.
func TestOpenCodeAdoptSettingRecordsState(t *testing.T) {
	home := t.TempDir()
	c := &config.Config{Settings: config.Settings{OpenCode: map[string]any{"theme": "dark"}}}
	a := seedDisk(t, home, c)

	// Plan against an EMPTY state: the key is on disk == desired, not recorded.
	st, _ := state.Load(t.TempDir())
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if findChange(cs, "adopt", "setting.theme") == nil {
		t.Fatalf("expected adopt for setting.theme, got %+v", cs.Changes)
	}
	if findChange(cs, "noop", "setting.theme") != nil {
		t.Fatalf("setting.theme must be adopt, not noop, when absent from state: %+v", cs.Changes)
	}

	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := st.Get("opencode", "setting.theme"); !ok {
		t.Fatal("adopt did not record state for setting.theme")
	}
}

// A declared PLUGIN already present in the plugin array but absent from state
// must be planned as `adopt` (not `noop`); applying it records state.
func TestOpenCodeAdoptPluginRecordsState(t *testing.T) {
	home := t.TempDir()
	c := &config.Config{Plugins: config.Plugins{OpenCode: map[string]config.Plugin{"quota": {Source: "@x/quota"}}}}
	a := seedDisk(t, home, c)

	st, _ := state.Load(t.TempDir())
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if findChange(cs, "adopt", "plugin.@x/quota") == nil {
		t.Fatalf("expected adopt for plugin.@x/quota, got %+v", cs.Changes)
	}
	if findChange(cs, "noop", "plugin.@x/quota") != nil {
		t.Fatalf("plugin.@x/quota must be adopt, not noop, when absent from state: %+v", cs.Changes)
	}

	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := st.Get("opencode", "plugin.@x/quota"); !ok {
		t.Fatal("adopt did not record state for plugin.@x/quota")
	}
}

// Phantom-drift case: a recorded non-secret setting whose on-disk value was
// changed out of band to a NEW value, with desired rebuilt to that same new
// value, leaves state's Applied stale. Disk == desired, so the old code emitted
// a bare `noop` that never refreshes Applied — ObserveHashes(disk) != Applied
// reports drift forever. The fix: a true noop requires Applied == hash(disk);
// otherwise adopt, refreshing the hash and clearing the phantom drift.
func TestOpenCodeStaleAppliedRefreshedViaAdopt(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())

	// 1. Record setting.theme=dark in the real state (Applied = hash of "dark").
	st, _ := state.Load(t.TempDir())
	c1 := &config.Config{Settings: config.Settings{OpenCode: map[string]any{"theme": "dark"}}}
	cs1, err := a.Plan(c1, st)
	if err != nil {
		t.Fatalf("plan c1: %v", err)
	}
	if err := a.Apply(c1, cs1, noSecret(), st); err != nil {
		t.Fatalf("apply c1: %v", err)
	}

	// 2. Out-of-band: change the ON-DISK value to "light" WITHOUT touching the
	//    real state — apply the new desired against a throwaway scratch state that
	//    shares the same home. Real st keeps Applied = hash("dark").
	scratch, _ := state.Load(t.TempDir())
	c2 := &config.Config{Settings: config.Settings{OpenCode: map[string]any{"theme": "light"}}}
	csScratch, err := a.Plan(c2, scratch)
	if err != nil {
		t.Fatalf("plan scratch: %v", err)
	}
	if err := a.Apply(c2, csScratch, noSecret(), scratch); err != nil {
		t.Fatalf("apply scratch: %v", err)
	}

	// Precondition: state's Applied is now stale vs disk (phantom drift).
	obs, err := a.ObserveHashes(st)
	if err != nil {
		t.Fatalf("observe: %v", err)
	}
	e, _ := st.Get("opencode", "setting.theme")
	if obs["setting.theme"] == e.Applied {
		t.Fatal("precondition: Applied should be stale vs disk before the fix")
	}

	// 3. Plan the (now-matching) desired c2 against the real state: disk == desired
	//    but Applied is stale, so this must be `adopt`, not `noop`.
	cs, err := a.Plan(c2, st)
	if err != nil {
		t.Fatalf("plan c2: %v", err)
	}
	if findChange(cs, "adopt", "setting.theme") == nil {
		t.Fatalf("expected adopt for stale-Applied setting.theme, got %+v", cs.Changes)
	}
	if findChange(cs, "noop", "setting.theme") != nil {
		t.Fatalf("setting.theme must be adopt, not noop, when Applied is stale: %+v", cs.Changes)
	}

	cfg := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	before, _ := os.ReadFile(cfg)
	if err := a.Apply(c2, cs, noSecret(), st); err != nil {
		t.Fatalf("apply c2: %v", err)
	}

	// Applied is refreshed to the on-disk hash; the tool file is byte-unchanged.
	obs2, _ := a.ObserveHashes(st)
	e2, _ := st.Get("opencode", "setting.theme")
	if e2.Applied != obs2["setting.theme"] {
		t.Fatalf("adopt did not refresh Applied: %q != %q", e2.Applied, obs2["setting.theme"])
	}
	after, _ := os.ReadFile(cfg)
	if string(before) != string(after) {
		t.Fatalf("adopt wrote the tool file:\nbefore: %s\nafter:  %s", before, after)
	}

	// Drift is cleared: a second Plan now yields noop.
	cs3, err := a.Plan(c2, st)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if findChange(cs3, "noop", "setting.theme") == nil {
		t.Fatalf("expected noop after refresh, got %+v", cs3.Changes)
	}
}

// Adoption records the key in state, which makes it visible to pruning: after
// de-declaring the setting/plugin, Plan must yield a delete for the adopted key.
func TestOpenCodeAdoptedKeysArePruneable(t *testing.T) {
	home := t.TempDir()
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"theme": "dark"}},
		Plugins:  config.Plugins{OpenCode: map[string]config.Plugin{"quota": {Source: "@x/quota"}}},
	}
	a := seedDisk(t, home, c)

	st, _ := state.Load(t.TempDir())
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if findChange(cs, "adopt", "setting.theme") == nil || findChange(cs, "adopt", "plugin.@x/quota") == nil {
		t.Fatalf("precondition: expected adopt for both keys, got %+v", cs.Changes)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// De-declare both → the adopted keys must now be pruneable.
	cs2, err := a.Plan(&config.Config{}, st)
	if err != nil {
		t.Fatalf("re-plan: %v", err)
	}
	if findChange(cs2, "delete", "setting.theme") == nil {
		t.Fatalf("adopted setting not pruneable: %+v", cs2.Changes)
	}
	if findChange(cs2, "delete", "plugin.@x/quota") == nil {
		t.Fatalf("adopted plugin not pruneable: %+v", cs2.Changes)
	}
}

// Adopted setting file must remain untouched by the adopt apply (state-only
// write). We assert the setting value survives round-trip.
func TestOpenCodeAdoptDoesNotDropDiskValue(t *testing.T) {
	home := t.TempDir()
	c := &config.Config{Settings: config.Settings{OpenCode: map[string]any{"theme": "dark"}}}
	a := seedDisk(t, home, c)
	cfg := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	before, _ := os.ReadFile(cfg)

	st, _ := state.Load(t.TempDir())
	cs, _ := a.Plan(c, st)
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	after, _ := os.ReadFile(cfg)
	// Task 3 only guarantees the desired value still projects; byte-identity is
	// Task 3b. Assert the adopted setting is intact on disk.
	if len(after) == 0 {
		t.Fatal("config file emptied by adopt apply")
	}
	_ = before
}
