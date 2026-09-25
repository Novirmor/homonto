package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/tidwall/gjson"
)

func cliFixture(t *testing.T) (*Adapter, *state.State) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", "")
	a := New(t.TempDir(), t.TempDir()).WithProjectRoot(t.TempDir())
	st, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return a, st
}

func writeCLIFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCLIFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func applyCLI(t *testing.T, a *Adapter, st *state.State, c *config.Config) adapter.ChangeSet {
	t.Helper()
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestOpenCodeCLIMigratesPriorStateWithoutTouchingV1(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent cli", true: "existing cli"}[existing], func(t *testing.T) {
			a, st := cliFixture(t)
			legacy := "{\n // V1 stays as-is\n \"theme\":\"gruvbox\",\"scroll_speed\":3,\"diff_style\":\"stacked\",\"custom\":true\n}\n"
			writeCLIFile(t, a.tuiFile(), legacy)
			server := "{\n // preserve server syntax and comments\n \"model\":\"provider/model\"\n}\n"
			writeCLIFile(t, a.cfgFile(), server)
			st.Set("opencode", "tui.theme", `"gruvbox"`, secret.Hash(`"gruvbox"`))
			st.Set("opencode", "tui.scroll_speed", `3`, secret.Hash(`3`))
			st.Set("opencode", "tui.diff_style", `"stacked"`, secret.Hash(`"stacked"`))
			before, err := a.ObserveHashes(st)
			if err != nil || before["tui.theme"] != secret.Hash(`"gruvbox"`) {
				t.Fatalf("legacy observe: %v, %v", before, err)
			}
			if existing {
				writeCLIFile(t, a.cliFile(), `{"theme":{"name":"other","mode":"dark"},"scroll":{"acceleration":true},"tabs":{"mode":"off"},"plugins":["user-plugin"],"custom":{"keep":42}}`)
			}
			c := &config.Config{TUI: config.TUI{OpenCode: map[string]any{"theme": "gruvbox", "scroll_speed": int64(3)}}}
			cs := applyCLI(t, a, st, c)
			for _, key := range []string{"tui.theme", "tui.scroll_speed", "tui.diff_style"} {
				if _, ok := st.Get("opencode", key); ok {
					t.Fatalf("legacy state not retired: %s; %+v", key, cs)
				}
			}
			raw := readCLIFile(t, a.cliFile())
			if gjson.Get(raw, "theme.name").String() != "gruvbox" || gjson.Get(raw, "scroll.speed").Int() != 3 {
				t.Fatalf("legacy declarations not mapped: %s", raw)
			}
			for _, key := range []string{"scroll_speed", "diff_style"} {
				if gjson.Get(raw, key).Exists() {
					t.Fatalf("V1 field leaked to cli.json: %s", raw)
				}
			}
			if existing {
				for key, want := range map[string]string{"theme.mode": "dark", "scroll.acceleration": "true", "tabs.mode": "off", "plugins.0": "user-plugin", "custom.keep": "42"} {
					if gjson.Get(raw, key).String() != want {
						t.Fatalf("unmanaged %s lost: %s", key, raw)
					}
				}
			}
			if readCLIFile(t, a.tuiFile()) != legacy || readCLIFile(t, a.cfgFile()) != server {
				t.Fatal("migration changed a V1 or server file")
			}
			if _, err := os.Stat(filepath.Join(a.ProjectRoot, "cli.json")); !os.IsNotExist(err) {
				t.Fatalf("created project CLI file: %v", err)
			}
			for _, r := range a.Describe(c) {
				if r.Kind == "tui" && (r.Destination != a.cliFile() || !strings.HasPrefix(r.Key, cliStatePrefix)) {
					t.Fatalf("wrong CLI description: %+v", r)
				}
			}
			obs, err := a.ObserveHashes(st)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range st.Keys("opencode") {
				e, _ := st.Get("opencode", key)
				if obs[key] != e.Applied {
					t.Fatalf("observe %s: %v != %s", key, obs, e.Applied)
				}
			}
			cs = applyCLI(t, a, st, c)
			for _, ch := range cs.Changes {
				if ch.Action != "noop" {
					t.Fatalf("not idempotent: %+v", ch)
				}
			}
			if readCLIFile(t, a.cliFile()) != raw {
				t.Fatal("noop rewrote cli.json")
			}
		})
	}
}

