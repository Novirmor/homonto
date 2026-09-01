// Package config defines the homonto.toml schema and its load/migrate/validate
// pipeline. The schema types and their immediate accessors live in this file;
// decode+migrate+normalize+Load live in load.go, validation in validate.go, and
// framework expansion in expand.go. Splitting the formerly 1300-line god file
// keeps each concern under 350 lines without changing any behavior.
package config

import (
	"slices"
	"sort"
	"strings"
)

// MCP is a declared MCP server. Env values may hold unresolved ${...} tokens.
type MCP struct {
	Command []string          `toml:"command"`
	Env     map[string]string `toml:"env"`
	Targets []string          `toml:"targets"`
	// Scope selects where the server projects: "user" (default) → the global
	// tool config; "project" → the project-level config the tool merges over
	// it (OpenCode <repo>/opencode.jsonc), so a repository's servers don't
	// run in every other session.
	Scope string `toml:"scope"`
}

// ScopeOrDefault returns the scope, defaulting to user (the historical
// always-global projection) when unset.
func (m MCP) ScopeOrDefault() string {
	if m.Scope == "" {
		return "user"
	}
	return m.Scope
}

// TargetsOrAll returns the explicit targets, or every tool when none are set
// — OpenCode, the only adapter since v0.13.0.
func (m MCP) TargetsOrAll() []string {
	if len(m.Targets) == 0 {
		return []string{"opencode"}
	}
	return m.Targets
}

// Resource is the common shape of every declarable managed resource: a source
// string (builtin:|local:|remote:), an install scope, optional target-tool
// restriction, and an optional content digest (required for remote: sources).
// Embedded by MCP, Skill, Command, Subagent, Agent, and Framework so the
// loader's source/scope/target validation runs uniformly.
type Resource struct {
	Source  string   `toml:"source"`
	Scope   string   `toml:"scope"`
	Targets []string `toml:"targets"`
	// Digest is the sha256 content pin required when Source is a remote: source.
	Digest string `toml:"digest"`
}

// TargetsOrAll returns the explicit targets, or every tool when none are set
// — OpenCode, the only adapter since v0.13.0.
func (r Resource) TargetsOrAll() []string {
	if len(r.Targets) == 0 {
		return []string{"opencode"}
	}
	return r.Targets
}

// NamedResource pairs a Resource with the name it was declared under, plus
// projection-mode metadata that only applies to subagents. The engine and
// adapters iterate []NamedResource so each entry carries its declarative key.
type NamedResource struct {
	Name     string
	Resource Resource
	// Mode is the subagent projection mode ("link"|"copy"); empty means link and
	// is the only value skills/commands ever carry. It lets the adapters route a
	// copy-mode subagent to the content-file path instead of the symlink path.
	Mode string
}

// Subagent is a declarative agent projected by `apply` (distinct from the shared
// Resource so it can carry lifecycle fields the reconciliation adds without
// affecting skills/commands). `mode` selects link (symlink, today's behavior) or
// copy (an editable, versioned, mergeable install — landing incrementally);
// `version` is informational until pinning is wired.
type Subagent struct {
	Source  string   `toml:"source"`
	Scope   string   `toml:"scope"`
	Targets []string `toml:"targets"`
	Mode    string   `toml:"mode"`
	Version string   `toml:"version"`
	// Digest is the sha256 content pin ("sha256:<hex>") required when Source is a
	// remote: source and unused otherwise.
	Digest string `toml:"digest"`
	// Claude is the post-removal detector for [subagents.<name>.claude] blocks:
	// Claude Code support was removed in v0.13.0, so any non-empty value is
	// rejected at load naming the subagent. OpenCode is the live per-tool
	// model override for THIS subagent, declared as
	// [subagents.<name>.opencode]; a declared subagent must set a non-empty
	// model for every tool it is enabled for, and effort and variant are
	// optional and merge field-by-field at render.
	Claude   ModelRoute `toml:"claude"`
	OpenCode ModelRoute `toml:"opencode"`
}

// ModelOverrideFor returns this subagent's override for tool.
func (s Subagent) ModelOverrideFor(tool string) ModelRoute {
	switch tool {
	case "opencode":
		return s.OpenCode
	}
	return ModelRoute{}
}

// IsTuneOnly reports whether this entry only tunes an agent's models rather than
// declaring one: no source, but at least one per-tool model block.
//
// It exists because a framework's subagents may not be re-declared explicitly
// (that collision is an error), which would otherwise leave no way at all to
// retune the model of an agent you installed via [frameworks.*] — the main
// reason to reach for an override. So
//
//	[subagents.onto-skeptic.opencode]
//	effort = "max"
//
// with no source is read as "tune onto-skeptic", not "declare it": it projects
// nothing, and never collides with the framework that owns the agent.
func (s Subagent) IsTuneOnly() bool {
	if strings.TrimSpace(s.Source) != "" {
		return false
	}
	return s.OpenCode != ModelRoute{}
}

