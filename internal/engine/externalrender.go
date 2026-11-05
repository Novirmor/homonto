package engine

import (
	"bytes"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/Novirmor/homonto/internal/agentfm"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/fsutil"
	"github.com/Novirmor/homonto/internal/remote"
)

// externalRenderKey is the render identity of a local:/remote: subagent. It
// can never equal a builtin catalog name, so an external agent's frontmatter
// cannot claim a builtin role's tools or the directory grants the engine
// computes for bundled agents (ADR 0096).
func externalRenderKey(name string) string { return "external/" + name }

// renderedSubagentDir holds rendered external subagents.
func (e *Engine) renderedSubagentDir() string {
	return filepath.Join(e.StateDir, "rendered", "subagents")
}

// externalSubagentSource is where a declared local:/remote: subagent's
// content lives, and whether it is available yet (a remote one appears once
// apply has fetched and verified it).
func (e *Engine) externalSubagentSource(entry config.NamedResource) (string, bool) {
	var p string
	switch {
	case remote.IsRemoteSource(entry.Resource.Source):
		p = filepath.Join(e.remoteSubagentDir(), entry.Name+".md")
	case strings.HasPrefix(entry.Resource.Source, "local:"):
		p = filepath.Join(e.ContentDir, "subagents", strings.TrimPrefix(entry.Resource.Source, "local:")+".md")
	default:
		return "", false
	}
	return p, fsutil.FileExists(p)
}

// renderExternalSubagents renders every declared local:/remote: subagent
// whose content carries a homonto: intent block, exactly as builtins render:
// model, variant and steps from [subagents.<name>.opencode], permissions from
// the intent, shell rules from installed services. Content without intent
// stays verbatim, and a model override on it is an error, because nothing
// would ever apply it.
func (e *Engine) renderExternalSubagents() (map[string][]byte, error) {
	entries, err := e.Cfg.ExpandedSubagentEntriesForTool("opencode")
	if err != nil {
		return nil, nil // expansion errors surface through plan and doctor
	}
	var base agentfm.RenderContext
	rendered := map[string][]byte{}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Resource.Source, "builtin:") {
			continue
		}
		path, ok := e.externalSubagentSource(entry)
		if !ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("subagents.%s: %w", entry.Name, err)
		}
		route := e.Cfg.Subagents[entry.Name].OpenCode
		intent, err := agentfm.NeedsTransform(data)
		if err != nil {
			return nil, fmt.Errorf("subagents.%s: %w", entry.Name, err)
		}
		if !intent {
			if route.IsSet() {
				return nil, fmt.Errorf("subagents.%s declares a model override, but %s has no homonto: intent block, so it is projected verbatim and the override could never apply; add a homonto: block to render it, or remove [subagents.%s.opencode]", entry.Name, entry.Resource.Source, entry.Name)
			}
			continue
		}
		if route.Model == "" {
			routes, err := e.Routes()
			if err != nil {
				return nil, err
			}
			sel, ok := routes.policyModel(entry.Name)
			if !ok {
				return nil, fmt.Errorf("subagents.%s renders from its homonto: intent block; set [subagents.%s.opencode] model or cover it with a [routing] policy", entry.Name, entry.Name)
			}
			route.Model, route.Variant = sel.Model, sel.Variant
		}
		if base.Names == nil {
			ctxs, err := e.subagentRenderContext()
			if err != nil {
				return nil, err
			}
			base = ctxs["opencode"]
		}
		key := externalRenderKey(entry.Name)
		ctx := base
		ctx.Names = maps.Clone(base.Names)
		if ctx.Names == nil {
			ctx.Names = map[string]string{}
		}
		ctx.Names[key] = entry.Name
		ctx.Overrides = map[string]agentfm.ModelSpec{key: {Model: route.Model, Variant: route.Variant, Effort: route.Effort, Steps: route.Steps, BashAllowAdd: route.BashAllowAdd}}
		ctx.ExternalDirectoriesByAgent = nil
		ctx.ExternalDirectoryDeniesByAgent = nil
		ctx.EditDirectoryDeniesByAgent = nil
		out, err := agentfm.Render(key, data, "opencode", &ctx)
		if err != nil {
			return nil, fmt.Errorf("subagents.%s: %w", entry.Name, err)
		}
		rendered[entry.Name] = out
	}
	return rendered, nil
}

