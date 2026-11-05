package agentfm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

// ClaudePolicyKey is the frontmatter key a rendered Claude Code agent carries
// its homonto policy under. Claude Code ignores keys it does not know; the
// PreToolUse hook homonto installs (`homonto runtime policy`) reads it for
// the running agent and enforces it.
const ClaudePolicyKey = "homonto_policy"

// ClaudePolicy is the part of an agent's homonto: intent Claude Code cannot
// express in agent frontmatter: per-command shell rules, path denials and the
// spawn list. The hook evaluates it for every matching tool call.
type ClaudePolicy struct {
	Bash []PolicyRule `json:"bash,omitempty"`
	// EditDeny are directories the agent may not write.
	EditDeny []string `json:"editDeny,omitempty"`
	// PathDeny are directories the agent may neither read nor write.
	PathDeny []string `json:"pathDeny,omitempty"`
	// Spawn lists the agents it may dispatch; nil is unrestricted.
	Spawn *[]string `json:"spawn,omitempty"`
}

func (p ClaudePolicy) empty() bool {
	return len(p.Bash) == 0 && len(p.EditDeny) == 0 && len(p.PathDeny) == 0 && p.Spawn == nil
}

// EncodeClaudePolicy renders a policy as the frontmatter value.
func EncodeClaudePolicy(p ClaudePolicy) string {
	b, _ := json.Marshal(p)
	return base64.StdEncoding.EncodeToString(b)
}

// DecodeClaudePolicy parses a frontmatter value.
func DecodeClaudePolicy(v string) (ClaudePolicy, error) {
	var p ClaudePolicy
	b, err := base64.StdEncoding.DecodeString(strings.Trim(strings.TrimSpace(v), `"`))
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	return p, nil
}

// Claude Code tool names the intents map to.
var (
	claudeEditTools    = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}
	claudeNetworkTools = []string{"WebFetch", "WebSearch"}
)

// renderClaude returns the Claude Code frontmatter lines for an intent agent:
// its model, the tools it may never use, its turn budget, and the policy the
// hook enforces. A primary agent is the session's main agent (settings
// `agent`), so it renders like any other agent.
func renderClaude(name string, h Homonto, spec ModelSpec, ctx *RenderContext, pathDenies, editDenies []string) ([]string, error) {
	if spec.Variant != "" || spec.Effort != "" {
		return nil, fmt.Errorf("agentfm: agent %q: a Claude Code agent takes only a model, not a variant or effort", name)
	}
	if err := prepareBash(name, &h, &spec, ctx); err != nil {
		return nil, err
	}
	var lines []string
	if spec.Model != "" {
		lines = append(lines, fmt.Sprintf("model: %q", spec.Model))
	}
	var denied []string
	if h.ReadOnly {
		denied = append(denied, claudeEditTools...)
	}
	if h.Bash != nil && !*h.Bash {
		denied = append(denied, "Bash")
	}
	if h.Network != nil && !*h.Network {
		denied = append(denied, claudeNetworkTools...)
	}
	if !h.Dialogs {
		denied = append(denied, "AskUserQuestion")
	}
	if h.Spawn != nil && len(*h.Spawn) == 0 {
		denied = append(denied, "Agent")
	}
	if roles, err := BuiltinRoles(); err != nil {
		return nil, err
	} else if role, builtin := roles[name]; !builtin || !role.Coordinator {
		// Only the coordinator reaches homonto's runtime tools (ADR 0047).
		denied = append(denied, "mcp__homonto")
	}
	if len(denied) > 0 {
		lines = append(lines, "disallowedTools: "+strings.Join(denied, ", "))
	}
	if h.Steps > 0 {
		lines = append(lines, fmt.Sprintf("maxTurns: %d", h.Steps))
	}
	p := ClaudePolicy{PathDeny: cleanDirs(pathDenies)}
	if h.Bash == nil || *h.Bash {
		p.Bash = bashRules(h, spec.BashAllowAdd)
	}
	if !h.ReadOnly {
		p.EditDeny = cleanDirs(editDenies)
	}
	if h.Spawn != nil && len(*h.Spawn) > 0 {
		spawn := slices.Clone(*h.Spawn)
		p.Spawn = &spawn
	}
	if !p.empty() {
		lines = append(lines, fmt.Sprintf("%s: %q", ClaudePolicyKey, EncodeClaudePolicy(p)))
	}
	return lines, nil
}

