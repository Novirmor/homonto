package claude

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/adapter/baseadapter"
	"github.com/noviopenworks/homonto/internal/adapter/copyproj"
	"github.com/noviopenworks/homonto/internal/adapter/fileproj"
	"github.com/noviopenworks/homonto/internal/adapter/jsoncodec"
	"github.com/noviopenworks/homonto/internal/adapter/structproj"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/copyfile"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/jsonutil"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
)

// Adapter projects desired config into Claude Code's files under home.
type Adapter struct {
	baseadapter.Base
}

// New builds a Claude adapter at user scope. home is the $HOME root; content
// holds owned skills. Use WithProjectRoot to install project-scope skills.
func New(home, content string) *Adapter {
	return &Adapter{Base: baseadapter.Base{
		Tool:          "claude",
		VariantSuffix: ".claude.md",
		Home:          home,
		Content:       content,
	}}
}

// WithProjectRoot sets the project root (the homonto.toml directory). It is
// used for project-scope resource placement. Explicit settings remain in the
// user settings file; project-scoped MCP servers use the project MCP file.
func (a *Adapter) WithProjectRoot(projectRoot string) *Adapter {
	a.Base.ProjectRoot = projectRoot
	return a
}

// WithCatalogRoot sets the materialized builtin-catalog root that builtin:<name>
// skills link from. Mirrors WithProjectRoot.
func (a *Adapter) WithCatalogRoot(catalogRoot string) *Adapter {
	a.Base.CatalogRoot = catalogRoot
	return a
}

// WithCommandCatalogRoot sets the materialized builtin-command root that
// builtin:<name> commands link from. Mirrors WithCatalogRoot.
func (a *Adapter) WithCommandCatalogRoot(commandCatalogRoot string) *Adapter {
	a.Base.CommandCatalogRoot = commandCatalogRoot
	return a
}

// WithSubagentCatalogRoot sets the materialized builtin-subagent root that
// builtin:<name> subagents link from. Mirrors WithCommandCatalogRoot.
func (a *Adapter) WithSubagentCatalogRoot(subagentCatalogRoot string) *Adapter {
	a.Base.SubagentCatalogRoot = subagentCatalogRoot
	return a
}

// WithRemoteSubagentRoot sets the materialized remote-subagent root that
// remote:<url> subagents link from (populated by the engine's verify pipeline
// before apply). Mirrors WithSubagentCatalogRoot.
func (a *Adapter) WithRemoteSubagentRoot(remoteSubagentRoot string) *Adapter {
	a.Base.RemoteSubagentRoot = remoteSubagentRoot
	return a
}

func (a *Adapter) claudeJSON() string { return filepath.Join(a.Home, ".claude.json") }
func (a *Adapter) settingsJSON() string {
	return filepath.Join(a.Home, ".claude", "settings.json")
}

// projectSettingsJSON is the project-level settings file (merged by Claude Code
// over the user one, project winning on conflicting keys). It remains part of
// the projection plumbing to prune prior projsetting.* state entries.
func (a *Adapter) projectSettingsJSON() string {
	return filepath.Join(a.ProjectRoot, ".claude", "settings.json")
}

// readProjectSettings reads the project-level settings document, or an empty
// root when no project root is known — recorded projsetting.* keys still prune
// cleanly (state-only) without inventing a relative path to read.
func (a *Adapter) readProjectSettings() ([]byte, error) {
	if a.ProjectRoot == "" {
		return jsonutil.Standardize(nil)
	}
	return readStandardized(a.projectSettingsJSON())
}

// projectMCPJSON is Claude Code's project MCP file, merged over the user-level
// servers. Only project-scoped [mcps.*] entries land here; call only when
// projectRoot is set.
func (a *Adapter) projectMCPJSON() string {
	return filepath.Join(a.ProjectRoot, ".mcp.json")
}

// readProjectMCP mirrors readProjectSettings for .mcp.json.
func (a *Adapter) readProjectMCP() ([]byte, error) {
	if a.ProjectRoot == "" {
		return jsonutil.Standardize(nil)
	}
	return readStandardized(a.projectMCPJSON())
}

// mcpValue renders one declared server as Claude's mcpServers entry, or
// ok=false when there is nothing runnable to project for this tool.
func mcpValue(m config.MCP) (string, bool) {
	if !baseadapter.MCPProjected(m, "claude") {
		return "", false
	}
	// Claude Code's real schema: command is a string with a separate args
	// array (matching `claude mcp add` output; empty keys omitted).
	obj := map[string]any{"type": "stdio", "command": m.Command[0]}
	if len(m.Command) > 1 {
		obj["args"] = m.Command[1:]
	}
	if len(m.Env) > 0 {
		obj["env"] = m.Env
	}
	return structproj.MustJSON(obj), true
}

