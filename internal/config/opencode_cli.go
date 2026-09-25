package config

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/jsonutil"
)

var openCodeCLIRules = map[string]string{
	"$schema":    "string",
	"theme.name": "string", "theme.mode": "system|dark|light",
	"animations": "bool", "mouse": "bool",
	"cursor.style": "block|underline|line|default", "cursor.blinking": "bool",
	"scroll.speed": "speed", "scroll.acceleration": "bool",
	"leader.timeout": "positive integer",
	"prompt.editor":  "bool", "prompt.paste": "compact|full", "prompt.image_preview": "bool",
	"session.sidebar": "auto|hide", "session.scrollbar": "bool",
	"session.thinking": "show|hide", "session.grouping": "auto|none",
	"session.image_preview": "bool", "session.tps": "bool",
	"session.markdown": "source|rendered", "session.new_location": "launch|inherit",
	"session.permissions": "prompt|autoaccept",
	"tabs.mode":           "auto|on|off", "tabs.enabled": "bool", "tabs.scope": "cwd|global",
	"tabs.layout": "horizontal|vertical", "tabs.indicators": "status|numbers",
	"diffs.source": "branch|committed|working", "diffs.wrap": "word|none",
	"diffs.tree": "bool", "diffs.single": "bool", "diffs.view": "auto|split|unified",
	"attention.notifications": "bool", "attention.sound": "bool", "attention.volume": "volume",
	"attention.sound_pack":     "string",
	"attention.sounds.default": "string", "attention.sounds.question": "string",
	"attention.sounds.permission": "string", "attention.sounds.error": "string",
	"attention.sounds.done": "string", "attention.sounds.subagent_done": "string",
	"terminal.title": "bool", "terminal.copy": "manual|select",
	"mini.thinking": "show|hide", "mini.tools": "show|hide", "mini.shell_output": "show|hide",
	"mini.turn_summary": "show|hide", "mini.footer": "show|hide", "mini.splash": "show|hide",
	"mini.work_spinner": "block-soft-slide|block-soft-sweep|block-low-comet|block-low-duet|block-shuttle|block-bridge|block-squeeze|small-toggle|square-toggle|grow-shrink|quadrant-orbit|crosshatch|density-wave|seed",
	"mini.mono":         "bool", "mini.replay": "bool", "mini.replay_limit": "positive integer",
	"debug.devtools": "bool", "debug.timing": "bool", "debug.turn_tokens": "bool|verbose",
}

