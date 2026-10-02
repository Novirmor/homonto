// Package resourcepath is the single source of truth for where each tool's
// owned skills, commands, and subagents are linked, as a function of the
// resource kind, tool, and install scope. The adapters and the engine's doctor
// all call Dir so the path convention lives in exactly one place — important
// because the kind leaf names are not uniform (OpenCode uses plural "skills"
// but singular "command" and "agent"), the two scopes do not share a common
// base directory, and the scope-flip rule for inactive-scope pruning must be
// consistent across all three resource kinds.
//
// This package unifies the former skillpath / commandpath / subagentpath
// trio, whose three near-identical switch bodies had drifted in subtle ways.
package resourcepath

import (
	"fmt"
	"path/filepath"
)

// Kind identifies a managed resource kind that gets linked into a tool.
type Kind string

const (
	Skill    Kind = "skill"
	Command  Kind = "command"
	Subagent Kind = "subagent"
)

// Dir returns the directory a tool's owned resources of kind are linked into.
//
//	claude   + user     + skill    -> <home>/.claude/skills
//	claude   + project  + skill    -> <projectRoot>/.claude/skills
//	opencode + user     + skill    -> <home>/.config/opencode/skills
//	opencode + project  + skill    -> <projectRoot>/.opencode/skills
//	claude   + user     + command  -> <home>/.claude/commands
//	claude   + project  + command  -> <projectRoot>/.claude/commands
//	opencode + user     + command  -> <home>/.config/opencode/command
//	opencode + project  + command  -> <projectRoot>/.opencode/command
//	claude   + user     + subagent -> <home>/.claude/agents
//	claude   + project  + subagent -> <projectRoot>/.claude/agents
//	opencode + user     + subagent -> <home>/.config/opencode/agent
//	opencode + project  + subagent -> <projectRoot>/.opencode/agent
//
// Any scope other than "project" is treated as "user" (config.Load rejects
// empty/invalid scope; this fallback only guards against an unnormalized value
// reaching here). An unknown tool or kind returns "".
func Dir(kind Kind, tool, scope, home, projectRoot string) string {
	leaf, ok := leafName(kind, tool)
	if !ok {
		return ""
	}
	project := scope == "project"
	switch tool {
	case "claude":
		// Both scopes share the .claude base directory; only the prefix
		// differs (user lives under $HOME, project under the repo).
		if project {
			return filepath.Join(projectRoot, ".claude", leaf)
		}
		return filepath.Join(home, ".claude", leaf)
	case "opencode":
		if project {
			// OpenCode reads project skills/commands/subagents from
			// <repo>/.opencode/<leaf>; this differs from its global
			// ~/.config/opencode/<leaf>, so it is not a base-directory swap.
			// https://opencode.ai/docs/skills/
			return filepath.Join(projectRoot, ".opencode", leaf)
		}
		return filepath.Join(home, ".config", "opencode", leaf)
	}
	return ""
}

// leafName is the per-kind, per-tool leaf directory name. skills is plural
// "skills" in both tools; commands is plural in Claude and singular in
// OpenCode; subagents is "agents" in Claude and "agent" in OpenCode.
func leafName(kind Kind, tool string) (string, bool) {
	switch kind {
	case Skill:
		return "skills", true
	case Command:
		switch tool {
		case "claude":
			return "commands", true
		case "opencode":
			return "command", true
		}
	case Subagent:
		switch tool {
		case "claude":
			return "agents", true
		case "opencode":
			return "agent", true
		}
	}
	return "", false
}

// OtherScope returns the opposite scope, used to locate a resource's
// inactive-scope link so a scope switch can prune it. "project" maps to
// "user"; every other value (including "user" and "") maps to "project".
func OtherScope(scope string) string {
	if scope == "project" {
		return "user"
	}
	return "project"
}

// String returns a debug-friendly description of kind. Useful in error
// messages where a bare string would lose context.
func (k Kind) String() string { return fmt.Sprintf("resourcepath.Kind(%q)", string(k)) }
