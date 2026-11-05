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

func TestTierDerivation(t *testing.T) {
	if got := OpenCode().Tier(); got != TierWorkflow {
		t.Errorf("OpenCode tier = %s", got)
	}
	if got := ClaudeCode().Tier(); got != TierWorkflow {
		t.Errorf("Claude Code tier = %s", got)
	}
	lite := Declaration{Provides: map[Name]Level{Skills: Enforced, Subagents: Enforced, QuestionDialog: Limited, MCP: Enforced, AgentToolLimits: InstructionOnly}}
	if got := lite.Tier(); got != TierLite {
		t.Errorf("lite tier = %s", got)
	}
	if got := (Declaration{Provides: map[Name]Level{MCP: Enforced}}).Tier(); got != TierProjection {
		t.Errorf("projection tier = %s", got)
	}
}

func TestLimitedSatisfiesAndIsReported(t *testing.T) {
	d := ClaudeCode()
	if err := d.Check("onto", []string{"skills", "shell-rules", "question-dialog", "permission-events"}); err != nil {
		t.Fatalf("accepted limitations must satisfy: %v", err)
	}
	if err := d.Check("panel", []string{"status-ui"}); err == nil || !strings.Contains(err.Error(), "status-ui") {
		t.Fatalf("absent capability: %v", err)
	}
	for n, level := range d.Provides {
		if (level == Limited || level == InstructionOnly) && d.Limitations[n] == "" {
			t.Errorf("%s is %s without a stated limitation", n, level)
		}
		if d.Native[n] == "" {
			t.Errorf("%s has no native spelling", n)
		}
	}
	r := d.Report()
	if r.Tier != TierWorkflow || r.Capabilities["status-ui"] != "absent" || r.Capabilities["question-dialog"] != Limited {
		t.Fatalf("report: %+v", r)
	}
	lines := r.Findings()
	if !strings.HasPrefix(lines[0], "ok: harness claude 2.1: workflow tier; enforced: ") || !strings.Contains(lines[0], "limited: shell-rules, question-dialog, permission-events") ||
		!strings.Contains(lines[0], "absent: status-ui") || len(lines) != 4 {
		t.Fatalf("findings: %q", lines)
	}
	if _, ok := ForHarness("codex"); ok {
		t.Fatal("codex has no declaration")
	}
}
