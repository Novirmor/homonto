package config

import (
	"strings"
	"testing"
)

const ontoEngine = "[engines.onto]\nsource = \"builtin:onto\"\nscope = \"project\"\n"

func loadRouting(t *testing.T, body string) (*Config, error) {
	t.Helper()
	return Load(writeTree(t, t.TempDir(), map[string]string{"homonto.toml": body}))
}

func mustRoutes(t *testing.T, body string) map[string]AgentRoute {
	t.Helper()
	c, err := loadRouting(t, body)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := c.AgentRoutes("opencode")
	if err != nil {
		t.Fatal(err)
	}
	return routes
}

// TestRoutingDefaultReplacesPerAgentBlocks: one default policy is a model
// source for every agent, so no [subagents.*] block is needed.
func TestRoutingDefaultReplacesPerAgentBlocks(t *testing.T) {
	routes := mustRoutes(t, ontoEngine+"[routing.default.opencode]\nmodel = \"p/base\"\n")
	for _, agent := range []string{"homonto", "onto-explorer", "onto-implementer", "onto-reviewer", "onto-skeptic"} {
		r := routes[agent]
		if r.From != RouteFromPolicy || r.Policy.Model != "p/base" || r.Selector != "routing.default.opencode" {
			t.Fatalf("%s: %+v", agent, r)
		}
	}
}

func TestRoutingWithoutAnyModelSourceStillFails(t *testing.T) {
	_, err := loadRouting(t, ontoEngine)
	if err == nil || !strings.Contains(err.Error(), "model is required (or cover the agent with a [routing] policy") {
		t.Fatalf("err = %v", err)
	}
}

// TestRoutingPrecedence: pin > stage > engine > default, per ADR 0098.
func TestRoutingPrecedence(t *testing.T) {
	routes := mustRoutes(t, ontoEngine+`
[routing.default.opencode]
model = "p/default"
[routing.engines.onto.opencode]
model = "p/engine"
[routing.engines.onto.stages.verify.opencode]
model = "p/verify"
[subagents.onto-reviewer.opencode]
model = "p/pinned"
`)
	for agent, want := range map[string][2]string{
		"homonto":          {"p/default", "routing.default.opencode"},
		"onto-reviewer":    {"p/pinned", "subagents.onto-reviewer.opencode"},
		"onto-skeptic":     {"p/verify", "routing.engines.onto.stages.verify.opencode"},
		"onto-explorer":    {"p/engine", "routing.engines.onto.opencode"},
		"onto-implementer": {"p/engine", "routing.engines.onto.opencode"},
	} {
		r := routes[agent]
		model := r.Pin.Model
		if r.Policy != nil {
			model = r.Policy.Model
		}
		if model != want[0] || r.Selector != want[1] {
			t.Errorf("%s: model %q via %q, want %q via %q", agent, model, r.Selector, want[0], want[1])
		}
	}
	// onto-explorer serves open, design, build and verify; only verify has a
	// stage policy, so the stages disagree and the engine policy applies.
	if note := routes["onto-explorer"].Note; !strings.Contains(note, "onto/verify → routing.engines.onto.stages.verify.opencode") || !strings.Contains(note, "onto/open → routing.engines.onto.opencode") {
		t.Fatalf("explorer note = %q", note)
	}
	if routes["onto-skeptic"].Note != "" {
		t.Fatalf("skeptic note = %q", routes["onto-skeptic"].Note)
	}
}

// TestRoutingAgreeingStagesApply: an agent bound to several stages takes a
// stage policy when every one of its stages carries the same policy.
func TestRoutingAgreeingStagesApply(t *testing.T) {
	routes := mustRoutes(t, ontoEngine+`
[routing.default.opencode]
model = "p/default"
[routing.engines.onto.stages.build.opencode]
model = "p/heavy"
[routing.engines.onto.stages.verify.opencode]
model = "p/heavy"
`)
	r := routes["onto-implementer"]
	if r.Policy.Model != "p/heavy" || r.Selector != "routing.engines.onto.stages.build.opencode, routing.engines.onto.stages.verify.opencode" || r.Note != "" {
		t.Fatalf("implementer: %+v", r)
	}
	if r := routes["onto-explorer"]; r.Policy.Model != "p/default" || !strings.Contains(r.Note, "disagree") {
		t.Fatalf("explorer: %+v", r)
	}
}