// SubagentCatalogName returns the builtin catalog name a source resolves to.
// Only builtin: sources have one — local:/remote: content is not catalog-keyed.
func SubagentCatalogName(source string) (string, bool) {
	return strings.CutPrefix(source, "builtin:")
}

// TargetsOrAll returns the explicit targets, or every tool when none are set
// — OpenCode, the only adapter since v0.13.0.
func (s Subagent) TargetsOrAll() []string {
	if len(s.Targets) == 0 {
		return []string{"opencode"}
	}
	return s.Targets
}

// ScopeOrDefault returns the scope, defaulting to project when unset.
func (s Subagent) ScopeOrDefault() string {
	if s.Scope == "" {
		return "project"
	}
	return s.Scope
}

// ModeOrDefault returns the projection mode, defaulting to link (symlink).
func (s Subagent) ModeOrDefault() string {
	if s.Mode == "" {
		return "link"
	}
	return s.Mode
}

// asResource projects the subagent onto the shared Resource shape used by the
// link-based projection pipeline (the only path implemented today).
func (s Subagent) asResource() Resource {
	return Resource{Source: s.Source, Scope: s.ScopeOrDefault(), Targets: s.Targets, Digest: s.Digest}
}

// Agent is a v2 lifecycle-managed agent (distinct from the v1 [subagents]
// symlink Resource): it carries version + mode for update/migration later.
type Agent struct {
	Source  string   `toml:"source"`  // builtin:<name> | local:<name>
	Version string   `toml:"version"` // optional; empty = unpinned
	Targets []string `toml:"targets"` // optional; empty = every tool (OpenCode)
	Mode    string   `toml:"mode"`    // optional; copy | link (empty = link)
}

// TargetsOrAll returns the explicit targets, or every tool when none are set
// — OpenCode, the only adapter since v0.13.0.
func (a Agent) TargetsOrAll() []string {
	if len(a.Targets) == 0 {
		return []string{"opencode"}
	}
	return a.Targets
}

// ModeOrDefault returns the lifecycle mode, defaulting to link when unset.
func (a Agent) ModeOrDefault() string {
	if a.Mode == "" {
		return "link"
	}
	return a.Mode
}

// ModelRoute is one tool's model binding for a subagent: which model id to
// stamp, an optional reasoning effort, and an optional variant tag the agentfm
// renderer uses to pick a per-tool file suffix. Declared under a
// [subagents.<name>.<tool>] block; the model field is required at load when the
// subagent is installed for that tool.
type ModelRoute struct {
	Model   string `toml:"model"`
	Effort  string `toml:"effort"`
	Variant string `toml:"variant"`
}

// Plugin is one declared plugin. Source is the tool-native identifier: the
// npm package / local plugin path placed in the opencode `plugin` array.
type Plugin struct {
	Source  string         `toml:"source"`
	Enabled *bool          `toml:"enabled"` // nil == true (default enabled)
	Config  map[string]any `toml:"config"`  // non-sensitive per-plugin options
}

// IsEnabled reports whether the plugin is enabled (default true when omitted).
func (p Plugin) IsEnabled() bool { return p.Enabled == nil || *p.Enabled }

// Plugins groups per-tool plugin declarations. Claude is the post-removal
// detector for [plugins.claude.<name>]: Claude Code support was removed in
// v0.13.0, so any non-empty map is rejected at load. OpenCode is the live
// [plugins.opencode.<name>] surface.
type Plugins struct {
	Claude   map[string]Plugin `toml:"claude"`
	OpenCode map[string]Plugin `toml:"opencode"`
}

// Settings groups per-tool arbitrary managed settings keys. Values are
// projected through the structured-document contract into the tool's native
// settings file. Claude is the post-removal detector for [settings.claude]:
// Claude Code support was removed in v0.13.0, so any non-empty map is
// rejected at load. OpenCode is the live [settings.opencode] surface.
type Settings struct {
	Claude   map[string]any `toml:"claude"`
	OpenCode map[string]any `toml:"opencode"`
}

// TUI declares per-tool TUI settings projected to a tool-native TUI file. Only
// OpenCode has a separate TUI file (~/.config/opencode/tui.json).
type TUI struct {
	OpenCode map[string]any `toml:"opencode"`
}

// Marketplace is the post-removal detector shape for
// [marketplaces.claude.<name>] declarations: Claude Code support was removed
// in v0.13.0, so any non-empty value is rejected at load. The locator fields
// survive only so the TOML decoder can populate the detector.
type Marketplace struct {
	Source     string `toml:"source"`      // github | url | git-subdir | directory
	Repo       string `toml:"repo"`        // github
	URL        string `toml:"url"`         // url, git-subdir
	Path       string `toml:"path"`        // git-subdir, directory
	AutoUpdate *bool  `toml:"auto_update"` // optional
}

