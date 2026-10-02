// Package claude projects desired config into Claude Code's files. It is the
// claude target's adapter (ADR 0066): an explicit, opt-in projection target
// alongside opencode, built by the engine only when the config names the
// claude target.
//
// Current surface (the first slice): stdio MCP servers. User-scoped servers
// land in Claude's MCP registry (~/.claude.json, or $CLAUDE_CONFIG_DIR/
// .claude.json when that override is absolute); project-scoped servers land in
// the config repo's .mcp.json. Skills/commands/subagents cannot target claude
// yet (config rejects the target), but their file-projection plumbing is wired
// through baseadapter so state records left by pre-v0.13.0 claude installs
// reconcile through the normal managed-link contract once the adapter is
// activated instead of dangling forever.
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

// ConfigDir returns Claude Code's configuration directory: $CLAUDE_CONFIG_DIR
// when set to an absolute path, otherwise <home>/.claude. Exported so the
// engine's doctor and the adapter agree on where claude's files live.
func ConfigDir(home string) string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(home, ".claude")
}

// claudeJSON returns Claude Code's MCP registry document. Without an override
// it is the ~/.claude.json file at the home root; with an absolute
// $CLAUDE_CONFIG_DIR Claude keeps its state files inside that directory.
func (a *Adapter) claudeJSON() string {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); filepath.IsAbs(dir) {
		return filepath.Join(dir, ".claude.json")
	}
	return filepath.Join(a.Home, ".claude.json")
}

// New builds a Claude adapter at user scope. home is $HOME; content holds
// owned skills. Use WithProjectRoot to install project-scope resources.
func New(home, content string) *Adapter {
	return &Adapter{Base: baseadapter.Base{
		Tool:          "claude",
		VariantSuffix: ".claude.md",
		Home:          home,
		Content:       content,
	}}
}

// WithProjectRoot sets the project root (the homonto.toml directory). It is
// used for project-scope resource placement; project-scoped MCP servers use
// the project .mcp.json.
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
// remote:<url> subagents link from. Mirrors WithSubagentCatalogRoot.
func (a *Adapter) WithRemoteSubagentRoot(remoteSubagentRoot string) *Adapter {
	a.Base.RemoteSubagentRoot = remoteSubagentRoot
	return a
}

// WithRepo puts the adapter in repo mode (ADR 0024 stage 2): it projects only
// repo-tagged project-scoped resources into the repo's files (.mcp.json for
// MCPs), recording under the "claude" namespace of that repository's state
// partition. Built by the engine's per-(tool, repo) fan-out whenever the
// config targets claude or the partition carries claude records.
func (a *Adapter) WithRepo(repo string) *Adapter {
	a.Base.RepoName = repo
	return a
}

// projectMCPJSON is Claude Code's project MCP file (.mcp.json at the project
// root), merged over the user-level servers after the user approves it.
func (a *Adapter) projectMCPJSON() string {
	return filepath.Join(a.ProjectRoot, ".mcp.json")
}

// readProjectMCP reads the project MCP document, or an empty root when no
// project root is known — recorded projmcp.* keys still prune cleanly
// (state-only) without inventing a relative ".mcp.json" path to read.
func (a *Adapter) readProjectMCP() ([]byte, error) {
	if a.ProjectRoot == "" {
		return jsonutil.Standardize(nil)
	}
	return readStandardized(a.projectMCPJSON())
}

// mcpValue renders one declared server as Claude's mcpServers entry, or
// ok=false when there is nothing runnable to project for this tool. Claude
// Code's stdio schema: command is a string with a separate args array and a
// flat env map. Empty args/env are EMITTED as [] / {} — Claude Code 2.1.285
// writes them for a bare command (pinned in testdata/), and matching its
// bytes lets a claude-written entry adopt as already-desired instead of being
// rewritten on first apply.
func mcpValue(m config.MCP) (string, bool) {
	if !baseadapter.MCPProjected(m, "claude") {
		return "", false
	}
	args := []string{}
	if len(m.Command) > 1 {
		args = m.Command[1:]
	}
	env := map[string]string{}
	for k, v := range m.Env {
		env[k] = v
	}
	obj := map[string]any{"type": "stdio", "command": m.Command[0], "args": args, "env": env}
	return structproj.MustJSON(obj), true
}

