package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/catalog"
)

// The configuration reference's key index is generated from the decoder's
// own struct tags, so a key the decoder accepts cannot be missing from the
// reference and a removed key cannot linger in it (M5.2). Regenerate with
// `task docs-config`.
const (
	referenceDoc   = "../../docs/guides/configuration.md"
	referenceBegin = "<!-- BEGIN GENERATED KEY INDEX (task docs-config) -->"
	referenceEnd   = "<!-- END GENERATED KEY INDEX -->"
)

// referencePlaceholders names the user-chosen key segment under each map.
var referencePlaceholders = map[string]string{
	"repos":                    "<alias>",
	"routing.workflows":        "<workflow>",
	"routing.engines":          "<engine>",
	"routing.engines.*.stages": "<stage>",
	"routing.models.<harness>": "<model>",
	"decisions.providers":      "<provider>",
	"decisions.points":         "<package.point>",
	"processes":                "<process>",
}

// referenceRawKeys lists the keys of tables decoded as raw string maps so an
// unknown key can be rejected by name.
var referenceRawKeys = map[string][]string{
	"tooling": {toolingKeyShellProxy, toolingKeyCodeIntel},
	"tmp":     {"dir"},
}

// referenceCollapsed are removed tables shown as one row each: they are
// decoded only so load can refuse them by name.
var referenceCollapsed = map[string]string{
	"models":         "removed: model tiers; declare models per agent in [subagents.<name>.<harness>]",
	"marketplaces":   "removed: Claude Code marketplaces are bring-your-own in its settings.json",
	"plugins.claude": "removed: Claude Code plugins are bring-your-own in its settings.json",
	"agents":         "legacy: [agents.<name>] (source, version, targets, mode) folds into a copy-mode [subagents.<name>]",
}

