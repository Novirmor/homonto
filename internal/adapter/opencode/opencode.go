package opencode

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

// Adapter projects desired config into OpenCode's opencode.jsonc under home.
type Adapter struct {
	baseadapter.Base
}

// New builds an OpenCode adapter at user scope. home is $HOME; content holds
// owned skills. Use WithProjectRoot to install project-scope skills.
func New(home, content string) *Adapter {
	return &Adapter{Base: baseadapter.Base{
		Tool:          "opencode",
		VariantSuffix: ".opencode.md",
		Home:          home,
		Content:       content,
	}}
}

// WithProjectRoot sets the project root (the homonto.toml directory). It is
// used for project-scope resource placement. Explicit settings and plugins
// remain in the user config; project-scoped MCP servers use the project config.
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

func (a *Adapter) cfgFile() string {
	return filepath.Join(a.Home, ".config", "opencode", "opencode.jsonc")
}

// projectCfgFile is the project-level OpenCode config (merged by OpenCode over
// the global one, project winning on conflicting keys). It remains part of the
// projection plumbing to prune prior projsetting.* state entries.
func (a *Adapter) projectCfgFile() string {
	return filepath.Join(a.ProjectRoot, "opencode.jsonc")
}

// readProjectCfg reads the project-level config document, or an empty root when
// no project root is known — recorded projsetting.* keys still prune cleanly
// (state-only) without inventing a relative "opencode.jsonc" path to read.
func (a *Adapter) readProjectCfg() ([]byte, error) {
	if a.ProjectRoot == "" {
		return jsonutil.Standardize(nil)
	}
	return readStandardized(a.projectCfgFile())
}

// tuiFile is the second managed file: OpenCode reads TUI settings from a
// separate ~/.config/opencode/tui.json. [tui.opencode] keys project here under
// the "tui." state namespace, independent of opencode.jsonc.
func (a *Adapter) tuiFile() string {
	return filepath.Join(a.Home, ".config", "opencode", "tui.json")
}

// mcpValue renders one declared server as OpenCode's mcp entry, or ok=false
// when there is nothing runnable to project for this tool.
func mcpValue(m config.MCP) (string, bool) {
	if !baseadapter.MCPProjected(m, "opencode") {
		return "", false
	}
	obj := map[string]any{"type": "local", "command": m.Command, "enabled": true}
	if len(m.Env) > 0 {
		obj["environment"] = m.Env
	}
	return structproj.MustJSON(obj), true
}

// desiredMCPs maps the user-scoped servers to their mcp.* state keys (the
// global opencode.jsonc). Project-scoped servers fall back here only when no
// project root is known.
func (a *Adapter) desiredMCPs(c *config.Config) map[string]string {
	out := map[string]string{}
	for name, m := range c.MCPs {
		if m.ScopeOrDefault() == "project" && a.ProjectRoot != "" {
			continue
		}
		if v, ok := mcpValue(m); ok {
			out["mcp."+name] = v
		}
	}
	return out
}

// desiredProjectMCPs maps the project-scoped servers to their projmcp.* state
// keys — the same mcp.<name> entries, written into the project-level
// opencode.jsonc instead, so one repository's servers don't run in every other
// session.
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

// desiredSettings maps each [settings.opencode] key to its setting.* state key
// (explicit settings always live in the global opencode.jsonc). homonto no
// longer derives a default main/small_model from any route — an operator who
// wants a specific model declares it via [settings.opencode], and otherwise
// OpenCode uses its own default.
//
// `model_variant` is homonto's companion to `model`: instead of (or in
// addition to) writing the variant into the id yourself
// (`model = "openai/gpt-5#high"`), declare it separately and the two are
// combined at projection time — the same companion shape
// [subagents.<name>.opencode] uses. It never reaches opencode.jsonc as its
// own key.
func (a *Adapter) desiredSettings(c *config.Config) map[string]string {
	out := map[string]string{}
	for k, v := range c.Settings.OpenCode {
		out["setting."+k] = structproj.MustJSON(v)
	}
	if mv, ok := c.Settings.OpenCode["model_variant"]; ok {
		model, _ := c.Settings.OpenCode["model"].(string)
		variant, _ := mv.(string)
		combined := model
		if variant != "" && !strings.HasSuffix(model, "#"+variant) {
			combined = model + "#" + variant
		}
		out["setting.model"] = structproj.MustJSON(combined)
		delete(out, "setting.model_variant")
	}
	return out
}

