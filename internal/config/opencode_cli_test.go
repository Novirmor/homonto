package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestLoadOpenCodeCLINativeV2(t *testing.T) {
	c, err := loadDocCfg(t, `
[tui.opencode]
"$schema" = "https://opencode.ai/v2/cli.json"
animations = false
mouse = true
theme = { name = "gruvbox", mode = "dark" }
cursor = { style = "line", blinking = false }
scroll = { speed = 0.001, acceleration = true }
leader = { timeout = 2000 }
prompt = { editor = true, paste = "compact", image_preview = false }
session = { sidebar = "hide", scrollbar = false, thinking = "show", grouping = "none", image_preview = true, tps = true, markdown = "source", new_location = "inherit", permissions = "prompt" }
tabs = { mode = "off", scope = "global", layout = "vertical", indicators = "numbers" }
diffs = { source = "working", wrap = "none", tree = false, single = true, view = "unified" }
attention = { notifications = true, sound = false, volume = 0.4, sound_pack = "opencode.default", sounds = { permission = "/tmp/custom.wav" } }
terminal = { title = true, copy = "manual" }
mini = { thinking = "hide", tools = "show", shell_output = "hide", turn_summary = "show", footer = "show", splash = "hide", work_spinner = "seed", mono = true, replay = false, replay_limit = 200 }
debug = { devtools = false, timing = true, turn_tokens = "verbose" }
experimental = { "feature.with.dots" = true }
[tui.opencode.keybinds]
leader = "ctrl+x"
"app.exit" = ["ctrl+c", "ctrl+d", "<leader>q"]
"prompt.paste" = { key = { name = "v", ctrl = true }, event = "press", preventDefault = false, fallthrough = false }
"help.show" = false
"session.tab.select.10" = "alt+0"
`)
	if err != nil {
		t.Fatal(err)
	}
	leaves, err := OpenCodeCLISettings(c.TUI.OpenCode)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]any{
		"theme.name": "gruvbox", "scroll.speed": 0.001,
		"session.permissions": "prompt", "mini.replay_limit": int64(200),
		`experimental.feature\.with\.dots`: true, `keybinds.help\.show`: false,
	} {
		if !reflect.DeepEqual(leaves[path], want) {
			t.Fatalf("%s = %#v; want %#v", path, leaves[path], want)
		}
	}
	if _, ok := leaves[`keybinds.prompt\.paste`].(map[string]any); !ok {
		t.Fatalf("binding must remain atomic: %#v", leaves)
	}
}

// The OpenCode 1 spellings fail at load and name their V2 key (ADR 0081).
func TestOpenCodeCLIRejectsV1Spellings(t *testing.T) {
	for doc, want := range map[string]string{
		`theme = "gruvbox"`:                        "declare theme.name instead",
		"scroll_speed = 3":                         "declare scroll.speed instead",
		"scroll_acceleration = { enabled = true }": "declare scroll.acceleration instead",
		"leader_timeout = 1500":                    "declare leader.timeout instead",
	} {
		if err := loadDoc(t, "[tui.opencode]\n"+doc); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", doc, err, want)
		}
	}
	if err := loadDoc(t, "[tui.opencode]\ntheme = { name = \"gruvbox\" }\nscroll = { speed = 3 }"); err != nil {
		t.Fatalf("V2 spellings rejected: %v", err)
	}
}