func cleanDirs(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		out = append(out, filepath.Clean(d))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Decision is the outcome of a policy evaluation.
type Decision struct {
	Action string // "" (no opinion), allow, ask or deny
	Reason string
}

// Evaluate judges one tool call against the policy. cwd resolves relative
// paths. It never widens access: "" leaves the harness's own rules in charge.
func (p ClaudePolicy) Evaluate(tool string, input map[string]any, cwd string) Decision {
	str := func(k string) string { v, _ := input[k].(string); return v }
	switch tool {
	case "Bash":
		if len(p.Bash) == 0 {
			return Decision{}
		}
		command := str("command")
		action := ""
		for _, r := range p.Bash {
			if WildcardMatch(r.Pattern, command) {
				action = r.Action
			}
		}
		if action == "deny" {
			return Decision{"deny", "homonto policy denies this command for this agent"}
		}
		return Decision{Action: action}
	case "Agent", "Task":
		if p.Spawn == nil {
			return Decision{}
		}
		if t := str("subagent_type"); !slices.Contains(*p.Spawn, t) {
			return Decision{"deny", fmt.Sprintf("homonto policy: this agent may dispatch only %s, not %q", strings.Join(*p.Spawn, ", "), t)}
		}
		return Decision{}
	}
	path := str("file_path")
	if path == "" {
		path = str("notebook_path")
	}
	if path == "" {
		path = str("path")
	}
	if path == "" {
		return Decision{}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)
	for _, dir := range p.PathDeny {
		if within(dir, path) {
			return Decision{"deny", "homonto policy: " + dir + " is coordinator-owned workflow state"}
		}
	}
	if slices.Contains(claudeEditTools, tool) {
		for _, dir := range p.EditDeny {
			if within(dir, path) {
				return Decision{"deny", "homonto policy: this agent may not edit under " + dir}
			}
		}
	}
	return Decision{}
}

func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// WildcardMatch matches a shell rule pattern against a whole command: `*`
// matches any run of characters (including none and newlines), `?` any one
// character, everything else itself — the semantics OpenCode applies to the
// same rules.
func WildcardMatch(pattern, s string) bool {
	p, t := []rune(pattern), []rune(s)
	pi, ti, star, mark := 0, 0, -1, 0
	for ti < len(t) {
		switch {
		case pi < len(p) && (p[pi] == '?' || p[pi] == t[ti]):
			pi++
			ti++
		case pi < len(p) && p[pi] == '*':
			star, mark = pi, ti
			pi++
		case star >= 0:
			pi = star + 1
			mark++
			ti = mark
		default:
			return false
		}
	}
	for pi < len(p) && p[pi] == '*' {
		pi++
	}
	return pi == len(p)
}

// ClaudePolicyOf reads the policy from a rendered Claude Code agent file; ok
// is false when the agent carries none.
func ClaudePolicyOf(content []byte) (ClaudePolicy, bool, error) {
	fm, _, found := split(content)
	if !found {
		return ClaudePolicy{}, false, nil
	}
	for _, line := range strings.Split(string(fm), "\n") {
		if v, ok := strings.CutPrefix(line, ClaudePolicyKey+":"); ok {
			p, err := DecodeClaudePolicy(v)
			if err != nil {
				return ClaudePolicy{}, false, errors.New("agentfm: malformed " + ClaudePolicyKey)
			}
			return p, true, nil
		}
	}
	return ClaudePolicy{}, false, nil
}
