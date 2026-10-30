package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/jsonutil"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/tidwall/gjson"
)

func noSecret() *secret.Resolver {
	return &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
}

// cfgWithSkills builds a config whose named skills are explicit local resources
// at scope, each targeted at the adapter's tool via SkillEntriesForTool.
func cfgWithSkills(scope string, names ...string) *config.Config {
	c := &config.Config{Skills: map[string]config.Resource{}}
	for _, name := range names {
		c.Skills[name] = config.Resource{Source: "local:" + name, Scope: scope}
	}
	return c
}

func TestOpenCodeProjectsSettingAndPreservesKeys(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte("{\n  // keep me\n  \"theme\":\"x\",\n  \"plugin\":[\"existing\"]\n}"), 0o644)

	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"share": "manual"}},
		Plugins:  config.Plugins{OpenCode: map[string]config.Plugin{"quota": {Source: "@slkiser/opencode-quota"}}},
	}

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(filepath.Join(dir, "opencode.jsonc"))
	doc, _ := jsonutil.Standardize(raw)
	if gjson.GetBytes(doc, "theme").String() != "x" {
		t.Fatal("unmanaged key lost")
	}
	if gjson.GetBytes(doc, "share").String() != "manual" {
		t.Fatalf("setting not projected: %s", doc)
	}
	if plugins := gjson.GetBytes(doc, "plugin").Array(); len(plugins) != 2 {
		t.Fatalf("plugin array = %v", plugins)
	}

	// idempotent second plan (plugin not duplicated, setting noop)
	cs2, _ := a.Plan(c, st)
	for _, ch := range cs2.Changes {
		if ch.Action != "noop" {
			t.Fatalf("expected idempotent noop, got %+v", ch)
		}
	}
}

func TestOpenCodeSecretSettingIsIdempotent(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"provider": map[string]any{"brave": map[string]any{"options": map[string]any{"apiKey": "${pass:ai/brave}"}}}}},
	}
	res := &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "SECRET", nil }}

	cs, _ := a.Plan(c, st)
	if err := a.Apply(c, cs, res, st); err != nil {
		t.Fatal(err)
	}
	cs2, _ := a.Plan(c, st)
	for _, ch := range cs2.Changes {
		if ch.Action != "noop" {
			t.Fatalf("secret-backed setting not idempotent: %+v", ch)
		}
	}
}

// TestOpenCodeStateAbsentDiskValueIsRedacted covers unknown provenance: the
// key is not in state (fresh/lost state.json) but the on-disk value may be a
// previously applied resolved secret — plan must never print it as Old.
func TestOpenCodeStateAbsentDiskValueIsRedacted(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(dir, 0o755)
	disk := `{"provider":{"brave":{"options":{"apiKey":"sk-previously-applied-secret"}}}}`
	os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte(disk), 0o600)

	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir()) // empty state: provenance unknown
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"provider": map[string]any{"brave": map[string]any{"options": map[string]any{"apiKey": "literal"}}}}},
	}
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, ch := range cs.Changes {
		if ch.Key != "setting.provider" {
			continue
		}
		found = true
		if ch.Action != "update" {
			t.Fatalf("expected update, got %s", ch.Action)
		}
		if strings.Contains(ch.Old, "sk-previously-applied-secret") {
			t.Fatalf("state-absent plan leaked the on-disk value in Old: %q", ch.Old)
		}
		if ch.Old != adapter.SecretRedaction {
			t.Fatalf("unknown-provenance Old should be redacted, got %q", ch.Old)
		}
	}
	if !found {
		t.Fatal("expected a setting.provider change")
	}
}

// TestOpenCodeAdoptOnlyApplyLeavesFileByteIdentical proves the conditional
// write: an apply whose only changes are adoptions (disk already matches, state
// empty) must not touch opencode.jsonc — hand-written JSONC comments survive.
func TestOpenCodeAdoptOnlyApplyLeavesFileByteIdentical(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(dir, 0o755)
	cfgPath := filepath.Join(dir, "opencode.jsonc")
	original := "{\n  // keep me: a real JSONC comment\n  \"theme\": \"x\",\n  \"plugin\": [\"existing\"]\n}\n"
	os.WriteFile(cfgPath, []byte(original), 0o644)

	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir()) // empty state → declared matches yield adopt
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"theme": "x"}},
		Plugins:  config.Plugins{OpenCode: map[string]config.Plugin{"existing": {Source: "existing"}}},
	}

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	sawAdopt := false
	for _, ch := range cs.Changes {
		if ch.Action != "adopt" && ch.Action != "noop" {
			t.Fatalf("expected only adopt/noop, got %s %s", ch.Action, ch.Key)
		}
		if ch.Action == "adopt" {
			sawAdopt = true
		}
	}
	if !sawAdopt {
		t.Fatal("expected at least one adopt change")
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}

	got, _ := os.ReadFile(cfgPath)
	if string(got) != original {
		t.Fatalf("adopt-only apply rewrote the file.\nwant: %q\ngot:  %q", original, string(got))
	}
}