// referenceNotes annotate generated keys. Every note must match a generated
// key: a stale note fails the test. Claims of "rejected" are exercised by
// TestDiagnosticsNameKeyValueAndFix.
var referenceNotes = map[string]string{
	"schema_version": "0, 1 or 2; newer fails at load",
	"harnesses":      `"opencode", "claude"; default ["opencode"]`,

	"engines.<name>.settings.*":                           "package-defined: the manifest's [settings] schema",
	"engines.<name>.processes.<process>.*":                "package-defined: the manifest's [processes] schema",
	"workflows.<name>.settings.*":                         "package-defined: the manifest's [settings] schema",
	"workflows.<name>.processes.<process>.*":              "package-defined: the manifest's [processes] schema",
	"services.<name>.settings.*":                          "package-defined: the manifest's [settings] schema",
	"services.<name>.processes.<process>.*":               "package-defined: the manifest's [processes] schema",
	"engines.<name>.scope":                                "required: user | project",
	"workflows.<name>.scope":                              "required: user | project",
	"services.<name>.scope":                               "required: user | project",
	"skills.<name>.scope":                                 "required: user | project",
	"commands.<name>.scope":                               "required: user | project",
	"subagents.<name>.scope":                              "user | project; default project",
	"engines.<name>.repo":                                 "rejected: packages project into the config root only",
	"workflows.<name>.repo":                               "rejected: packages project into the config root only",
	"services.<name>.repo":                                "rejected: packages project into the config root only",
	"engines.<name>.digest":                               "required with a remote: source",
	"workflows.<name>.digest":                             "required with a remote: source",
	"services.<name>.digest":                              "required with a remote: source",
	"subagents.<name>.digest":                             "required with a remote: source",
	"skills.<name>.settings.*":                            "rejected: only packages take settings",
	"skills.<name>.processes.<process>.*":                 "rejected: only packages take processes",
	"commands.<name>.settings.*":                          "rejected: only packages take settings",
	"commands.<name>.processes.<process>.*":               "rejected: only packages take processes",
	"skills.<name>.digest":                                "rejected: skills are builtin: only",
	"commands.<name>.digest":                              "rejected: commands are builtin: only",
	"subagents.<name>.mode":                               "link (default) | copy",
	"subagents.<name>.version":                            "informational",
	"subagents.<name>.<harness>.model":                    "OpenCode provider/model; Claude Code alias or model id",
	"subagents.<name>.<harness>.effort":                   "rejected on both harnesses: OpenCode uses variant, Claude Code takes only a model",
	"subagents.<name>.<harness>.variant":                  "OpenCode only; rejected for Claude Code",
	"subagents.<name>.<harness>.steps":                    "positive; OpenCode steps, Claude Code maxTurns",
	"subagents.<name>.<harness>.bash_allow_add":           "exact commands only; builtin agents",
	"settings.<harness>.*":                                "free-form, merged into opencode.jsonc or ~/.claude/settings.json; OpenCode reserves mcp, plugin, plugins, model_variant",
	"tui.opencode.*":                                      "OpenCode V2 cli.json schema; unknown fields fail",
	"plugins.opencode.<name>.source":                      "an npm package, a plugin path, or permission-observer",
	"plugins.opencode.<name>.enabled":                     "default true",
	"plugins.opencode.<name>.config.*":                    "rejected: OpenCode has no per-plugin config",
	"mcps.*":                                              "removed: MCP servers are bring-your-own (ADR 0085)",
	"integrations.opencode.workflow_bridge":               "removed: true fails at load (ADR 0079); delete a leftover false",
	"integrations.opencode.workflow_context":              "default true with onto, to or a hosting workflow",
	"tooling.shell_proxy":                                 "rtk | none; default none",
	"tooling.code_intel":                                  "graphify | okf | none; default none",
	"tmp.dir":                                             `default ".tmp" when [tmp] is present`,
	"workflow.root":                                       `default "docs"`,
	"workflow.git":                                        "existing | managed; schema 2 only",
	"worktrees.dir":                                       "schema 2 only",
	"repos.<alias>":                                       "path to a Git worktree's top level",
	"integration.<name>.repos":                            `schema 2 only; declared aliases, or ["*"]`,
	"integration.<name>.repo":                             "schema 2 only; one of repos",
	"integration.<name>.cwd":                              "schema 2 only; relative to that checkout",
	"integration.<name>.command":                          "schema 2 only; argv array",
	"routing.default.<harness>.mode":                      "static (default) | auto",
	"routing.default.<harness>.model":                     "static only",
	"routing.default.<harness>.variant":                   "static only; OpenCode only",
	"routing.default.<harness>.providers":                 "auto only; OpenCode only",
	"routing.default.<harness>.prefer":                    "auto only: listed (default) | cost | latency",
	"routing.default.<harness>.fallback":                  "auto only; required",
	"routing.default.<harness>.candidates":                "auto only",
	"routing.default.<harness>.needs":                     "auto only; tags from routing.models",
	"routing.default.<harness>.max_cost":                  "auto only",
	"routing.default.<harness>.max_latency":               "auto only",
	"routing.workflows.<workflow>.<harness>.*":            "the routing.default.<harness> fields; a declared workflow",
	"routing.engines.<engine>.<harness>.*":                "the routing.default.<harness> fields; a declared engine",
	"routing.engines.<engine>.stages.<stage>.<harness>.*": "the routing.default.<harness> fields; a stage the engine manifest declares",
	"routing.models.<harness>.<model>.cost":               "your own unit; compared only",
	"routing.models.<harness>.<model>.latency":            "your own unit; compared only",
	"routing.models.<harness>.<model>.tags":               "matched by an auto policy's needs",
	"decisions.providers.<provider>.model":                "the provider's model id",
	"decisions.default":                                   "provider for points without their own entry",
	"decisions.min_confidence":                            "0 to 1",
	"decisions.timeout":                                   `Go duration; default "30s"`,
	"decisions.providers.<provider>.protocol":             "systemone | openai-decisions",
	"decisions.providers.<provider>.endpoint":             "required for systemone",
	"decisions.providers.<provider>.api_key_env":          "the environment variable holding the credential",
}