func TestOpenCodeCLIAdoptsNativeSettingsAndPrunesLeaves(t *testing.T) {
	a, st := cliFixture(t)
	original := "{\n  \"theme\": {\"name\": \"gruvbox\", \"mode\": \"light\"},\n  \"keybinds\": {\"app.exit\": [\"ctrl+c\", \"<leader>q\"], \"help.show\": false},\n  \"attention\": {\"sounds\": {\"done\": \"user.wav\"}}\n}\n"
	writeCLIFile(t, a.cliFile(), original)
	c := &config.Config{TUI: config.TUI{OpenCode: map[string]any{
		"theme":    map[string]any{"name": "gruvbox"},
		"keybinds": map[string]any{"app.exit": []string{"ctrl+c", "<leader>q"}},
	}}}
	cs := applyCLI(t, a, st, c)
	if len(cs.Changes) != 2 {
		t.Fatalf("unexpected managed keys: %+v", cs)
	}
	for _, ch := range cs.Changes {
		if ch.Action != "adopt" {
			t.Fatalf("expected adoption: %+v", ch)
		}
	}
	if readCLIFile(t, a.cliFile()) != original {
		t.Fatal("adoption changed bytes")
	}
	applyCLI(t, a, st, &config.Config{})
	raw := readCLIFile(t, a.cliFile())
	if gjson.Get(raw, "theme.name").Exists() || gjson.Get(raw, `keybinds.app\.exit`).Exists() {
		t.Fatalf("managed leaves not pruned: %s", raw)
	}
	if gjson.Get(raw, "theme.mode").String() != "light" || !gjson.Get(raw, `keybinds.help\.show`).Exists() || gjson.Get(raw, "attention.sounds.done").String() != "user.wav" {
		t.Fatalf("pruning removed unmanaged siblings: %s", raw)
	}
	if len(st.Keys("opencode")) != 0 {
		t.Fatalf("orphaned state: %v", st.Keys("opencode"))
	}
}

func TestOpenCodeCLINativeProjectionAlongsideV1ServerConfig(t *testing.T) {
	a, st := cliFixture(t)
	writeCLIFile(t, a.cliFile(), `{"attention":{"sounds":{"done":"unmanaged.wav"}},"keybinds":{"help.show":false}}`)
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"model": "provider/model"}},
		MCPs:     map[string]config.MCP{"example": {Command: []string{"example", "serve"}, Targets: []string{"opencode"}}},
		Plugins:  config.Plugins{OpenCode: map[string]config.Plugin{"example": {Source: "example-plugin"}}},
		TUI: config.TUI{OpenCode: map[string]any{
			"theme":        map[string]any{"name": "gruvbox", "mode": "system"},
			"scroll":       map[string]any{"speed": 3, "acceleration": false},
			"attention":    map[string]any{"sounds": map[string]any{"permission": "permission.wav"}},
			"keybinds":     map[string]any{"prompt.paste": map[string]any{"key": map[string]any{"name": "v", "ctrl": true}, "event": "press", "fallthrough": false}},
			"experimental": map[string]any{"test.flag@local": true},
		}},
	}
	applyCLI(t, a, st, c)
	raw := readCLIFile(t, a.cliFile())
	for path, want := range map[string]string{
		"theme.name": "gruvbox", "theme.mode": "system", "scroll.speed": "3", "scroll.acceleration": "false",
		"attention.sounds.permission": "permission.wav", "attention.sounds.done": "unmanaged.wav",
		`keybinds.prompt\.paste.key.name`: "v", `keybinds.prompt\.paste.key.ctrl`: "true",
		`keybinds.help\.show`: "false", `experimental.test\.flag\@local`: "true",
	} {
		if gjson.Get(raw, path).String() != want {
			t.Fatalf("%s not projected or preserved: %s", path, raw)
		}
	}
	server := readCLIFile(t, a.cfgFile())
	if gjson.Get(server, "model").String() != "provider/model" || gjson.Get(server, "mcp.example.type").String() != "local" || !gjson.Get(server, "mcp.example.enabled").Bool() || gjson.Get(server, "plugin.0").String() != "example-plugin" || gjson.Get(server, "plugins").Exists() {
		t.Fatalf("V1 server shape changed: %s", server)
	}
	obs, err := a.ObserveHashes(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range st.Keys("opencode") {
		e, _ := st.Get("opencode", key)
		if obs[key] != e.Applied {
			t.Fatalf("native hash mismatch for %s: %v", key, obs)
		}
	}
	for _, ch := range applyCLI(t, a, st, c).Changes {
		if ch.Action != "noop" {
			t.Fatalf("mixed V2 CLI / V1 server not idempotent: %+v", ch)
		}
	}
}

