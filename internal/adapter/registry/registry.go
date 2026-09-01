// Package registry is the tool-id-keyed adapter registry: the engine sources its
// tool adapters from here instead of a hardcoded slice, so adding a built-in
// adapter is a single registration in Builtins() rather than an edit to the
// engine's composition root. Deps bundles the construction inputs an adapter may
// need; a Factory builds one adapter from them.
package registry

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/adapter/opencode"
)

// Deps are the construction inputs shared across adapters. An adapter's Factory
// uses whichever fields it needs (Codex, for instance, uses only Home).
type Deps struct {
	Home               string
	ContentDir         string
	ProjectRoot        string
	CatalogDir         string
	CommandCatalogDir  string
	SubagentCatalogDir string
	RemoteSubagentDir  string
}

// Factory builds one adapter from the shared dependencies.
type Factory func(Deps) adapter.Adapter

// Registry holds adapter factories keyed by tool id, preserving registration
// order so the built adapter list is deterministic.
type Registry struct {
	order     []string
	factories map[string]Factory
}

// New returns an empty registry.
func New() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register adds a factory under a tool id. It panics on a duplicate id — a
// double registration is a programming error, caught at startup, not silently
// shadowed.
func (r *Registry) Register(id string, f Factory) {
	if _, dup := r.factories[id]; dup {
		panic(fmt.Sprintf("registry: adapter %q already registered", id))
	}
	r.factories[id] = f
	r.order = append(r.order, id)
}

// Build constructs every registered adapter from d, in registration order.
func (r *Registry) Build(d Deps) []adapter.Adapter {
	out := make([]adapter.Adapter, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.factories[id](d))
	}
	return out
}

// Builtins returns a fresh registry with the built-in adapters registered. This
// is the single place built-in adapters are wired: adding one is one Register
// line here. A fresh registry per call keeps it free of global mutable state.
// OpenCode is the only adapter since v0.13.0 (Claude Code and the codex pilot
// were removed — see docs/ROADMAP.md).
func Builtins() *Registry {
	r := New()
	r.Register("opencode", func(d Deps) adapter.Adapter {
		return opencode.New(d.Home, d.ContentDir).
			WithProjectRoot(d.ProjectRoot).
			WithCatalogRoot(d.CatalogDir).
			WithCommandCatalogRoot(d.CommandCatalogDir).
			WithSubagentCatalogRoot(d.SubagentCatalogDir).
			WithRemoteSubagentRoot(d.RemoteSubagentDir)
	})
	return r
}