// desiredProjectMCPs maps the project-scoped servers to their projmcp.* state
// keys — the same mcpServers entries, written into <projectRoot>/.mcp.json
// (Claude Code's project MCP file) instead of the global ~/.claude.json, so
// one repository's servers don't run in every other session.
func (a *Adapter) desiredProjectMCPs(c *config.Config) map[string]string {
	out := map[string]string{}
	if a.ProjectRoot == "" {
		return out
	}
	for name, m := range c.MCPs {
		if m.ScopeOrDefault() != "project" {
			continue
		}
		if v, ok := mcpValue(m); ok {
			out["projmcp."+name] = v
		}
	}
	return out
}

// desired returns managed key -> unresolved JSON-encoded desired value.
func (a *Adapter) desired(c *config.Config) map[string]string {
	out := map[string]string{}
	for name, m := range c.MCPs {
		// Project-scoped servers live in .mcp.json (desiredProjectMCPs); they
		// fall back here only when no project root is known.
		if m.ScopeOrDefault() == "project" && a.ProjectRoot != "" {
			continue
		}
		if v, ok := mcpValue(m); ok {
			out["mcp."+name] = v
		}
	}
	for k, v := range c.Settings.Claude {
		out["setting."+k] = structproj.MustJSON(v)
	}
	// homonto no longer projects a route-derived default main model: an
	// operator who wants a specific Claude main model declares it explicitly
	// via [settings.claude].model above. Each tool uses its own default
	// otherwise.
	for _, pl := range c.Plugins.Claude {
		// Source-keyed: enabledPlugins[<source>] carries the plugin's enabled
		// value, so a disabled plugin emits a managed `false` (not absence).
		out["plugin."+pl.Source] = structproj.MustJSON(pl.IsEnabled())
	}
	for _, pl := range c.Plugins.Claude {
		// pluginConfigs[<source>] carries the whole {options:…} object so write
		// and read-back stay symmetric (see current()); no config → no key.
		if len(pl.Config) > 0 {
			out["pluginconfig."+pl.Source] = structproj.MustJSON(map[string]any{"options": pl.Config})
		}
	}
	for name, mk := range c.Marketplaces.Claude {
		// extraKnownMarketplaces[<name>] carries the whole {source:…} object so
		// write and read-back stay symmetric (see current()).
		out["marketplace."+name] = structproj.MustJSON(marketplaceValue(mk))
	}
	return out
}

// desiredProjectSettings is the project-level counterpart of desired, for keys
// that belong in <projectRoot>/.claude/settings.json instead of the user
// settings. homonto no longer derives any main-model key from a route (an
// operator who wants a specific main model declares it via [settings.claude]),
// so today this returns nothing — kept as a hook so the projsetting.* state
// namespace stays pruned cleanly and a future project-scoped setting has a
// home.
func (a *Adapter) desiredProjectSettings(c *config.Config) map[string]string {
	return map[string]string{}
}

// marketplaceValue builds the canonical extraKnownMarketplaces value for a
// declared marketplace. Only the type-relevant locator fields are emitted, so a
// github marketplace never carries an empty url/path that would differ from an
// adopted on-disk entry. autoUpdate is present only when auto_update was set.
func marketplaceValue(mk config.Marketplace) map[string]any {
	src := map[string]any{"source": mk.Source}
	switch mk.Source {
	case "github":
		src["repo"] = mk.Repo
	case "url":
		src["url"] = mk.URL
	case "git-subdir":
		src["url"] = mk.URL
		src["path"] = mk.Path
	case "directory":
		src["path"] = mk.Path
	}
	out := map[string]any{"source": src}
	if mk.AutoUpdate != nil {
		out["autoUpdate"] = *mk.AutoUpdate
	}
	return out
}

// Document-path mappings for each structured-document namespace, threaded into
// structproj.Project/Apply/Observe. Config-supplied names are escaped so a name
// containing dots/@/|/# addresses the literal key rather than nesting or being
// dropped (mirroring the prior inline SetJSON/GetJSON escaping).
func mcpPath(key string) string { return "mcpServers." + jsonutil.EscapePath(trim(key, "mcp.")) }
func projMCPPath(key string) string {
	return "mcpServers." + jsonutil.EscapePath(trim(key, "projmcp."))
}
func settingPath(key string) string { return jsonutil.EscapePath(trim(key, "setting.")) }
func projSettingPath(key string) string {
	return jsonutil.EscapePath(trim(key, "projsetting."))
}
func pluginPath(key string) string {
	return "enabledPlugins." + jsonutil.EscapePath(trim(key, "plugin."))
}
func pluginConfigPath(key string) string {
	return "pluginConfigs." + jsonutil.EscapePath(trim(key, "pluginconfig."))
}
func marketplacePath(key string) string {
	return "extraKnownMarketplaces." + jsonutil.EscapePath(trim(key, "marketplace."))
}