func TestOpenCodeCLIObserveDriftAndMissing(t *testing.T) {
	a, st := cliFixture(t)
	c := &config.Config{TUI: config.TUI{OpenCode: map[string]any{"theme": "gruvbox"}}}
	applyCLI(t, a, st, c)
	key := cliStatePrefix + "theme.name"
	e, _ := st.Get("opencode", key)
	writeCLIFile(t, a.cliFile(), `{"theme":{"name":"changed","mode":"dark"}}`)
	obs, err := a.ObserveHashes(st)
	if err != nil || obs[key] == e.Applied || obs[key] == "" {
		t.Fatalf("edit not observed: %v, %v", obs, err)
	}
	if err := os.Remove(a.cliFile()); err != nil {
		t.Fatal(err)
	}
	obs, err = a.ObserveHashes(st)
	if err != nil || obs[key] != "" {
		t.Fatalf("absent key observed: %v, %v", obs, err)
	}
	applyCLI(t, a, st, &config.Config{})
	if _, err := os.Stat(a.cliFile()); !os.IsNotExist(err) {
		t.Fatalf("prune recreated missing cli.json: %v", err)
	}
}

func TestOpenCodeCLIEscapedLeavesSurviveStateReloadAndPrune(t *testing.T) {
	a, st := cliFixture(t)
	writeCLIFile(t, a.cliFile(), `{"experimental":{"keep":true},"keybinds":{"help.show":false},"theme":{"mode":"dark"}}`)
	configPath := filepath.Join(t.TempDir(), "homonto.toml")
	writeCLIFile(t, configPath, `
[tui.opencode]
theme = { name = "quoted\" name\\with\nnewline" }
[tui.opencode.experimental]
'flag.with.dots' = true
'slash\.flag|*?#@:tail' = false
[tui.opencode.keybinds]
"prompt.paste" = { key = { name = '"\\', ctrl = true }, metadata = { label = 'paste "text"', flags = [true, false] } }
`)
	c, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	applyCLI(t, a, st, c)
	keys := []string{
		`tui.cli.theme.name`,
		`tui.cli.experimental.flag\.with\.dots`,
		`tui.cli.experimental.slash\\\.flag\|\*\?\#\@\:tail`,
		`tui.cli.keybinds.prompt\.paste`,
	}
	for _, key := range keys {
		if _, ok := st.Get("opencode", key); !ok {
			t.Fatalf("missing literal leaf state %q: %v", key, st.Keys("opencode"))
		}
	}
	raw := readCLIFile(t, a.cliFile())
	var disk map[string]any
	if err := json.Unmarshal([]byte(raw), &disk); err != nil {
		t.Fatal(err)
	}
	for _, group := range []string{"theme", "experimental", "keybinds"} {
		for name, want := range c.TUI.OpenCode[group].(map[string]any) {
			if got := disk[group].(map[string]any)[name]; !reflect.DeepEqual(got, want) {
				t.Fatalf("literal %s[%q] = %#v; want %#v", group, name, got, want)
			}
		}
	}
	stateDir := t.TempDir()
	if err := st.Save(stateDir); err != nil {
		t.Fatal(err)
	}
	st, err = state.Load(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	obs, err := a.ObserveHashes(st)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		e, ok := st.Get("opencode", key)
		if !ok || obs[key] != e.Applied {
			t.Fatalf("reloaded leaf %q: entry=%+v observed=%q", key, e, obs[key])
		}
	}
	for _, ch := range applyCLI(t, a, st, c).Changes {
		if ch.Action != "noop" {
			t.Fatalf("not idempotent after reload: %+v", ch)
		}
	}
	if readCLIFile(t, a.cliFile()) != raw {
		t.Fatal("noop rewrote escaped values")
	}
	applyCLI(t, a, st, &config.Config{})
	disk = nil
	if err := json.Unmarshal([]byte(readCLIFile(t, a.cliFile())), &disk); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"experimental": map[string]any{"keep": true},
		"keybinds":     map[string]any{"help.show": false},
		"theme":        map[string]any{"mode": "dark"},
	}
	if !reflect.DeepEqual(disk, want) || len(st.Keys("opencode")) != 0 {
		t.Fatalf("prune changed unmanaged siblings or retained leaves: disk=%#v state=%v", disk, st.Keys("opencode"))
	}
}

func TestOpenCodeCLIRetiresLegacyStateWithoutReadingOrCreatingFiles(t *testing.T) {
	a, st := cliFixture(t)
	st.Set("opencode", "tui.theme", `"old"`, secret.Hash(`"old"`))
	writeCLIFile(t, a.tuiFile(), "invalid legacy JSON")
	applyCLI(t, a, st, &config.Config{})
	if readCLIFile(t, a.tuiFile()) != "invalid legacy JSON" {
		t.Fatal("retirement touched legacy file")
	}
	if _, err := os.Stat(a.cliFile()); !os.IsNotExist(err) {
		t.Fatalf("state-only retirement created cli.json: %v", err)
	}
	if len(st.Keys("opencode")) != 0 {
		t.Fatal("legacy state retained")
	}
}