// desiredMCPs maps the user-scoped servers to their mcp.* state keys (the
// global ~/.claude.json). Project-scoped servers fall back here only when no
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

// desiredProjectMCPs maps the project-scoped servers tagged at this adapter's
// repo ("" = the config repo) to their projmcp.* state keys — the same
// mcpServers entries, written into that repository's .mcp.json instead, so
// project servers don't run in every other session.
func (a *Adapter) desiredProjectMCPs(c *config.Config) map[string]string {
	out := map[string]string{}
	if a.ProjectRoot == "" {
		return out
	}
	for name, m := range c.MCPs {
		if m.ScopeOrDefault() != "project" || m.Repo != a.RepoName {
			continue
		}
		if v, ok := mcpValue(m); ok {
			out["projmcp."+name] = v
		}
	}
	return out
}

// Document-path mappings for the structured-document namespaces, threaded
// into structproj.Project/Apply/Observe. Config-supplied names are escaped so
// a name containing dots/@/|/# addresses the literal key rather than nesting.
func mcpDocPath(key string) string {
	return "mcpServers." + jsonutil.EscapePath(trim(key, "mcp."))
}
func projMCPDocPath(key string) string {
	return "mcpServers." + jsonutil.EscapePath(trim(key, "projmcp."))
}

func (a *Adapter) Plan(c *config.Config, st *state.State) (adapter.ChangeSet, error) {
	if err := a.Expand(c); err != nil {
		return adapter.ChangeSet{}, err
	}
	// Repo mode keeps the shape of opencode's: global files (the user MCP
	// registry) belong to the config-repo adapter alone.
	global := a.RepoName == ""
	var doc []byte
	var err error
	if global {
		if doc, err = readStandardized(a.claudeJSON()); err != nil {
			return adapter.ChangeSet{}, err
		}
	}
	projDoc, err := a.readProjectMCP()
	if err != nil {
		return adapter.ChangeSet{}, err
	}
	cs := adapter.ChangeSet{Tool: a.Name()}
	codec := jsoncodec.Codec{}
	// Registry-relocation guard: when an absolute $CLAUDE_CONFIG_DIR moves
	// the MCP registry away from $HOME/.claude.json, previously managed
	// servers stay recorded in state but would silently strand in the old
	// file — possibly with resolved credential env still active there. Detect
	// the stranding (recorded entries still living in the old registry,
	// matching their applied hashes) and refuse the plan naming both paths.
	// The reverse direction (unsetting an override) is undetectable — the old
	// path left with the environment — and stays a documented limitation.
	if global {
		if err := a.checkRegistryRelocation(st); err != nil {
			return adapter.ChangeSet{}, err
		}
	}
	if global {
		if changes, err := structproj.Project("claude", "mcp.", a.desiredMCPs(c), doc, st, codec, mcpDocPath); err != nil {
			return adapter.ChangeSet{}, err
		} else {
			cs.Changes = append(cs.Changes, changes...)
		}
	}
	if changes, err := structproj.Project("claude", "projmcp.", a.desiredProjectMCPs(c), projDoc, st, codec, projMCPDocPath); err != nil {
		return adapter.ChangeSet{}, err
	} else {
		cs.Changes = append(cs.Changes, changes...)
	}
	// File-projection namespaces go through the shared symlink contract: each
	// Project call emits create/relocate/relink + adopt for its links and plans
	// NO deletes — the generic delete loop below stays the single source of
	// file-prefix deletes. No skill/command/subagent can target claude yet, so
	// these are empty for fresh configs; they exist so state records left by
	// pre-v0.13.0 claude installs reconcile through the managed-link contract.
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
			cs.Changes = append(cs.Changes, adapter.Change{Action: "create", Key: "subagentcopy." + name, New: op.Dst, Cause: adapter.CauseDeclare})
		case copyfile.Update, copyfile.LocalEdit:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "update", Key: "subagentcopy." + name, New: op.Dst, Cause: adapter.CauseUpdate})
		case copyfile.Prune:
			cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: "subagentcopy." + name, Old: op.Dst, Cause: adapter.CauseRemove})
		}
	}
	// Orphans: a state key no longer declared in config is de-declared — plan
	// a delete. (A declared key missing from disk is drift, handled above.)
	// Old is always redacted: a removed key's provenance is stale by definition.
	declared := map[string]bool{}
	for k := range a.desiredMCPs(c) {
		declared[k] = true
	}
	for k := range a.desiredProjectMCPs(c) {
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
	// Structured namespaces this adapter does not manage (yet) are retired as
	// state-only deletes — the same treatment opencode gives obsolete tui.*
	// keys. A pre-v0.13.0 config may still carry claude setting./plugin./
	// pluginconfig./marketplace. records; without this pass they would sit in
	// state forever as phantom drift. The on-disk values are NOT touched:
	// homonto does not manage those files in this slice, so they become
	// ordinary unmanaged content, preserved per the ownership contract.
	if global {
		for _, key := range st.Keys("claude") {
			if staleStructuredKey(key) {
				cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: key, Old: adapter.SecretRedaction, Cause: adapter.CauseRemove})
			}
		}
	}
	// The generic prune covers the file-projection prefixes; the structured
	// prefixes (mcp./projmcp.) are pruned by their structproj.Project calls
	// above (avoiding a double delete), and subagentcopy.* by its own pass.
	for _, k := range st.Keys("claude") {
		if declared[k] || !managedPrefix(k) {
			continue
		}
		cs.Changes = append(cs.Changes, adapter.Change{Action: "delete", Key: k, Old: adapter.SecretRedaction, Cause: adapter.CauseRemove})
	}
	// Keys come from map iteration (random order); a plan must render the
	// same way every run. Keys are unique within a changeset.
	sort.SliceStable(cs.Changes, func(i, j int) bool { return cs.Changes[i].Key < cs.Changes[j].Key })
	return cs, nil
}

