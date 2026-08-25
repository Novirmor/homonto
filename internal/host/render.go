package host

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"text/template"

	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

//go:embed assets
var assets embed.FS

// templateData is what the wrapper templates render from. It is
// deliberately tiny: a template that needs more than the workflow's name
// and how to invoke the binary is a template that has started to contain
// policy.
type templateData struct {
	Workflow     string
	WorkflowNoun string
	Entrypoint   string
	Binary       string
	// BinaryJSON is the binary as a JSON string literal, for embedding in
	// the JavaScript plugin without hand-rolled quoting.
	BinaryJSON string
}

// newTemplateData builds the render context for one request.
func newTemplateData(r InstallRequest) templateData {
	binary := r.binary()
	encoded, err := json.Marshal(binary)
	if err != nil {
		// Unreachable: a string always marshals.
		encoded = []byte(`"homonto"`)
	}
	return templateData{
		Workflow:     string(r.Workflow),
		WorkflowNoun: workflowNoun(r.Workflow),
		Entrypoint:   entrypoint(r.Workflow),
		Binary:       binary,
		BinaryJSON:   string(encoded),
	}
}

// workflowNoun is how a wrapper describes the work to a user.
func workflowNoun(w workspacecfg.Workflow) string {
	if w == workspacecfg.WorkflowChange {
		return "formal change"
	}
	return "task"
}

// render executes one embedded template.
func render(name string, data templateData) ([]byte, error) {
	body, err := fs.ReadFile(assets, name)
	if err != nil {
		return nil, fmt.Errorf("host: read embedded %s: %w", name, err)
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(body))
	if err != nil {
		return nil, fmt.Errorf("host: parse embedded %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("host: render %s: %w", name, err)
	}
	out := buf.Bytes()
	if !bytes.HasSuffix(out, []byte("\n")) {
		out = append(out, '\n')
	}
	return out, nil
}

// hookEntries returns the Claude hook entries Homonto owns, with the
// binary substituted in.
func hookEntries(binary string) (map[string]any, error) {
	body, err := fs.ReadFile(assets, "assets/claude/settings.hooks.json")
	if err != nil {
		return nil, fmt.Errorf("host: read embedded hooks: %w", err)
	}
	// The placeholder is replaced rather than templated because the file is
	// checked-in JSON: keeping it valid JSON on disk means it can be
	// linted, formatted, and diffed like anything else.
	body = bytes.ReplaceAll(body, []byte("BINARY"), []byte(binary))
	var hooks map[string]any
	if err := json.Unmarshal(body, &hooks); err != nil {
		return nil, fmt.Errorf("host: decode embedded hooks: %w", err)
	}
	return hooks, nil
}

// generatedFiles returns the whole files one tool's integration owns, in a
// stable order.
func generatedFiles(r InstallRequest) ([]PlannedFile, error) {
	dir, err := r.Tool.Dir()
	if err != nil {
		return nil, err
	}
	data := newTemplateData(r)
	name := data.Entrypoint

	var sources []struct {
		path     string
		template string
		mode     fs.FileMode
	}
	switch r.Tool {
	case ToolClaude:
		// Claude gets ONE skill entry, and a command that does nothing but
		// invoke it. Two entry points for one workflow is two places for a
		// rule to hide.
		sources = []struct {
			path     string
			template string
			mode     fs.FileMode
		}{
			{dir + "/skills/" + name + "/SKILL.md", "assets/claude/skill.md.tmpl", 0o644},
			{dir + "/commands/" + name + ".md", "assets/claude/command.md.tmpl", 0o644},
		}
	case ToolOpenCode:
		sources = []struct {
			path     string
			template string
			mode     fs.FileMode
		}{
			{dir + "/skill/" + name + "/SKILL.md", "assets/opencode/skill.md.tmpl", 0o644},
			{dir + "/command/" + name + ".md", "assets/opencode/command.md.tmpl", 0o644},
			{dir + "/plugin/homonto.js", "assets/opencode/plugin.js.tmpl", 0o644},
		}
	}

	out := make([]PlannedFile, 0, len(sources))
	for _, src := range sources {
		body, err := render(src.template, data)
		if err != nil {
			return nil, err
		}
		out = append(out, PlannedFile{
			Path: src.path, Mode: src.mode, content: Mark(src.path, body),
		})
	}
	return out, nil
}

// ignoreEntries are the .gitignore lines that keep generated host files
// project-local.
func ignoreEntries(t Tool) ([]string, error) {
	dir, err := t.Dir()
	if err != nil {
		return nil, err
	}
	return []string{dir + "/"}, nil
}

// settingsPath is the shared document Homonto projects hooks into.
func settingsPath(t Tool) (string, bool) {
	if t != ToolClaude {
		return "", false
	}
	return ".claude/settings.json", true
}

// wrapperVerbs are the only binary invocations a generated wrapper is
// allowed to contain. The install tests assert this exactly: a wrapper
// that learned a fifth verb has started to contain workflow policy, and
// the whole point of a thin integration is that it cannot.
func wrapperVerbs() []string {
	return []string{"next", "report", "decide", "accept-edit", "host probe", "host guard"}
}

// Invocations returns every command a wrapper actually runs, as the
// argument string following the binary.
//
// It reads INVOCATIONS rather than mentions. A wrapper's prose names the
// binary constantly — in error messages, in explanations — and a check
// that matched those would be satisfied by rewording rather than by the
// wrapper staying thin. Markdown wrappers invoke inside fenced code
// blocks; the JavaScript plugin invokes through one helper that takes an
// argument array.
func Invocations(path, body, binary string) []string {
	if strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".mjs") {
		return jsInvocations(body)
	}
	return fencedInvocations(body, binary)
}

// fencedInvocations reads the commands inside a markdown file's fenced
// code blocks.
func fencedInvocations(body, binary string) []string {
	var (
		out    []string
		inside bool
	)
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inside = !inside
			continue
		}
		if !inside {
			continue
		}
		trimmed := strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(trimmed, binary+" ")
		if !ok {
			continue
		}
		out = append(out, strings.TrimSpace(rest))
	}
	return out
}

// jsArgsMarker opens the plugin's single invocation helper. Every call to
// the binary goes through it, which is what makes the plugin's surface
// checkable at all.
const jsArgsMarker = "homonto($, ["

// jsInvocations reads the argument arrays the plugin passes to the binary.
func jsInvocations(body string) []string {
	var out []string
	rest := body
	for {
		idx := strings.Index(rest, jsArgsMarker)
		if idx < 0 {
			return out
		}
		rest = rest[idx+len(jsArgsMarker):]
		end := strings.Index(rest, "]")
		if end < 0 {
			return out
		}
		var args []string
		for _, field := range strings.Split(rest[:end], ",") {
			field = strings.TrimSpace(field)
			field = strings.Trim(field, `"'`)
			if field != "" {
				args = append(args, field)
			}
		}
		out = append(out, strings.Join(args, " "))
		rest = rest[end:]
	}
}

// containsOnlyWrapperVerbs reports whether every invocation in body runs
// one of the allowed verbs.
func containsOnlyWrapperVerbs(path, body, binary string) (string, bool) {
	for _, invocation := range Invocations(path, body, binary) {
		allowed := false
		for _, verb := range wrapperVerbs() {
			if invocation == verb || strings.HasPrefix(invocation, verb+" ") {
				allowed = true
				break
			}
		}
		if !allowed {
			return invocation, false
		}
	}
	return "", true
}