// desiredProjectSettings is the project-level counterpart of desiredSettings.
// homonto no longer derives any main/small_model key from a route, so today
// this returns nothing — kept as a hook so the projsetting.* state namespace
// stays pruned cleanly and a future project-scoped setting has a home.
func (a *Adapter) desiredProjectSettings(c *config.Config) map[string]string {
	return map[string]string{}
}

// desiredTUI maps each [tui.opencode] key to its tui.* state key (tui.json).
func desiredTUI(c *config.Config) map[string]string {
	out := map[string]string{}
	for k, v := range c.TUI.OpenCode {
		out["tui."+k] = structproj.MustJSON(v)
	}
	return out
}

// Document-path mappings for each structured-document namespace. Config-supplied
// names are escaped so a name with dots/@/|/# addresses the literal key.
func mcpDocPath(key string) string { return "mcp." + jsonutil.EscapePath(trim(key, "mcp.")) }
func projMCPDocPath(key string) string {
	return "mcp." + jsonutil.EscapePath(trim(key, "projmcp."))
}
func settingDocPath(key string) string { return jsonutil.EscapePath(trim(key, "setting.")) }
func projSettingDocPath(key string) string {
	return jsonutil.EscapePath(trim(key, "projsetting."))
}
func tuiDocPath(key string) string { return jsonutil.EscapePath(trim(key, "tui.")) }

