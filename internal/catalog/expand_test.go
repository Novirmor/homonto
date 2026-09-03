package catalog

import (
	"strings"
	"testing"
	"testing/fstest"
)

// graphFS builds a catalog whose skill content always exists, so Load passes and
// tests can focus on the dependency graph. deps maps framework -> dep names;
// skills maps framework -> its own skill names; commands maps framework -> its
// own command names.
func graphFS(deps map[string][]string, skills, commands map[string][]string) fstest.MapFS {
	m := fstest.MapFS{"version.txt": {Data: []byte("0.1.0")}}
	for fw, sk := range skills {
		var b strings.Builder
		b.WriteString("name = \"" + fw + "\"\nversion = \"0.1.0\"\n")
		if d := deps[fw]; len(d) > 0 {
			b.WriteString("[dependencies]\nframeworks = [")
			for i, dep := range d {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString("\"" + dep + "\"")
			}
			b.WriteString("]\n")
		}
		b.WriteString("[skills]\n")
		for _, s := range sk {
			b.WriteString(s + " = \"skills/" + s + "\"\n")
			m["skills/"+s+"/SKILL.md"] = &fstest.MapFile{Data: []byte("x")}
		}
		if cs := commands[fw]; len(cs) > 0 {
			b.WriteString("[commands]\n")
			for _, cmd := range cs {
				b.WriteString(cmd + " = \"commands/" + cmd + ".md\"\n")
				m["commands/"+cmd+".md"] = &fstest.MapFile{Data: []byte("x")}
			}
		}
		m["frameworks/"+fw+"/framework.toml"] = &fstest.MapFile{Data: []byte(b.String())}
	}
	return m
}

func TestExpandTransitiveAndDedup(t *testing.T) {
	// depfw -> basefw, specfw; basefw and specfw share "shared".
	c, err := Load(graphFS(
		map[string][]string{"depfw": {"basefw", "specfw"}},
		map[string][]string{
			"depfw":  {"depfw-open"},
			"basefw": {"base-skill", "shared"},
			"specfw": {"specfw-new", "shared"},
		},
		nil,
	))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := c.Expand([]string{"depfw"})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.Name)
	}
	want := []string{"base-skill", "depfw-open", "shared", "specfw-new"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("expanded (sorted, deduped) = %v, want %v", names, want)
	}
}

func TestExpandDetectsCycle(t *testing.T) {
	c, err := Load(graphFS(
		map[string][]string{"a": {"b"}, "b": {"a"}},
		map[string][]string{"a": {"sa"}, "b": {"sb"}},
		nil,
	))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	_, err = c.Expand([]string{"a"})
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
	if !strings.Contains(err.Error(), "a") || !strings.Contains(err.Error(), "b") {
		t.Fatalf("cycle error should name the chain, got %v", err)
	}
}

func TestExpandCommandsTransitiveAndDedup(t *testing.T) {
	c, err := Load(graphFS(
		map[string][]string{"depfw": {"basefw", "specfw"}},
		map[string][]string{"depfw": {"s"}, "basefw": {"s"}, "specfw": {"s"}},
		map[string][]string{
			"depfw":  {"depfw-cmd"},
			"basefw": {"brainstorm-cmd", "shared-cmd"},
			"specfw": {"specfw-cmd", "shared-cmd"},
		},
	))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got, err := c.ExpandCommands([]string{"depfw"})
	if err != nil {
		t.Fatalf("expand commands: %v", err)
	}
	var names []string
	for _, e := range got {
		names = append(names, e.Name)
	}
	want := []string{"brainstorm-cmd", "depfw-cmd", "shared-cmd", "specfw-cmd"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("expanded commands = %v, want %v", names, want)
	}
}

func TestExpandCommandsDetectsCycle(t *testing.T) {
	c, err := Load(graphFS(
		map[string][]string{"a": {"b"}, "b": {"a"}},
		map[string][]string{"a": {"sa"}, "b": {"sb"}},
		map[string][]string{"a": {"ca"}, "b": {"cb"}},
	))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, err := c.ExpandCommands([]string{"a"}); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestExpandSubagentsIncludesFrameworkSubagent(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	got, err := c.ExpandSubagents([]string{"onto"})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	found := false
	for _, e := range got {
		if e.Name == "onto-explorer" {
			found = true
			if e.Framework != "onto" {
				t.Errorf("onto-explorer framework = %q, want onto", e.Framework)
			}
		}
	}
	if !found {
		t.Fatal("onto-explorer not returned by ExpandSubagents([onto])")
	}
	to, err := c.ExpandSubagents([]string{"to"})
	if err != nil {
		t.Fatalf("expand to: %v", err)
	}
	found = false
	for _, e := range to {
		if e.Name == "to" {
			found = true
			if e.Framework != "to" {
				t.Errorf("to framework = %q, want to", e.Framework)
			}
		}
	}
	if !found {
		t.Fatalf("to framework must expand its primary dispatcher, got %v", to)
	}
}

func TestWorkflowFrameworksInstallSharedHomontoKnowledge(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for _, framework := range []string{"onto", "to"} {
		skills, err := c.Expand([]string{framework})
		if err != nil {
			t.Fatalf("expand %s: %v", framework, err)
		}
		found := false
		for _, skill := range skills {
			if skill.Name == "homonto" {
				found = true
				if skill.Framework != framework {
					t.Errorf("%s homonto skill framework = %q", framework, skill.Framework)
				}
			}
		}
		if !found {
			t.Errorf("%s framework does not install the shared homonto skill", framework)
		}
	}
}

func TestWorkflowFrameworksInstallDedicatedBypassCommandsOnly(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for framework, bypass := range map[string]string{"onto": "onto-bypass", "to": "to-bypass"} {
		skills, err := c.Expand([]string{framework})
		if err != nil {
			t.Fatalf("expand skills for %s: %v", framework, err)
		}
		commands, err := c.ExpandCommands([]string{framework})
		if err != nil {
			t.Fatalf("expand commands for %s: %v", framework, err)
		}
		if hasNamedSkill(skills, bypass) || !hasNamedCommand(commands, bypass) {
			t.Errorf("%s bypass %q must install as a command, not a model-discoverable skill", framework, bypass)
		}
	}
}

func hasNamedSkill(resources []ExpandedSkill, name string) bool {
	for _, resource := range resources {
		if resource.Name == name {
			return true
		}
	}
	return false
}

func hasNamedCommand(resources []ExpandedCommand, name string) bool {
	for _, resource := range resources {
		if resource.Name == name {
			return true
		}
	}
	return false
}
