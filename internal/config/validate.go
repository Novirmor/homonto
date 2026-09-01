package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/remote"
)

// validate rejects a config that would project nothing or corrupt a tool file.
// One section per helper, run in a fixed order so which error wins is
// deterministic even for a config with several problems.
func validate(c *Config) error {
	// Legacy [models.<tool>.<tier>] tables were removed (D2 — no more tier
	// routing). The TOML decoder populates the Models field on Config as a
	// detector (pelletier/go-toml/v2 cannot write to unexported fields, so the
	// field is exported but its type private); reject any non-empty value
	// naming the offender, so a config edited for the tier system fails loudly
	// instead of silently losing its model declarations.
	if err := rejectLegacyModels(c.Models); err != nil {
		return err
	}
	if err := validateTooling(c.Tooling); err != nil {
		return err
	}
	if err := validateResources("skills", c.Skills); err != nil {
		return err
	}
	if err := validateResources("commands", c.Commands); err != nil {
		return err
	}
	if err := validateFrameworks(c); err != nil {
		return err
	}
	if err := validateSubagents(c.Subagents); err != nil {
		return err
	}
	if err := validateRemovedTools(c); err != nil {
		return err
	}
	if err := validateRepos(c); err != nil {
		return err
	}
	if err := validateRepoTargets(c); err != nil {
		return err
	}
	if err := validateModels(c); err != nil {
		return err
	}
	if err := validateMCPs(c); err != nil {
		return err
	}
	if err := validatePlugins(c); err != nil {
		return err
	}
	return validateSettingsAndTUI(c)
}

// validateFrameworks checks the framework source rule and the onto/to
// exclusivity.
//
// Frameworks have their own source rule: builtin:<name> (expanded from the
// embedded catalog) or local:<path> (a local framework root). Every other
// source expands nothing and is rejected loudly (F35).
//
// onto and to are an exclusive choice per repository: enterprise tooling
// vs. simple development. Their skills give conflicting process guidance
// and their binaries each expect to own the workflow, so declaring both
// is a config error, not a projection concern.
func validateFrameworks(c *Config) error {
	if err := validateFrameworkResources(c.Frameworks); err != nil {
		return err
	}
	if _, hasOnto := c.Frameworks["onto"]; hasOnto {
		if _, hasTo := c.Frameworks["to"]; hasTo {
			return fmt.Errorf("parse config: [frameworks.onto] and [frameworks.to] are mutually exclusive; pick one workflow framework per repository (onto for evidence-gated enterprise changes, to for simple development)")
		}
	}
	return nil
}

// validateRepos checks the shape of every [repos] entry: a plain name usable
// as an identifier (later stages key per-repo state and plan labels on it),
// a non-empty path, and no two names sharing one raw path. Existence and
// git-worktree checks need the filesystem and run in Load, where the config
// file's directory (the base for relative paths) is known.
func validateRepos(c *Config) error {
	names := sortedStringKeys(c.Repos)
	for _, name := range names {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || name != filepath.Base(name) {
			return fmt.Errorf("parse config: repos entry %q is not a plain name", name)
		}
		if err := validateKey("repos", name); err != nil {
			return err
		}
		p := strings.TrimSpace(c.Repos[name])
		if p == "" {
			return fmt.Errorf("parse config: repos.%s has an empty path", name)
		}
		c.Repos[name] = p
	}
	seen := map[string]string{} // raw path -> first name
	for _, name := range names {
		p := c.Repos[name]
		if prev, dup := seen[p]; dup {
			return fmt.Errorf("parse config: repos.%s and repos.%s share path %q; one entry per repository", name, prev, p)
		}
		seen[p] = name
	}
	return nil
}

