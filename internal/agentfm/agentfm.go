// Package agentfm renders OpenCode subagent frontmatter from one neutral source.
//
// An agent declares its intent once, tool-neutrally, in a `homonto:` frontmatter
// block, and Render() emits the tool's native dialect (OpenCode is the only
// adapter since v0.13.0):
//
//	---
//	name: onto-reviewer
//	description: ...
//	mode: subagent
//	homonto:
//	  read_only: true       # deny edits/writes
//	  bash: false           # optional; false denies bash (default: allowed)
//	  network: false        # optional; false denies web fetch/search
//	  dialogs: true         # allow the interactive question/dialog tool
//	  spawn: []             # delegation topology: agents this one may dispatch
//	  primary: true         # OpenCode primary agent
//	  steps: 60             # iteration budget (OpenCode steps)
//	---
//	<prompt body>
//
// The model an agent renders as comes from the config's explicit
// [subagents.<name>.opencode] block — there are no roles or tiers, and an agent
// with no such block (and thus no model) is a load-time error, not a silent
// default.
//
// OpenCode denies by exception: a `permission:` map carries the denials, and
// every capability the intent does not deny stays at the tool's default.
// read_only/bash/spawn:[] render fully; `dialogs` renders as
// `question: allow|deny`; a named spawn list renders as task globs; `steps`
// renders as `steps:`. Every non-homonto frontmatter line except `mode:` is
// preserved verbatim (`mode:` is re-emitted from `primary`).
package agentfm

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Homonto is the neutral capability intent declared under the `homonto:` key.
// Model selection is config-driven ([subagents.<name>.<tool>]); a legacy
// `role:` field in the YAML, if present, is silently dropped by the YAML
// decoder as an unknown field.
type Homonto struct {
	ReadOnly  bool      `yaml:"read_only"`  // deny edits/writes
	Bash      *bool     `yaml:"bash"`       // nil = default (allowed); false = deny
	Network   *bool     `yaml:"network"`    // nil = default (allowed); false = deny web fetch/search
	BashAllow []string  `yaml:"bash_allow"` // allowlisted shell commands; all other commands ask
	BashDeny  []string  `yaml:"bash_deny"`  // denied even inside bash_allow's reach; rendered last so it wins
	Dialogs   bool      `yaml:"dialogs"`    // allow the question/dialog tool
	Spawn     *[]string `yaml:"spawn"`      // nil = unrestricted; [] = none; [a,b] = only these
	Primary   bool      `yaml:"primary"`    // OpenCode primary agent
	Steps     int       `yaml:"steps"`      // OpenCode iteration budget
}

// ModelSpec is a fully-resolved model choice for one tool: which model, which
// variant of it, and how hard to think. Each tool spells these differently —
// see Render — so they are carried neutrally and rendered per tool.
type ModelSpec struct {
	Model   string
	Variant string
	Effort  string
	// BashAllowAdd appends exact commands to the agent's base bash_allow
	// (rendered after the base list, before "*": ask ordering stays intact).
	BashAllowAdd []string
}

// RenderContext carries the per-subagent model overrides the render needs for
// the tool being rendered (OpenCode, the only adapter since v0.13.0). Overrides
// is keyed by subagent name. A non-nil context is a production render and
// requires a non-empty model for every rendered agent; a nil context is
// reserved for catalog projection tests that intentionally omit model routing.
type RenderContext struct {
	Overrides map[string]ModelSpec
	// ExternalDirectoriesByAgent names the resolved [repos] paths that each
	// bundled writable workflow role may access. The engine owns this map so a
	// custom agent cannot self-grant access by declaring frontmatter.
	ExternalDirectoriesByAgent map[string][]string
	// Targets names actually projected to this tool. It lets materialization skip
	// an unselected tool variant without weakening validation for selected agents.
	Targets map[string]bool
}

// NeedsTransform reports whether content carries a `homonto:` frontmatter block
// (and therefore must be rendered per tool rather than projected verbatim).
// Malformed capability intent is an error: treating it as verbatim would drop
// its declared permissions during projection.
func NeedsTransform(content []byte) (bool, error) {
	fm, _, ok := split(content)
	if !ok {
		return false, nil
	}
	_, has, err := parseHomonto(fm)
	return has, err
}

// ProjectsFor reports whether content is projected for tool at all. It is
// false only where Render deliberately emits nothing — and since v0.13.0
// (OpenCode the only adapter) no such case remains: every agent, primary
// included, renders. Callers use it to tell "deliberately not projected here"
// apart from "should be here and is missing", so a by-design absence is never
// reported as a fixable finding.
func ProjectsFor(content []byte, tool string) (bool, error) {
	// Projection is decided by the neutral block alone (primary vs not), never by
	// the model spec, so an empty context is the right question to ask here.
	rendered, err := Render("", content, tool, nil)
	if err != nil {
		return false, err
	}
	return rendered != nil, nil
}