type referenceRow struct{ key, typ string }

func referenceRows() []referenceRow {
	var rows []referenceRow
	var walk func(path string, t reflect.Type)
	join := func(path, seg string) string {
		if path == "" {
			return seg
		}
		return path + "." + seg
	}
	placeholder := func(path string) string {
		if p, ok := referencePlaceholders[path]; ok {
			return p
		}
		// Placeholders match on the path with earlier user segments wildcarded.
		parts := strings.Split(path, ".")
		for i, seg := range parts {
			if strings.HasPrefix(seg, "<") {
				parts[i] = "*"
			}
		}
		if p, ok := referencePlaceholders[strings.Join(parts, ".")]; ok {
			return p
		}
		if p, ok := referencePlaceholders[parts[len(parts)-1]]; ok {
			return p
		}
		return "<name>"
	}
	leaf := func(path string, t reflect.Type) {
		rows = append(rows, referenceRow{path, referenceType(t)})
	}
	walk = func(path string, t reflect.Type) {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		if note, ok := referenceCollapsed[path]; ok {
			status, _, _ := strings.Cut(note, ":")
			rows = append(rows, referenceRow{join(path, "*"), status})
			return
		}
		// Every routing selector takes the same policy fields; they are
		// listed once, under routing.default.
		if t == reflect.TypeFor[RoutePolicy]() && path != "routing.default.<harness>" {
			rows = append(rows, referenceRow{join(path, "*"), "policy"})
			return
		}
		switch t.Kind() {
		case reflect.Struct:
			// Fields named for each harness with one type fold into one
			// <harness> segment: the keys are the same on every harness.
			harnessType := map[reflect.Type]int{}
			for i := range t.NumField() {
				f := t.Field(i)
				name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
				if _, collapsed := referenceCollapsed[join(path, name)]; !collapsed && slices.Contains(HarnessIDs(), name) {
					harnessType[f.Type]++
				}
			}
			folded := map[reflect.Type]bool{}
			for i := range t.NumField() {
				f := t.Field(i)
				name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
				if name == "" || name == "-" || !f.IsExported() {
					continue
				}
				if _, collapsed := referenceCollapsed[join(path, name)]; !collapsed && slices.Contains(HarnessIDs(), name) && harnessType[f.Type] > 1 {
					if !folded[f.Type] {
						folded[f.Type] = true
						walk(join(path, "<harness>"), f.Type)
					}
					continue
				}
				walk(join(path, name), f.Type)
			}
		case reflect.Map:
			if keys, ok := referenceRawKeys[path]; ok {
				for _, k := range keys {
					leaf(join(path, k), t.Elem())
				}
				return
			}
			elem := t.Elem()
			if elem.Kind() == reflect.Interface {
				rows = append(rows, referenceRow{join(path, "*"), "free-form"})
				return
			}
			walk(join(path, placeholder(path)), elem)
		default:
			leaf(path, t)
		}
	}
	walk("", reflect.TypeFor[Config]())
	return rows
}

func referenceType(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Int, reflect.Int64:
		return "integer"
	case reflect.Bool:
		return "boolean"
	case reflect.Float64:
		return "number"
	case reflect.Slice:
		return "list of " + referenceType(t.Elem()) + "s"
	case reflect.Interface:
		return "free-form"
	}
	return t.Kind().String()
}