// TestRoutingWorkflowsThatShareAnAgentMustAgree: h-spike ships in every
// hosting workflow; differing workflow policies fall back to the default.
func TestRoutingWorkflowsThatShareAnAgentMustAgree(t *testing.T) {
	base := `
[workflows.h-workflow-gh]
source = "builtin:h-workflow-gh"
scope = "project"
[workflows.h-workflow-gl]
source = "builtin:h-workflow-gl"
scope = "project"
[routing.default.opencode]
model = "p/default"
[routing.workflows.h-workflow-gh.opencode]
model = "p/gh"
`
	r := mustRoutes(t, base)["h-spike"]
	if r.Policy.Model != "p/default" || !strings.Contains(r.Note, "h-workflow-gl → routing.default.opencode") {
		t.Fatalf("split: %+v", r)
	}
	r = mustRoutes(t, base+"[routing.workflows.h-workflow-gl.opencode]\nmodel = \"p/gh\"\n")["h-spike"]
	if r.Policy.Model != "p/gh" || r.Note != "" {
		t.Fatalf("agreeing: %+v", r)
	}
}

func TestRoutingRejectsInvalidPolicies(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"[routing.engines.ghost.opencode]\nmodel = \"p/m\"\n", "routing.engines.ghost names no declared engine"},
		{"[routing.workflows.ghost.opencode]\nmodel = \"p/m\"\n", "routing.workflows.ghost names no declared workflow"},
		{ontoEngine + "[routing.default.opencode]\nmodel = \"p/m\"\n[routing.engines.onto.stages.ship.opencode]\nmodel = \"p/m\"\n", "routing.engines.onto.stages.ship is not a stage of onto (open, design, build, verify, close)"},
		{"[routing.default.opencode]\nvariant = \"high\"\n", "static policy and needs a model"},
		{"[routing.default.opencode]\nmodel = \"nomodel\"\n", `must be a provider/model id`},
		{"[routing.default.opencode]\nmodel = \"p/m\"\nfallback = \"p/x\"\n", "belong to mode = \"auto\""},
		{"[routing.default.opencode]\nmode = \"auto\"\n", "needs a fallback model"},
		{"[routing.default.opencode]\nmode = \"auto\"\nmodel = \"p/m\"\nfallback = \"p/x\"\n", "model and variant are not allowed"},
		{"[routing.default.opencode]\nmode = \"auto\"\nfallback = \"p/x\"\nprefer = \"vibes\"\n", `prefer "vibes" is not one of`},
		{"[routing.default.opencode]\nmode = \"auto\"\nfallback = \"p/x\"\nproviders = [\"a/b\"]\n", "providers must be provider ids"},
		{"[routing.default.opencode]\nmode = \"auto\"\nfallback = \"p/x\"\nneeds = [\"Big Brain\"]\n", "must be a lowercase tag"},
		{"[routing.default.opencode]\nmode = \"auto\"\nfallback = \"p/x\"\nmax_cost = -1.0\n", "must be non-negative"},
		{"[routing.default.opencode]\nmode = \"auto\"\nfallback = \"p/x\"\ncandidates = [\"p/a\", \"p/a\"]\n", "candidates must be distinct"},
		{"[routing.default.opencode]\nmode = \"turbo\"\nmodel = \"p/m\"\n", `mode "turbo" is not static or auto`},
		{"[routing.models.opencode.\"p/m\"]\ncost = -2.0\n", "must be non-negative"},
		{"[routing.default.claude]\nmodel = \"p/m\"\n", "must be a Claude Code alias"},
		{"[routing.default.claude]\nmode = \"auto\"\nfallback = \"sonnet\"\nproviders = [\"anthropic\"]\n", "providers do not apply to Claude Code"},
	} {
		if _, err := loadRouting(t, tc.body); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.body, err, tc.want)
		}
	}
}
