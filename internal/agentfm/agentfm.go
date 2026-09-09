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
//	  network: true         # optional; explicitly allow/deny web fetch/search
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
// OpenCode's `permission:` map carries explicit rules; omitted capabilities
// stay at the tool's default. `network` renders allow/deny for web fetch/search.
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
	"unicode"

	"gopkg.in/yaml.v3"
)

// Homonto is the neutral capability intent declared under the `homonto:` key.
// Model selection is config-driven ([subagents.<name>.<tool>]); unknown neutral
// capability fields are rejected rather than silently ignored.
type Homonto struct {
	ReadOnly    bool      `yaml:"read_only"`              // deny edits/writes
	Bash        *bool     `yaml:"bash"`                   // nil = default (allowed); false = deny
	Network     *bool     `yaml:"network"`                // nil = tool default; true/false = allow/deny web fetch/search
	BashDefault string    `yaml:"bash_default,omitempty"` // empty/ask = guarded; allow = trusted shell execution
	BashAllow   []string  `yaml:"bash_allow"`             // allowed shell commands above the baseline
	BashAsk     []string  `yaml:"bash_ask,omitempty"`     // protected prompts, including after config additions
	BashDeny    []string  `yaml:"bash_deny"`              // denied even inside bash_allow's reach; rendered last so it wins
	Dialogs     bool      `yaml:"dialogs"`                // allow the question/dialog tool
	Spawn       *[]string `yaml:"spawn"`                  // nil = unrestricted; [] = none; [a,b] = only these
	Primary     bool      `yaml:"primary"`                // OpenCode primary agent
	Steps       int       `yaml:"steps,omitempty"`        // OpenCode iteration budget
}

// ModelSpec is a fully-resolved model choice for one tool: which model, which
// variant of it, and how hard to think. Each tool spells these differently —
// see Render — so they are carried neutrally and rendered per tool.
type ModelSpec struct {
	Model   string
	Variant string
	Effort  string
	Steps   *int
	// BashAllowAdd appends exact commands to the agent's base bash_allow
	// before protected asks, composition guards, and final denies.
	BashAllowAdd []string
}

// RenderContext carries the per-subagent model overrides the render needs for
// the tool being rendered (OpenCode, the only adapter since v0.13.0). Overrides
// is keyed by subagent name. A non-nil context is a production render and
// requires a non-empty model for every rendered agent; a nil context is
// reserved for catalog projection tests that intentionally omit model routing.
type RenderContext struct {
	Overrides map[string]ModelSpec
	// Names maps catalog identities to their single installed host name.
	Names map[string]string
	// ShellProxy is the resolved tooling provider; only "rtk" derives wrapped
	// allows. Protected asks and trusted-shell denies also cover RTK without it.
	ShellProxy string
	// ExternalDirectoriesByAgent names the resolved workspace directories that each
	// bundled writable workflow role may access. The engine owns this map so a
	// custom agent cannot self-grant access by declaring frontmatter.
	ExternalDirectoriesByAgent map[string][]string
	// ExternalDirectoryDeniesByAgent excludes coordinator-owned records even
	// when they lie beneath an otherwise trusted source root. Denies render last.
	ExternalDirectoryDeniesByAgent map[string][]string
	// EditDirectoryDeniesByAgent additionally carries worktree-relative paths,
	// which native edit/write/apply_patch use instead of absolute paths.
	EditDirectoryDeniesByAgent map[string][]string
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
		if bytes.HasPrefix(bytes.TrimPrefix(content, []byte("\xef\xbb\xbf")), []byte("---")) {
			return false, fmt.Errorf("agentfm: malformed frontmatter fence; use --- on separate LF or CRLF lines")
		}
		return false, nil
	}
	_, has, err := parseHomonto(fm)
	return has, err
}

