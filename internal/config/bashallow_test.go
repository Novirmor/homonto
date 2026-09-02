package config

import (
	"strings"
	"testing"
)

// TestLoadValidatesBashAllowAdd (A7): exact commands pass; pattern
// metacharacters, shell composition, environment assignments, credential-like
// names, and destructive commands fail at load.
func TestLoadValidatesBashAllowAdd(t *testing.T) {
	framework := `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

` + modelsFor("onto-explorer", "onto-reviewer", "onto-implementer", "onto-skeptic")
	ok := []string{"go test ./...", "git status", "pnpm run test:unit"}
	for _, add := range ok {
		doc := framework + `
[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
bash_allow_add = ["` + add + `"]
`
		if err := loadDoc(t, doc); err != nil {
			t.Errorf("valid addition %q rejected: %v", add, err)
		}
	}
	bad := []string{"git *", "a && b", "a | b", "FOO=bar make", "echo $TOKEN", "rm -rf /", "sudo make", "sh -c 'x'"}
	for _, add := range bad {
		doc := framework + `
[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
bash_allow_add = ["` + strings.ReplaceAll(add, "'", "\\'") + `"]
`
		if err := loadDoc(t, doc); err == nil {
			t.Errorf("invalid addition %q accepted", add)
		}
	}
}

// TestBashAllowAddRidesTheTuneOnlySignal: a source-less entry with
// bash_allow_add (plus its required model) is a tune-only entry (no agent
// projection), like any model-only tune.
func TestBashAllowAddRidesTheTuneOnlySignal(t *testing.T) {
	doc := `
[frameworks.onto]
source = "builtin:onto"
scope = "project"

` + modelsFor("onto-explorer", "onto-reviewer", "onto-implementer", "onto-skeptic") + `
[subagents.onto.opencode]
model = "anthropic/claude-opus-4-8"
bash_allow_add = ["git status"]
`
	if err := loadDoc(t, doc); err != nil {
		t.Fatalf("tune-only bash_allow_add must load: %v", err)
	}
	c, err := loadDocCfg(t, doc)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Subagents["onto"].IsTuneOnly() {
		t.Fatal("bash_allow_add entry must be tune-only")
	}
	if got := c.Subagents["onto"].OpenCode.BashAllowAdd; len(got) != 1 || got[0] != "git status" {
		t.Fatalf("BashAllowAdd lost: %v", got)
	}
}