func OpenCodeCLISettings(raw map[string]any) (map[string]any, error) {
	out := map[string]any{}
	origins := map[string]string{}
	put := func(path, origin string, value any) error {
		if previous, ok := origins[path]; ok {
			return fmt.Errorf("tui.opencode: %s and %s both configure V2 %s; declare only one", previous, origin, path)
		}
		if _, err := json.Marshal(value); err != nil {
			return fmt.Errorf("tui.opencode.%s: invalid JSON value: %w", origin, err)
		}
		origins[path], out[path] = origin, value
		return nil
	}
	var walk func(string, string, any) error
	walk = func(path, origin string, value any) error {
		if rule, ok := openCodeCLIRules[path]; ok {
			if !validCLIValue(rule, value) {
				return fmt.Errorf("tui.opencode.%s: V2 %s requires %s", origin, path, rule)
			}
			return put(path, origin, value)
		}
		group := path == "keybinds" || path == "experimental"
		for key := range openCodeCLIRules {
			group = group || strings.HasPrefix(key, path+".")
		}
		if !group {
			return fmt.Errorf("tui.opencode.%s: unsupported OpenCode V2 CLI setting; use the native keys at https://opencode.ai/v2/docs/cli/config/ (legacy keybind IDs, diff_style, attention.enabled and terminal plugins are not migrated)", origin)
		}
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("tui.opencode.%s: V2 %s requires a table", origin, path)
		}
		for _, key := range sortedCLIKeys(obj) {
			v := obj[key]
			if err := validateKey("tui.opencode."+origin, key); err != nil {
				return err
			}
			if path == "keybinds" {
				if !openCodeCLIKeybinds[key] {
					return fmt.Errorf("tui.opencode.keybinds.%s: unsupported V2 command ID; use a native command ID from https://opencode.ai/v2/docs/cli/keybinds/", key)
				}
				if !validCLIBinding(v) {
					return fmt.Errorf("tui.opencode.keybinds.%s: invalid V2 binding; use a string, false, descriptor, binding object or array of alternatives", key)
				}
				if err := put(path+"."+jsonutil.EscapePath(key), origin+"."+key, v); err != nil {
					return err
				}
				continue
			}
			if path == "experimental" {
				if _, ok := v.(bool); !ok {
					return fmt.Errorf("tui.opencode.experimental.%s: requires a boolean", key)
				}
				if err := put(path+"."+jsonutil.EscapePath(key), origin+"."+key, v); err != nil {
					return err
				}
				continue
			}
			if strings.ContainsAny(key, `.|*?#@\:`) {
				return fmt.Errorf("tui.opencode.%s: unsupported V2 field %q; use nested TOML tables", origin, key)
			}
			if err := walk(path+"."+key, origin+"."+key, v); err != nil {
				return err
			}
		}
		return nil
	}
	for _, key := range sortedCLIKeys(raw) {
		if err := validateKey("tui.opencode", key); err != nil {
			return nil, err
		}
		path, value := key, raw[key]
		switch key {
		case "theme":
			if _, ok := value.(string); ok {
				path = "theme.name"
			}
		case "scroll_speed":
			path = "scroll.speed"
		case "leader_timeout":
			path = "leader.timeout"
		case "scroll_acceleration":
			obj, ok := value.(map[string]any)
			if !ok || len(obj) != 1 {
				return nil, fmt.Errorf("tui.opencode.scroll_acceleration: legacy form requires only enabled; use scroll.acceleration in V2")
			}
			value, ok = obj["enabled"]
			if !ok {
				return nil, fmt.Errorf("tui.opencode.scroll_acceleration: missing enabled; use scroll.acceleration in V2")
			}
			path = "scroll.acceleration"
		default:
			if strings.ContainsAny(key, `.|*?#@\:`) && key != "$schema" {
				return nil, fmt.Errorf("tui.opencode: unsupported V2 field %q; use nested TOML tables", key)
			}
		}
		if err := walk(path, key, value); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func sortedCLIKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func validCLIValue(rule string, value any) bool {
	switch rule {
	case "string":
		_, ok := value.(string)
		return ok
	case "bool":
		_, ok := value.(bool)
		return ok
	case "bool|verbose":
		_, ok := value.(bool)
		return ok || value == "verbose"
	case "speed", "volume", "positive integer":
		var n float64
		switch v := value.(type) {
		case int:
			n = float64(v)
		case int64:
			n = float64(v)
		case float64:
			n = v
		default:
			return false
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return false
		}
		switch rule {
		case "speed":
			return n >= 0.001
		case "volume":
			return n >= 0 && n <= 1
		default:
			return n > 0 && math.Trunc(n) == n
		}
	default:
		s, ok := value.(string)
		return ok && slices.Contains(strings.Split(rule, "|"), s)
	}
}

func validCLIKey(value any) bool {
	if _, ok := value.(string); ok {
		return true
	}
	obj, ok := value.(map[string]any)
	if !ok || !validCLIValue("string", obj["name"]) {
		return false
	}
	for k, v := range obj {
		if k == "name" {
			continue
		}
		if !slices.Contains([]string{"ctrl", "shift", "meta", "super", "hyper"}, k) || !validCLIValue("bool", v) {
			return false
		}
	}
	return true
}

func validCLIBindingItem(value any) bool {
	if validCLIKey(value) {
		return true
	}
	obj, ok := value.(map[string]any)
	if !ok || !validCLIKey(obj["key"]) {
		return false
	}
	for k, v := range obj {
		switch k {
		case "key":
		case "event":
			if !validCLIValue("press|release", v) {
				return false
			}
		case "preventDefault", "fallthrough":
			if !validCLIValue("bool", v) {
				return false
			}
		}
	}
	return true
}

func validCLIBinding(value any) bool {
	if b, ok := value.(bool); ok {
		return !b
	}
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			if !validCLIBindingItem(item) {
				return false
			}
		}
		return true
	case []string:
		return true
	default:
		return validCLIBindingItem(value)
	}
}

