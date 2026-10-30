package opencode

import (
	"testing"

	"github.com/noviopenworks/homonto/internal/config"
)

// With no [settings.opencode].model and no route-derived default, the adapter
// must NOT synthesize setting.model or setting.small_model keys — OpenCode
// uses its own defaults.
func TestDesiredSettings_NoMainModelWhenSettingsAbsent(t *testing.T) {
	c := &config.Config{
		Frameworks: map[string]config.Resource{
			"onto": {Source: "builtin:onto", Scope: "project", Targets: []string{"opencode"}},
		},
	}
	a := New(t.TempDir(), t.TempDir())
	got := a.desiredSettings(c)
	if v, ok := got["setting.model"]; ok {
		t.Errorf("homonto must not synthesize setting.model when [settings.opencode].model is absent, got %q", v)
	}
	if v, ok := got["setting.small_model"]; ok {
		t.Errorf("homonto must not synthesize setting.small_model when [settings.opencode] is absent, got %q", v)
	}
}

// An explicit [settings.opencode].model is the only way to project
// setting.model. It lands in the global opencode.jsonc.
func TestDesiredSettings_ExplicitSettingStillProjects(t *testing.T) {
	c := &config.Config{
		Frameworks: map[string]config.Resource{
			"onto": {Source: "builtin:onto", Scope: "project", Targets: []string{"opencode"}},
		},
		Settings: config.Settings{OpenCode: map[string]any{"model": "explicit/model"}},
	}
	a := New(t.TempDir(), t.TempDir())
	if got := a.desiredSettings(c); got["setting.model"] != `"explicit/model"` {
		t.Errorf("explicit [settings.opencode].model must project, got %q", got["setting.model"])
	}
}

// projectScopedConfig is a config whose only model-backed resource is a
// project-scoped framework. homonto no longer derives any route-driven keys,
// so desiredProjectSettings is empty regardless of scope.
func projectScopedConfig() *config.Config {
	return &config.Config{
		Frameworks: map[string]config.Resource{
			"onto": {Source: "builtin:onto", Scope: "project", Targets: []string{"opencode"}},
		},
	}
}

func TestDesiredSettings_ProjectScopedNoSynthesizedModelKeys(t *testing.T) {
	c := projectScopedConfig()
	a := New(t.TempDir(), t.TempDir()).WithProjectRoot(t.TempDir())
	global := a.desiredSettings(c)
	if _, ok := global["setting.model"]; ok {
		t.Error("project-scoped resources must not synthesize setting.model")
	}
	if _, ok := global["setting.small_model"]; ok {
		t.Error("project-scoped resources must not synthesize setting.small_model")
	}
	proj := a.desiredProjectSettings(c)
	if len(proj) != 0 {
		t.Errorf("no route-derived project settings exist anymore, got %v", proj)
	}
}

func TestDesiredSettings_UserScopedNoSynthesizedModelKeys(t *testing.T) {
	c := projectScopedConfig()
	c.Frameworks["onto"] = config.Resource{Source: "builtin:onto", Scope: "user", Targets: []string{"opencode"}}
	a := New(t.TempDir(), t.TempDir()).WithProjectRoot(t.TempDir())
	if got := a.desiredSettings(c); got["setting.model"] != "" || got["setting.small_model"] != "" {
		t.Errorf("a user-scoped framework must not synthesize model keys, got %v", got)
	}
	if proj := a.desiredProjectSettings(c); len(proj) != 0 {
		t.Errorf("no route-derived project settings exist anymore, got %v", proj)
	}
}
