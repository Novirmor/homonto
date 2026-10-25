package catalog

import (
	"strings"
	"testing"
)

func TestNoEngineHasMinimalCatalogSurface(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	no, ok := c.Framework("no")
	if !ok {
		t.Fatal("missing no engine manifest")
	}
	if no.Kind != "engine" {
		t.Fatalf("no kind = %q, want engine", no.Kind)
	}
	if len(no.Skills) != 1 || no.Skills["no"] != "skills/no" {
		t.Fatalf("no skills = %#v, want only no", no.Skills)
	}
	if len(no.Commands) != 1 || no.Commands["no"] != "commands/no.md" {
		t.Fatalf("no commands = %#v, want only no", no.Commands)
	}
	if len(no.Subagents) != 2 || no.Subagents["no-implementer"] == "" || no.Subagents["no-reviewer"] == "" {
		t.Fatalf("no subagents = %#v, want implementer and reviewer only", no.Subagents)
	}

	entries, err := c.Expand([]string{"no"})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Framework != "no" {
			t.Fatalf("no expansion includes %q from %q", entry.Name, entry.Framework)
		}
	}

	skill := hPromptText(t, "skills/no/SKILL.md")
	for _, want := range []string{"`no` provides exactly two subagents", "`no-implementer`", "`no-reviewer`"} {
		if !strings.Contains(skill, want) {
			t.Errorf("no skill missing %q", want)
		}
	}
}

func TestBuiltinPackageKinds(t *testing.T) {
	c, err := New()
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"h": "workflow", "onto": "engine", "to": "engine", "no": "engine"} {
		pkg, ok := c.Framework(name)
		if !ok || pkg.Kind != want {
			t.Errorf("%s kind = %q, want %q", name, pkg.Kind, want)
		}
	}
}
