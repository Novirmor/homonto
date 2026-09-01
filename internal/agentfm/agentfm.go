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
	"strings"

	"gopkg.in/yaml.v3"
)

// Homonto is the neutral capability intent declared under the `homonto:` key.
// Model selection is config-driven ([subagents.<name>.<tool>]); a legacy
// `role:` field in the YAML, if present, is silently dropped by the YAML
// decoder as an unknown field.
type Homonto struct {
	ReadOnly bool      `yaml:"read_only"` // deny edits/writes
	Bash     *bool     `yaml:"bash"`      // nil = default (allowed); false = deny
	Dialogs  bool      `yaml:"dialogs"`   // allow the question/dialog tool
	Spawn    *[]string `yaml:"spawn"`     // nil = unrestricted; [] = none; [a,b] = only these
	Primary  bool      `yaml:"primary"`   // OpenCode primary agent
	Steps    int       `yaml:"steps"`     // OpenCode iteration budget
}

// ModelSpec is a fully-resolved model choice for one tool: which model, which
// variant of it, and how hard to think. Each tool spells these differently —
// see Render — so they are carried neutrally and rendered per tool.
type ModelSpec struct {
	Model   string
	Variant string
	Effort  string
}

// RenderContext carries the per-subagent model overrides the render needs for
// the tool being rendered (OpenCode, the only adapter since v0.13.0). Overrides
// is keyed by subagent name. A non-nil context is a production render and
// requires a non-empty model for every rendered agent; a nil context is
// reserved for catalog projection tests that intentionally omit model routing.
type RenderContext struct {
	Overrides map[string]ModelSpec
	// Targets names actually projected to this tool. It lets materialization skip
	// an unselected tool variant without weakening validation for selected agents.
	Targets map[string]bool
}

// NeedsTransform reports whether content carries a `homonto:` frontmatter block
// (and therefore must be rendered per tool rather than projected verbatim). A
// malformed `homonto:` block is reported as not-transformed: NeedsTransform is a
// quick filter (drives whether Render is called at all), and a parse error in
// the block surfaces from Render itself, where the agent name is in scope for a
// clearer message.
func NeedsTransform(content []byte) bool {
	fm, _, ok := split(content)
	if !ok {
		return false
	}
	_, has, _ := parseHomonto(fm)
	return has
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
	var spec ModelSpec
	if ctx != nil {
		var ok bool
		spec, ok = ctx.Overrides[name]
		if !ok || spec.Model == "" {
			return nil, fmt.Errorf("agentfm: agent %q has no model for tool %s; [subagents.%s.%s] model is required", name, tool, name, tool)
		}
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
		// OpenCode is the mirror image: `variant` is its own field, and there is
		// no effort concept at all — dropping it here is why the config layer
		// reports the drop once at plan time rather than failing.
		if spec.Model != "" {
			extra = append(extra, "model: "+spec.Model)
		}
		if spec.Variant != "" {
			extra = append(extra, "variant: "+spec.Variant)
		}
		if h.Steps > 0 {
			extra = append(extra, fmt.Sprintf("steps: %d", h.Steps))
		}
		if perm := opencodePermission(h); perm != "" {
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

// opencodePermission renders the OpenCode `permission:` block body (indented
// lines) for the neutral intent, including the delegation topology as task globs.
func opencodePermission(h Homonto) string {
	var lines []string
	if h.ReadOnly {
		lines = append(lines, "  edit: deny")
	}
	if h.Bash != nil && !*h.Bash {
		lines = append(lines, "  bash: deny")
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
	var doc struct {
		Homonto *Homonto `yaml:"homonto"`
	}
	if err := yaml.Unmarshal(fm, &doc); err != nil {
		return Homonto{}, false, err
	}
	if doc.Homonto == nil {
		return Homonto{}, false, nil
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