// Render returns content rewritten for tool ("opencode"), or nil bytes when
// the agent must NOT be projected for that tool. Content with no frontmatter
// or no `homonto:` block is returned unchanged.
func Render(name string, content []byte, tool string, ctx *RenderContext) ([]byte, error) {
	fm, body, ok := split(content)
	if !ok {
		return content, nil
	}
	h, has, err := parseHomonto(fm)
	if err != nil {
		// A malformed `homonto:` block must NOT be silently projected as if
		// the agent had no neutral capability intent — that would emit a
		// weakened agent (no model line, default permissions) with no signal.
		// Name the parse failure so a typo in the block is loud, not silent.
		return nil, fmt.Errorf("agentfm: malformed homonto block: %w", err)
	}
	if !has {
		return content, nil
	}
	// A non-nil context marks a production render after framework expansion.
	// Unlike the nil catalog-test context, it must resolve a non-empty model.
	var (
		spec                       ModelSpec
		externalDirectories        []string
		managesExternalDirectories bool
	)
	if ctx != nil {
		var ok bool
		spec, ok = ctx.Overrides[name]
		if !ok || spec.Model == "" {
			return nil, fmt.Errorf("agentfm: agent %q has no model for tool %s; [subagents.%s.%s] model is required", name, tool, name, tool)
		}
		externalDirectories, managesExternalDirectories = ctx.ExternalDirectoriesByAgent[name]
	}

	// Preserve every frontmatter line except the homonto block and the mode line
	// (re-emitted per tool below).
	var kept []string
	for _, ln := range stripHomontoBlock(fm) {
		if strings.HasPrefix(strings.TrimSpace(ln), "mode:") {
			continue
		}
		kept = append(kept, ln)
	}

	var extra []string
	switch tool {
	case "opencode":
		mode := "subagent"
		if h.Primary {
			mode = "primary"
		}
		extra = append(extra, "mode: "+mode)
		// OpenCode keeps the base model ID and selected variant separate. The
		// provider defines the available variants (medium, high, xhigh, …).
		// There is no separate effort concept: an `effort:` value is rejected
		// at load, not silently dropped here.
		if spec.Model != "" {
			extra = append(extra, "model: "+spec.Model)
			if spec.Variant != "" {
				extra = append(extra, "variant: "+spec.Variant)
			}
		}
		if h.Steps > 0 {
			extra = append(extra, fmt.Sprintf("steps: %d", h.Steps))
		}
		if h.Bash != nil && !*h.Bash && (len(spec.BashAllowAdd) > 0 || len(h.BashDeny) > 0) {
			return nil, fmt.Errorf("agentfm: agent %q declares bash: deny but carries bash_allow_add/bash_deny entries; a denied agent cannot gain exact allows or denies", name)
		}
		if perm := opencodePermission(h, spec.BashAllowAdd, externalDirectories, managesExternalDirectories); perm != "" {
			extra = append(extra, "permission:", perm)
		}
	default:
		return nil, fmt.Errorf("agentfm: unknown tool %q", tool)
	}

	var b bytes.Buffer
	b.WriteString("---\n")
	for _, ln := range kept {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	for _, ln := range extra {
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	b.WriteString("---\n")
	b.Write(body)
	return b.Bytes(), nil
}

// bashCompositionGuards are shell-composition patterns re-asked after every
// bash allowlist: separators, conditional chains, pipes, command
// substitution, backticks, redirection, and multi-line commands. Each lets an
// allowed prefix chain a second command, which the allow glob would otherwise
// match and run without a prompt.
var bashCompositionGuards = []string{
	"*;*", "*&&*", "*||*", "*|*", "*$(*", "*`*", "*>*", "*<*", "*\n*",
}

// opencodePermission renders the OpenCode `permission:` block body (indented
// lines) for the neutral intent, including the delegation topology as task globs.
func opencodePermission(h Homonto, additions, externalDirectories []string, managesExternalDirectories bool) string {
	var lines []string
	if h.ReadOnly {
		lines = append(lines, "  edit: deny")
	}
	if h.Bash != nil && !*h.Bash {
		lines = append(lines, "  bash: deny")
	} else if len(h.BashAllow) > 0 || len(additions) > 0 || len(h.BashDeny) > 0 {
		// OpenCode evaluates the final matching rule, so the broad prompt must
		// precede the command-specific allows. Additions (bash_allow_add, the
		// reviewed permission-suggestion output) append after the base list,
		// deduplicated.
		lines = append(lines, "  bash:", `    "*": ask`)
		seen := map[string]bool{}
		for _, command := range append(append([]string{}, h.BashAllow...), additions...) {
			if strings.TrimSpace(command) == "" || seen[command] {
				continue
			}
			seen[command] = true
			lines = append(lines, fmt.Sprintf("    %q: allow", command))
		}
		// Composition guards, emitted AFTER the allows: a prefix allow like
		// "git status*" also matches "git status; curl …" under glob
		// matching, and last-match-wins would run the chained command
		// silently. Re-asking every compound form closes that hole for the
		// whole allowlist at once.
		for _, guard := range bashCompositionGuards {
			lines = append(lines, fmt.Sprintf("    %q: ask", guard))
		}
		// Explicit denies render LAST: a deny like "onto bypass*" must beat
		// the broad "onto *" allow for last-match-wins, closing the
		// single-command gate-skipping surface guards cannot see (no
		// composition involved).
		for _, command := range h.BashDeny {
			if strings.TrimSpace(command) == "" {
				continue
			}
			lines = append(lines, fmt.Sprintf("    %q: deny", command))
		}
	}
	if h.Network != nil && !*h.Network {
		lines = append(lines, "  webfetch: deny", "  websearch: deny")
	}
	if !h.ReadOnly && managesExternalDirectories {
		lines = append(lines, "  external_directory:")
		// Agent rules override inherited global permissions. Deny everything
		// outside the declared paths before re-allowing those trusted roots.
		lines = append(lines, `    "*": deny`)
		dirs := append([]string(nil), externalDirectories...)
		sort.Strings(dirs)
		seen := map[string]bool{}
		for _, dir := range dirs {
			pattern := filepath.ToSlash(filepath.Join(filepath.Clean(dir), "**"))
			if seen[pattern] {
				continue
			}
			seen[pattern] = true
			lines = append(lines, fmt.Sprintf("    %q: allow", pattern))
		}
	}
	// dialogs is enforced both ways: an agent whose protocol is "return a
	// Questions: section, never prompt" must actually be unable to prompt —
	// omitting the line would leave OpenCode's default (available) in place
	// and the intent silently unenforced.
	if h.Dialogs {
		lines = append(lines, "  question: allow")
	} else {
		lines = append(lines, "  question: deny")
	}
	if h.Spawn != nil {
		if len(*h.Spawn) == 0 {
			lines = append(lines, "  task: deny")
		} else {
			lines = append(lines, "  task:", `    "*": deny`)
			for _, a := range *h.Spawn {
				lines = append(lines, fmt.Sprintf("    %q: allow", a))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// split separates content into its frontmatter lines and the remaining body.
// ok is false when content does not open with a `---` frontmatter fence.
func split(content []byte) (fm []byte, body []byte, ok bool) {
	if !bytes.HasPrefix(content, []byte("---\n")) {
		return nil, nil, false
	}
	rest := content[len("---\n"):]
	fm, body, found := bytes.Cut(rest, []byte("\n---\n"))
	if !found {
		return nil, nil, false
	}
	return fm, body, true
}

// parseHomonto reads the `homonto:` block from frontmatter YAML. It returns the
// parsed block, whether a block was present at all, and a parse error if the
// block exists but is malformed. The two outcomes a caller must distinguish —
// "no block, project verbatim" vs "block present but unparseable, fail loudly" —
// are surfaced as (zero, false, nil) and (zero, false, err) respectively.
func parseHomonto(fm []byte) (Homonto, bool, error) {
	var raw map[string]yaml.Node
	if err := yaml.Unmarshal(fm, &raw); err != nil {
		return Homonto{}, false, err
	}
	node, present := raw["homonto"]
	if !present {
		return Homonto{}, false, nil
	}
	if node.Tag == "!!null" {
		return Homonto{}, false, fmt.Errorf("homonto block must not be null")
	}
	if node.Kind != yaml.MappingNode {
		return Homonto{}, false, fmt.Errorf("homonto block must be an object")
	}
	allowed := map[string]bool{
		"read_only": true, "bash": true, "network": true, "bash_allow": true,
		"bash_deny": true, "dialogs": true, "spawn": true, "primary": true,
		"steps": true,
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !allowed[key] {
			return Homonto{}, false, fmt.Errorf("homonto block has unknown capability %q", key)
		}
	}
	var doc struct {
		Homonto *Homonto `yaml:"homonto"`
	}
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return Homonto{}, false, err
	}
	if doc.Homonto == nil {
		return Homonto{}, false, fmt.Errorf("homonto block must be an object")
	}
	return *doc.Homonto, true, nil
}

// stripHomontoBlock returns the frontmatter lines with the `homonto:` key and its
// indented child lines removed, and comment-only lines dropped (the catalog's
// homonto comments are maintainer notes that must not leak into the already-
// rendered projected file). Every other line is preserved verbatim.
func stripHomontoBlock(fm []byte) []string {
	var out []string
	lines := strings.Split(string(fm), "\n")
	skipping := false
	for _, ln := range lines {
		if skipping {
			// Child lines of the block are indented; the first non-indented,
			// non-blank line ends the block.
			if strings.TrimSpace(ln) == "" || ln[0] == ' ' || ln[0] == '\t' {
				continue
			}
			skipping = false
		}
		if ln == "homonto:" || strings.HasPrefix(ln, "homonto:") {
			skipping = true
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		out = append(out, ln)
	}
	return out
}
