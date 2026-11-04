// Package capability is the single registry of harness capabilities (ADR 0082
// vocabulary). Package manifests name the capabilities they require, adapters
// declare the ones their harness provides, and plan compares the two. Keeping
// one vocabulary here means manifest validation, plan diagnostics and doctor
// cannot drift apart.
//
// A harness capability is not a package capability: framework.toml's
// [dependencies].capabilities / [provides].capabilities are semantic
// interfaces one package offers another ("name@major"), resolved between
// packages. These are features of the harness the package runs on.
package capability

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Name is one harness capability from the ADR 0082 vocabulary.
type Name string

// The vocabulary. Adding a capability is a contract change: document it in
// docs/contracts/capabilities.md and declare it in every adapter.
const (
	Skills           Name = "skills"
	Commands         Name = "commands"
	Subagents        Name = "subagents"
	AgentToolLimits  Name = "agent-tool-limits"
	ShellRules       Name = "shell-rules"
	QuestionDialog   Name = "question-dialog"
	ContextHooks     Name = "context-hooks"
	PermissionEvents Name = "permission-events"
	MCP              Name = "mcp"
	StatusUI         Name = "status-ui"
)

var vocabulary = []Name{
	Skills, Commands, Subagents, AgentToolLimits, ShellRules,
	QuestionDialog, ContextHooks, PermissionEvents, MCP, StatusUI,
}

// Vocabulary returns every known capability in declaration order.
func Vocabulary() []Name { return slices.Clone(vocabulary) }

// Known reports whether name is in the vocabulary.
func Known(name string) bool { return slices.Contains(vocabulary, Name(name)) }

// Level is how a harness provides a capability.
type Level string

const (
	// Enforced: the harness itself guarantees the behavior (a denied tool is
	// unreachable, a hook always fires).
	Enforced Level = "enforced"
	// InstructionOnly: the capability exists only as instructions the model is
	// asked to follow. It never satisfies a requirement; plan names it so the
	// gap is not mistaken for a missing adapter feature.
	InstructionOnly Level = "instruction-only"
	// Limited: the harness provides the capability with a gap the
	// limited-mode policy accepted (ADR 0105). It satisfies a requirement,
	// and doctor and the generated harness map name the limitation.
	Limited Level = "limited"
)

// Satisfies reports whether a level meets a requirement.
func (l Level) Satisfies() bool { return l == Enforced || l == Limited }

// Declaration is what one adapter's harness provides at a pinned version.
type Declaration struct {
	Harness  string
	Version  string
	Provides map[Name]Level
	// Native spells each provided capability in the harness's own terms,
	// for the generated harness map the doctrine links to.
	Native map[Name]string
	// Limitations explains every Limited or InstructionOnly capability.
	Limitations map[Name]string
}

// Gap is one required capability a declaration does not satisfy.
type Gap struct {
	Name  Name
	Level Level // empty when the capability is absent altogether
}

func (g Gap) String() string {
	if g.Level == "" {
		return string(g.Name)
	}
	return fmt.Sprintf("%s (%s only)", g.Name, g.Level)
}