// ValidateInstalledName keeps native/verbatim agent metadata from overriding
// the filename alias. It never rewrites content owned by the user.
func ValidateInstalledName(name string, content []byte) error {
	fm, _, ok := split(content)
	if !ok {
		_, err := NeedsTransform(content)
		return err
	}
	var header struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(fm, &header); err != nil {
		return fmt.Errorf("agentfm: malformed frontmatter: %w", err)
	}
	if header.Name != "" && header.Name != name {
		return fmt.Errorf("frontmatter name %q conflicts with installed alias %q; use the matching config key or update the source name (linked content is not rewritten)", header.Name, name)
	}
	return nil
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
		if _, err := NeedsTransform(content); err != nil {
			return nil, err
		}
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
		installedName := name
		if ctx != nil && ctx.Names[name] != "" {
			installedName = ctx.Names[name]
		}
		if installedName != "" {
			if err := ValidateInstalledName(installedName, content); err != nil {
				return nil, fmt.Errorf("agentfm: builtin %q native projection: %w", name, err)
			}
		}
		return content, nil
	}
	// A non-nil context marks a production render after framework expansion.
	// Unlike the nil catalog-test context, it must resolve a non-empty model.
	var (
		spec                       ModelSpec
		externalDirectories        []string
		externalDirectoryDenies    []string
		editDirectoryDenies        []string
		managesExternalDirectories bool
	)
	if ctx != nil {
		var ok bool
		spec, ok = ctx.Overrides[name]
		if !ok || spec.Model == "" {
			return nil, fmt.Errorf("agentfm: agent %q has no model for tool %s; [subagents.%s.%s] model is required", name, tool, name, tool)
		}
		externalDirectories, managesExternalDirectories = ctx.ExternalDirectoriesByAgent[name]
		externalDirectoryDenies = ctx.ExternalDirectoryDeniesByAgent[name]
		editDirectoryDenies = append(append([]string(nil), externalDirectoryDenies...), ctx.EditDirectoryDeniesByAgent[name]...)
		for _, dir := range editDirectoryDenies {
			if dir == "" || strings.ContainsAny(dir, "*?") || strings.ContainsFunc(dir, unicode.IsControl) {
				return nil, fmt.Errorf("agentfm: agent %q edit directory %q must not be empty or contain controls or wildcard characters", name, dir)
			}
		}
		for _, dirs := range [][]string{externalDirectories, externalDirectoryDenies} {
			for _, dir := range dirs {
				if !filepath.IsAbs(dir) || strings.ContainsAny(dir, "*?") || strings.ContainsFunc(dir, unicode.IsControl) {
					return nil, fmt.Errorf("agentfm: agent %q permission directory %q must be an absolute path without controls or wildcard characters", name, dir)
				}
			}
		}
		for _, value := range []string{spec.Model, spec.Variant} {
			if strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) {
				return nil, fmt.Errorf("agentfm: agent %q model/variant must not contain controls or line breaks", name)
			}
		}
		if spec.Steps != nil {
			if *spec.Steps <= 0 {
				return nil, fmt.Errorf("agentfm: agent %q steps must be positive", name)
			}
			h.Steps = *spec.Steps
		}
		if h.Spawn != nil {
			spawn := append([]string(nil), (*h.Spawn)...)
			for i, source := range spawn {
				if alias := ctx.Names[source]; alias != "" {
					spawn[i] = alias
				}
			}
			h.Spawn = &spawn
		}
	}

	// Preserve every frontmatter line except the homonto block and the mode line
	// (re-emitted per tool below).
	var kept []string
	for _, ln := range stripHomontoBlock(fm) {
		if ctx != nil && ctx.Names[name] != "" && strings.HasPrefix(ln, "name:") {
			ln = fmt.Sprintf("name: %q", ctx.Names[name])
		}
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
			extra = append(extra, fmt.Sprintf("model: %q", spec.Model))
			if spec.Variant != "" {
				extra = append(extra, fmt.Sprintf("variant: %q", spec.Variant))
			}
		}
		if h.Steps > 0 {
			extra = append(extra, fmt.Sprintf("steps: %d", h.Steps))
		}
		if h.Bash != nil && !*h.Bash && (len(spec.BashAllowAdd) > 0 || len(h.BashDeny) > 0) {
			return nil, fmt.Errorf("agentfm: agent %q declares bash: deny but carries bash_allow_add/bash_deny entries; a denied agent cannot gain exact allows or denies", name)
		}
		if ctx != nil && ctx.ShellProxy == "rtk" && (h.Bash == nil || *h.Bash) {
			h.BashAllow = rtkPatterns(h.BashAllow, false)
			spec.BashAllowAdd = rtkPatterns(spec.BashAllowAdd, false)
		}
		// Known wrappers must not evade protected prompts or a trusted-shell
		// deny just because RTK is not configured as the shell proxy.
		h.BashAsk = rtkPatterns(h.BashAsk, true)
		if h.BashDefault == "allow" || (ctx != nil && ctx.ShellProxy == "rtk") {
			h.BashDeny = rtkPatterns(h.BashDeny, true)
		}
		if perm := opencodePermission(h, spec.BashAllowAdd, externalDirectories, externalDirectoryDenies, editDirectoryDenies, managesExternalDirectories); perm != "" {
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
	if ctx != nil && ctx.Names[name] != "" {
		if err := ValidateInstalledName(ctx.Names[name], b.Bytes()); err != nil {
			return nil, fmt.Errorf("agentfm: agent %q alias projection: %w", name, err)
		}
	}
	return b.Bytes(), nil
}

