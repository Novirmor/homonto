package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

func TestNewChangeWorkflowSelectionPolicy(t *testing.T) {
	policy := hPromptText(t, "skills/homonto/references/workflow-selection.md")
	plain := strings.Join(strings.Fields(policy), " ")
	for _, want := range []string{
		"Before creating a task, inspect the request and any recorded workspace task",
		"Resume a matching task in its recorded engine",
		"Do not ask again on resume",
		"**`no`**", "Engine selection is one-way for this task",
		"`/to`, `/onto-fix`, `/onto-tweak`, and",
		"`/onto` selects the onto family, not its preset",
		"ask **once before task creation**",
		"short task-specific recommendation",
		"**`to`**", "**`onto fix`**", "**`onto tweak`**", "**Full `onto`**",
		"A small bug may fit either `to` or `onto fix`",
		"a small non-bug edit may fit `to` or `onto tweak`",
		"Exclude unavailable or invalid options",
		"follow its normal upgrade to full onto without another selection prompt",
		"not permission to publish, skip verification",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("workflow selection policy missing %q", want)
		}
	}
	for _, file := range []string{
		"subagents/homonto.md",
		"skills/homonto/SKILL.md",
		"skills/homonto/references/autonomy.md",
		"skills/onto/SKILL.md",
		"skills/to/SKILL.md",
		"skills/h-resolve-issue/references/autonomy.md",
	} {
		if !strings.Contains(hPromptText(t, file), "workflow-selection.md") {
			t.Errorf("%s does not refer to the shared selection policy", file)
		}
	}
	for file, want := range map[string]string{
		"commands/onto.md":                "`/onto` selects onto but not fix, tweak, or full",
		"commands/h-resolve-issue.md":     "skill owns spike, engine selection, execution and publication",
		"skills/h-resolve-issue/SKILL.md": "Resume a unique match without prompting",
	} {
		if !strings.Contains(hPromptText(t, file), want) {
			t.Errorf("%s missing %q", file, want)
		}
	}
}

func TestWorkflowSelectionReferenceMaterializesWithEitherFramework(t *testing.T) {
	c, err := Load(embedded.FS)
	if err != nil {
		t.Fatal(err)
	}
	for _, framework := range []string{"onto", "to"} {
		t.Run(framework, func(t *testing.T) {
			entries, err := c.Expand([]string{framework})
			if err != nil {
				t.Fatal(err)
			}
			var skills []string
			for _, entry := range entries {
				skills = append(skills, entry.Name)
			}
			dir := t.TempDir()
			if err := c.Materialize(dir, skills, "none", "none", "", nil); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "homonto", "references", "workflow-selection.md")
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("shared reference absent: %v", err)
			}
		})
	}
}