// validateRepoTargets checks every repo = "<name>" declaration (ADR 0024
// stage 2): the name must be declared under [repos], the resource must be
// project-scoped (a repo-targeted user-scope resource would mean one repo's
// name in every session's global config — exactly what scoping prevents), and
// frameworks do not take repo at all (their projection is config-repo only).
func validateRepoTargets(c *Config) error {
	declared := func(name string) bool { _, ok := c.Repos[name]; return ok }
	check := func(label, repo, scope string, isProject bool) error {
		if repo == "" {
			return nil
		}
		if !declared(repo) {
			return fmt.Errorf("parse config: %s repo %q is not declared under [repos]", label, repo)
		}
		if !isProject {
			return fmt.Errorf("parse config: %s repo %q requires scope = \"project\" — a repo-targeted resource projects into that repository's .opencode/, which only project scope reaches", label, repo)
		}
		return nil
	}
	// Deterministic walks so a config failing several rules names the same
	// offender every run.
	for _, name := range sortedResourceNames(c.Skills) {
		r := c.Skills[name]
		if err := check("skills."+name, r.Repo, r.Scope, r.Scope == "project"); err != nil {
			return err
		}
	}
	for _, name := range sortedResourceNames(c.Commands) {
		r := c.Commands[name]
		if err := check("commands."+name, r.Repo, r.Scope, r.Scope == "project"); err != nil {
			return err
		}
	}
	for _, name := range sortedSubagentNames(c) {
		s := c.Subagents[name]
		if err := check("subagents."+name, s.Repo, s.Scope, s.ScopeOrDefault() == "project"); err != nil {
			return err
		}
	}
	for _, name := range sortedMCPNames(c) {
		m := c.MCPs[name]
		if err := check("mcps."+name, m.Repo, m.Scope, m.ScopeOrDefault() == "project"); err != nil {
			return err
		}
	}
	for _, name := range sortedResourceNames(c.Frameworks) {
		if r := c.Frameworks[name]; r.Repo != "" {
			return fmt.Errorf("parse config: frameworks.%s repo %q — frameworks project into the config repository only; declare their skills/commands/subagents per repo instead", name, r.Repo)
		}
	}
	return nil
}

