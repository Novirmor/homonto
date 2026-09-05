package catalog

import (
	"strings"
	"testing"
	"testing/fstest"
)

func baseFS() fstest.MapFS {
	return fstest.MapFS{
		"version.txt":                               {Data: []byte("0.1.0")},
		"frameworks/base/framework.toml":            {Data: []byte("name = \"base\"\nversion = \"0.1.0\"\n[skills]\nbaseskill = \"frameworks/base/skills/baseskill\"\n")},
		"frameworks/base/skills/baseskill/SKILL.md": {Data: []byte("base")},
	}
}

func TestLoadOverlays_OverlayAddsFramework(t *testing.T) {
	overlay := fstest.MapFS{
		"frameworks/ext/framework.toml":           {Data: []byte("name = \"ext\"\nversion = \"0.1.0\"\n[skills]\nextskill = \"frameworks/ext/skills/extskill\"\n")},
		"frameworks/ext/skills/extskill/SKILL.md": {Data: []byte("ext")},
	}
	c, err := LoadOverlays(baseFS(), overlay)
	if err != nil {
		t.Fatalf("LoadOverlays: %v", err)
	}
	if _, ok := c.Framework("base"); !ok {
		t.Error("base framework missing")
	}
	if _, ok := c.Framework("ext"); !ok {
		t.Error("overlay framework ext missing")
	}
	// The overlay skill is expandable.
	sk, err := c.Expand([]string{"ext"})
	if err != nil || len(sk) != 1 || sk[0].Name != "extskill" {
		t.Errorf("Expand(ext) = %v, %v; want extskill", sk, err)
	}
}

func TestLoadOverlays_ShadowConflictErrors(t *testing.T) {
	// Overlay redefines "baseskill" to a DIFFERENT path -> strict conflict.
	overlay := fstest.MapFS{
		"frameworks/ext/framework.toml":            {Data: []byte("name = \"ext\"\nversion = \"0.1.0\"\n[skills]\nbaseskill = \"frameworks/ext/skills/baseskill\"\n")},
		"frameworks/ext/skills/baseskill/SKILL.md": {Data: []byte("shadow")},
	}
	_, err := LoadOverlays(baseFS(), overlay)
	if err == nil || !strings.Contains(err.Error(), "baseskill") {
		t.Fatalf("overlay shadowing a base skill should conflict, got %v", err)
	}
}

func TestLoadOverlays_NoOverlayIdenticalToLoad(t *testing.T) {
	b := baseFS()
	c, err := LoadOverlays(b)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Framework("base"); !ok {
		t.Error("base framework should load with no overlays")
	}
}

func TestLoadOverlays_RejectsLooseSubagentShadowingFrameworkContent(t *testing.T) {
	base := baseFS()
	base["frameworks/base/framework.toml"] = &fstest.MapFile{Data: []byte("name = \"base\"\nversion = \"0.1.0\"\n[subagents]\nagent = \"subagents/agent.md\"\n")}
	base["subagents/agent.md"] = &fstest.MapFile{Data: []byte("base agent")}
	overlay := fstest.MapFS{
		"subagents/agent.md":            {Data: []byte("overlay agent")},
		"frameworks/ext/framework.toml": {Data: []byte("name = \"ext\"\nversion = \"0.1.0\"\n")},
	}
	_, err := LoadOverlays(base, overlay)
	if err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("loose subagent shadow = %v, want content conflict", err)
	}
}