// renderReference produces the generated section: the key index, then the
// settings every builtin package accepts.
func renderReference(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(referenceBegin + "\n\n")
	b.WriteString("Every key the decoder accepts, generated from the config schema. `<name>`\n")
	b.WriteString("and similar segments are yours to choose. A key marked *rejected* or\n")
	b.WriteString("*removed* is decoded only so that load can refuse it and name the fix.\n\n")
	b.WriteString("| Key | Type | Notes |\n|---|---|---|\n")
	used := map[string]bool{}
	for _, r := range referenceRows() {
		note, ok := referenceNotes[r.key]
		if top, _, _ := strings.Cut(r.key, ".*"); r.typ == "removed" || r.typ == "legacy" {
			note, ok = referenceCollapsed[top], true
		}
		used[r.key] = ok
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", r.key, r.typ, strings.ReplaceAll(note, "|", `\|`))
	}
	for key := range referenceNotes {
		if !used[key] {
			t.Errorf("referenceNotes[%q] matches no key the decoder accepts", key)
		}
	}
	b.WriteString("\n### Builtin package settings\n\n")
	b.WriteString("Generated from the shipped manifests. Set them under\n")
	b.WriteString("`[engines.<name>.settings]`, `[workflows.<name>.settings]` or\n")
	b.WriteString("`[services.<name>.settings]`. `homonto explain settings <package>` shows\n")
	b.WriteString("the resolved values.\n\n")
	cl, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	b.WriteString("| Package | Kind | Setting | Type | Default | Values |\n|---|---|---|---|---|---|\n")
	wrote := false
	for _, name := range cl.PackageNames() {
		fw, _ := cl.Framework(name)
		for _, key := range slices.Sorted(maps.Keys(fw.Settings)) {
			s := fw.Settings[key]
			def := "—"
			if s.Required {
				def = "required"
			} else if s.Default != nil {
				def = fmt.Sprintf("`%v`", s.Default)
			}
			values := "—"
			if len(s.Enum) > 0 {
				var parts []string
				for _, v := range s.Enum {
					parts = append(parts, fmt.Sprintf("`%v`", v))
				}
				values = strings.Join(parts, ", ")
			}
			fmt.Fprintf(&b, "| `%s` | %s | `%s` | %s | %s | %s |\n", name, fw.Kind, key, s.Type, def, values)
			wrote = true
		}
		if fw.Processes != nil {
			fmt.Fprintf(&b, "| `%s` | %s | processes | table | — | %s |\n", name, fw.Kind, fw.Processes.Description)
			wrote = true
		}
	}
	if !wrote {
		b.WriteString("| — | | | | | |\n")
	}
	b.WriteString("\n" + referenceEnd)
	return b.String()
}

// TestConfigReferenceMatchesTheSchema fails when docs/guides/configuration.md
// is out of date with the decoder. HOMONTO_UPDATE_REFERENCE=1 rewrites it.
func TestConfigReferenceMatchesTheSchema(t *testing.T) {
	path, err := filepath.Abs(referenceDoc)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	begin, end := strings.Index(doc, referenceBegin), strings.Index(doc, referenceEnd)
	if begin < 0 || end < begin {
		t.Fatalf("%s lacks the generated key index markers", referenceDoc)
	}
	want := renderReference(t)
	got := doc[begin : end+len(referenceEnd)]
	if got == want {
		return
	}
	if os.Getenv("HOMONTO_UPDATE_REFERENCE") == "1" {
		if err := os.WriteFile(path, []byte(doc[:begin]+want+doc[end+len(referenceEnd):]), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("%s key index is out of date with the config schema; run `task docs-config`", referenceDoc)
}

// TestConfigReferenceDocumentsEveryTopLevelTable requires a prose section,
// linked from the Contents table, for every top-level key.
func TestConfigReferenceDocumentsEveryTopLevelTable(t *testing.T) {
	data, err := os.ReadFile(referenceDoc)
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	contents, _, _ := strings.Cut(doc, "## General rules")
	seen := map[string]bool{}
	for _, r := range referenceRows() {
		top, _, _ := strings.Cut(r.key, ".")
		if seen[top] {
			continue
		}
		seen[top] = true
		if !strings.Contains(contents, "`"+top+"`") && !strings.Contains(contents, "`["+top) {
			t.Errorf("top-level key %q has no entry in the configuration reference's Contents table", top)
		}
	}
}