// sortedResourceNames returns the map's keys in deterministic order.
func sortedResourceNames(m map[string]Resource) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedMCPNames returns the map's keys in deterministic order.
func sortedMCPNames(c *Config) []string {
	out := make([]string, 0, len(c.MCPs))
	for k := range c.MCPs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// validateRemovedTools fails closed on every declaration naming a tool whose
// adapter was removed in v0.13.0 (Claude Code and the codex MCP pilot), with
// an error that names the offending key and says what to do — the same
// fail-closed precedent as the v0.3.0 framework removal. The schema keeps
// detector fields so the TOML decoder can capture these declarations; without
// them a stale config would fail with an opaque decode error instead of a
// migration message.
func validateRemovedTools(c *Config) error {
	if len(c.Settings.Claude) > 0 {
		keys := sortedKeys(c.Settings.Claude)
		return fmt.Errorf("parse config: settings.claude.%s — Claude Code support was removed in v0.13.0; OpenCode is the only adapter, move the key to [settings.opencode] or delete it", keys[0])
	}
	if len(c.Plugins.Claude) > 0 {
		names := sortedPluginNames(c.Plugins.Claude)
		return fmt.Errorf("parse config: plugins.claude.%s — Claude Code support was removed in v0.13.0; delete the block", names[0])
	}
	if len(c.Marketplaces.Claude) > 0 {
		names := sortedMarketplaceNames(c.Marketplaces.Claude)
		return fmt.Errorf("parse config: marketplaces.claude.%s — marketplaces were a Claude-only feature removed with the adapter in v0.13.0; delete the block", names[0])
	}
	for _, name := range sortedSubagentNames(c) {
		if sa := c.Subagents[name]; sa.Claude != (ModelRoute{}) {
			return fmt.Errorf("parse config: subagents.%s.claude — Claude Code support was removed in v0.13.0; move the model block to [subagents.%s.opencode] or delete it", name, name)
		}
	}
	return nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedStringKeys returns the map's keys in deterministic order, so a config
// that fails validation names the same offender every run.
func sortedStringKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedPluginNames(m map[string]Plugin) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMarketplaceNames(m map[string]Marketplace) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// validateMCPs rejects MCP entries that could not project cleanly: names
// unusable as JSON object keys, servers with no command, targets naming no
// adapter (a silent typo), bad scopes, and the codex project-scope
// combination the pilot cannot honor.
func validateMCPs(c *Config) error {
	// Every other name becomes a key written into a tool's JSON file. sjson
	// treats index-like segments ("0", "-1") as array positions, silently
	// turning the containing object into a JSON ARRAY; empty names address
	// nothing. Reject both up front with the offending entry named.
	for name, m := range c.MCPs {
		if err := validateKey("mcps", name); err != nil {
			return err
		}
		// An MCP with no command cannot project — both adapters would skip it,
		// so a declared server would silently do nothing. Fail fast instead.
		if len(m.Command) == 0 {
			return fmt.Errorf("parse config: mcps entry %q has no command; an MCP server needs a command to run", name)
		}
		// A target that names no adapter means the MCP is projected nowhere —
		// a silent typo. OpenCode is the only adapter since v0.13.0; a target
		// naming a removed tool gets its own migration message.
		for _, target := range m.Targets {
			if target == "claude" {
				return fmt.Errorf("parse config: mcps entry %q targets \"claude\", but Claude Code support was removed in v0.13.0; remove the target or delete the entry", name)
			}
			if target == "codex" {
				return fmt.Errorf("parse config: mcps entry %q targets \"codex\", but the codex pilot was removed in v0.13.0; remove the target or delete the entry", name)
			}
			if !isMCPTarget(target) {
				return fmt.Errorf("parse config: mcps entry %q targets unknown tool %q; the only valid target is \"opencode\"", name, target)
			}
		}
		switch m.Scope {
		case "", "user", "project":
			// ok
		default:
			return fmt.Errorf("parse config: mcps entry %q scope %q is invalid; valid values are \"user\" and \"project\"", name, m.Scope)
		}
	}
	return nil
}

// validatePlugins rejects plugin declarations that would collide or silently
// project nothing, for both [plugins.claude] and [plugins.opencode].
func validatePlugins(c *Config) error {
	for _, tool := range []struct {
		name string
		m    map[string]Plugin
	}{
		{"plugins.opencode", c.Plugins.OpenCode},
	} {
		// Both adapters project keyed by source, so two decl names sharing a
		// source would collide on one projected key with last-writer-wins over
		// random map iteration order — a non-deterministic plan. Reject it.
		seenSource := map[string]string{} // source -> first decl name
		for declName, pl := range tool.m {
			if err := validateKey(tool.name, declName); err != nil {
				return err
			}
			// A plugin with no source projects nothing (no enabledPlugins key /
			// no plugin-array value), so a declared plugin would silently do
			// nothing. Fail fast naming the plugin.
			if strings.TrimSpace(pl.Source) == "" {
				return fmt.Errorf("parse config: %s plugin %q has an empty source", tool.name, declName)
			}
			// OpenCode plugins are a plain array on disk with no per-plugin
			// config slot, so a declared config could project nowhere. Reject it.
			if tool.name == "plugins.opencode" && len(pl.Config) > 0 {
				return fmt.Errorf("parse config: %s plugin %q declares config, but OpenCode has no per-plugin config on disk (its plugins are a plain array); remove config", tool.name, declName)
			}
			if prev, dup := seenSource[pl.Source]; dup {
				return fmt.Errorf("parse config: %s plugins %q and %q share source %q", tool.name, prev, declName, pl.Source)
			}
			seenSource[pl.Source] = declName
		}
	}
	return nil
}

// validateMarketplaces checks that each marketplace declaration carries the
// locator fields its source kind requires.
// validateSettingsAndTUI rejects settings keys that homonto itself manages in
// the same tool file — they would collide with its own writes: opencode
// projects MCPs and plugins as the `mcp` and `plugin` structures in
// opencode.jsonc.
func validateSettingsAndTUI(c *Config) error {
	for k := range c.Settings.OpenCode {
		if err := validateKey("settings.opencode", k); err != nil {
			return err
		}
		if k == "mcp" || k == "plugin" {
			return fmt.Errorf("parse config: settings.opencode key %q is reserved (homonto manages %s there); rename it", k, k)
		}
	}
	// `model_variant` selects the model's variant tier (medium, high, xhigh,
	// …) the same way `model` selects the model: declared as a companion
	// key, combined into the `provider/model#variant` id at projection. It
	// needs its model, must be a plain token, and cannot double up on an id
	// that already carries a suffix.
	if mv, ok := c.Settings.OpenCode["model_variant"]; ok {
		variant, isStr := mv.(string)
		model, modelOK := c.Settings.OpenCode["model"].(string)
		switch {
		case !isStr || strings.TrimSpace(variant) == "":
			return fmt.Errorf("parse config: settings.opencode model_variant must be a non-empty string (e.g. \"high\", \"xhigh\")")
		case !modelOK || strings.TrimSpace(model) == "":
			return fmt.Errorf("parse config: settings.opencode model_variant is set but model is not — a variant needs its model; declare model too")
		case strings.ContainsAny(variant, " \t") || strings.ContainsAny(variant, "#"):
			return fmt.Errorf("parse config: settings.opencode model_variant %q is invalid; a variant is a plain token appended to the model id (e.g. \"high\")", variant)
		case strings.Contains(model, "#"):
			return fmt.Errorf("parse config: settings.opencode model %q already carries a #variant suffix; drop model_variant", model)
		}
	}
	// [tui.opencode] keys project into a second managed file (tui.json). Reject
	// index-like/empty names for the same JSON-array-corruption reason as
	// [settings.opencode].
	for k := range c.TUI.OpenCode {
		if err := validateKey("tui.opencode", k); err != nil {
			return err
		}
	}
	return nil
}

// Load reads, parses, and validates a homonto.toml file into a Config. It runs
// the decode → migrate → normalize → validate pipeline, failing closed on the
// first malformed or unsupported declaration.
// validateKey rejects names unusable as literal JSON object keys: empty, or
// index-like (all digits, or "-" followed by digits — sjson array semantics).
func validateKey(kind, name string) error {
	if name == "" {
		return fmt.Errorf("parse config: %s entry %q is empty", kind, name)
	}
	if indexLike(name) {
		return fmt.Errorf("parse config: %s entry %q would be treated as a JSON array index and corrupt the target file; rename it", kind, name)
	}
	return nil
}

// validateResources checks name, scope, source, and targets for every declared
// resource of a given kind (frameworks, skills, commands, subagents).
func validateResources(kind string, resources map[string]Resource) error {
	for name, r := range resources {
		if err := validateResourceName(kind, name); err != nil {
			return err
		}
		label := kind + "." + name
		switch r.Scope {
		case "user", "project":
			// ok
		case "":
			return fmt.Errorf("parse config: %s is missing required scope; valid values are \"user\" and \"project\"", label)
		default:
			return fmt.Errorf("parse config: %s scope %q is invalid; valid values are \"user\" and \"project\"", label, r.Scope)
		}
		if err := validateSource(label, r.Source, r.Digest, false); err != nil {
			return err
		}
		if err := validateLocalPlainName(label, r.Source); err != nil {
			return err
		}
		for _, target := range r.Targets {
			if err := validateTarget(label, target); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateFrameworkResources validates [frameworks.X] entries. A framework
// source must be builtin:<name> (expanded from the embedded catalog),
// local:<path> (a local framework root resolved relative to the config dir), or
// remote:<url> (a framework root fetched through the trust pipeline, which
// REQUIRES a sha256 digest pin). Unlike skills/commands, a local FRAMEWORK
// source MAY carry path components, so the plain-name guard is deliberately not
// applied here. A bare name or a typo expands nothing and is rejected loudly
// (F35); a digest on a builtin/local source is a no-op and rejected.
func validateFrameworkResources(resources map[string]Resource) error {
	for name, r := range resources {
		if err := validateResourceName("frameworks", name); err != nil {
			return err
		}
		label := "frameworks." + name
		switch r.Scope {
		case "user", "project":
			// ok
		case "":
			return fmt.Errorf("parse config: %s is missing required scope; valid values are \"user\" and \"project\"", label)
		default:
			return fmt.Errorf("parse config: %s scope %q is invalid; valid values are \"user\" and \"project\"", label, r.Scope)
		}
		// A remote: framework installs through the same trust pipeline as a remote
		// subagent, so it REQUIRES a valid sha256 digest pin (parsed here so a
		// malformed remote framework fails at load, mirroring remote subagents).
		// builtin:/local: keep their existing rule: a digest on them is a no-op and
		// rejected.
		if remote.IsRemoteSource(r.Source) {
			if _, err := remote.ParseRemoteSource(r.Source); err != nil {
				return fmt.Errorf("parse config: %s: %w", label, err)
			}
			if r.Digest == "" {
				return fmt.Errorf("parse config: %s remote source %q requires a digest = \"sha256:<hex>\" pin", label, r.Source)
			}
			if _, err := remote.ParseDigest(r.Digest); err != nil {
				return fmt.Errorf("parse config: %s: %w", label, err)
			}
			for _, target := range r.Targets {
				if err := validateTarget(label, target); err != nil {
					return err
				}
			}
			continue
		}
		if r.Digest != "" {
			return fmt.Errorf("parse config: %s digest is only valid on a remote: source", label)
		}
		builtinOK := strings.HasPrefix(r.Source, "builtin:") && strings.TrimPrefix(r.Source, "builtin:") != ""
		localOK := strings.HasPrefix(r.Source, "local:") && strings.TrimPrefix(r.Source, "local:") != ""
		if !builtinOK && !localOK {
			return fmt.Errorf("parse config: %s source %q must be a builtin:<name>, local:<path>, or remote:<url> source (another source would expand nothing)", label, r.Source)
		}
		for _, target := range r.Targets {
			if err := validateTarget(label, target); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateLocalPlainName rejects a local: source that is not a plain name (no
// `.`/`..`/path separators), so it can never resolve/link/materialize a file
// outside the provider root. It is a no-op for non-local sources. Shared by
// validateResources (skills/commands) and validateSubagents so the two paths
// cannot drift.
func validateLocalPlainName(label, source string) error {
	src, ok := strings.CutPrefix(source, "local:")
	if !ok {
		return nil
	}
	if src == "" || src == "." || src == ".." || strings.ContainsAny(src, `/\`) || src != filepath.Base(src) {
		return fmt.Errorf("parse config: %s local source %q must be a plain name (no path components)", label, source)
	}
	return nil
}

// validateSubagents checks each [subagents.<name>]: a valid name, a builtin/local
// source, known targets, a user|project scope (already normalized to project when
// omitted), and a mode of link. copy is reserved for the forthcoming copy-mode
// projection and rejected until that lands, so the field is never a silent no-op.
func validateSubagents(subagents map[string]Subagent) error {
	for name, s := range subagents {
		if err := validateResourceName("subagents", name); err != nil {
			return err
		}
		label := "subagents." + name
		// A tune-only entry ([subagents.<name>.<tool>] with no source) retunes an
		// agent a framework already declared, so the declaration rules — source,
		// scope, local-name safety — are not its to satisfy. Its model blocks are
		// still validated, by validateSubagentOverrides.
		if s.IsTuneOnly() {
			continue
		}
		switch s.Scope {
		case "user", "project":
			// ok (empty was normalized to project at load)
		default:
			return fmt.Errorf("parse config: %s scope %q is invalid; valid values are \"user\" and \"project\"", label, s.Scope)
		}
		if err := validateSource(label, s.Source, s.Digest, true); err != nil {
			return err
		}
		// A local: source is resolved to a file by name; reject a path-traversal
		// name so it cannot read/link outside the provider root.
		if err := validateLocalPlainName(label, s.Source); err != nil {
			return err
		}
		for _, target := range s.Targets {
			if err := validateTarget(label, target); err != nil {
				return err
			}
		}
		switch s.Mode {
		case "", "link", "copy":
			// ok — link projects a symlink, copy projects a managed content file
		default:
			return fmt.Errorf("parse config: %s mode %q is invalid; valid values are \"link\" and \"copy\"", label, s.Mode)
		}
	}
	return nil
}

// mcpTargetTools are valid MCP targets; resourceTargetTools are valid targets
// for skills/commands/subagents/frameworks. OpenCode is the only adapter
// since v0.13.0 — Claude Code and the codex pilot were removed, and their
// target values are rejected by validateTarget with a migration message
// rather than a bare "unknown tool".
var mcpTargetTools = []string{"opencode"}
var resourceTargetTools = []string{"opencode"}

func isMCPTarget(t string) bool      { return slices.Contains(mcpTargetTools, t) }
func isResourceTarget(t string) bool { return slices.Contains(resourceTargetTools, t) }

// validateTarget rejects one target value for a managed resource, naming the
// removed tools separately from an unknown one so a stale config gets a
// migration message instead of a typo report.
func validateTarget(label, target string) error {
	switch target {
	case "claude":
		return fmt.Errorf("parse config: %s targets \"claude\", but Claude Code support was removed in v0.13.0; remove the target", label)
	case "codex":
		return fmt.Errorf("parse config: %s targets \"codex\", but the codex pilot was removed in v0.13.0; remove the target", label)
	}
	if !isResourceTarget(target) {
		return fmt.Errorf("parse config: %s targets unknown tool %q; the only valid target is \"opencode\"", label, target)
	}
	return nil
}

func validateResourceName(kind, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) || name != filepath.Base(name) {
		return fmt.Errorf("parse config: %s entry %q is not a plain name", kind, name)
	}
	return validateKey(kind, name)
}

func validSource(source string) bool {
	for _, prefix := range []string{"builtin:", "local:"} {
		if strings.HasPrefix(source, prefix) && strings.TrimPrefix(source, prefix) != "" {
			return true
		}
	}
	return false
}

// validateSource accepts builtin:/local: sources unchanged, and a remote:
// source only when allowRemote is set (subagents only today), it parses, and it
// carries a well-formed sha256 digest pin. A non-remote source carrying a digest
// is rejected as unexpected so the field is never a silent no-op.
func validateSource(label, source, digest string, allowRemote bool) error {
	if remote.IsRemoteSource(source) {
		if !allowRemote {
			return fmt.Errorf("parse config: %s remote sources are only supported for subagents", label)
		}
		if _, err := remote.ParseRemoteSource(source); err != nil {
			return fmt.Errorf("parse config: %s: %w", label, err)
		}
		if digest == "" {
			return fmt.Errorf("parse config: %s remote source %q requires a digest = \"sha256:<hex>\" pin", label, source)
		}
		if _, err := remote.ParseDigest(digest); err != nil {
			return fmt.Errorf("parse config: %s: %w", label, err)
		}
		return nil
	}
	if digest != "" {
		return fmt.Errorf("parse config: %s digest is only valid on a remote: source", label)
	}
	if !validSource(source) {
		return fmt.Errorf("parse config: %s source %q is invalid; use builtin:<name>, local:<name>, or remote:<url>", label, source)
	}
	return nil
}

// variantToken rejects a variant that could not render as a model-id suffix
// (`provider/model#variant`): empty after trim, whitespace, or an embedded `#`
// (which would stack a second suffix or corrupt the id).
func variantToken(label, variant string) error {
	if strings.ContainsAny(variant, " \t") || strings.Contains(variant, "#") {
		return fmt.Errorf("parse config: %s variant %q is invalid; a variant is a plain token appended as #%s to the model id (e.g. \"high\", \"xhigh\")", label, variant, variant)
	}
	return nil
}

// validateModelSpec checks one model/variant/effort triple against what `tool`
// can actually express, naming label as the offender. `model` is required when
// requireModel is set (the per-subagent must-declare check passes true; the
// per-override walk passes false so it can flag malformed values without
// demanding a model field that an effort-only override wouldn't have).
func validateModelSpec(tool, label string, r ModelRoute, requireModel bool) error {
	model := strings.TrimSpace(r.Model)
	variant := strings.TrimSpace(r.Variant)
	effort := strings.TrimSpace(r.Effort)
	if requireModel && model == "" {
		return fmt.Errorf("parse config: %s model is required", label)
	}
	switch tool {
	case "opencode":
		if effort != "" {
			return fmt.Errorf("parse config: %s sets effort %q, but OpenCode has no effort setting — variants (medium, high, xhigh, …) are selected with variant, which renders as #%s on the model id", label, effort, "variant")
		}
		if variant != "" {
			return variantToken(label, variant)
		}
		if strings.Contains(model, "#") && variant != "" {
			return fmt.Errorf("parse config: %s model %q already carries a #variant suffix; drop the separate variant field", label, model)
		}
	}
	return nil
}

// validateModels ensures every declared subagent resolves an explicit per-tool
// model for every tool it is enabled for, and that every model/variant/effort
// value is one the target tool can actually express. A subagent that lacks a
// model for an enabled tool fails loading — there is no tier or role default
// to fall back on, so the agent would otherwise render with no model line at
// all.
//
// The must-declare check applies only to builtin: subagents: local:/remote:
// content is projected verbatim (never rendered through agentfm), so an
// override on those sources could never apply and is rejected separately by
// validateSubagentOverrides. The per-subagent override walk also runs here so
// both checks report a deterministic offender.
//
// The check covers BOTH explicit [subagents.<name>] declarations and the
// builtin subagents a framework expands. The explicit walk below iterates
// c.Subagents by config key; the framework-expanded walk iterates
// ExpandedSubagentEntriesForTool and resolves each expanded builtin's model
// the same way rendering does (catalog name → c.Subagents[<catalog-name>]
// override block). Without the second walk a config that installs a framework
// but omits the per-tool model blocks for its expanded agents would load
// clean and render agents with no model line — the silent default R1 forbids.
func validateModels(c *Config) error {
	for _, tool := range c.EnabledModelTools() {
		for _, name := range sortedSubagentNames(c) {
			sa := c.Subagents[name]
			// A tune-only entry declares no agent; its overrides (if any) are
			// checked below by validateSubagentOverrides. The must-declare
			// check applies only to subagents that actually project one.
			if sa.IsTuneOnly() {
				continue
			}
			// Only builtin: subagents are rendered through agentfm; local:
			// and remote: content is projected verbatim and has no model line
			// to stamp, so requiring an override would contradict the
			// override-on-non-builtin rejection below.
			if !isBuiltinSubagent(sa) {
				continue
			}
			if !slices.Contains(sa.TargetsOrAll(), tool) {
				continue
			}
			label := "subagents." + name + "." + tool
			if err := validateModelSpec(tool, label, sa.ModelOverrideFor(tool), true); err != nil {
				return err
			}
		}
		// Framework-expanded walk: cover builtin subagents a framework
		// installs that have no matching explicit [subagents.<name>]
		// declaration. The explicit walk above misses them (their config
		// keys are absent from c.Subagents), and without this check they
		// would render through agentfm with no model line.
		//
		// Resolve an expanded entry's model the same way rendering does
		// (engine.subagentRenderContext): iterate c.Subagents and key each
		// override by the entry's resolved catalog name, so an explicit
		// [subagents.x] source="builtin:architect" contributes the override
		// for catalog name "architect" (not "x"). An explicit declaration
		// (any non-tune-only entry whose source resolves to the catalog
		// name) targeting this tool was already checked above; only entries with
		// NO explicit declaration for this tool — or with only a tune-only retune
		// — fall through here.
		//
		// Tolerate expansion errors (e.g. a stale [frameworks.comet] whose
		// catalog was removed): the engine and doctor already report them
		// as warnings, the explicit walk above still catches must-declare
		// failures for explicit declarations, and propagating here would
		// regress `homonto doctor` on configs it was designed to tolerate.
		expanded, err := c.ExpandedSubagentEntriesForTool(tool)
		if err != nil {
			continue
		}
		routeByCat := map[string]ModelRoute{}
		declaredByCat := map[string]bool{}
		for _, name := range sortedSubagentNames(c) {
			sa := c.Subagents[name]
			var cat string
			if sa.IsTuneOnly() {
				// A tune-only entry names the catalog agent directly (its
				// retune target). Its override still applies, but it does
				// not count as an explicit declaration for the must-declare
				// check (which the explicit walk owns).
				cat = name
			} else {
				catName, ok := SubagentCatalogName(sa.Source)
				if !ok {
					continue
				}
				cat = catName
				// A declaration only covers an expanded agent for tools it
				// targets. Its override may still be a model source for either
				// rendered tool, matching engine.subagentRenderContext.
				if slices.Contains(sa.TargetsOrAll(), tool) {
					declaredByCat[cat] = true
				}
			}
			if r := sa.ModelOverrideFor(tool); r != (ModelRoute{}) {
				routeByCat[cat] = r
			}
		}
		for _, e := range expanded {
			cat, ok := SubagentCatalogName(e.Resource.Source)
			if !ok {
				continue // local:/remote: content is projected verbatim
			}
			if declaredByCat[cat] {
				continue // explicit declaration; checked above
			}
			label := "subagents." + cat + "." + tool
			if err := validateModelSpec(tool, label, routeByCat[cat], true); err != nil {
				return err
			}
		}
	}
	return validateSubagentOverrides(c)
}

// isBuiltinSubagent reports whether the subagent's source resolves to a
// catalog-rendered agent (the only kind agentfm renders, and therefore the
// only kind an override can apply to).
func isBuiltinSubagent(s Subagent) bool {
	_, ok := SubagentCatalogName(s.Source)
	return ok
}

// sortedSubagentNames returns the config's subagent names in deterministic
// order, so a config that fails validation names the same offender every run.
func sortedSubagentNames(c *Config) []string {
	names := make([]string, 0, len(c.Subagents))
	for name := range c.Subagents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// rejectLegacyModels fails naming the first [models.<tool>.<level>] block the
// TOML decoder picked up. pelletier/go-toml/v2 populates the Config's Models
// detector field, so any non-empty value triggers this rejection; without it,
// a config edited for the removed tier system would parse clean and silently
// lose its model declarations. The offender is walked in sorted order for a
// deterministic error.
func rejectLegacyModels(m modelsTable) error {
	for _, tool := range []struct {
		name   string
		routes map[string]ModelRoute
	}{
		{"claude", m.Claude},
		{"opencode", m.OpenCode},
	} {
		if len(tool.routes) == 0 {
			continue
		}
		levels := make([]string, 0, len(tool.routes))
		for level := range tool.routes {
			levels = append(levels, level)
		}
		sort.Strings(levels)
		return fmt.Errorf("parse config: models.%s.%s is an unknown table — model tiers were removed; declare per-agent models via [subagents.<name>.%s]", tool.name, levels[0], tool.name)
	}
	return nil
}

// validateSubagentOverrides checks every [subagents.<name>.<tool>] block —
// deliberately IGNORING the entry's targets, because the engine applies
// overrides unconditionally when it renders both tools' variants. The previous
// version iterated TargetsOrAll(), which let an untargeted tool's block skip
// validation entirely and stamp any value straight into a live agent file.
//
// It also rejects the two silent-no-op classes the review found: an override on
// a local:/remote: source (that content is never rendered, so the override can
// never apply), and a tune-only entry naming an agent that is not installed (a
// typo'd name would otherwise validate, plan, and apply clean while retuning
// nothing).
func validateSubagentOverrides(c *Config) error {
	names := make([]string, 0, len(c.Subagents))
	for name := range c.Subagents {
		names = append(names, name)
	}
	sort.Strings(names) // deterministic: the same config must fail on the same offender

	// The installed builtin agents, by catalog name — what a tune-only entry
	// must resolve against. Computed lazily: only configs that carry overrides
	// pay for the framework expansion.
	var installed map[string]bool
	installedBuiltins := func() (map[string]bool, error) {
		if installed != nil {
			return installed, nil
		}
		installed = map[string]bool{}
		for _, tool := range []string{"opencode"} {
			entries, err := c.ExpandedSubagentEntriesForTool(tool)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if cat, ok := SubagentCatalogName(e.Resource.Source); ok {
					installed[cat] = true
				}
			}
		}
		return installed, nil
	}

	seen := map[string]map[string]struct {
		entry string
		ov    ModelRoute
	}{} // catalog name -> tool -> first override seen
	for _, name := range names {
		sa := c.Subagents[name]
		hasOverride := sa.OpenCode != (ModelRoute{})
		if !hasOverride {
			continue
		}

		// Resolve the catalog name the override applies to. Overrides only make
		// sense for builtin (catalog-rendered) agents: local:/remote: content is
		// projected verbatim, so an override there would be accepted and then
		// silently discarded — reject it instead.
		cat := name
		if !sa.IsTuneOnly() {
			var ok bool
			if cat, ok = SubagentCatalogName(sa.Source); !ok {
				return fmt.Errorf("parse config: subagents.%s declares a model override, but its source %q is not builtin: — local:/remote: agents are projected verbatim and never rendered, so the override could never apply", name, sa.Source)
			}
		} else {
			known, err := installedBuiltins()
			if err != nil {
				return err
			}
			if !known[cat] {
				return fmt.Errorf("parse config: subagents.%s tunes an agent that is not installed — no framework or [subagents.*] declaration provides builtin:%s (typo?)", name, cat)
			}
		}

		for _, tool := range []string{"opencode"} {
			ov := sa.ModelOverrideFor(tool)
			if ov == (ModelRoute{}) {
				continue
			}
			label := "subagents." + name + "." + tool
			// Validate the fragment itself. A variant whose model comes from the
			// override itself can be judged here; an override that names only a
			// variant (with the model coming from elsewhere) is checked at render
			// where the merged spec is known — agentfm.Render errors loudly on an
			// unrenderable combination instead.
			if err := validateModelSpec(tool, label, ov, false); err != nil {
				return err
			}
			// Conflicts are judged per CATALOG name: one builtin renders one
			// file, so two entries' overrides for it must agree or the winner
			// would be map-iteration luck (a different render — and a different
			// materialize fingerprint — every run).
			if seen[cat] == nil {
				seen[cat] = map[string]struct {
					entry string
					ov    ModelRoute
				}{}
			}
			if prev, dup := seen[cat][tool]; dup && prev.ov != ov {
				return fmt.Errorf("parse config: subagents.%s.%s conflicts with subagents.%s.%s — one builtin (%s) renders one file, so its overrides must agree", name, tool, prev.entry, tool, cat)
			}
			seen[cat][tool] = struct {
				entry string
				ov    ModelRoute
			}{name, ov}
		}
	}
	return nil
}

// indexLike reports whether sjson would treat name as an array index:
// all-digit ("0", "42") or "-" followed by digits ("-1", the append form).
func indexLike(name string) bool {
	t := strings.TrimPrefix(name, "-")
	if t == "" {
		return false // "-" alone is a plain key
	}
	for i := 0; i < len(t); i++ {
		if t[i] < '0' || t[i] > '9' {
			return false
		}
	}
	return true
}