func TestLoadRejectsUnsupportedOpenCodeCLI(t *testing.T) {
	for _, tc := range []struct{ doc, key string }{
		{`diff_style = "stacked"`, "diff_style"},
		{`font = "mono"`, "font"},
		{`plugins = ["local-plugin"]`, "plugins"},
		{`plugin = ["legacy-plugin"]`, "plugin"},
		{`"$schema" = true`, "$schema"},
		{`"$schema" = { url = "https://opencode.ai/v2/cli.json" }`, "$schema"},
		{`attention = { enabled = true }`, "attention.enabled"},
		{`keybinds = { app_exit = "ctrl+c" }`, "app_exit"},
		{`keybinds = { "invented.command" = "ctrl+c" }`, "invented.command"},
		{`keybinds = { "app.exit" = true }`, "app.exit"},
		{`keybinds = { "app.exit" = { event = "press" } }`, "app.exit"},
		{`keybinds = { "app.exit" = { key = "ctrl+c", event = "repeat" } }`, "app.exit"},
		{`keybinds = { "app.exit" = [false] }`, "app.exit"},
		{`keybinds = { "app.exit" = { name = "c", alt = true } }`, "app.exit"},
		{`keybinds = { "app.exit" = { name = "c", "ctrl|shift" = true } }`, "app.exit"},
		{`theme = { unknown = "x" }`, "theme.unknown"},
		{`theme = { mode = "auto" }`, "theme.mode"},
		{`theme = false`, "theme"},
		{`theme = { "name.extra" = "x" }`, "name.extra"},
		{`"theme.name" = "x"`, "theme.name"},
		{`scroll_speed = 0`, "scroll_speed"},
		{`scroll = { speed = nan }`, "scroll.speed"},
		{`scroll = { speed = inf }`, "scroll.speed"},
		{`scroll_acceleration = true`, "scroll_acceleration"},
		{`scroll_acceleration = { enabled = true, extra = false }`, "scroll_acceleration"},
		{`scroll_acceleration = { other = true }`, "scroll_acceleration"},
		{`scroll = { acceleration = "false" }`, "scroll.acceleration"},
		{`attention = { volume = 1.1 }`, "attention.volume"},
		{`attention = { sounds = { unknown = "x.wav" } }`, "sounds.unknown"},
		{`mini = { replay_limit = 1.5 }`, "mini.replay_limit"},
		{`leader_timeout = 0`, "leader_timeout"},
		{`mini = { work_spinner = "unknown" }`, "mini.work_spinner"},
		{`debug = { turn_tokens = "false" }`, "debug.turn_tokens"},
		{`debug = { turn_tokens = { verbose = true } }`, "debug.turn_tokens"},
		{`debug = { turn_tokens = ["verbose"] }`, "debug.turn_tokens"},
		{`keybinds = { "app.exit" = { key = "ctrl+c", extra = { value = nan } } }`, "app.exit"},
		{`experimental = { "0" = true }`, "0"},
		{`experimental = { flag = "true" }`, "experimental.flag"},
		{`tabs = { mode = "auto|on" }`, "tabs.mode"},
	} {
		t.Run(tc.doc, func(t *testing.T) {
			err := loadDoc(t, "[tui.opencode]\n"+tc.doc)
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("want error naming %s, got %v", tc.key, err)
			}
		})
	}
}

func TestOpenCodeCLISchemaPermittedValues(t *testing.T) {
	c, err := loadDocCfg(t, `
[tui.opencode]
"$schema" = "./schemas/cli.json"
tabs = { enabled = false, mode = "auto" }
[tui.opencode.keybinds]
"app.exit" = { key = "ctrl+c", metadata = { label = 'Exit "now"', flags = [true, false] } }
"help.show" = [{ key = "f1", extra = 42 }]
`)
	if err != nil {
		t.Fatal(err)
	}
	leaves, err := OpenCodeCLISettings(c.TUI.OpenCode)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]any{
		"$schema": "./schemas/cli.json", "tabs.enabled": false, "tabs.mode": "auto",
		`keybinds.app\.exit`:  c.TUI.OpenCode["keybinds"].(map[string]any)["app.exit"],
		`keybinds.help\.show`: c.TUI.OpenCode["keybinds"].(map[string]any)["help.show"],
	} {
		if !reflect.DeepEqual(leaves[path], want) {
			t.Fatalf("%s = %#v; want %#v", path, leaves[path], want)
		}
	}
}

func TestOpenCodeCLIRejectsMalformedGroups(t *testing.T) {
	for _, group := range []string{"cursor", "scroll", "leader", "prompt", "session", "tabs", "diffs", "attention", "terminal", "mini", "debug", "keybinds", "experimental"} {
		for _, value := range []string{"false", "[]", "'invalid'"} {
			t.Run(group+"="+value, func(t *testing.T) {
				err := loadDoc(t, "[tui.opencode]\n"+group+" = "+value)
				if err == nil || !strings.Contains(err.Error(), group) {
					t.Fatalf("want error naming %s, got %v", group, err)
				}
			})
		}
	}
}

func TestOpenCodeServerFormatRetainedAndNativePluginConflictRejected(t *testing.T) {
	if err := loadDoc(t, `
[settings.opencode]
model = "provider/model"
small_model = "provider/small"
permission = { bash = "ask" }
agent = { review = { model = "provider/model" } }
provider = { custom = { npm = "@ai-sdk/openai-compatible" } }
[plugins.opencode.example]
source = "example-plugin"
`); err != nil {
		t.Fatalf("supported V1 server config rejected: %v", err)
	}
	if err := loadDoc(t, "[settings.opencode]\nmcp = {}"); err == nil || !strings.Contains(err.Error(), "bring-your-own") {
		t.Fatalf("settings.opencode.mcp accepted or unexplained: %v", err)
	}
	for _, key := range []string{"plugin", "plugins"} {
		err := loadDoc(t, "[settings.opencode]\n"+key+" = []")
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("conflicting %s accepted: %v", key, err)
		}
	}
}