func (a *Adapter) Plan(c *config.Config, st *state.State) (adapter.ChangeSet, error) {
	if err := a.Expand(c); err != nil {
		return adapter.ChangeSet{}, err
	}
	mj, err := readStandardized(a.claudeJSON())
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	sj, err := readStandardized(a.settingsJSON())
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	psj, err := a.readProjectSettings()
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	cs := adapter.ChangeSet{Tool: "claude"}
	des := a.desired(c)
	codec := jsoncodec.Codec{}
	// Structured-document namespaces go through the shared projection contract:
	// mcp.* lives in .claude.json; setting./plugin./pluginconfig./marketplace.*
	// all live in the user settings.json; projsetting.* lives in the
	// project-level settings.json. Each Project call sees only its prefix's
	// desired keys and prunes only its own recorded keys, so the generic delete
	// loop below no longer touches these prefixes.
	if changes, err := structproj.Project("claude", "mcp.", filterDesired(des, "mcp."), mj, st, codec, mcpPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	pmj, err := a.readProjectMCP()
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	if changes, err := structproj.Project("claude", "projmcp.", a.desiredProjectMCPs(c), pmj, st, codec, projMCPPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("claude", "setting.", filterDesired(des, "setting."), sj, st, codec, settingPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("claude", "projsetting.", a.desiredProjectSettings(c), psj, st, codec, projSettingPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("claude", "plugin.", filterDesired(des, "plugin."), sj, st, codec, pluginPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("claude", "pluginconfig.", filterDesired(des, "pluginconfig."), sj, st, codec, pluginConfigPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("claude", "marketplace.", filterDesired(des, "marketplace."), sj, st, codec, marketplacePath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	// File-projection namespaces go through the shared symlink contract: each
	// Project call emits create/relocate/relink + adopt for its links and plans
	// NO deletes — the generic delete loop below stays the single source of
	// file-prefix deletes.
	roots := a.ManagedRoots()
	skillChanges, err := fileproj.Project("claude", a.SkillFileLinks(), st, roots)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	cs.Changes = append(cs.Changes, skillChanges...)
	commandChanges, err := fileproj.Project("claude", a.CommandFileLinks(), st, roots)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	cs.Changes = append(cs.Changes, commandChanges...)
	subagentChanges, err := fileproj.Project("claude", a.SubagentFileLinks(), st, roots)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	cs.Changes = append(cs.Changes, subagentChanges...)
	// Orphans: a state key no longer declared in config is de-declared — plan a
	// delete. (A declared key missing from disk is drift, handled above.) Old is
	// always redacted: a removed key's provenance is stale by definition.
	declared := map[string]bool{}
	for k := range des {
		declared[k] = true
	}
	for _, entry := range a.Skills {
		declared["skill."+entry.Name] = true
	}
	for _, entry := range a.Commands {
		declared["command."+entry.Name] = true
	}
	for _, entry := range a.Subagents {
		declared["subagent."+entry.Name] = true
	}
	// Copy-mode subagents are managed content files (not symlinks): surface their
	// create/update/prune in the plan and abort on a foreign-file conflict. Apply
	// reconciles them in a dedicated pass (a.applyCopySubagents); subagentcopy.* is
	// deliberately outside filePrefix so the generic delete loop never touches
	// it.
	copyOps, err := a.PlanCopyOps(st)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	for _, op := range copyOps {
		name := copyproj.Name(op.Dst)
		switch op.Action {
		case copyfile.Conflict:
			return adapter.ChangeSet{}, fmt.Errorf("claude: %s exists and is not a homonto-managed copy-mode subagent; not overwriting", op.Dst)
		case copyfile.Create:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "create", Key: "subagentcopy." + name, New: op.Dst})
		case copyfile.Update, copyfile.LocalEdit:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "update", Key: "subagentcopy." + name, New: op.Dst})
		case copyfile.Prune:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: "subagentcopy." + name, Old: op.Dst})
		}
	}
	// The generic prune covers only the file-projection prefixes; the structured
	// prefixes are pruned by their structproj.Project calls above (avoiding a
	// double delete). subagentcopy.* is pruned by its own reconciler pass.
	for _, k := range st.Keys("claude") {
		if declared[k] || !filePrefix(k) {
			continue
		}
		cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: k, Old: adapter.SecretRedaction})
	}
	// Keys come from map iteration (random order); a plan must render the
	// same way every run. Keys are unique within a changeset.
	sort.SliceStable(cs.Changes, func(i, j int) bool { return cs.Changes[i].Key < cs.Changes[j].Key })
	return cs, nil
}

