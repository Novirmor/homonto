package capability

import (
	"strings"
	"testing"
)

func TestOpenCodeDeclaresEveryCapabilityEnforced(t *testing.T) {
	d := OpenCode()
	for _, n := range Vocabulary() {
		if d.Provides[n] != Enforced {
			t.Errorf("OpenCode declaration: %s = %q, want enforced", n, d.Provides[n])
		}
	}
	if err := d.Check("onto", []string{"skills", "subagents", "agent-tool-limits"}); err != nil {
		t.Fatal(err)
	}
}

func TestCheckNamesMissingAndInstructionOnlyGaps(t *testing.T) {
	d := Declaration{Harness: "lite", Version: "1.0", Provides: map[Name]Level{
		Skills:          Enforced,
		AgentToolLimits: InstructionOnly,
	}}
	err := d.Check("fixture", []string{"skills", "context-hooks", "agent-tool-limits", "context-hooks"})
	if err == nil {
		t.Fatal("expected a capability gap")
	}
	for _, want := range []string{`package "fixture"`, "agent-tool-limits (instruction-only only)", "context-hooks", "lite 1.0"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Count(err.Error(), "context-hooks") != 1 {
		t.Errorf("duplicate requirement reported twice: %v", err)
	}
}

func TestValidateNames(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"skills", "mcp"}, ""},
		{[]string{"skils"}, `unknown harness capability "skils"`},
		{[]string{"mcp", "mcp"}, `"mcp" twice`},
	} {
		err := ValidateNames("framework \"x\" [requires].harness", tc.names)
		if tc.want == "" {
			if err != nil {
				t.Errorf("%v: %v", tc.names, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%v: got %v, want %q", tc.names, err, tc.want)
		}
	}
}
