package catalog

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
	"gopkg.in/yaml.v3"
)

func TestWorkflowCommandsRouteTheirPrimaryAgents(t *testing.T) {
	entries, err := fs.ReadDir(embedded.FS, "commands")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		command := strings.TrimSuffix(entry.Name(), ".md")
		agent := ""
		switch {
		case command == "onto" || strings.HasPrefix(command, "onto-"):
			agent = "onto"
		case command == "to" || strings.HasPrefix(command, "to-"):
			agent = "to"
		default:
			continue
		}
		content, err := fs.ReadFile(embedded.FS, "commands/"+command+".md")
		if err != nil {
			t.Fatal(err)
		}
		frontmatter := parseEmbeddedFrontmatter(t, "commands/"+command+".md", content)
		if frontmatter["agent"] != agent {
			t.Errorf("/%s must route to primary agent %q", command, agent)
		}
		if subtask, ok := frontmatter["subtask"]; ok && subtask != false {
			t.Errorf("/%s must stay in the primary session, subtask = %v", command, subtask)
		}
	}
}

func TestEmbeddedCommandsUseSupportedOpenCodeFrontmatter(t *testing.T) {
	allowed := map[string]bool{
		"description": true, "agent": true, "model": true,
		"variant": true, "subtask": true,
	}
	err := fs.WalkDir(embedded.FS, "commands", func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(file) != ".md" {
			return err
		}
		content, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			return err
		}
		frontmatter := parseEmbeddedFrontmatter(t, file, content)
		if strings.TrimSpace(asString(frontmatter["description"])) == "" {
			t.Errorf("%s must have a description", file)
		}
		for key := range frontmatter {
			if !allowed[key] {
				t.Errorf("%s uses unsupported OpenCode command field %q", file, key)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedSkillsHaveOpenCodeMetadata(t *testing.T) {
	err := fs.WalkDir(embedded.FS, "skills", func(file string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Base(file) != "SKILL.md" {
			return err
		}
		content, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			return err
		}
		frontmatter := parseEmbeddedFrontmatter(t, file, content)
		wantName := path.Base(path.Dir(file))
		if frontmatter["name"] != wantName {
			t.Errorf("%s name = %q, want %q", file, frontmatter["name"], wantName)
		}
		if strings.TrimSpace(asString(frontmatter["description"])) == "" {
			t.Errorf("%s must have a description", file)
		}
		for _, unsupported := range []string{"argument-hint", "disable-model-invocation"} {
			if _, ok := frontmatter[unsupported]; ok {
				t.Errorf("%s uses unsupported OpenCode skill field %q", file, unsupported)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBypassResourcesAreDedicatedCommands(t *testing.T) {
	for _, resource := range []string{"onto-bypass", "to-bypass"} {
		if _, err := fs.Stat(embedded.FS, "skills/"+resource+"/SKILL.md"); err == nil {
			t.Errorf("%s must not be a model-discoverable skill", resource)
		}
		content, err := fs.ReadFile(embedded.FS, "commands/"+resource+".md")
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(content))
		if !strings.Contains(lower, "user explicitly asks") || !strings.Contains(lower, "complete contract") {
			t.Errorf("%s command must carry the explicit-user contract", resource)
		}
	}
	for _, root := range []string{"skills", "subagents"} {
		err := fs.WalkDir(embedded.FS, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.Contains(path, "bypass") {
				return err
			}
			content, err := fs.ReadFile(embedded.FS, path)
			if err != nil {
				return err
			}
			lower := strings.ToLower(string(content))
			for _, prohibited := range []string{"/onto-bypass", "/to-bypass", "onto-bypass", "to-bypass", "onto bypass", "to bypass"} {
				if strings.Contains(lower, prohibited) {
					t.Errorf("ordinary resource %s must not reference %q", path, prohibited)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkflowPromptsDefaultToAutonomousContinuation(t *testing.T) {
	for _, file := range []string{
		"skills/onto/SKILL.md", "skills/to/SKILL.md",
		"subagents/onto.md", "subagents/to.md",
	} {
		content, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(content)), " ")
		if !strings.Contains(text, "endpoint") || !strings.Contains(text, "pause") || !strings.Contains(text, "Continue") && !strings.Contains(text, "continue") {
			t.Errorf("%s must default to continuing through the workflow", file)
		}
	}

	policy, err := fs.ReadFile(embedded.FS, "skills/homonto/references/autonomy.md")
	if err != nil {
		t.Fatal(err)
	}
	policyText := strings.Join(strings.Fields(string(policy)), " ")
	for _, want := range []string{
		"choose the safest reversible option",
		"Ask the user only when",
		"not automatically a user question",
		"Do not ask for approval of a summary, proposal, plan, diff, phase transition, or close plan",
	} {
		if !strings.Contains(policyText, want) {
			t.Errorf("autonomy policy missing %q", want)
		}
	}

	for _, file := range []string{
		"skills/onto/SKILL.md", "skills/onto-open/SKILL.md",
		"skills/onto-design/SKILL.md", "skills/onto-build/SKILL.md",
		"skills/onto-verify/SKILL.md", "skills/onto-close/SKILL.md",
		"skills/onto-fix/SKILL.md", "skills/onto-tweak/SKILL.md",
	} {
		content, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			t.Fatal(err)
		}
		for _, prohibited := range []string{"Always fresh input", "when in doubt, stop and ask", "pause at the plan-ready gate"} {
			if strings.Contains(string(content), prohibited) {
				t.Errorf("%s retains ceremonial instruction %q", file, prohibited)
			}
		}
	}
}

func TestWorkflowRecoveryAndPresetTransitionsAreExecutable(t *testing.T) {
	dispatcher, err := fs.ReadFile(embedded.FS, "skills/onto/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"accepts the derived phase", "skips `onto advance`", "onto set workflow <name>"} {
		if !strings.Contains(string(dispatcher), want) {
			t.Errorf("onto dispatcher missing mismatch/upgrade instruction %q", want)
		}
	}
	for _, file := range []string{"skills/onto-fix/SKILL.md", "skills/onto-tweak/SKILL.md"} {
		content, err := fs.ReadFile(embedded.FS, file)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Join(strings.Fields(string(content)), " ")
		for _, want := range []string{"advance <name>` to enter verify", "set verify-scale <name> light", "set workflow <name> full"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s missing executable preset transition %q", file, want)
			}
		}
	}
	open, err := fs.ReadFile(embedded.FS, "skills/onto-open/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Fresh work only", "Downward-mismatch recovery", "do not run `onto new`", "directly to `onto-design`"} {
		if !strings.Contains(string(open), want) {
			t.Errorf("onto-open missing executable existing-workspace recovery %q", want)
		}
	}

	template, err := fs.ReadFile(embedded.FS, "skills/onto-verify/references/verification.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), "\nResult: <pass | fail>\n") || strings.Contains(string(template), "\nResult: pass | fail\n") {
		t.Error("verification template must not ship a placeholder that parses as a pass")
	}
}

func parseEmbeddedFrontmatter(t *testing.T, file string, content []byte) map[string]any {
	t.Helper()
	text := string(content)
	if !strings.HasPrefix(text, "---\n") {
		t.Fatalf("%s has no YAML frontmatter", file)
	}
	frontmatter, _, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	if !ok {
		t.Fatalf("%s has unterminated YAML frontmatter", file)
	}
	var fields map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &fields); err != nil {
		t.Fatalf("parse %s frontmatter: %v", file, err)
	}
	return fields
}

func asString(value any) string {
	text, _ := value.(string)
	return text
}
