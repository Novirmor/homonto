package agentfm

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
	"gopkg.in/yaml.v3"
)

func TestCRLFNeutralFrontmatterFailsClosedAndRoutes(t *testing.T) {
	content := strings.ReplaceAll(readOnlyReviewer, "\n", "\r\n")
	if needs, err := NeedsTransform([]byte(content)); !needs || err != nil {
		t.Fatalf("CRLF transform = %t, %v", needs, err)
	}
	context := ctx()
	context.Overrides["onto-reviewer"] = ModelSpec{Model: "provider/model: v1", Variant: "1"}
	out, err := Render("onto-reviewer", []byte(content), "opencode", context)
	if err != nil {
		t.Fatal(err)
	}
	fm, body, ok := split(out)
	var doc map[string]any
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		t.Fatal(err)
	}
	if !ok || doc["model"] != "provider/model: v1" || doc["variant"] != "1" || doc["homonto"] != nil {
		t.Fatalf("host model/variant must remain strings and neutral block must be stripped: %#v", doc)
	}
	permission := doc["permission"].(map[string]any)
	for _, tool := range []string{"edit", "bash", "task"} {
		if permission[tool] != "deny" {
			t.Errorf("%s = %#v", tool, permission[tool])
		}
	}
	if !bytes.Equal(body, []byte("You are a focused code reviewer.\r\n")) {
		t.Fatalf("body changed: %q", body)
	}
	for _, malformed := range []string{
		strings.TrimSuffix(content, "---\r\nYou are a focused code reviewer.\r\n"),
		strings.ReplaceAll(content, "\r\n", "\r"),
		"---\nhomonto:\r  read_only: true\r  bash: false\n---\nbody",
		strings.Replace(content, "read_only: true", "read_onnly: true", 1),
		strings.Replace(content, "bash: false", "bash: [false]", 1),
	} {
		if _, err := NeedsTransform([]byte(malformed)); err == nil {
			t.Errorf("malformed header accepted: %q", malformed)
		}
		if _, err := Render("onto-reviewer", []byte(malformed), "opencode", context); err == nil {
			t.Errorf("malformed header rendered: %q", malformed)
		}
	}
}

func TestInvalidIntentAndRouteRejected(t *testing.T) {
	for _, intent := range []string{"steps: 0", "steps: -1", "bash: false\n  bash_allow: [git status]", "bash: false\n  bash_deny: [git push]"} {
		if _, err := NeedsTransform([]byte("---\nhomonto:\n  " + intent + "\n---\nbody")); err == nil {
			t.Errorf("accepted %q", intent)
		}
	}
	for _, model := range []string{"provider/model\npermission: allow", "provider/model\r", "provider/\x00model", "provider/\tmodel", "provider/\u2028model"} {
		context := ctx()
		context.Overrides["onto-reviewer"] = ModelSpec{Model: model}
		if _, err := Render("onto-reviewer", []byte(readOnlyReviewer), "opencode", context); err == nil {
			t.Errorf("accepted model %q", model)
		}
	}
}

func TestCatalogAliasUsesSourceModelAndRenamesTaskGrant(t *testing.T) {
	context := ctx()
	context.Names = map[string]string{"onto-reviewer": "audit"}
	out, err := Render("onto-reviewer", []byte(readOnlyReviewer), "opencode", context)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateInstalledName("audit", out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `model: "opus"`) {
		t.Fatalf("lost source model: %s", out)
	}
	out, err = Render("onto", []byte(orchestrator), "opencode", context)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"audit": allow`) || strings.Contains(string(out), `"onto-reviewer": allow`) {
		t.Fatalf("task grant did not follow host alias: %s", out)
	}
}

func TestShippedAgentsHaveFiniteConfigurableSteps(t *testing.T) {
	files, err := fs.Glob(embedded.FS, "subagents/*.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		content, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			t.Fatal(err)
		}
		for _, budget := range []int{0, 37} {
			context := &RenderContext{Overrides: map[string]ModelSpec{"agent": {Model: "provider/model"}}}
			if budget > 0 {
				context.Overrides["agent"] = ModelSpec{Model: "provider/model", Steps: &budget}
			}
			out, err := Render("agent", content, "opencode", context)
			if err != nil {
				t.Fatal(err)
			}
			fm, _, _ := split(out)
			var doc struct {
				Steps int `yaml:"steps"`
			}
			if err := yaml.Unmarshal(fm, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Steps <= 0 || (budget > 0 && doc.Steps != budget) {
				t.Errorf("%s steps = %d, override %d", file, doc.Steps, budget)
			}
		}
	}
}
