package config

import (
	"strings"
	"testing"
)

// diagnosticsBase installs the onto engine with every agent covered by a
// routing policy, so a case can add one broken key to a config that
// otherwise loads.
const diagnosticsBase = `
[engines.onto]
source = "builtin:onto"
scope = "project"

[routing.default.opencode]
model = "anthropic/claude-sonnet-5-5"
`

// TestDiagnosticsNameKeyValueAndFix is the diagnostics audit (M5.2): every
// public rejection class names the offending key, the offending value when
// there is one, and what to do instead. The class list is the checklist in
// docs/guides/configuration.md#diagnostics; a new rejection class gets a
// case here.
func TestDiagnosticsNameKeyValueAndFix(t *testing.T) {
	for _, tc := range []struct {
		class, doc string
		// key, value and fix must each appear in the error. value is empty
		// for a class with no offending value (a missing key).
		key, value, fix string
	}{
		// Decoding.
		{"unknown key", "[workflow]\nroots = \"docs\"\n", "roots", "", "docs/guides/configuration.md"},
		{"split frameworks table", "[frameworks.onto]\nsource = \"builtin:onto\"\n", `frameworks entry "onto"`, "", "[engines.onto] or [workflows.onto]"},
		{"unknown table", "[engine.onto]\nsource = \"builtin:onto\"\n", "engine.onto", "", "docs/guides/configuration.md"},
		{"newer schema", "schema_version = 9\n", "schema version", "9", "upgrade homonto"},
		{"negative schema", "schema_version = -1\n", "schema_version", "", "non-negative"},

		// Removed surfaces.
		{"mcps", "[mcps.github]\ncommand = \"x\"\n", "mcps", "", "opencode.jsonc"},
		{"model tiers", "[models.opencode.default]\nmodel = \"a/b\"\n", "models.opencode.default", "", "[subagents.<name>.opencode]"},
		{"claude plugins", "[plugins.claude.x]\nsource = \"x\"\n", "plugins.claude.x", "", "enabledPlugins"},
		{"claude marketplaces", "[marketplaces.claude.x]\nsource = \"github\"\n", "marketplaces.claude.x", "", "extraKnownMarketplaces"},
		{"workflow bridge", "[integrations.opencode]\nworkflow_bridge = true\n", "integrations.opencode.workflow_bridge", "", "workflow_context"},
		{"own skill", "[skills.mine]\nsource = \"local:mine\"\nscope = \"project\"\n", "skills.mine", "local:mine", ".opencode/skills"},

		// Resources.
		{"missing scope", "[skills.handoff]\nsource = \"builtin:handoff\"\n", "skills.handoff", "", `"user" and "project"`},
		{"bad scope", "[skills.handoff]\nsource = \"builtin:handoff\"\nscope = \"global\"\n", "skills.handoff", "global", `"user" and "project"`},
		{"bad source", "[skills.handoff]\nsource = \"handoff\"\nscope = \"project\"\n", "skills.handoff", "handoff", "builtin:<name>"},
		{"remote without digest", "[subagents.x]\nsource = \"remote:https://example.com/x.tar.gz\"\n", "subagents.x", "remote:https://example.com/x.tar.gz", "digest"},
		{"digest without remote", "[subagents.x]\nsource = \"local:x\"\ndigest = \"sha256:" + strings.Repeat("0", 64) + "\"\n", "subagents.x", "", "remote:"},
		{"unknown target", "[skills.handoff]\nsource = \"builtin:handoff\"\nscope = \"project\"\ntargets = [\"vim\"]\n", "skills.handoff", "vim", "valid targets"},
		{"unknown harness", "harnesses = [\"vim\"]\n", "harnesses", "vim", "valid harnesses"},
		{"duplicate harness", "harnesses = [\"opencode\", \"opencode\"]\n", "harnesses", "opencode", "list each harness once"},
		{"wrong kind", "[engines.h-workflow-gh]\nsource = \"builtin:h-workflow-gh\"\nscope = \"project\"\n", "engines.h-workflow-gh", "builtin:h-workflow-gh", "is a workflow, not an engine"},
		{"bad subagent mode", "[subagents.x]\nsource = \"local:x\"\nmode = \"hard\"\n", "subagents.x", "hard", "valid values"},

		// Agent models.
		{"model required", "[engines.onto]\nsource = \"builtin:onto\"\nscope = \"project\"\n", "subagents.homonto.opencode", "", "[routing] policy"},
		{"model variant suffix", diagnosticsBase + "[subagents.onto-skeptic.opencode]\nmodel = \"a/b#high\"\n", "subagents.onto-skeptic.opencode", "a/b#high", "declare variant separately"},
		{"opencode effort", diagnosticsBase + "[subagents.onto-skeptic.opencode]\nmodel = \"a/b\"\neffort = \"high\"\n", "subagents.onto-skeptic.opencode", "high", "variant"},
		{"steps", diagnosticsBase + "[subagents.onto-skeptic.opencode]\nmodel = \"a/b\"\nsteps = 0\n", "subagents.onto-skeptic.opencode", "0", "positive"},
		{"bad variant", diagnosticsBase + "[subagents.onto-skeptic.opencode]\nmodel = \"a/b\"\nvariant = \"a b\"\n", "subagents.onto-skeptic.opencode", "a b", "plain variant name"},
		{"bash_allow_add pattern", diagnosticsBase + "[subagents.onto-skeptic.opencode]\nmodel = \"a/b\"\nbash_allow_add = [\"go *\"]\n", "subagents.onto-skeptic.opencode", "go *", "exact command"},
		{"tune uninstalled", "[subagents.onto-skeptic.opencode]\nmodel = \"a/b\"\n", "subagents.onto-skeptic", "", "no framework or [subagents.*] declaration"},
		{"tune with scope", diagnosticsBase + "[subagents.onto-skeptic]\nscope = \"user\"\n[subagents.onto-skeptic.opencode]\nmodel = \"a/b\"\n", "subagents.onto-skeptic", "", "remove them or declare a source"},
		{"claude variant", "harnesses = [\"claude\"]\n[routing.default.claude]\nmodel = \"sonnet\"\nvariant = \"high\"\n", "routing.default.claude", "", "only a model"},

		// Native settings.
		{"reserved opencode key", "[settings.opencode]\nmcp = {}\n", "settings.opencode", "mcp", ""},
		{"plugin config", "[plugins.opencode.x]\nsource = \"x\"\nconfig = { a = 1 }\n", "plugins.opencode.x", "", "remove config"},
		{"plugin empty source", "[plugins.opencode.x]\nsource = \"\"\n", "plugins.opencode.x", "", "source"},

		// Workspace layout.
		{"tmp unknown key", "[tmp]\npath = \".tmp\"\n", "tmp.path", "", "takes only dir"},
		{"tmp absolute", "[tmp]\ndir = \"/tmp/x\"\n", "[tmp] dir", "/tmp/x", "relative path below the workspace root"},
		{"workflow git", "schema_version = 2\n[workflow]\ngit = \"auto\"\n", "workflow.git", "auto", "'existing' or 'managed'"},
		{"workflow git on legacy", "[workflow]\ngit = \"managed\"\n", "workflow.git", "", "schema_version = 2"},
		{"workflow root escape", "[workflow]\nroot = \"../x\"\n", "workflow.root", "../x", "below the configuration repository"},
		{"repos missing", "[repos]\napi = \"./nope\"\n", "repos.api", "nope", "relative to the config file"},
		{"integration on legacy", "[integration.c]\nrepos = [\"a\"]\nrepo = \"a\"\ncwd = \".\"\ncommand = [\"x\"]\n", "integration.c", "", "schema_version = 2"},
		{"tooling value", "[tooling]\ncode_intel = \"ctags\"\n", "tooling.code_intel", "ctags", "accepted: graphify, okf, none"},

		// Routing.
		{"static without model", "[routing.default.opencode]\nmode = \"static\"\n", "routing.default.opencode", "", "needs a model"},
		{"auto without fallback", "[routing.default.opencode]\nmode = \"auto\"\n", "routing.default.opencode", "", "needs a fallback"},
		{"auto with model", "[routing.default.opencode]\nmode = \"auto\"\nmodel = \"a/b\"\nfallback = \"a/b\"\n", "routing.default.opencode", "", "name a fallback instead"},
		{"route mode", "[routing.default.opencode]\nmode = \"smart\"\nmodel = \"a/b\"\n", "routing.default.opencode", "smart", "static or auto"},
		{"route prefer", "[routing.default.opencode]\nmode = \"auto\"\nprefer = \"speed\"\nfallback = \"a/b\"\n", "routing.default.opencode", "speed", "listed, cost, latency"},
		{"route model id", "[routing.default.opencode]\nmodel = \"sonnet\"\n", "routing.default.opencode", "sonnet", "provider/model"},
		{"route unknown engine", "[routing.engines.ghost.opencode]\nmodel = \"a/b\"\n", "routing.engines.ghost", "", "declare [engines.ghost]"},
		{"claude providers", "[routing.default.claude]\nmode = \"auto\"\nproviders = [\"anthropic\"]\nfallback = \"sonnet\"\n", "routing.default.claude", "", "do not apply to Claude Code"},

		// Decisions.
		{"decision protocol", "[decisions.providers.p]\nprotocol = \"grpc\"\nmodel = \"m\"\napi_key_env = \"K\"\n", "decisions.providers.p", "grpc", "is not one of"},
		{"decision endpoint", "[decisions.providers.p]\nprotocol = \"systemone\"\nmodel = \"m\"\napi_key_env = \"K\"\n", "decisions.providers.p", "", "endpoint is required"},
		{"decision provider name", "[decisions.providers.P]\nprotocol = \"openai-decisions\"\nmodel = \"m\"\napi_key_env = \"K\"\n", "decisions.providers.P", "", "lowercase letters"},

		// Package settings.
		{"unknown setting", "[engines.no]\nsource = \"builtin:no\"\nscope = \"project\"\n[engines.no.settings]\nisolate = \"x\"\n[routing.default.opencode]\nmodel = \"a/b\"\n", "engines.no.settings.isolate", "", "known: isolation"},
		{"setting value", "[engines.no]\nsource = \"builtin:no\"\nscope = \"project\"\n[engines.no.settings]\nisolation = \"always\"\n[routing.default.opencode]\nmodel = \"a/b\"\n", "engines.no.settings.isolation", "always", "ask"},
		{"settings on a skill", "[skills.handoff]\nsource = \"builtin:handoff\"\nscope = \"project\"\n[skills.handoff.settings]\na = 1\n", "skills.handoff", "", "only [workflows.*], [engines.*] and [services.*]"},
	} {
		t.Run(tc.class, func(t *testing.T) {
			err := loadDoc(t, tc.doc)
			if err == nil {
				t.Fatalf("config loaded; want a rejection")
			}
			msg := err.Error()
			for _, part := range []struct{ what, want string }{{"key", tc.key}, {"value", tc.value}, {"fix", tc.fix}} {
				if part.want != "" && !strings.Contains(msg, part.want) {
					t.Errorf("error does not name the %s %q:\n%s", part.what, part.want, msg)
				}
			}
		})
	}
}

// A Claude Code model block alone tunes an installed agent, as the
// configuration reference documents. It was read as a sourceless
// declaration and rejected.
func TestClaudeOnlyTuneLoads(t *testing.T) {
	c, err := loadDocCfg(t, `harnesses = ["opencode", "claude"]
[engines.onto]
source = "builtin:onto"
scope = "project"
[routing.default.opencode]
model = "anthropic/claude-sonnet-5-5"
[routing.default.claude]
model = "sonnet"
[subagents.onto-skeptic.claude]
model = "opus"
`)
	if err != nil {
		t.Fatalf("a Claude-only tune block is rejected: %v", err)
	}
	sa := c.Subagents["onto-skeptic"]
	if !sa.IsTuneOnly() || sa.Claude.Model != "opus" {
		t.Fatalf("onto-skeptic = %+v", sa)
	}
	routes, err := c.AgentRoutes("claude")
	if err != nil {
		t.Fatal(err)
	}
	if r := routes["onto-skeptic"]; r.From != RouteFromPin || r.Pin.Model != "opus" {
		t.Errorf("claude route for onto-skeptic = %+v", r)
	}
}