var openCodeCLIKeybinds = func() map[string]bool {
	out := map[string]bool{}
	for _, key := range strings.Fields(`
leader app.exit app.clear app.debug app.console app.scrap app.toggle.animations
app.toggle.file_context app.toggle.diffwrap app.toggle.paste_summary command.palette.show
help.show docs.open opencode.settings server.pair service.restart location.reload
diff.open diff.close diff.down diff.up diff.page.down diff.page.up diff.half_page.down
diff.half_page.up diff.first diff.last diff.toggle diff.expand diff.expand_all diff.collapse
diff.switch_focus diff.next_hunk diff.previous_hunk diff.next_file diff.previous_file
diff.toggle_file_tree diff.single_patch diff.switch_source diff.toggle_view diff.mark_reviewed diff.help
prompt.editor theme.switch theme.switch_mode theme.mode.lock session.sidebar.toggle
pane.focus.left pane.focus.right terminal.select terminal.toggle terminal.close
session.toggle.scrollbar opencode.status opencode.debug session.export session.copy session.copy.id
session.move session.new session.list open.menu session.tab.next session.tab.previous
session.tab.history.back session.tab.history.forward session.tab.next_unread session.tab.previous_unread
session.tab.close session.tab.reopen session.timeline session.fork session.rename session.delete
session.share session.unshare session.interrupt session.background session.compact session.aside
session.cd session.queued_prompts queued_prompt.delete session.toggle.exploration_grouping
session.child.first session.child.next session.child.previous session.parent session.pin.toggle
stash.delete model.dialog.provider model.dialog.favorite model.list model.cycle_recent
model.cycle_recent_reverse model.cycle_favorite model.cycle_favorite_reverse mcp.list provider.connect
agent.list agent.cycle agent.cycle.reverse variant.cycle variant.list session.page.up session.page.down
session.line.up session.line.down session.half.page.up session.half.page.down session.first session.last
session.message.next session.message.previous session.message.user.next session.message.user.previous
session.messages_last_user messages.copy session.undo session.redo session.toggle.thinking
prompt.submit prompt.queue prompt.editor_context.clear prompt.images.view prompt.skills prompt.stash
prompt.stash.pop prompt.stash.list prompt.clear prompt.paste input.submit input.newline
input.move.left input.move.right input.move.up input.move.down input.select.left input.select.right
input.select.up input.select.down input.line.home input.line.end input.select.line.home input.select.line.end
input.visual.line.home input.visual.line.end input.select.visual.line.home input.select.visual.line.end
input.buffer.home input.buffer.end input.select.buffer.home input.select.buffer.end input.delete.line
input.delete.to.line.end input.delete.to.line.start input.backspace input.delete input.undo input.redo
input.word.forward input.word.backward input.select.word.forward input.select.word.backward
input.delete.word.forward input.delete.word.backward input.select.all prompt.history.previous prompt.history.next
composer.subagent.up composer.subagent.down composer.subagent.select composer.subagent.interrupt
composer.shell.up composer.shell.down composer.shell.select composer.shell.kill composer.terminal.up
composer.terminal.down composer.terminal.select dialog.select.prev dialog.select.next dialog.select.page_up
dialog.select.page_down dialog.select.home dialog.select.end dialog.select.submit dialog.prompt.submit
dialog.integration.rename dialog.integration.delete dialog.worktree.generate dialog.move_session.new
dialog.move_session.move dialog.move_session.delete dialog.move_session.refresh prompt.autocomplete.prev
prompt.autocomplete.next prompt.autocomplete.hide prompt.autocomplete.select prompt.autocomplete.complete
permission.prompt.fullscreen plugins.toggle dialog.mcp.toggle dialog.plugins.error dialog.plugins.install
dialog.plugins.update dialog.plugins.check terminal.suspend terminal.title.toggle plugins.list plugins.install
which-key.toggle which-key.layout.toggle which-key.pending.toggle which-key.group.previous which-key.group.next
which-key.scroll.up which-key.scroll.down which-key.page.up which-key.page.down which-key.home which-key.end
`) {
		out[key] = true
	}
	for n := 1; n <= 10; n++ {
		out[fmt.Sprintf("session.tab.select.%d", n)] = true
		if n < 10 {
			out[fmt.Sprintf("session.quick_switch.%d", n)] = true
		}
	}
	return out
}()