func (a *Adapter) Plan(c *config.Config, st *state.State) (adapter.ChangeSet, error) {
	if err := a.Expand(c); err != nil {
		return adapter.ChangeSet{}, err
	}
	doc, err := readStandardized(a.cfgFile())
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	projDoc, err := a.readProjectCfg()
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	tuiDoc, err := readStandardized(a.tuiFile())
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	cs := adapter.ChangeSet{Tool: "opencode"}

	// Structured-document namespaces go through the shared projection contract:
	// mcp./setting.* live in the global opencode.jsonc; projsetting.* lives in
	// the project-level opencode.jsonc; tui.* lives in tui.json. Each Project
	// call prunes only its own recorded keys, so the generic delete loop below no
	// longer touches these prefixes. plugin.* stays bespoke (array membership).
	codec := jsoncodec.Codec{}
	des := a.desiredMCPs(c)
	if changes, err := structproj.Project("opencode", "mcp.", des, doc, st, codec, mcpDocPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("opencode", "projmcp.", a.desiredProjectMCPs(c), projDoc, st, codec, projMCPDocPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("opencode", "setting.", a.desiredSettings(c), doc, st, codec, settingDocPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("opencode", "projsetting.", a.desiredProjectSettings(c), projDoc, st, codec, projSettingDocPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	if changes, err := structproj.Project("opencode", "tui.", desiredTUI(c), tuiDoc, st, codec, tuiDocPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	for _, pl := range c.Plugins.OpenCode {
		src := pl.Source
		_, inState := st.Get("opencode", "plugin."+src)
		if !pl.IsEnabled() {
			// Disabled: ensure absent, but only ever remove a homonto-managed
			// entry (recorded in state). A present-but-unmanaged source is left
			// untouched. The delete is emitted for ANY recorded entry, present on
			// disk or not: an entry removed out of band used to emit nothing,
			// leaving the state record orphaned — the declared loop then shielded
			// it from the generic prune, so `status` reported "missing (deleted
			// out of band)" forever and no apply could clear it. (Apply's delete
			// is idempotent on an absent array element and only rewrites the doc
			// when its bytes actually change.)
			if inState {
				cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: "plugin." + src, Old: adapter.SecretRedaction})
			}
			continue
		}
		if arrayHas(doc, "plugin", src) {
			// Present on disk. If recorded, steady-state noop; otherwise adopt it
			// into state so pruning and drift can see it (plugin names are plain,
			// never secret-bearing).
			if inState {
				cs.Changes = append(cs.Changes, adapter.Change{Action: "noop", Key: "plugin." + src})
			} else {
				cs.Changes = append(cs.Changes, adapter.Change{Action: "adopt", Key: "plugin." + src, New: structproj.MustJSON(src)})
			}
		} else {
			cs.Changes = append(cs.Changes, adapter.Change{Action: "create", Key: "plugin." + src, New: structproj.MustJSON(src)})
		}
	}
	// File-projection namespaces go through the shared symlink contract: each
	// Project call emits create/relocate/relink + adopt for its links and plans
	// NO deletes — the generic delete loop below stays the single source of
	// file-prefix deletes.
	roots := a.ManagedRoots()
	skillChanges, err := fileproj.Project("opencode", a.SkillFileLinks(), st, roots)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	cs.Changes = append(cs.Changes, skillChanges...)
	commandChanges, err := fileproj.Project("opencode", a.CommandFileLinks(), st, roots)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	cs.Changes = append(cs.Changes, commandChanges...)
	subagentChanges, err := fileproj.Project("opencode", a.SubagentFileLinks(), st, roots)
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
	for k := range c.Settings.OpenCode {
		declared["setting."+k] = true
	}
	for k := range c.TUI.OpenCode {
		declared["tui."+k] = true
	}
	for _, pl := range c.Plugins.OpenCode {
		declared["plugin."+pl.Source] = true
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
	// Copy-mode subagents are managed content files: surface create/update/prune
	// and abort on a foreign-file conflict; Apply reconciles them in a dedicated
	// pass. subagentcopy.* is outside managedPrefix so the generic delete loop
	// never touches it.
	copyOps, err := a.PlanCopyOps(st)
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	for _, op := range copyOps {
		name := copyproj.Name(op.Dst)
		switch op.Action {
		case copyfile.Conflict:
			return adapter.ChangeSet{}, fmt.Errorf("opencode: %s exists and is not a homonto-managed copy-mode subagent; not overwriting", op.Dst)
		case copyfile.Create:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "create", Key: "subagentcopy." + name, New: op.Dst})
		case copyfile.Update, copyfile.LocalEdit:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "update", Key: "subagentcopy." + name, New: op.Dst})
		case copyfile.Prune:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: "subagentcopy." + name, Old: op.Dst})
		}
	}
	// The generic prune covers plugin.* (array membership) and the file-projection
	// prefixes; the structured prefixes (mcp./setting./tui.) are pruned by their
	// structproj.Project calls above (avoiding a double delete).
	for _, k := range st.Keys("opencode") {
		if declared[k] || !managedPrefix(k) {
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
// present, so an unchanged key reproduces its Entry.Applied (mirroring claude,
// as far as opencode's data model allows). Only hashes escape — raw values
// (possibly resolved secrets) never leave the adapter.
func (a *Adapter) ObserveHashes(st *state.State) (map[string]string, error) {
	doc, err := readStandardized(a.cfgFile())
	if err != nil {
		return nil, err
	}
	tuiDoc, err := readStandardized(a.tuiFile())
	if err != nil {
		return nil, err
	}
	codec := jsoncodec.Codec{}
	out := map[string]string{}
	// Structured-document keys (mcp./setting.* in opencode.jsonc; tui.* in
	// tui.json) re-hash their on-disk value through the shared contract.
	if obs, err := structproj.Observe("opencode", "mcp.", doc, st, codec, mcpDocPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	if obs, err := structproj.Observe("opencode", "setting.", doc, st, codec, settingDocPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	projDoc, err := a.readProjectCfg()
	if err != nil {
		return nil, err
	}
	if obs, err := structproj.Observe("opencode", "projmcp.", projDoc, st, codec, projMCPDocPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	if obs, err := structproj.Observe("opencode", "projsetting.", projDoc, st, codec, projSettingDocPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	if obs, err := structproj.Observe("opencode", "tui.", tuiDoc, st, codec, tuiDocPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	// File-projection keys (skill./command./subagent.*) live on disk as symlinks;
	// each re-hashes its recorded link through the shared contract, reading at the
	// recorded dst so a pending scope switch is not misread as drift.
	for k, v := range fileproj.Observe("opencode", "skill.", st) {
		out[k] = v
	}
	for k, v := range fileproj.Observe("opencode", "command.", st) {
		out[k] = v
	}
	for k, v := range fileproj.Observe("opencode", "subagent.", st) {
		out[k] = v
	}
	for _, key := range st.Keys("opencode") {
		switch {
		case hasPrefix(key, "plugin."):
			// Plugins are array membership with no scalar to re-hash: presence
			// means unchanged by definition, so return the key's own Applied.
			if arrayHas(doc, "plugin", trim(key, "plugin.")) {
				if e, ok := st.Get("opencode", key); ok {
					out[key] = e.Applied
				}
			}
		case hasPrefix(key, "subagentcopy."):
			// A copy-mode subagent lives on disk as a real file; its Applied is the
			// content hash and Desired holds the dst path.
			e, ok := st.Get("opencode", key)
			if !ok {
				continue
			}
			content, err := os.ReadFile(e.Desired)
			if err != nil {
				continue
			}
			out[key] = copyfile.Hash(content)
		}
		// absent from disk → omit
	}
	return out, nil
}

func (a *Adapter) Apply(cfg *config.Config, cs adapter.ChangeSet, res *secret.Resolver, st *state.State) error {
	if err := a.Expand(cfg); err != nil {
		return err
	}
	doc, err := readStandardized(a.cfgFile())
	if err != nil {
		return err
	}
	projDoc, err := a.readProjectCfg()
	if err != nil {
		return err
	}
	tuiDoc, err := readStandardized(a.tuiFile())
	if err != nil {
		return err
	}
	// Write opencode.jsonc only when a managed key in it actually changed.
	// adopt/noop are state-only and must leave the file byte-for-byte untouched
	// (JSONC comments preserved); skill.* is symlink work, not JSON. tuiChanged
	// gates tui.json's write independently, so a change to one file never
	// rewrites the other.
	codec := jsoncodec.Codec{}
	// Structured-document prefixes go through the shared contract. Order matters
	// for byte-identical output: mcp./plugin./setting.* all live in opencode.jsonc,
	// and the prior single sorted-change loop appended them in mcp < plugin <
	// setting order — so apply them in that order too. tui.* lives in tui.json.
	doc, docChanged, err := structproj.Apply("opencode", "mcp.", filterChanges(cs.Changes, "mcp."), doc, codec, res, st, mcpDocPath)
	if err != nil {
		return err
	}
	// plugin.* is bespoke array membership (structproj's keyed codec cannot model
	// it): create/update adds the element, delete removes it, adopt records state.
	for _, c := range filterChanges(cs.Changes, "plugin.") {
		switch c.Action {
		case "noop":
			continue
		case "adopt":
			// Records a pre-existing membership into state without touching disk.
			val, err := res.ResolveJSON(c.New)
			if err != nil {
				return err
			}
			st.Set("opencode", c.Key, c.New, secret.Hash(jsonutil.Canonical(structproj.MustJSON(val))))
		case "delete":
			next, rerr := jsonutil.RemoveArrayElem(doc, "plugin", trim(c.Key, "plugin."))
			if rerr != nil {
				return rerr
			}
			// Only count a real removal as a doc change: a delete that merely
			// drops an orphaned state record (element already absent on disk)
			// must not rewrite opencode.jsonc — a rewrite normalizes the JSONC
			// and destroys the user's comments for nothing.
			if !bytes.Equal(next, doc) {
				doc = next
				docChanged = true
			}
			st.Delete("opencode", c.Key)
		default: // create | update
			val, err := res.ResolveJSON(c.New)
			if err != nil {
				return err
			}
			if doc, err = jsonutil.EnsureArrayElem(doc, "plugin", trim(c.Key, "plugin.")); err != nil {
				return err
			}
			docChanged = true
			st.Set("opencode", c.Key, c.New, secret.Hash(jsonutil.Canonical(structproj.MustJSON(val))))
		}
	}
	{
		var ch bool
		doc, ch, err = structproj.Apply("opencode", "setting.", filterChanges(cs.Changes, "setting."), doc, codec, res, st, settingDocPath)
		if err != nil {
			return err
		}
		docChanged = docChanged || ch
	}
	projDoc, projChanged, err := structproj.Apply("opencode", "projmcp.", filterChanges(cs.Changes, "projmcp."), projDoc, codec, res, st, projMCPDocPath)
	if err != nil {
		return err
	}
	{
		var ch bool
		projDoc, ch, err = structproj.Apply("opencode", "projsetting.", filterChanges(cs.Changes, "projsetting."), projDoc, codec, res, st, projSettingDocPath)
		if err != nil {
			return err
		}
		projChanged = projChanged || ch
	}
	tuiDoc, tuiChanged, err := structproj.Apply("opencode", "tui.", filterChanges(cs.Changes, "tui."), tuiDoc, codec, res, st, tuiDocPath)
	if err != nil {
		return err
	}
	// File-projection keys (skill./command./subagent.): adopt records state only;
	// delete removes the managed symlink. Their create/update are handled by the
	// fileproj.ApplyLinks pass below; noop and subagentcopy.* are handled
	// elsewhere. The fallback recovers a de-declared key's on-disk dst at user
	// scope when state lacks a recorded dst, matching the prior inline behavior.
	roots := a.ManagedRoots()
	if err := fileproj.ApplyState("opencode", filterChanges(cs.Changes, "skill."), st, roots, func(k string) string {
		return filepath.Join(a.SkillsDir("user"), trim(k, "skill."))
	}); err != nil {
		return err
	}
	if err := fileproj.ApplyState("opencode", filterChanges(cs.Changes, "command."), st, roots, func(k string) string {
		return filepath.Join(a.CommandsDir("user"), trim(k, "command.")+".md")
	}); err != nil {
		return err
	}
	if err := fileproj.ApplyState("opencode", filterChanges(cs.Changes, "subagent."), st, roots, func(k string) string {
		return filepath.Join(a.SubagentsDir("user"), trim(k, "subagent.")+".md")
	}); err != nil {
		return err
	}
	// Fail fast on link conflicts before writing any file. A conflict in any of
	// the three namespaces must be detected here, before any JSON write or state
	// mutation below — otherwise a command conflict could let Apply partially
	// write opencode.jsonc and commit skill-link state before erroring.
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
			return fmt.Errorf("opencode: %s exists and is not a homonto-managed copy-mode subagent; not overwriting", op.Dst)
		}
	}
	if docChanged {
		if err := fsutil.WriteAtomic(a.cfgFile(), doc); err != nil {
			return err
		}
	}
	if projChanged && a.ProjectRoot != "" {
		if err := fsutil.WriteAtomic(a.projectCfgFile(), projDoc); err != nil {
			return err
		}
	}
	if tuiChanged {
		if err := fsutil.WriteAtomic(a.tuiFile(), tuiDoc); err != nil {
			return err
		}
	}
	// Prune each namespace's inactive-scope orphan (left after a per-resource
	// scope switch), then create the link and record state. Runs after the JSON
	// writes. Only our own managed symlink is ever removed (IsManaged guards it);
	// a foreign file or an absent path is left untouched.
	if err := fileproj.ApplyLinks("opencode", a.SkillFileLinks(), st, roots); err != nil {
		return err
	}
	if err := fileproj.ApplyLinks("opencode", a.CommandFileLinks(), st, roots); err != nil {
		return err
	}
	if err := fileproj.ApplyLinks("opencode", a.SubagentFileLinks(), st, roots); err != nil {
		return err
	}
	// Reconcile copy-mode subagent content files (write/update/prune + state).
	if err := a.ApplyCopySubagents(st); err != nil {
		return err
	}
	return nil
}