// TestOpenCodeCreateStillWritesFile guards against over-suppression: a real
// create must still write opencode.jsonc.
func TestOpenCodeCreateStillWritesFile(t *testing.T) {
	home := t.TempDir()
	a := New(home, t.TempDir())
	st, _ := state.Load(t.TempDir())
	c := &config.Config{
		Settings: config.Settings{OpenCode: map[string]any{"share": "manual"}},
	}
	cs, _ := a.Plan(c, st)
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	raw, err := os.ReadFile(a.cfgFile())
	if err != nil {
		t.Fatalf("create did not write the config file: %v", err)
	}
	if gjson.GetBytes(raw, "share").String() != "manual" {
		t.Fatalf("setting not written: %s", raw)
	}
}

// TestOpenCodeReleasesProjectedMCPsWithoutTouchingFiles covers the
// bring-your-own handover (ADR 0085): servers an earlier release projected
// leave state, and both opencode.jsonc files stay byte-identical.
func TestOpenCodeReleasesProjectedMCPsWithoutTouchingFiles(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	dir := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(dir, 0o755)
	global := "{\n  // mine now\n  \"mcp\": {\"codegraph\": {\"type\": \"local\", \"command\": [\"codegraph\"], \"enabled\": true}}\n}\n"
	local := "{\"mcp\": {\"github\": {\"type\": \"local\", \"command\": [\"gh-mcp\"], \"enabled\": true}}}\n"
	os.WriteFile(filepath.Join(dir, "opencode.jsonc"), []byte(global), 0o644)
	os.WriteFile(filepath.Join(project, "opencode.jsonc"), []byte(local), 0o644)

	a := New(home, t.TempDir()).WithProjectRoot(project)
	st, _ := state.Load(t.TempDir())
	st.Set("opencode", "mcp.codegraph", `{"type":"local"}`, "h1")
	st.Set("opencode", "projmcp.github", `{"type":"local"}`, "h2")
	c := &config.Config{}
	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatal(err)
	}
	released := map[string]bool{}
	for _, ch := range cs.Changes {
		if ch.Cause == adapter.CauseRelease && ch.Action == "delete" {
			released[ch.Key] = true
		}
	}
	if !released["mcp.codegraph"] || !released["projmcp.github"] {
		t.Fatalf("expected both MCP keys released, got %+v", cs.Changes)
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mcp.codegraph", "projmcp.github"} {
		if _, ok := st.Get("opencode", key); ok {
			t.Fatalf("%s still in state", key)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "opencode.jsonc")); string(got) != global {
		t.Fatalf("global config rewritten: %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(project, "opencode.jsonc")); string(got) != local {
		t.Fatalf("project config rewritten: %q", got)
	}
	cs, err = a.Plan(c, st)
	if err != nil || len(cs.Changes) != 0 {
		t.Fatalf("released keys still planned: %+v, %v", cs.Changes, err)
	}
}

func TestOpenCodeLinksOwnedSkill(t *testing.T) {
	home := t.TempDir()
	content := t.TempDir()
	os.MkdirAll(filepath.Join(content, "skills", "graphify"), 0o755)
	a := New(home, content)
	st, _ := state.Load(t.TempDir())
	c := cfgWithSkills("user", "graphify")

	cs, _ := a.Plan(c, st)
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, ".config", "opencode", "skills", "graphify")
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("skill link missing: %v", err)
	}
}

func TestOpenCodeSkillsOnlyPlanShowsLinkChanges(t *testing.T) {
	home := t.TempDir()
	content := t.TempDir()
	os.MkdirAll(filepath.Join(content, "skills", "onto"), 0o755)
	a := New(home, content)
	st, _ := state.Load(t.TempDir())
	c := cfgWithSkills("user", "onto")

	cs, err := a.Plan(c, st)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	nonNoop := 0
	for _, ch := range cs.Changes {
		if ch.Action != "noop" {
			nonNoop++
		}
	}
	if nonNoop == 0 {
		t.Fatal("skills-only config: plan must contain a non-noop change for the missing link")
	}
	if err := a.Apply(c, cs, noSecret(), st); err != nil {
		t.Fatalf("apply: %v", err)
	}
	cs2, _ := a.Plan(c, st)
	for _, ch := range cs2.Changes {
		if ch.Action != "noop" {
			t.Fatalf("second plan must be all noop, got %s %s", ch.Action, ch.Key)
		}
	}
}