// validateExternalSubagents preserves Build's fail-fast validation for local
// and already-materialized remote agents without resolving routing policies.
// In particular, an auto policy must not run `opencode models` until Plan.
func (e *Engine) validateExternalSubagents() error {
	entries, err := e.Cfg.ExpandedSubagentEntriesForTool("opencode")
	if err != nil {
		return nil // expansion errors surface through plan and doctor
	}
	routes, err := e.Cfg.AgentRoutes("opencode")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Resource.Source, "builtin:") {
			continue
		}
		path, ok := e.externalSubagentSource(entry)
		if !ok {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("subagents.%s: %w", entry.Name, err)
		}
		route := e.Cfg.Subagents[entry.Name].OpenCode
		intent, err := agentfm.NeedsTransform(data)
		if err != nil {
			return fmt.Errorf("subagents.%s: %w", entry.Name, err)
		}
		if !intent {
			if route.IsSet() {
				return fmt.Errorf("subagents.%s declares a model override, but %s has no homonto: intent block, so it is projected verbatim and the override could never apply; add a homonto: block to render it, or remove [subagents.%s.opencode]", entry.Name, entry.Resource.Source, entry.Name)
			}
			continue
		}
		if route.Model == "" && routes[entry.Name].From != config.RouteFromPolicy {
			return fmt.Errorf("subagents.%s renders from its homonto: intent block; set [subagents.%s.opencode] model or cover it with a [routing] policy", entry.Name, entry.Name)
		}
		// Render with a placeholder model to exercise all intent validation
		// (including frontmatter keys that rendering owns) without selecting an
		// auto policy. The real render happens during Plan.
		key := externalRenderKey(entry.Name)
		model := route.Model
		if model == "" {
			model = "validation/model"
		}
		ctx := &agentfm.RenderContext{
			Names: map[string]string{key: entry.Name},
			Overrides: map[string]agentfm.ModelSpec{key: {
				Model: model, Variant: route.Variant, Effort: route.Effort, Steps: route.Steps, BashAllowAdd: route.BashAllowAdd,
			}},
		}
		if _, err := agentfm.Render(key, data, "opencode", ctx); err != nil {
			return fmt.Errorf("subagents.%s: %w", entry.Name, err)
		}
	}
	return nil
}

// refreshExternalRenders recomputes the rendered external subagents and hands
// them to every adapter.
func (e *Engine) refreshExternalRenders() error {
	rendered, err := e.renderExternalSubagents()
	if err != nil {
		return err
	}
	e.renderedExternal = rendered
	type renderedSetter interface {
		SetRenderedSubagents(string, map[string][]byte)
	}
	for _, a := range e.Adapters {
		if s, ok := a.(renderedSetter); ok {
			s.SetRenderedSubagents(e.renderedSubagentDir(), rendered)
		}
	}
	for _, t := range e.RepoTargets {
		if s, ok := t.Adapter.(renderedSetter); ok {
			s.SetRenderedSubagents(e.renderedSubagentDir(), rendered)
		}
	}
	return nil
}

// writeRenderedSubagents publishes the rendered files adapters link to and
// removes renders no declaration needs any more.
func (e *Engine) writeRenderedSubagents() error {
	dir := e.renderedSubagentDir()
	if len(e.renderedExternal) == 0 && !fsutil.FileExists(dir) {
		return nil
	}
	if err := fsutil.RequireRealParents(e.StateDir, dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	want := map[string]bool{}
	for name, data := range e.renderedExternal {
		file := name + ".opencode.md"
		want[file] = true
		path := filepath.Join(dir, file)
		if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, data) {
			continue
		}
		if err := fsutil.WriteControlPlaneWithin(e.StateDir, path, data, 0o644); err != nil {
			return fmt.Errorf("rendered subagent %s: %w", name, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !want[entry.Name()] {
			if err := os.Remove(filepath.Join(dir, entry.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}