// ObserveHashes hashes the current on-disk value of every recorded key still
// present, so an unchanged key reproduces its Entry.Applied (see the plan's
// noop identity: Applied == secret.Hash(jsonutil.Canonical(disk))). Only hashes
// escape — raw values (possibly resolved secrets) never leave the adapter.
func (a *Adapter) ObserveHashes(st *state.State) (map[string]string, error) {
	mj, err := readStandardized(a.claudeJSON())
	if err != nil {
		return nil, err
	}
	sj, err := readStandardized(a.settingsJSON())
	if err != nil {
		return nil, err
	}
	codec := jsoncodec.Codec{}
	out := map[string]string{}
	// Structured-document keys (mcp.* in .claude.json; setting./plugin./
	// pluginconfig./marketplace.* in settings.json) re-hash through the contract.
	if obs, err := structproj.Observe("claude", "mcp.", mj, st, codec, mcpPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	for _, o := range []struct {
		prefix  string
		pathFor structproj.PathFor
	}{
		{"setting.", settingPath},
		{"plugin.", pluginPath},
		{"pluginconfig.", pluginConfigPath},
		{"marketplace.", marketplacePath},
	} {
		if obs, err := structproj.Observe("claude", o.prefix, sj, st, codec, o.pathFor); err != nil {
			return nil, err
		} else {
			for k, v := range obs {
				out[k] = v
			}
		}
	}
	psj, err := a.readProjectSettings()
	if err != nil {
		return nil, err
	}
	if obs, err := structproj.Observe("claude", "projsetting.", psj, st, codec, projSettingPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	pmj, err := a.readProjectMCP()
	if err != nil {
		return nil, err
	}
	if obs, err := structproj.Observe("claude", "projmcp.", pmj, st, codec, projMCPPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	// File-projection keys (skill./command./subagent.*) live on disk as symlinks;
	// each re-hashes its recorded link through the shared contract, reading at the
	// recorded dst so a pending scope switch is not misread as drift.
	for k, v := range fileproj.Observe("claude", "skill.", st) {
		out[k] = v
	}
	for k, v := range fileproj.Observe("claude", "command.", st) {
		out[k] = v
	}
	for k, v := range fileproj.Observe("claude", "subagent.", st) {
		out[k] = v
	}
	for _, key := range st.Keys("claude") {
		if !hasPrefix(key, "subagentcopy.") {
			continue
		}
		// A copy-mode subagent lives on disk as a real file; its Applied is the
		// content hash and Desired holds the dst path.
		e, ok := st.Get("claude", key)
		if !ok {
			continue
		}
		content, err := os.ReadFile(e.Desired)
		if err != nil {
			continue // missing → omit (engine infers missing)
		}
		out[key] = copyfile.Hash(content)
	}
	return out, nil
}

func (a *Adapter) Apply(cfg *config.Config, cs adapter.ChangeSet, res *secret.Resolver, st *state.State) error {
	if err := a.Expand(cfg); err != nil {
		return err
	}
	mj, err := readStandardized(a.claudeJSON())
	if err != nil {
		return err
	}
	sj, err := readStandardized(a.settingsJSON())
	if err != nil {
		return err
	}
	psj, err := a.readProjectSettings()
	if err != nil {
		return err
	}
	// Write a tool file only when a managed key living in it actually changed.
	// adopt/noop are state-only and must leave the file byte-for-byte untouched
	// (comments/formatting preserved); skill.* is symlink work, not JSON.
	codec := jsoncodec.Codec{}
	// Structured-document prefixes go through the shared contract: mcp.* lives in
	// .claude.json; setting./plugin./pluginconfig./marketplace.* all live in
	// settings.json (threaded through one doc so a change to any of them writes the
	// file exactly once). Each Apply reports whether its document actually changed.
	mj, mjChanged, err := structproj.Apply("claude", "mcp.", filterChanges(cs.Changes, "mcp."), mj, codec, res, st, mcpPath)
	if err != nil {
		return err
	}
	pmj, err := a.readProjectMCP()
	if err != nil {
		return err
	}
	pmj, pmjChanged, err := structproj.Apply("claude", "projmcp.", filterChanges(cs.Changes, "projmcp."), pmj, codec, res, st, projMCPPath)
	if err != nil {
		return err
	}
	sjChanged := false
	// Prefixes are applied in lexicographic key order (marketplace < plugin <
	// pluginconfig < setting) so newly-created keys are appended to settings.json
	// in the same order the prior single sorted-change loop produced — preserving
	// byte-for-byte output.
	for _, p := range []struct {
		prefix  string
		pathFor structproj.PathFor
	}{
		{"marketplace.", marketplacePath},
		{"plugin.", pluginPath},
		{"pluginconfig.", pluginConfigPath},
		{"setting.", settingPath},
	} {
		var ch bool
		sj, ch, err = structproj.Apply("claude", p.prefix, filterChanges(cs.Changes, p.prefix), sj, codec, res, st, p.pathFor)
		if err != nil {
			return err
		}
		sjChanged = sjChanged || ch
	}
	psj, psjChanged, err := structproj.Apply("claude", "projsetting.", filterChanges(cs.Changes, "projsetting."), psj, codec, res, st, projSettingPath)
	if err != nil {
		return err
	}
	// File-projection keys (skill./command./subagent.): adopt records state only;
	// delete removes the managed symlink. Their create/update are handled by the
	// fileproj.ApplyLinks pass below; noop and subagentcopy.* are handled
	// elsewhere. The fallback recovers a de-declared key's on-disk dst at user
	// scope when state lacks a recorded dst, matching the prior inline behavior.
	roots := a.ManagedRoots()
	if err := fileproj.ApplyState("claude", filterChanges(cs.Changes, "skill."), st, roots, func(k string) string {
		return filepath.Join(a.SkillsDir("user"), trim(k, "skill."))
	}); err != nil {
		return err
	}
	if err := fileproj.ApplyState("claude", filterChanges(cs.Changes, "command."), st, roots, func(k string) string {
		return filepath.Join(a.CommandsDir("user"), trim(k, "command.")+".md")
	}); err != nil {
		return err
	}
	if err := fileproj.ApplyState("claude", filterChanges(cs.Changes, "subagent."), st, roots, func(k string) string {
		return filepath.Join(a.SubagentsDir("user"), trim(k, "subagent.")+".md")
	}); err != nil {
		return err
	}
	// Fail fast on link conflicts before writing any file. A conflict in any of
	// the three namespaces must be detected here, before any JSON write or state
	// mutation below — otherwise a command conflict could let Apply partially
	// write JSON and commit skill-link state before erroring.
	if err := fileproj.Conflicts(a.SkillFileLinks(), roots); err != nil {
		return err
	}
	if err := fileproj.Conflicts(a.CommandFileLinks(), roots); err != nil {
		return err
	}
	if err := fileproj.Conflicts(a.SubagentFileLinks(), roots); err != nil {
		return err
	}
	// Fail fast on a copy-mode subagent conflict too, before any file is written.
	copyOps, err := a.PlanCopyOps(st)
	if err != nil {
		return err
	}
	for _, op := range copyOps {
		if op.Action == copyfile.Conflict {
			return fmt.Errorf("claude: %s exists and is not a homonto-managed copy-mode subagent; not overwriting", op.Dst)
		}
	}
	if mjChanged {
		if err := fsutil.WriteAtomic(a.claudeJSON(), mj); err != nil {
			return err
		}
	}
	if sjChanged {
		if err := fsutil.WriteAtomic(a.settingsJSON(), sj); err != nil {
			return err
		}
	}
	if psjChanged && a.ProjectRoot != "" {
		if err := fsutil.WriteAtomic(a.projectSettingsJSON(), psj); err != nil {
			return err
		}
	}
	if pmjChanged && a.ProjectRoot != "" {
		if err := fsutil.WriteAtomic(a.projectMCPJSON(), pmj); err != nil {
			return err
		}
	}
	// Prune each namespace's inactive-scope orphan (left after a per-resource
	// scope switch), then create the link and record state. Runs after the JSON
	// writes. Only our own managed symlink is ever removed (IsManaged guards it);
	// a foreign file or an absent path is left untouched.
	if err := fileproj.ApplyLinks("claude", a.SkillFileLinks(), st, roots); err != nil {
		return err
	}
	if err := fileproj.ApplyLinks("claude", a.CommandFileLinks(), st, roots); err != nil {
		return err
	}
	if err := fileproj.ApplyLinks("claude", a.SubagentFileLinks(), st, roots); err != nil {
		return err
	}
	// Reconcile copy-mode subagent content files (write/update/prune + state),
	// backing up any local edit. Conflicts were already rejected above.
	if err := a.ApplyCopySubagents(st); err != nil {
		return err
	}
	return nil
}