// Gaps returns the required capabilities d does not provide (enforced or with
// an accepted limitation),
// sorted by name. Unknown names are reported as absent; ValidateNames is the
// place that rejects them by name.
func (d Declaration) Gaps(required []string) []Gap {
	var out []Gap
	seen := map[Name]bool{}
	for _, r := range required {
		n := Name(r)
		if seen[n] {
			continue
		}
		seen[n] = true
		if level := d.Provides[n]; !level.Satisfies() {
			out = append(out, Gap{Name: n, Level: level})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Check fails closed when d cannot run a package requiring required, naming
// every gap and the harness version the declaration was pinned to.
func (d Declaration) Check(pkg string, required []string) error {
	gaps := d.Gaps(required)
	if len(gaps) == 0 {
		return nil
	}
	names := make([]string, len(gaps))
	for i, g := range gaps {
		names[i] = g.String()
	}
	return fmt.Errorf("package %q requires harness capabilities %s that %s %s does not provide; remove %s from the package's targets or use a harness that provides them",
		pkg, strings.Join(names, ", "), d.Harness, d.Version, d.Harness)
}

// ValidateNames rejects a requirement list with an unknown or duplicate name.
// label prefixes the error ("framework \"x\" [requires].harness").
func ValidateNames(label string, names []string) error {
	seen := map[string]bool{}
	for _, n := range names {
		if !Known(n) {
			known := make([]string, len(vocabulary))
			for i, v := range vocabulary {
				known[i] = string(v)
			}
			return fmt.Errorf("%s names unknown harness capability %q (known: %s)", label, n, strings.Join(known, ", "))
		}
		if seen[n] {
			return fmt.Errorf("%s lists harness capability %q twice", label, n)
		}
		seen[n] = true
	}
	return nil
}

// Declarer is implemented by an adapter that declares its harness's
// capabilities. An adapter without it provides nothing, so any requirement
// fails against it.
type Declarer interface {
	Capabilities() Declaration
}

// OpenCode is the OpenCode 2 declaration. Every capability is enforced by the
// harness: per-agent permission maps, shell allow/ask/deny patterns, the
// native question tool, plugin hooks and events, MCP, and the TUI status slot
// (docs/harness-architecture.md, "Known harness capabilities").
func OpenCode() Declaration {
	provides := make(map[Name]Level, len(vocabulary))
	for _, n := range vocabulary {
		provides[n] = Enforced
	}
	return Declaration{Harness: "opencode", Version: "2", Provides: provides, Native: map[Name]string{
		Skills:           "skills under `.opencode/skills/`, loaded by the `skill` tool",
		Commands:         "slash commands under `.opencode/command/`",
		Subagents:        "the `task` tool with the agent's name",
		AgentToolLimits:  "each agent's `permission` map",
		ShellRules:       "`permission.bash` allow/ask/deny patterns",
		QuestionDialog:   "the built-in `question` tool",
		ContextHooks:     "the workflow plugin's context and compaction hooks",
		PermissionEvents: "the workflow plugin's `permission.asked`/`permission.replied` events",
		MCP:              "configured MCP servers; homonto's runtime tools come from the workflow plugin",
		StatusUI:         "the workflow plugin's TUI status panel",
	}}
}

// ClaudeCode is the Claude Code declaration, verified against 2.1.318
// (docs/harness-architecture.md). Limitations follow ADR 0105.
func ClaudeCode() Declaration {
	return Declaration{Harness: "claude", Version: "2.1", Provides: map[Name]Level{
		Skills:           Enforced,
		Commands:         Enforced,
		Subagents:        Enforced,
		AgentToolLimits:  Enforced,
		ShellRules:       Limited,
		QuestionDialog:   Limited,
		ContextHooks:     Enforced,
		PermissionEvents: Limited,
		MCP:              Enforced,
	}, Native: map[Name]string{
		Skills:           "skills under `.claude/skills/`, loaded by the `Skill` tool",
		Commands:         "slash commands under `.claude/commands/`",
		Subagents:        "the `Agent` tool with `subagent_type` set to the agent's name",
		AgentToolLimits:  "each agent's `tools`/`disallowedTools` frontmatter and homonto's `PreToolUse` hook",
		ShellRules:       "`permissions` `Bash(...)` allow/ask/deny rules in `settings.json`",
		QuestionDialog:   "the `AskUserQuestion` tool",
		ContextHooks:     "homonto's `SessionStart` hook (startup, resume, clear and compaction)",
		PermissionEvents: "homonto's `PermissionRequest`, `PermissionDenied` and `SubagentStop` hooks",
		MCP:              "`.mcp.json` servers; homonto's runtime tools are `mcp__homonto__<tool>`",
	}, Limitations: map[Name]string{
		ShellRules:       "Bash rules match command prefixes; a command reached through a wrapper the rules do not name is not caught. Coordinator-only tool access is also guarded by homonto's hook.",
		QuestionDialog:   "AskUserQuestion is unavailable in non-interactive (`claude -p`) sessions and takes at most four questions, so a publication draft holds at most four items. A question that arrives pre-answered is denied.",
		PermissionEvents: "Claude Code reports a worker's permission prompt but not the user's answer. A prompt whose tool never ran before the worker stopped is handed back as refused.",
	}}
}

// Tier is the highest workflow support level a declaration reaches (ADR 0082).
type Tier string

const (
	TierWorkflow   Tier = "workflow"
	TierLite       Tier = "lite"
	TierProjection Tier = "projection"
)

var tierRequirements = []struct {
	tier  Tier
	needs []Name
}{
	{TierWorkflow, []Name{Skills, Commands, Subagents, AgentToolLimits, ShellRules, QuestionDialog, ContextHooks, MCP}},
	{TierLite, []Name{Skills, Subagents, QuestionDialog, MCP}},
}

// Tier derives the declaration's tier from what it provides.
func (d Declaration) Tier() Tier {
	for _, t := range tierRequirements {
		ok := true
		for _, n := range t.needs {
			ok = ok && d.Provides[n].Satisfies()
		}
		if ok {
			return t.tier
		}
	}
	return TierProjection
}

// Declarations returns every built-in harness declaration, OpenCode first.
func Declarations() []Declaration { return []Declaration{OpenCode(), ClaudeCode()} }

// ForHarness returns the built-in declaration for a harness id.
func ForHarness(harness string) (Declaration, bool) {
	for _, d := range Declarations() {
		if d.Harness == harness {
			return d, true
		}
	}
	return Declaration{}, false
}

// Report is one harness's capability summary for doctor.
type Report struct {
	Harness      string            `json:"harness"`
	Version      string            `json:"version"`
	Tier         Tier              `json:"tier"`
	Capabilities map[string]Level  `json:"capabilities"`
	Limitations  map[string]string `json:"limitations,omitempty"`
}

// Report summarizes the declaration; a capability it does not provide is
// reported as "absent".
func (d Declaration) Report() Report {
	r := Report{Harness: d.Harness, Version: d.Version, Tier: d.Tier(), Capabilities: map[string]Level{}}
	for _, n := range vocabulary {
		level, ok := d.Provides[n]
		if !ok {
			level = "absent"
		}
		r.Capabilities[string(n)] = level
		if note := d.Limitations[n]; note != "" {
			if r.Limitations == nil {
				r.Limitations = map[string]string{}
			}
			r.Limitations[string(n)] = note
		}
	}
	return r
}

// Findings renders the report as doctor lines: the tier, then each
// capability grouped by level, then every accepted limitation.
func (r Report) Findings() []string {
	groups := map[Level][]string{}
	for _, n := range vocabulary {
		level := r.Capabilities[string(n)]
		groups[level] = append(groups[level], string(n))
	}
	line := fmt.Sprintf("ok: harness %s %s: %s tier", r.Harness, r.Version, r.Tier)
	for _, level := range []Level{Enforced, Limited, InstructionOnly, "absent"} {
		if len(groups[level]) > 0 {
			line += fmt.Sprintf("; %s: %s", level, strings.Join(groups[level], ", "))
		}
	}
	out := []string{line}
	for _, n := range vocabulary {
		if note := r.Limitations[string(n)]; note != "" {
			out = append(out, fmt.Sprintf("info: harness %s: %s is %s (accepted, ADR 0105): %s", r.Harness, n, r.Capabilities[string(n)], note))
		}
	}
	return out
}
