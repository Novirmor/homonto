package catalog

import (
	"strings"
	"testing"
)

func TestMultirepoCoordinationContract(t *testing.T) {
	shared := hPromptText(t, "skills/homonto/references/execution.md")
	for _, want := range []string{
		"A task selects exactly one engine", "preserve it on resume",
		"exactly one repository per implementer assignment", "one writer per checkout",
		"all declared repository paths as readable context", "not a runtime sandbox",
		"no scheduler or cross-session checkout reservation",
		"Ask the user once per task", "per-repo exceptions",
		"Continue independent assignments", "parent owns all source commits",
		"Review completed per-repo candidates in parallel", "combined interfaces and integration results",
		"ANY listed repo changes", "pinned to their clean committed revisions",
		"Dirty unchanged verification inputs require a user disposition",
		"Pause every participating writer", "HOMONTO_INTEGRATION_CONTEXT",
		"never to a configured failure", "Ask before delivering a partial subset",
	} {
		if !strings.Contains(shared, want) {
			t.Errorf("shared contract missing %q", want)
		}
	}
	for _, file := range []string{"commands/to.md", "commands/to-do.md", "skills/to-do/SKILL.md", "skills/to/SKILL.md", "subagents/to-implementer.md", "skills/onto-build/SKILL.md"} {
		text := hPromptText(t, file)
		for _, forbidden := range []string{"strictly one at a time", "One implementer at a time", "never in parallel", "implementers edit and commit", "parallel implementers remain supported for disjoint-file tasks"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s retains obsolete contract %q", file, forbidden)
			}
		}
	}
	no := hPromptText(t, "skills/no/references/execution.md")
	for _, want := range []string{"selects `no` exclusively", "Never invoke onto/to", "one repository per implementer assignment", "combined interfaces and integration evidence", "Ask before partial delivery"} {
		if !strings.Contains(no, want) {
			t.Errorf("no entry missing %q", want)
		}
	}
}
