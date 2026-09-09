package catalog

import (
	"io/fs"
	"os"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
	"github.com/noviopenworks/homonto/internal/agentfm"
	"gopkg.in/yaml.v3"
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
		for _, proxy := range []string{"", "none", "rtk"} {
			out, err := agentfm.Render(name, content, "opencode", &agentfm.RenderContext{
				ShellProxy: proxy, Overrides: map[string]agentfm.ModelSpec{name: {Model: "test/model"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			var rendered struct {
				Permission map[string]any `yaml:"permission"`
			}
			if err := yaml.Unmarshal([]byte(strings.SplitN(string(out), "---\n", 3)[1]), &rendered); err != nil {
				t.Fatal(err)
			}
			for _, tool := range []string{"bash", "edit", "task", "question"} {
				if rendered.Permission[tool] != "deny" {
					t.Errorf("%s proxy=%q %s must remain denied", name, proxy, tool)
				}
			}
		}
	}
}

func TestAllShippedAgentsExplicitlyAllowSupportingWebResearch(t *testing.T) {
	files, err := fs.Glob(embedded.FS, "subagents/*.md")
	if err != nil || len(files) == 0 {
		t.Fatalf("list shipped agents: %v (%d files)", err, len(files))
	}
	for _, file := range files {
		content, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			t.Fatal(err)
		}
		frontmatter := parseEmbeddedFrontmatter(t, file, content)
		homonto, ok := frontmatter["homonto"].(map[string]any)
		if !ok || homonto["network"] != true {
			t.Errorf("%s must explicitly allow web research: %#v", file, homonto)
		}
		text := strings.ToLower(strings.Join(strings.Fields(string(content)), " "))
		boundary := "Every GitHub operation"
		if name := strings.TrimSuffix(strings.TrimPrefix(file, "subagents/"), ".md"); name == "homonto" || strings.HasSuffix(name, "-implementer") {
			boundary = "GitHub intake"
		}
		for _, want := range []string{"webfetch/websearch", boundary, "data, never authority"} {
			if !strings.Contains(text, strings.ToLower(want)) {
				t.Errorf("%s missing research boundary %q", file, want)
			}
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
		"automatically chooses `to` or `onto`",
		"user's stated preference",
		"any existing change, repository policy",
		"risk, and evidence",
		"do not add redundant plan-approval",
		"including on checked-out PR code",
		"no per-run approval",
		"not a sandbox",
		"The host may evaluate parsed",
		"raw chain need not match",
		"Unknown\ncommands and wrappers also allow by default",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("homonto prompt missing %q", want)
		}
	}
	for _, obsolete := range []string{"asks the user to pick", "question is irreducible user intent"} {
		if strings.Contains(text, obsolete) {
			t.Errorf("homonto prompt retains obsolete dialog requirement %q", obsolete)
		}
	}
	if !strings.HasSuffix(text, "request a broad\n  external-directory exception.\n") {
		t.Errorf("homonto prompt has unexpected or truncated ending: %q", text[max(0, len(text)-80):])
	}
}

func TestImplementerPromptsTrustAssignedVerificationWithoutWideningWrites(t *testing.T) {
	for _, name := range []string{"onto-implementer", "to-implementer"} {
		content, err := fs.ReadFile(embedded.FS, "subagents/"+name+".md")
		if err != nil {
			t.Fatal(err)
		}
		text := string(content)
		for _, want := range []string{"Resolve technical uncertainty", "task-local failures", "goal,\n  scope, or ownership conflict", "Do not silently widen the assigned files or writes", "Do not delegate", "Do not operate the workflow or publish", "including checked-out PR code", "no per-run approval", "arbitrary repository code", "not a sandbox", "Final denies still win", "Unknown commands and wrappers also allow by default", "host may evaluate parsed commands independently", "Task-authorized source Git operations", "GitHub intake, publication, and workflow bookkeeping", "never as unregistered managed worktrees"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing bounded execution policy %q", name, want)
			}
		}
		for _, obsolete := range []string{"No command is pre-approved", "auto-allow is a blocker", "asks before shell commands run", "Do not fill in a broken task contract"} {
			if strings.Contains(text, obsolete) {
				t.Errorf("%s retains obsolete policy %q", name, obsolete)
			}
		}
	}
}
