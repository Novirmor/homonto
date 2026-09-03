package catalog

import (
	"io/fs"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

func TestWorkflowCommandsRouteTheirPrimaryAgents(t *testing.T) {
	for command, agent := range map[string]string{"onto": "onto", "to": "to"} {
		content, err := fs.ReadFile(embedded.FS, "commands/"+command+".md")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "agent: "+agent) {
			t.Errorf("/%s must route to primary agent %q", command, agent)
		}
	}
}

func TestBypassResourcesAreDedicatedAndManualOnly(t *testing.T) {
	for _, skill := range []string{"onto-bypass", "to-bypass"} {
		content, err := fs.ReadFile(embedded.FS, "skills/"+skill+"/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "disable-model-invocation: true") {
			t.Errorf("%s must disable model invocation", skill)
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