// Marketplaces detects [marketplaces.claude] declarations — a Claude-only
// feature, removed with the adapter in v0.13.0.
type Marketplaces struct {
	Claude map[string]Marketplace `toml:"claude"`
}

// CurrentConfigSchemaVersion is the homonto.toml schema version this binary
// supports. A config declaring a higher version is rejected fail-closed at load.
const CurrentConfigSchemaVersion = 1

// Config is the tool-agnostic desired state parsed from homonto.toml.
type Config struct {
	// SchemaVersion is the homonto.toml format version. Absent/0 means a legacy
	// (pre-versioning) config and is treated as the current version; a value
	// greater than CurrentConfigSchemaVersion is rejected fail-closed at load so
	// an older binary never silently mis-applies a newer config.
	SchemaVersion int                 `toml:"schema_version,omitempty"`
	MCPs          map[string]MCP      `toml:"mcps"`
	Frameworks    map[string]Resource `toml:"frameworks"`
	Skills        map[string]Resource `toml:"skills"`
	Commands      map[string]Resource `toml:"commands"`
	Subagents     map[string]Subagent `toml:"subagents"`
	// Models captures any legacy [models.<tool>.<tier>] block so Load can
	// detect and reject it. The field must be exported for pelletier/go-toml/v2
	// to populate it; the private type is not an access restriction, since
	// callers can read its exported fields.
	Models modelsTable `toml:"models"`
	// Tooling captures the raw [tooling] table (shell-proxy and
	// code-intelligence provider selection). It is a raw map so an unknown key
	// survives decode and can be rejected by name; read it through
	// ResolvedTooling, which applies the "none" defaults.
	Tooling      toolingTable     `toml:"tooling"`
	Plugins      Plugins          `toml:"plugins"`
	Settings     Settings         `toml:"settings"`
	TUI          TUI              `toml:"tui"`
	Marketplaces Marketplaces     `toml:"marketplaces"`
	Agents       map[string]Agent `toml:"agents"`

	// baseDir is the absolute directory of the homonto.toml this config was
	// loaded from. It resolves a [frameworks.X] source="local:<path>" framework
	// root relative to the config file, so local paths need not be threaded
	// through the Expanded* method signatures. Empty for a config not built via
	// Load (e.g. decode in tests): local frameworks then resolve relative to cwd.
	baseDir string

	// remoteFrameworkDirs maps a [frameworks.X] source="remote:<url>" name to the
	// verified cache dir the engine resolved through the remote trust pipeline
	// (fetch → verify → digest-pin → revocation). The engine injects it via
	// SetRemoteFrameworkDirs after resolution so FrameworkCatalog overlays a
	// remote framework root exactly like a local:<path> one. Nil for a config with
	// no remote frameworks (or before resolution): the builtin path is unchanged.
	remoteFrameworkDirs map[string]string
}

// SetRemoteFrameworkDirs injects the verified cache dirs the engine resolved for
// this config's remote frameworks (name → cache dir). FrameworkCatalog then
// overlays each as a framework root keyed by its config name, so both expansion
// (Plan) and catalog materialization (apply) see the remote framework's
// resources projected as builtin:<name>, identical to a local framework.
func (c *Config) SetRemoteFrameworkDirs(dirs map[string]string) {
	c.remoteFrameworkDirs = dirs
}

// SkillEntriesForTool returns the declared skills whose targets include tool
// (or all tools when targets is unset), as NamedResources sorted by name.
func (c *Config) SkillEntriesForTool(tool string) []NamedResource {
	return entriesForTool(c.Skills, tool)
}

// CommandEntriesForTool returns the declared commands whose targets include
// tool (or all tools when targets is unset), as NamedResources sorted by name.
func (c *Config) CommandEntriesForTool(tool string) []NamedResource {
	return entriesForTool(c.Commands, tool)
}

// SubagentEntriesForTool returns the declared subagents whose targets include
// tool (or all tools when targets is unset), as NamedResources sorted by name.
// Tune-only entries (no agent of their own) are excluded: they retune a
// framework-owned subagent, not project one.
func (c *Config) SubagentEntriesForTool(tool string) []NamedResource {
	var out []NamedResource
	for name, s := range c.Subagents {
		// A tune-only entry declares no agent — it retunes one a framework owns,
		// so it must not project, and must not count as an explicit declaration
		// that collides with that framework.
		if s.IsTuneOnly() {
			continue
		}
		if slices.Contains(s.TargetsOrAll(), tool) {
			out = append(out, NamedResource{Name: name, Resource: s.asResource(), Mode: s.ModeOrDefault()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// modelsTable is the post-removal detector shape for legacy [models.<tool>.<tier>]
// blocks. Load rejects any non-empty value naming the offending key, so a
// config edited for the old tier system gets a clear error instead of silently
// dropping its model declarations.
type modelsTable struct {
	Claude   map[string]ModelRoute `toml:"claude"`
	OpenCode map[string]ModelRoute `toml:"opencode"`
}
