package config

import (
	"strings"
	"testing"
)

func TestExpertsAreIndependentPackages(t *testing.T) {
	doc := "[experts.architecture]\nsource='builtin:architecture'\nscope='project'\n" + modelsFor("homonto", "expert-architecture", "expert-architecture-reviewer")
	c, err := loadDocCfg(t, doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Engines) != 0 || len(c.Workflows) != 0 {
		t.Fatal("an expert implicitly installed an engine/workflow")
	}
	if err := c.ValidateExpertInstallation(); err != nil {
		t.Fatal(err)
	}
	experts, err := c.ResolvedExperts("opencode")
	if err != nil || len(experts) != 1 || experts[0].Skill != "expert-architecture" {
		t.Fatalf("inventory = %+v, %v", experts, err)
	}
	entries, err := c.ExpandedSubagentEntriesForTool("opencode")
	if err != nil || len(entries) != 3 {
		t.Fatalf("expert-only agents = %+v, %v", entries, err)
	}
}

func TestExpertValidation(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"[experts.x]\nsource='builtin:onto'\nscope='project'", "not an expert"},
		{"[experts.x]\nsource='builtin:missing'\nscope='project'", "unknown expert"},
		{"[experts.architecture]\nsource='builtin:architecture'", "scope"},
		{"[experts.x]\nsource='builtin:architecture'\nscope='project'\n[engines.x]\nsource='builtin:no'\nscope='project'", "both an expert"},
		{"[experts.x]\nsource='builtin:architecture'\nscope='project'\nrepo='app'", "repo is not supported"},
		{"[experts.x]\nsource='builtin:architecture'\nscope='project'\nskill='ignored'", "external:"},
		{"[experts.x]\nsource='remote:https://example.com/expert.tar.gz'\nscope='project'", "digest"},
		{"[experts.x]\nsource='external:team/*'\nscope='project'\ndescription='test'", "invalid external"},
		{"[experts.x]\nsource='external:homonto'\nscope='project'\ndescription='test'", "own expert"},
		{"[experts.x]\nsource='external:team/advisor'\nscope='project'", "description"},
	} {
		t.Run(tc.want+tc.body, func(t *testing.T) {
			err := loadDoc(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

func TestExternalExpertDoesNotOwnNativeResources(t *testing.T) {
	c, err := loadDocCfg(t, "[experts.company]\nsource='external:team/advisor'\nscope='project'\ndescription='Company expertise'\nskill='company-rules'\nreviewer='team/reviewer'\n"+modelsFor("homonto"))
	if err != nil {
		t.Fatal(err)
	}
	experts, err := c.ResolvedExperts("opencode")
	if err != nil || len(experts) != 1 || !experts[0].External || experts[0].Analyst != "team/advisor" {
		t.Fatalf("external inventory: %+v, %v", experts, err)
	}
	entries, err := c.ExpandedSubagentEntriesForTool("opencode")
	if err != nil || len(entries) != 1 || entries[0].Name != "homonto" {
		t.Fatalf("external agent was projected: %+v, %v", entries, err)
	}
}

func TestExpertsComposeWithEveryEngine(t *testing.T) {
	for engine, agents := range map[string][]string{
		"no":   {"no-implementer", "no-reviewer"},
		"to":   {"to-explorer", "to-implementer", "to-reviewer", "to-skeptic"},
		"onto": {"onto-explorer", "onto-implementer", "onto-reviewer", "onto-skeptic"},
	} {
		t.Run(engine, func(t *testing.T) {
			doc := "[engines." + engine + "]\nsource='builtin:" + engine + "'\nscope='project'\n[experts.ui]\nsource='builtin:ui-ux'\nscope='project'\n" + modelsFor(append(agents, "homonto", "expert-ui-ux", "expert-ui-ux-reviewer")...)
			c, err := loadDocCfg(t, doc)
			if err != nil {
				t.Fatal(err)
			}
			entries, err := c.ExpandedSubagentEntriesForTool("opencode")
			if err != nil || len(entries) != len(agents)+3 {
				t.Fatalf("composed agents = %+v, %v", entries, err)
			}
		})
	}
}
