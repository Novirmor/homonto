package catalog

import (
	"io/fs"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

func TestSubagentsEmbedded(t *testing.T) {
	for _, name := range []string{
		"onto", "onto-reviewer", "onto-explorer", "onto-implementer", "onto-skeptic",
		"to", "to-reviewer", "to-explorer", "to-implementer", "to-skeptic",
	} {
		p := "subagents/" + name + ".md"
		if _, err := fs.Stat(embedded.FS, p); err != nil {
			t.Errorf("%s not embedded: %v", p, err)
		}
	}
}

func TestReadOnlySubagentsDenyBash(t *testing.T) {
	for _, name := range []string{
		"onto-reviewer", "onto-explorer", "onto-skeptic",
		"to-reviewer", "to-explorer", "to-skeptic",
	} {
		file := "subagents/" + name + ".md"
		content, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			t.Fatal(err)
		}
		frontmatter := parseEmbeddedFrontmatter(t, file, content)
		homonto, ok := frontmatter["homonto"].(map[string]any)
		if !ok {
			t.Fatalf("%s has no homonto capability block", file)
		}
		if homonto["read_only"] != true || homonto["bash"] != false {
			t.Errorf("%s must deny both edits and bash: %#v", file, homonto)
		}
	}
}

func TestOntoPrimaryPromptIsComplete(t *testing.T) {
	content, err := fs.ReadFile(embedded.FS, "subagents/onto.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Count(text, "## The tooling around you: homonto") != 1 {
		t.Errorf("onto prompt must contain one tooling section")
	}
	if !strings.HasSuffix(text, "Hand the\nuser to the `to` primary after a demotion.\n") {
		t.Errorf("onto prompt has unexpected or truncated ending: %q", text[max(0, len(text)-80):])
	}
}
