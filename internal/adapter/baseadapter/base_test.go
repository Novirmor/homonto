package baseadapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/agentfm"
	"github.com/noviopenworks/homonto/internal/config"
)

func TestBuiltinAliasLinksAndCopiesSourceRenderedHostName(t *testing.T) {
	root := t.TempDir()
	b := &Base{Tool: "opencode", Home: t.TempDir(), ProjectRoot: root, SubagentCatalogRoot: root, VariantSuffix: ".opencode.md"}
	content := []byte("---\nname: onto-reviewer\nhomonto:\n  read_only: true\n  bash: false\n---\nbody\n")
	rendered, err := agentfm.Render("onto-reviewer", content, "opencode", &agentfm.RenderContext{
		Names:     map[string]string{"onto-reviewer": "audit"},
		Overrides: map[string]agentfm.ModelSpec{"onto-reviewer": {Model: "provider/model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "onto-reviewer.opencode.md")
	if err := os.WriteFile(source, rendered, 0o644); err != nil {
		t.Fatal(err)
	}
	entry := config.NamedResource{Name: "audit", Resource: config.Resource{Source: "builtin:onto-reviewer", Scope: "project"}}
	b.Subagents = []config.NamedResource{entry}
	links := b.SubagentFileLinks()
	if len(links) != 1 || filepath.Base(links[0].Dst) != "audit.md" {
		t.Fatalf("alias links = %#v", links)
	}
	resolved := links[0].Src
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(links[0].Dst), resolved)
	}
	if filepath.Clean(resolved) != source {
		t.Fatalf("alias must link catalog source, got %s", resolved)
	}
	if err := agentfm.ValidateInstalledName("audit", rendered); err != nil {
		t.Fatal(err)
	}
	b.Subagents[0].Mode = "copy"
	desired, err := b.copySubagentDesired()
	if err != nil {
		t.Fatal(err)
	}
	if string(desired[links[0].Dst]) != string(rendered) {
		t.Fatal("copy alias lost rendered host metadata")
	}
}

func TestLocalAliasMismatchIsDiagnosticWithoutSourceMutation(t *testing.T) {
	for _, mode := range []string{"link", "copy"} {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "subagents"), 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "subagents", "reviewer.md")
		original := "---\r\nname: reviewer\r\npermission:\r\n  edit: deny\r\n---\r\nprompt\r\n"
		if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
			t.Fatal(err)
		}
		b := &Base{Tool: "opencode", Content: root}
		c := &config.Config{Subagents: map[string]config.Subagent{"audit": {Source: "local:reviewer", Mode: mode}}}
		if err := b.Expand(c); err == nil || !strings.Contains(err.Error(), "subagents.audit") || !strings.Contains(err.Error(), "frontmatter name") {
			t.Fatalf("%s mismatch = %v", mode, err)
		}
		actual, err := os.ReadFile(path)
		if err != nil || string(actual) != original {
			t.Fatalf("%s mutated local source: %v", mode, err)
		}
		delete(c.Subagents, "audit")
		c.Subagents["reviewer"] = config.Subagent{Source: "local:reviewer", Mode: mode}
		if err := b.Expand(c); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCRLFNeutralBuiltinCannotFallBackToUnrenderedLink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "reviewer.md"), []byte("---\r\nname: reviewer\r\nhomonto:\r\n  read_only: true\r\n  bash: false\r\n---\r\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := &Base{Tool: "opencode", SubagentCatalogRoot: root, VariantSuffix: ".opencode.md"}
	if !b.SkipsSubagent(config.NamedResource{Name: "reviewer", Resource: config.Resource{Source: "builtin:reviewer"}}) {
		t.Fatal("CRLF neutral intent fell through to unrendered link")
	}
}
