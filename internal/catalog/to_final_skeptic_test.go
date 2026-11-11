package catalog

import (
	"strings"
	"testing"
)

// Dispatch is instruction-enforced; these tests pin the shipped instructions,
// not a claim that a real harness completed independent reviews.
func TestToFinalSkepticInstructions(t *testing.T) {
	prompt := hPromptText(t, "skills/to-done/SKILL.md")
	for _, want := range []string{
		"`off`: zero, intentionally disabled", "`one`: one", "`two`: two independent passes",
		"`auto`: two", "Higher-risk criteria take precedence", "requirements for independent review override",
		"Only a literal `Status: complete`", "refresh every required pass", "reclassify `auto`",
		"selected and completed counts", "Distinguish disabled",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("missing %q", want)
		}
	}
}