// rtkPatterns preserves originals. Allows use only known equivalent native
// command families; other executables use literal passthrough. Restrictions
// mirror both forms even for wildcard executables, since they grant nothing.
func rtkPatterns(commands []string, restrict bool) []string {
	patterns := append([]string(nil), commands...)
	for _, command := range commands {
		words := strings.Fields(command)
		if len(words) == 0 || words[0] == "rtk" {
			continue
		}
		// A grant must have a literal executable prefix; never derive "rtk proxy *".
		if !restrict && strings.ContainsAny(words[0][:1], "*?") {
			continue
		}
		native := false
		// Inspected RTK command families. Names such as test, run, read, and
		// lint have different semantics in RTK and must NOT be mirrored natively.
		switch words[0] {
		case "git", "go", "cargo", "npm", "pnpm", "pytest", "gh", "ls":
			native = true
		}
		if restrict || native {
			patterns = append(patterns, "rtk "+command)
		}
		patterns = append(patterns, "rtk proxy "+command)
	}
	return patterns
}

// bashCompositionGuards re-ask when a permission request contains shell
// composition: separators, pipes, substitutions, redirection, or newlines.
// The host may instead request parsed commands independently; these guards
// do not imply that every compound shell invocation prompts.
var bashCompositionGuards = []string{
	"*;*", "*&&*", "*||*", "*|*", "*$(*", "*`*", "*>*", "*<*", "*\n*",
}

