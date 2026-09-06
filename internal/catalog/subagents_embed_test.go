package catalog

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

func TestSubagentsEmbedded(t *testing.T) {
	for _, name := range []string{
		"homonto", "h-spike", "h-review",
		"onto-reviewer", "onto-explorer", "onto-implementer", "onto-skeptic",
		"to-reviewer", "to-explorer", "to-implementer", "to-skeptic",
	} {
		p := "subagents/" + name + ".md"
		if _, err := fs.Stat(embedded.FS, p); err != nil {
			t.Errorf("%s not embedded: %v", p, err)
		}
	}
	// The per-framework primaries are gone (ADR 0045): one shared homonto
	// coordinator replaced them, and nothing may resurrect the old files as
	// loose subagents — the commands all route agent: homonto.
	for _, gone := range []string{"onto", "to"} {
		p := "subagents/" + gone + ".md"
		if _, err := fs.Stat(embedded.FS, p); !os.IsNotExist(err) {
			t.Errorf("%s must not ship as a subagent (ADR 0045): %v", p, err)
		}
	}
}

func TestReadOnlySubagentsDenyBash(t *testing.T) {
	for _, name := range []string{
		"h-spike", "h-review",
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

// TestHomontoPrimaryPromptIsComplete pins the shared coordinator's prompt
// shape: one tooling section, both workflow doctrines referenced, the GitHub
// intake boundary stated, and the repo-boundary ending intact.
func TestHomontoPrimaryPromptIsComplete(t *testing.T) {
	content, err := fs.ReadFile(embedded.FS, "subagents/homonto.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if strings.Count(text, "## The tooling around you: homonto") != 1 {
		t.Errorf("homonto prompt must contain one tooling section")
	}
	for _, want := range []string{
		"`onto` and `to` dispatcher skills are your doctrine",
		"## GitHub intake",
		"without an explicit approval",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("homonto prompt missing %q", want)
		}
	}
	if !strings.HasSuffix(text, "request a broad\n  external-directory exception.\n") {
		t.Errorf("homonto prompt has unexpected or truncated ending: %q", text[max(0, len(text)-80):])
	}
}