// ObserveHashes hashes the current on-disk value of every recorded key still
// present, so an unchanged key reproduces its Entry.Applied. Only hashes
// escape — raw values (possibly resolved secrets) never leave the adapter.
func (a *Adapter) ObserveHashes(st *state.State) (map[string]string, error) {
	var doc []byte
	var err error
	if a.RepoName == "" {
		if doc, err = readStandardized(a.claudeJSON()); err != nil {
			return nil, err
		}
	}
	codec := jsoncodec.Codec{}
	out := map[string]string{}
	if obs, err := structproj.Observe("claude", "mcp.", doc, st, codec, mcpDocPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	projDoc, err := a.readProjectMCP()
	if err != nil {
		return nil, err
	}
	if obs, err := structproj.Observe("claude", "projmcp.", projDoc, st, codec, projMCPDocPath); err != nil {
		return nil, err
	} else {
		for k, v := range obs {
			out[k] = v
		}
	}
	// File-projection keys (skill./command./subagent.*) live on disk as
	// symlinks; each re-hashes its recorded link through the shared contract,
	// reading at the recorded dst so a pending scope switch is not misread as
	// drift.
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
		// A copy-mode subagent lives on disk as a real file; its Applied is
		// the content hash and Desired holds the dst path.
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
	global := a.RepoName == ""
	var doc []byte
	var err error
	if global {
		if doc, err = readStandardized(a.claudeJSON()); err != nil {
			return err
		}
	}
	codec := jsoncodec.Codec{}
	doc, docChanged, err := structproj.Apply("claude", "mcp.", filterChanges(cs.Changes, "mcp."), doc, codec, res, st, mcpDocPath)
	if err != nil {
		return err
	}
	projDoc, err := a.readProjectMCP()
	if err != nil {
		return err
	}
	projDoc, projChanged, err := structproj.Apply("claude", "projmcp.", filterChanges(cs.Changes, "projmcp."), projDoc, codec, res, st, projMCPDocPath)
	if err != nil {
		return err
	}
	// Retire state records of structured namespaces this adapter does not
	// manage (see Plan). State-only: no file is read or written for them.
	if global {
		for _, ch := range cs.Changes {
			if ch.Action == "delete" && staleStructuredKey(ch.Key) {
				st.Delete("claude", ch.Key)
			}
		}
	}
	// File-projection keys (skill./command./subagent.): adopt records state
	// only; delete removes the managed symlink. Their create/update are
	// handled by the fileproj.ApplyLinks pass below. The fallback recovers a
	// de-declared key's on-disk dst at user scope when state lacks a recorded
	// dst, mirroring opencode's recovery of pre-relocation records.
	roots := a.ManagedRoots()
	if err := fileproj.ApplyState("claude", filterChanges(cs.Changes, "skill."), st, roots, func(k string) []string {
		name := trim(k, "skill.")
		return []string{filepath.Join(a.SkillsDir("user"), name), filepath.Join(a.SkillsDir("project"), name)}
	}); err != nil {
		return err
	}
	if err := fileproj.ApplyState("claude", filterChanges(cs.Changes, "command."), st, roots, func(k string) []string {
		name := trim(k, "command.") + ".md"
		return []string{filepath.Join(a.CommandsDir("user"), name), filepath.Join(a.CommandsDir("project"), name)}
	}); err != nil {
		return err
	}
	if err := fileproj.ApplyState("claude", filterChanges(cs.Changes, "subagent."), st, roots, func(k string) []string {
		name := trim(k, "subagent.") + ".md"
		return []string{filepath.Join(a.SubagentsDir("user"), name), filepath.Join(a.SubagentsDir("project"), name)}
	}); err != nil {
		return err
	}
	// Fail fast on link conflicts before writing any file, so a conflict
	// cannot let Apply partially write the MCP document first.
	if err := fileproj.Conflicts("claude", a.SkillFileLinks(), st, roots); err != nil {
		return err
	}
	if err := fileproj.Conflicts("claude", a.CommandFileLinks(), st, roots); err != nil {
		return err
	}
	if err := fileproj.Conflicts("claude", a.SubagentFileLinks(), st, roots); err != nil {
		return err
	}
	copyOps, err := a.PlanCopyOps(st)
	if err != nil {
		return err
	}
	for _, op := range copyOps {
		if op.Action == copyfile.Conflict {
			return fmt.Errorf("claude: %s exists and is not a homonto-managed copy-mode subagent; not overwriting", op.Dst)
		}
	}
	// Write a tool file only when a managed key living in it actually changed;
	// adopt/noop are state-only and leave files byte-for-byte untouched.
	if docChanged && global {
		if err := fsutil.WriteAtomic(a.claudeJSON(), doc); err != nil {
			return err
		}
	}
	if projChanged && a.ProjectRoot != "" {
		if err := fsutil.WriteAtomic(a.projectMCPJSON(), projDoc); err != nil {
			return err
		}
	}
	if err := fileproj.ApplyLinks("claude", a.SkillFileLinks(), st, roots); err != nil {
		return err
	}
	if err := fileproj.ApplyLinks("claude", a.CommandFileLinks(), st, roots); err != nil {
		return err
	}
	if err := fileproj.ApplyLinks("claude", a.SubagentFileLinks(), st, roots); err != nil {
		return err
	}
	if err := a.ApplyCopySubagents(st); err != nil {
		return err
	}
	return nil
}
