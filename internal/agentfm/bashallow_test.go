package agentfm

import (
	"strings"
	"testing"
)

// TestRenderOpenCode_BashAllowAdd: additions merge after the base list,
// deduplicated, before "*": ask ordering stays intact; a bash: deny base with
// additions is refused; fingerprint-relevant fields flow through ModelSpec.
func TestRenderOpenCode_BashAllowAdd(t *testing.T) {
	allowlisted := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", `  spawn: [onto-implementer, onto-reviewer]
  bash_allow: ["onto *", "git status*"]
`, 1)
	ctx := ctx()
	ctx.Overrides["onto"] = ModelSpec{Model: "openai/gpt-5", BashAllowAdd: []string{"go test ./...", "git status*", "git status*"}}

	s, err := Render("onto", []byte(allowlisted), "opencode", ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := string(s)
	for _, want := range []string{`    "*": ask`, `    "onto *": allow`, `    "git status*": allow`, `    "go test ./...": allow`} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	// Deduplication: "git status*" appears exactly once as an allow.
	if strings.Count(out, `"git status*": allow`) != 1 {
		t.Errorf("addition not deduplicated against the base:\n%s", out)
	}
	// The deny-ask ordering: "*": ask must precede every allow.
	askIdx := strings.Index(out, `"*": ask`)
	for _, a := range []string{`"onto *"`, `"go test ./..."`} {
		if allowIdx := strings.Index(out, a); allowIdx < askIdx {
			t.Errorf("%s allowed before \"*\": ask:\n%s", a, out)
		}
	}
}

// TestRenderOpenCode_BashDenyRefusesAdditions (A7): an agent whose base
// denies bash cannot gain exact allows through bash_allow_add.
func TestRenderOpenCode_BashDenyRefusesAdditions(t *testing.T) {
	denied := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", "  bash: false\n", 1)
	ctx := ctx()
	ctx.Overrides["onto"] = ModelSpec{Model: "openai/gpt-5", BashAllowAdd: []string{"git status"}}
	if _, err := Render("onto", []byte(denied), "opencode", ctx); err == nil {
		t.Fatal("bash: deny + bash_allow_add must be refused")
	}
}

// TestRenderOpenCode_NoAdditionsKeepsBase: without additions the render is
// unchanged from the base-only allowlist.
func TestRenderOpenCode_NoAdditionsKeepsBase(t *testing.T) {
	allowlisted := strings.Replace(orchestrator, "  spawn: [onto-implementer, onto-reviewer]\n", "  bash_allow: [\"onto *\"]\n", 1)
	base, err := Render("onto", []byte(allowlisted), "opencode", ctx())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(base), "bash_allow_add") {
		t.Fatal("render leaked the config field")
	}
}