// opencodePermission renders the OpenCode `permission:` block body (indented
// lines) for the neutral intent, including the delegation topology as task globs.
func opencodePermission(h Homonto, additions, externalDirectories, externalDirectoryDenies, editDirectoryDenies []string, managesExternalDirectories bool) string {
	var lines []string
	if h.ReadOnly {
		lines = append(lines, "  edit: deny")
	} else if len(editDirectoryDenies) > 0 {
		// Add only record denies; preserve inherited edit asks and restrictions.
		lines = append(lines, "  edit:")
		dirs := append([]string(nil), editDirectoryDenies...)
		sort.Strings(dirs)
		seen := map[string]bool{}
		for _, dir := range dirs {
			pattern := filepath.ToSlash(filepath.Join(filepath.Clean(dir), "**"))
			if !seen[pattern] {
				lines = append(lines, fmt.Sprintf("    %q: deny", pattern))
				seen[pattern] = true
			}
		}
	}
	if h.Bash != nil && !*h.Bash {
		lines = append(lines, "  bash: deny")
	} else if h.BashDefault != "" || len(h.BashAllow) > 0 || len(additions) > 0 || len(h.BashAsk) > 0 || len(h.BashDeny) > 0 {
		// Construct the last-match-wins sequence before serializing unique keys.
		type rule struct{ pattern, action string }
		baseline := "ask"
		if h.BashDefault == "allow" {
			baseline = "allow"
		}
		rules := []rule{{"*", baseline}}
		addRules := func(action string, patterns []string) {
			for _, pattern := range patterns {
				if strings.TrimSpace(pattern) != "" {
					rules = append(rules, rule{pattern, action})
				}
			}
		}
		addRules("allow", h.BashAllow)
		addRules("allow", additions)
		addRules("ask", h.BashAsk)
		// Guards follow allows so a request containing "git status; curl ..."
		// re-asks despite matching "git status*". If the host requests each
		// parsed command separately, each receives its own matching rule.
		if h.BashDefault != "allow" {
			addRules("ask", bashCompositionGuards)
		}
		// Explicit denies render LAST, including after user additions: bypass
		// requests are single commands, not shell composition guards can catch.
		addRules("deny", h.BashDeny)
		// Keep each key at its LAST position, not just its last action at the
		// first position: a later overlapping glob could otherwise override it.
		last := map[string]int{}
		for i, r := range rules {
			last[r.pattern] = i
		}
		lines = append(lines, "  bash:")
		for i, r := range rules {
			if last[r.pattern] == i {
				lines = append(lines, fmt.Sprintf("    %q: %s", r.pattern, r.action))
			}
		}
	}
	if h.Network != nil {
		action := "deny"
		if *h.Network {
			action = "allow"
		}
		lines = append(lines, "  webfetch: "+action, "  websearch: "+action)
	}
	if !h.ReadOnly && managesExternalDirectories {
		lines = append(lines, "  external_directory:")
		// Agent rules override inherited global permissions. Deny everything
		// outside the declared paths before re-allowing those trusted roots.
		lines = append(lines, `    "*": deny`)
		dirs := append([]string(nil), externalDirectories...)
		sort.Strings(dirs)
		seen := map[string]bool{}
		denied := map[string]bool{}
		for _, dir := range externalDirectoryDenies {
			denied[filepath.ToSlash(filepath.Join(filepath.Clean(dir), "**"))] = true
		}
		for _, dir := range dirs {
			pattern := filepath.ToSlash(filepath.Join(filepath.Clean(dir), "**"))
			if seen[pattern] || denied[pattern] {
				continue
			}
			seen[pattern] = true
			lines = append(lines, fmt.Sprintf("    %q: allow", pattern))
		}
		patterns := make([]string, 0, len(denied))
		for pattern := range denied {
			patterns = append(patterns, pattern)
		}
		sort.Strings(patterns)
		for _, pattern := range patterns {
			lines = append(lines, fmt.Sprintf("    %q: deny", pattern))
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
	first, rest, found := bytes.Cut(content, []byte("\n"))
	if !found || string(bytes.TrimSuffix(first, []byte("\r"))) != "---" {
		return nil, nil, false
	}
	for offset := 0; offset < len(rest); {
		line, remaining, newline := bytes.Cut(rest[offset:], []byte("\n"))
		if string(bytes.TrimSuffix(line, []byte("\r"))) == "---" {
			fm = bytes.ReplaceAll(rest[:offset], []byte("\r\n"), []byte("\n"))
			return bytes.TrimSuffix(fm, []byte("\n")), remaining, true
		}
		if !newline {
			break
		}
		offset += len(line) + 1
	}
	return nil, nil, false
}

// resolveYAMLAlias follows references without rewriting the syntax tree. Invalid
// alias chains return nil; the caller still validates the target's kind and tag.
func resolveYAMLAlias(node *yaml.Node) *yaml.Node {
	seen := map[*yaml.Node]bool{}
	for node != nil && node.Kind == yaml.AliasNode {
		if seen[node] {
			return nil
		}
		seen[node] = true
		node = node.Alias
	}
	return node
}

// parseHomonto reads the `homonto:` block from frontmatter YAML. It returns the
// parsed block, whether a block was present at all, and a parse error if the
// block exists but is malformed. The two outcomes a caller must distinguish —
// "no block, project verbatim" vs "block present but unparseable, fail loudly" —
// are surfaced as (zero, false, nil) and (zero, false, err) respectively.
func parseHomonto(fm []byte) (Homonto, bool, error) {
	if bytes.ContainsRune(fm, '\r') {
		return Homonto{}, false, fmt.Errorf("frontmatter must use LF or CRLF line endings, not bare CR")
	}
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
		"bash_default": true, "bash_ask": true, "bash_deny": true, "dialogs": true, "spawn": true, "primary": true,
		"steps": true,
	}
	hasBashDefault := false
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i].Value
		if !allowed[key] {
			return Homonto{}, false, fmt.Errorf("homonto block has unknown capability %q", key)
		}
		value := node.Content[i+1]
		// yaml.v3 coerces scalar numbers/bools into strings and accepts null.
		// Check shell policy nodes before decoding so malformed rules fail closed.
		switch key {
		case "bash_default":
			hasBashDefault = true
			value = resolveYAMLAlias(value)
			if value == nil || value.Kind != yaml.ScalarNode || value.Tag != "!!str" || (value.Value != "" && value.Value != "allow" && value.Value != "ask") {
				return Homonto{}, false, fmt.Errorf("homonto bash_default must be a string: allow or ask (or empty for guarded behavior)")
			}
		case "bash_allow", "bash_ask", "bash_deny":
			value = resolveYAMLAlias(value)
			if value == nil || value.Kind != yaml.SequenceNode {
				return Homonto{}, false, fmt.Errorf("homonto %s must be a list of command strings", key)
			}
			for _, item := range value.Content {
				item = resolveYAMLAlias(item)
				if item == nil || item.Kind != yaml.ScalarNode || item.Tag != "!!str" || strings.TrimSpace(item.Value) == "" {
					return Homonto{}, false, fmt.Errorf("homonto %s entries must be non-empty command strings", key)
				}
			}
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
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == "steps" && doc.Homonto.Steps <= 0 {
			return Homonto{}, false, fmt.Errorf("homonto steps must be positive")
		}
	}
	if h := doc.Homonto; h.Bash != nil && !*h.Bash && (hasBashDefault || len(h.BashAllow) > 0 || len(h.BashAsk) > 0 || len(h.BashDeny) > 0) {
		return Homonto{}, false, fmt.Errorf("homonto bash: false contradicts bash_default or bash_allow/bash_ask/bash_deny entries")
	}
	if doc.Homonto.ReadOnly && doc.Homonto.BashDefault == "allow" {
		return Homonto{}, false, fmt.Errorf("homonto read_only: true contradicts bash_default: allow")
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
