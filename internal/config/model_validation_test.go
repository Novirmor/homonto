package config

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestUnknownConfigAndIgnoredTuningFieldsRejected(t *testing.T) {
	for _, doc := range []string{
		"unknown = true",
		"[workflow]\nrooot = 'docs'",
		"[subagents.audit.opencode]\nmodel = 'provider/model'\nvaraint = 'high'",
		"[subagents.audit]\nsource = 'builtin:onto-reviewer'\nstep = 100\n[subagents.audit.opencode]\nmodel = 'provider/model'",
		"[subagents.audit]\nsource = 'builtin:onto-reviewer'\nsteps = 100\n[subagents.audit.opencode]\nmodel = 'provider/model'",
	} {
		if err := loadDoc(t, doc); err == nil {
			t.Errorf("unknown config accepted: %s", doc)
		}
	}
	for _, field := range []string{"scope = 'project'", "repo = 'app'", "mode = 'link'", "version = '1'", "targets = ['opencode']", "digest = 'sha256:abc'"} {
		doc := "[subagents.onto-reviewer]\n" + field + "\n[subagents.onto-reviewer.opencode]\nmodel = 'provider/model'"
		if err := loadDoc(t, doc); err == nil || !strings.Contains(err.Error(), "tune-only") {
			t.Errorf("ignored field %s: %v", field, err)
		}
	}
}

func TestSubagentGuideBudgetExampleTunesInstalledAgent(t *testing.T) {
	guide, err := os.ReadFile("../../docs/guides/subagents.md")
	if err != nil {
		t.Fatal(err)
	}
	const header = "[subagents.onto-skeptic.opencode]\n"
	_, example, found := strings.Cut(string(guide), header)
	if !found {
		t.Fatal("guide model example missing")
	}
	model, _, found := strings.Cut(example, "```")
	if !found {
		t.Fatal("guide model example fence missing")
	}
	doc := "[frameworks.onto]\nsource = 'builtin:onto'\nscope = 'project'\n" + header + model + modelsFor("homonto", "onto-explorer", "onto-reviewer", "onto-implementer")
	c, err := loadDocCfg(t, doc)
	if err != nil {
		t.Fatalf("documented tune block must pass strict config loading: %v", err)
	}
	agent := c.Subagents["onto-skeptic"]
	if !agent.IsTuneOnly() || agent.OpenCode.Steps == nil || *agent.OpenCode.Steps != 120 {
		t.Fatalf("documented steps did not tune the installed agent: %#v", agent)
	}
}

func TestModelControlsAndNonpositiveBudgetsRejected(t *testing.T) {
	for _, model := range []string{"provider/model\n", "\tprovider/model", "provider/\rmodel", "provider/\x00model", "provider/\u2028model"} {
		doc := fmt.Sprintf("[subagents.audit]\nsource = 'builtin:onto-reviewer'\n[subagents.audit.opencode]\nmodel = %q\n", model)
		if err := loadDoc(t, doc); err == nil {
			t.Errorf("accepted model %q", model)
		}
	}
	for _, steps := range []int{-1, 0, 23} {
		doc := fmt.Sprintf("[subagents.audit]\nsource = 'builtin:onto-reviewer'\n[subagents.audit.opencode]\nmodel = 'provider/model'\nvariant = '1'\nsteps = %d\n", steps)
		err := loadDoc(t, doc)
		if steps > 0 && err != nil {
			t.Fatal(err)
		}
		if steps <= 0 && (err == nil || !strings.Contains(err.Error(), "steps must be positive")) {
			t.Errorf("steps %d: %v", steps, err)
		}
	}
}

func TestBuiltinAliasesMustHaveOneHostIdentity(t *testing.T) {
	doc := "[subagents.audit]\nsource = 'builtin:onto-reviewer'\n[subagents.audit.opencode]\nmodel = 'provider/model'\n"
	if err := loadDoc(t, doc); err != nil {
		t.Fatal(err)
	}
	doc += "[subagents.other]\nsource = 'builtin:onto-reviewer'\n[subagents.other.opencode]\nmodel = 'provider/model'\n"
	if err := loadDoc(t, doc); err == nil || !strings.Contains(err.Error(), "different aliases") {
		t.Fatalf("duplicate alias: %v", err)
	}
}

func TestBudgetRoutesCompareByValue(t *testing.T) {
	a, b, c := 23, 23, 24
	if !(ModelRoute{Steps: &a}).Equal(ModelRoute{Steps: &b}) || (ModelRoute{Steps: &a}).Equal(ModelRoute{Steps: &c}) || (ModelRoute{Steps: &a}).Equal(ModelRoute{}) {
		t.Fatal("budget route equality must compare presence and value, not pointer identity")
	}
}