func TestOpenCodeCLIReverseChangesCannotReplayLegacyWrites(t *testing.T) {
	a, st := cliFixture(t)
	legacy := `{"theme":"v1"}`
	writeCLIFile(t, a.tuiFile(), legacy)
	c := &config.Config{TUI: config.TUI{OpenCode: map[string]any{"theme": "before"}}}
	applyCLI(t, a, st, c)
	key := cliStatePrefix + "theme.name"
	before, _ := st.Get("opencode", key)
	c.TUI.OpenCode["theme"] = "after"
	applyCLI(t, a, st, c)
	reverse := adapter.ChangeSet{Tool: "opencode", Changes: []adapter.Change{{Action: "update", Key: key, New: before.Desired}}}
	if err := a.Apply(c, reverse, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	if gjson.Get(readCLIFile(t, a.cliFile()), "theme.name").String() != "before" || readCLIFile(t, a.tuiFile()) != legacy {
		t.Fatal("reverse change targeted the wrong file")
	}
	for _, action := range []adapter.Action{"create", "update", "adopt"} {
		oldReverse := adapter.ChangeSet{Tool: "opencode", Changes: []adapter.Change{{Action: action, Key: "tui.theme", New: `"v1"`}}}
		if err := a.Apply(c, oldReverse, noSecret(), st); err == nil || !strings.Contains(err.Error(), "re-plan") {
			t.Fatalf("historical %s not refused: %v", action, err)
		}
	}
	st.Set("opencode", "tui.theme", `"v1"`, secret.Hash(`"v1"`))
	cs := applyCLI(t, a, st, c)
	for _, ch := range cs.Changes {
		if ch.Key == "tui.theme" && ch.Action != "delete" {
			t.Fatalf("restored old checkpoint retargeted: %+v", ch)
		}
	}
	if readCLIFile(t, a.tuiFile()) != legacy {
		t.Fatal("restored checkpoint mutated V1")
	}
}

func TestOpenCodeCLIRejectsUnsupportedDeclarationsAndParentConflicts(t *testing.T) {
	for _, raw := range []map[string]any{
		{"diff_style": "stacked"},
		{"keybinds": map[string]any{"app_exit": "ctrl+c"}},
		{"attention": map[string]any{"enabled": true}},
	} {
		a, st := cliFixture(t)
		c := &config.Config{TUI: config.TUI{OpenCode: raw}}
		if _, err := a.Plan(c, st); err == nil {
			t.Fatalf("unsupported declaration planned: %v", raw)
		}
		if err := a.Apply(c, adapter.ChangeSet{}, noSecret(), st); err == nil {
			t.Fatalf("unsupported declaration applied: %v", raw)
		}
	}
	for _, disk := range []string{`{"theme":"v1"}`, `{"theme":null}`, `{"theme":[]}`, `[]`, `{broken`} {
		a, st := cliFixture(t)
		writeCLIFile(t, a.cliFile(), disk)
		c := &config.Config{TUI: config.TUI{OpenCode: map[string]any{"theme": "gruvbox"}}}
		if _, err := a.Plan(c, st); err == nil {
			t.Fatalf("conflicting file accepted: %s", disk)
		}
		cs := adapter.ChangeSet{Tool: "opencode", Changes: []adapter.Change{{Action: "create", Key: cliStatePrefix + "theme.name", New: `"gruvbox"`}}}
		if err := a.Apply(c, cs, noSecret(), st); err == nil {
			t.Fatalf("conflicting file overwritten: %s", disk)
		}
		if readCLIFile(t, a.cliFile()) != disk {
			t.Fatal("conflict changed bytes")
		}
	}
}

func TestOpenCodeCLIXDGAndRepoIsolation(t *testing.T) {
	a, st := cliFixture(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	a = New(a.Home, a.Content).WithProjectRoot(a.ProjectRoot)
	c := &config.Config{TUI: config.TUI{OpenCode: map[string]any{"mouse": false}}}
	applyCLI(t, a, st, c)
	if a.cliFile() != filepath.Join(xdg, "opencode", "cli.json") {
		t.Fatalf("XDG destination: %s", a.cliFile())
	}
	if _, err := os.Stat(filepath.Join(a.Home, ".config", "opencode", "cli.json")); !os.IsNotExist(err) {
		t.Fatalf("wrote fallback path: %v", err)
	}
	writeCLIFile(t, a.cliFile(), "invalid global config")
	repo := New(a.Home, a.Content).WithProjectRoot(t.TempDir()).WithRepo("other")
	repoState, err := state.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cs := applyCLI(t, repo, repoState, c)
	if len(cs.Changes) != 0 {
		t.Fatalf("repo planned global settings: %+v", cs)
	}
	if _, err := repo.ObserveHashes(repoState); err != nil {
		t.Fatal(err)
	}
	if readCLIFile(t, a.cliFile()) != "invalid global config" {
		t.Fatal("repo touched global settings")
	}
}
