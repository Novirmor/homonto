package agentfm

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSourceOnlyWorkerDeniesNestedWorkflowRecords(t *testing.T) {
	context := ctx()
	context.ExternalDirectoriesByAgent = map[string][]string{"onto": {"/source", "/source/records", "/worktrees/a"}}
	context.ExternalDirectoryDeniesByAgent = map[string][]string{"onto": {"/source/records", "/source/records"}}
	out, err := Render("onto", []byte(orchestrator), "opencode", context)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	allow, deny := strings.Index(text, `"/source/**": allow`), strings.LastIndex(text, `"/source/records/**": deny`)
	if allow < 0 || deny < allow || strings.Count(text, `"/source/records/**"`) != 2 {
		t.Fatalf("workflow denial must follow source allow, without duplicate YAML keys:\n%s", out)
	}
	fm, _, _ := split(out)
	var rendered struct {
		Permission map[string]any `yaml:"permission"`
	}
	if err := yaml.Unmarshal(fm, &rendered); err != nil {
		t.Fatal(err)
	}
	edit, ok := rendered.Permission["edit"].(map[string]any)
	if !ok || edit["*"] != nil || edit["/source/records/**"] != "deny" {
		t.Fatalf("native edits must deny records inside the host workspace: %#v", edit)
	}
	if !strings.Contains(text, `"/worktrees/a/**": allow`) || strings.Contains(text, `"/worktrees/**": allow`) {
		t.Fatalf("worktree access must remain per alias:\n%s", out)
	}
	readOnly := "---\nhomonto:\n  read_only: true\n  bash: false\n---\nbody\n"
	out, err = Render("onto", []byte(readOnly), "opencode", context)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  edit: deny", "  bash: deny"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %s:\n%s", want, out)
		}
	}
	if strings.Contains(string(out), "external_directory:") {
		t.Fatalf("read-only worker gained external access:\n%s", out)
	}
}
