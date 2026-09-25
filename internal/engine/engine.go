package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/adapter/opencode"
	"github.com/noviopenworks/homonto/internal/adapter/registry"
	"github.com/noviopenworks/homonto/internal/agentfm"
	"github.com/noviopenworks/homonto/internal/catalog"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/resourcepath"
	"github.com/noviopenworks/homonto/internal/scaffold"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// sortedRepoNames returns the declared repo names in deterministic order, so
// adapter fan-out order — and plan output — is stable.
func sortedRepoNames(repos map[string]string) []string {
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Engine wires config, adapters, secret resolver, and state for plan/apply.
type Engine struct {
	Cfg                 *config.Config
	Adapters            []adapter.Adapter
	State               *state.State
	StateDir            string
	ContentDir          string
	CatalogRoot         string // materialized builtin catalog root (<stateDir>/catalog/skills)
	CommandCatalogRoot  string // materialized builtin command root (<stateDir>/catalog/commands)
	SubagentCatalogRoot string // materialized builtin subagent root (<stateDir>/catalog/subagents)
	PluginCatalogRoot   string // materialized bundled plugin root (<stateDir>/catalog/plugins)
	RemoteRoot          string // materialized remote content root (<stateDir>/remote)
	RemoteCacheRoot     string // content-addressed remote cache (<stateDir>/cache/remote)
	Home                string
	ConfigPath          string           // exact absolute selected config filename, for every schema
	ProjectRoot         string           // directory of homonto.toml; skill-scope project root
	WorkspaceLayout     workspace.Layout // resolved schema-2 layout, including the exact config filename
	Resolver            *secret.Resolver
	// RepoTargets pairs each declared [repos] repository with its own adapter
	// and state partition (ADR 0024 stage 2): the adapter projects that repo's
	// repo-tagged project-scoped resources into the repo's .opencode/ tree,
	// recording state in <stateDir>/state.<name>.json so pruning, adoption,
	// and drift never cross repositories. Empty for single-repo configs.
	RepoTargets []RepoTarget
	// HomontoVersion is the running binary version, set by the CLI. When set, Plan
	// enforces each declared framework's [compat].homonto range fail-closed; empty
	// (tests/unstamped) skips the check.
	HomontoVersion string
	// Warnings collects non-fatal per-adapter failures from the last Plan (e.g.
	// an unparseable tool file); other tools still proceed.
	Warnings []string
}

// RepoTarget is one declared repository's projection pair.
type RepoTarget struct {
	Name    string          // the [repos] key
	Dir     string          // resolved absolute repository directory
	Adapter adapter.Adapter // opencode adapter in repo mode (Name() = "opencode@<name>")
	State   *state.State    // partition at <stateDir>/state.<name>.json
}

// Build loads config and wires both adapters. home is $HOME; contentDir is the
// local provider root; state lives in <repo>/.homonto next to the config. The
// context bounds the in-Build remote-framework resolution (fetch/verify can
// touch the network) and is propagated to every Resolver.Resolve call.
func Build(ctx context.Context, configPath, home, contentDir string) (*Engine, error) {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
	}
	var layout workspace.Layout
	if cfg.SchemaVersion >= 2 {
		layout, err = workspace.Load(configPath)
		if err != nil {
			return nil, err
		}
		if layout.WorktreesDir != "" {
			for name := range layout.Repos {
				if strings.ContainsAny(name, "*?") {
					return nil, fmt.Errorf("repos.%s: worktree namespace contains OpenCode permission wildcard characters", name)
				}
			}
		}
	}
	// A relative content dir is relative to the config file, not the shell
	// working directory — symlink targets must stay valid from anywhere.
	if !filepath.IsAbs(contentDir) {
		base, err := filepath.Abs(filepath.Dir(configPath))
		if err != nil {
			return nil, err
		}
		contentDir = filepath.Join(base, contentDir)
	}
	// The project root anchors project-scope skill installs — the same directory
	// that already anchors homonto/ and .homonto/ (the config file's directory).
	projectRoot, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return nil, err
	}
	// Anchor state (and the materialized catalog under it) on the absolute
	// projectRoot, not filepath.Dir(configPath): with the default relative
	// --config, the latter is "." and every catalog-skill symlink target would
	// be stored as ".homonto/catalog/skills/<name>" — relative to the *link's*
	// directory (e.g. .opencode/skills/), which dangles. contentDir is
	// absolutized above for the same reason; stateDir must match.
	stateDir := filepath.Join(projectRoot, ".homonto")
	catalogDir := filepath.Join(stateDir, "catalog", "skills")
	commandCatalogDir := filepath.Join(stateDir, "catalog", "commands")
	subagentCatalogDir := filepath.Join(stateDir, "catalog", "subagents")
	pluginCatalogDir := filepath.Join(stateDir, "catalog", "plugins")
	remoteRoot := filepath.Join(stateDir, "remote")
	remoteSubagentDir := filepath.Join(remoteRoot, "subagents")
	remoteCacheRoot := filepath.Join(stateDir, "cache", "remote")
	st, err := state.Load(stateDir)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		Cfg: cfg,
		Adapters: registry.Builtins().Build(registry.Deps{
			Home:               home,
			ContentDir:         contentDir,
			ProjectRoot:        projectRoot,
			CatalogDir:         catalogDir,
			CommandCatalogDir:  commandCatalogDir,
			SubagentCatalogDir: subagentCatalogDir,
			PluginCatalogDir:   pluginCatalogDir,
			RemoteSubagentDir:  remoteSubagentDir,
		}),
		State:               st,
		StateDir:            stateDir,
		ContentDir:          contentDir,
		CatalogRoot:         catalogDir,
		CommandCatalogRoot:  commandCatalogDir,
		SubagentCatalogRoot: subagentCatalogDir,
		PluginCatalogRoot:   pluginCatalogDir,
		RemoteRoot:          remoteRoot,
		RemoteCacheRoot:     remoteCacheRoot,
		Home:                home,
		ConfigPath:          configPath,
		ProjectRoot:         projectRoot,
		WorkspaceLayout:     layout,
		Resolver:            secret.NewResolver(),
	}
	// Resolve any [frameworks.X] source="remote:<url>" through the trust pipeline
	// now that the cache/lock/revocation paths are known, and inject the verified
	// cache dirs so BOTH Plan (framework expansion) and materializeCatalog (which
	// builds via Cfg.FrameworkCatalog()) overlay the remote framework roots. A
	// config with no remote frameworks resolves nothing (no network); a bad,
	// mismatched, or revoked digest fails closed here and aborts Build.
	dirs, err := e.resolveRemoteFrameworks(ctx)
	if err != nil {
		return nil, err
	}
	if len(dirs) > 0 {
		cfg.SetRemoteFrameworkDirs(dirs)
	}
	// Fan out one adapter+state pair per declared repository (ADR 0024 stage
	// 2). Each shares the config repo's materialized catalog roots (links
	// point here, absolute) but projects into its own root with its own state
	// partition. Sorted names keep adapter order — and plan output —
	// deterministic.
	for _, name := range sortedRepoNames(cfg.Repos) {
		st, err := state.LoadNamed(stateDir, name)
		if err != nil {
			return nil, fmt.Errorf("repo %s: %w", name, err)
		}
		a := opencode.New(home, contentDir).
			WithProjectRoot(cfg.RepoDirs()[name]).
			WithCatalogRoot(catalogDir).
			WithCommandCatalogRoot(commandCatalogDir).
			WithSubagentCatalogRoot(subagentCatalogDir).
			WithPluginCatalogRoot(pluginCatalogDir).
			WithRemoteSubagentRoot(remoteSubagentDir).
			WithRepo(name)
		e.RepoTargets = append(e.RepoTargets, RepoTarget{Name: name, Dir: cfg.RepoDirs()[name], Adapter: a, State: st})
	}
	return e, nil
}

// CatalogDir returns the materialized builtin catalog root.
func (e *Engine) CatalogDir() string { return e.CatalogRoot }

// CommandDir returns the materialized builtin command root.
func (e *Engine) CommandDir() string { return e.CommandCatalogRoot }

// SubagentDir returns the materialized builtin subagent root.
func (e *Engine) SubagentDir() string { return e.SubagentCatalogRoot }

// Plan runs each adapter's Plan. An adapter that fails (e.g. its tool file is
// unparseable) is skipped with a warning so the other tools still proceed; its
// file is never written. Warnings from the run are recorded on e.Warnings.
func (e *Engine) Plan() ([]adapter.ChangeSet, error) {
	if err := e.checkFrameworkCompat(); err != nil {
		return nil, err
	}
	e.Warnings = nil
	var sets []adapter.ChangeSet
	for _, a := range e.Adapters {
		cs, err := a.Plan(e.Cfg, e.State)
		if err != nil {
			e.Warnings = append(e.Warnings, fmt.Sprintf("%s skipped: %v", a.Name(), err))
			continue
		}
		sets = append(sets, cs)
	}
	for _, t := range e.RepoTargets {
		cs, err := t.Adapter.Plan(e.Cfg, t.State)
		if err != nil {
			e.Warnings = append(e.Warnings, fmt.Sprintf("%s skipped: %v", t.Adapter.Name(), err))
			continue
		}
		sets = append(sets, cs)
	}
	return sets, nil
}

// Apply is two-phase: resolve every non-noop change's secrets first (abort
// before any write on error), then apply each adapter, saving state after each
// successful adapter so a later failure never loses an earlier one's record.
// The context bounds remote-source fetches during materializeRemotes so the
// caller (typically a cobra command) can interrupt a hung network operation.
func (e *Engine) Apply(ctx context.Context, sets []adapter.ChangeSet) error {
	// Fail closed on a malformed plan before any side effect: an unknown tool
	// (otherwise silently skipped below) or an operation with an undefined action
	// (otherwise a silent no-op) must abort — never quietly drop a change to a
	// user's config files.
	knownTools := make(map[string]bool, len(e.Adapters)+len(e.RepoTargets))
	for _, a := range e.Adapters {
		knownTools[a.Name()] = true
	}
	for _, t := range e.RepoTargets {
		knownTools[t.Adapter.Name()] = true
	}
	for _, cs := range sets {
		if err := cs.Validate(knownTools); err != nil {
			return err
		}
	}
	// Remote materialization can write the shared state root even without
	// declared remotes. Reject redirected catalog/control parents before it runs.
	if err := e.preflightCatalogRoots(); err != nil {
		return err
	}
	for _, link := range e.workflowPluginLinks() {
		if err := e.preflightWorkflowPluginDestination(link); err != nil {
			return err
		}
	}
	for _, cs := range sets {
		for _, c := range cs.Changes {
			// Deletes carry no New value; nothing to resolve. Adopt is non-secret
			// by construction (Plan only emits it for a value without a ${...} ref),
			// so it too has nothing to resolve — the adapter's Apply records its
			// state hash directly from the already-matching on-disk value.
			if c.Action == "noop" || c.Action == "delete" || c.Action == "adopt" {
				continue
			}
			if _, err := e.Resolver.Resolve(c.New); err != nil {
				return err
			}
		}
	}
	// Resolve, verify, and materialize remote sources before any adapter links
	// them. This fetches → validates → pin-matches → caches, aborting the whole
	// apply before any adapter write if any remote resource fails closed.
	if err := e.materializeRemotes(ctx); err != nil {
		return err
	}
	// Materialize builtin skills before any adapter links them, so no symlink is
	// created ahead of its target.
	if err := e.materializeCatalog(); err != nil {
		return err
	}
	// The declared [tmp] directory is workspace surface, not catalog content:
	// create it and keep it gitignored even when nothing builtin is declared
	// (ADR 0048). Unconditional on config, independent of the materialize gate.
	if err := e.ensureTmpSurface(); err != nil {
		return err
	}
	// The workflow bridge is project-local runtime content. It observes the
	// workflow snapshot but never writes state, so it is installed after its
	// catalog source exists and before OpenCode can load the project directory.
	if err := e.ensureWorkflowBridge(); err != nil {
		return err
	}
	// Match each planned set to its adapter by tool name (Plan may have skipped
	// some adapters, so indexes need not line up). The config repo's adapters
	// record into the main state; each repo target's changeset records into
	// that repository's partition, saved immediately after its adapter writes
	// (ADR 0024 stage 2: one apply, per-repo state scoping).
	byName := map[string]adapter.Adapter{}
	for _, a := range e.Adapters {
		byName[a.Name()] = a
	}
	pair := map[string]RepoTarget{}
	for _, t := range e.RepoTargets {
		byName[t.Adapter.Name()] = t.Adapter
		pair[t.Adapter.Name()] = t
	}
	// Provenance recording brackets each adapter's write: origins + last
	// events for live keys, tombstones for deletes, one operation per apply
	// (allocated lazily so a no-op apply records nothing).
	enrich := e.enrichApply()
	for _, cs := range sets {
		a, ok := byName[cs.Tool]
		if !ok {
			continue
		}
		if t, isRepo := pair[cs.Tool]; isRepo {
			// Name the tool in every per-adapter failure: with several adapters
			// an unwrapped error leaves the user guessing which file broke.
			post := enrich(cs, t.State)
			if err := a.Apply(e.Cfg, cs, e.Resolver, t.State); err != nil {
				return fmt.Errorf("%s: %w", cs.Tool, err)
			}
			post()
			// Persist immediately into the repo's own partition.
			if err := t.State.SaveNamed(e.StateDir, t.Name); err != nil {
				return fmt.Errorf("%s: save state: %w", cs.Tool, err)
			}
			continue
		}
		// Name the tool in every per-adapter failure: with several adapters an
		// unwrapped error leaves the user guessing which file broke.
		post := enrich(cs, e.State)
		if err := a.Apply(e.Cfg, cs, e.Resolver, e.State); err != nil {
			return fmt.Errorf("%s: %w", cs.Tool, err)
		}
		post()
		// Persist immediately: a partial apply must keep the record of every
		// adapter that already wrote its files.
		if err := e.State.Save(e.StateDir); err != nil {
			return fmt.Errorf("%s: save state: %w", cs.Tool, err)
		}
	}
	e.recordVersions()
	return e.State.Save(e.StateDir)
}

// recordVersions writes down, in state, the binary and framework versions behind
// this apply — so `homonto update` can report the transition and `onto` can
// detect a binary/framework skew. Best-effort: a catalog that will not load
// leaves framework versions untouched rather than failing the completed apply.
func (e *Engine) recordVersions() {
	e.State.SetHomontoVersion(e.HomontoVersion)
	cl, err := e.Cfg.FrameworkCatalog()
	if err != nil {
		return
	}
	for name, r := range e.Cfg.Frameworks {
		catName, ok := config.FrameworkCatalogName(name, r.Source)
		if !ok {
			continue
		}
		if v, ok := cl.FrameworkVersion(catName); ok {
			e.State.SetFrameworkVersion(name, v)
		}
	}
}

// subagentRenderContext builds the per-tool agentfm render context: each
// subagent's override from [subagents.<name>.<tool>]. There are no role tiers
// anymore — every installed builtin agent MUST declare a non-empty model in
// its per-tool override block, enforced at config load. A tool with no
// overrides yields an empty map (which would fail validation, since an
// installed agent without a model is a load-time error).
//
// Overrides are keyed by the subagent's CATALOG name, not its config key,
// because materialization writes one rendered file per catalog name. Config
// validation rejects multiple installed aliases and conflicting model routes;
// Names supplies that source's single effective host identity separately.
func (e *Engine) subagentRenderContext() (map[string]agentfm.RenderContext, error) {
	return e.subagentRenderContextFor(nil)
}

func (e *Engine) subagentRenderContextFor(targets map[string]map[string]bool) (map[string]agentfm.RenderContext, error) {
	externalDirectories := make([]string, 0, len(e.Cfg.RepoDirs()))
	for _, dir := range e.Cfg.RepoDirs() {
		externalDirectories = append(externalDirectories, dir)
	}
	if e.WorkspaceLayout.ExplicitRepos() && e.WorkspaceLayout.WorktreesDir != "" {
		for name := range e.WorkspaceLayout.Repos {
			externalDirectories = append(externalDirectories, filepath.Join(e.WorkspaceLayout.WorktreesDir, name))
		}
	}
	sort.Strings(externalDirectories)
	externalDirectoriesByAgent := map[string][]string{}
	externalDirectoryDeniesByAgent := map[string][]string{}
	editDirectoryDeniesByAgent := map[string][]string{}
	records := e.Cfg.Workflow.RootOrDefault()
	if e.WorkspaceLayout.ExplicitRepos() {
		records = e.WorkspaceLayout.WorkflowRoot
	} else if !filepath.IsAbs(records) {
		records = filepath.Join(e.ProjectRoot, records)
	}
	recordRoots := []string{records, permissionCanonicalPath(records)}
	// Native record protection follows the builtin role even when installed
	// directly rather than through a framework. It grants no external access.
	for _, agent := range []string{"onto-implementer", "to-implementer"} {
		externalDirectoryDeniesByAgent[agent] = recordRoots
	}
	// ADR 0039/0045: the builtin workflow frameworks render declared-repo
	// access for their writable agents only — the shared homonto primary
	// (ADR 0045 replaced the per-framework onto/to primaries) and the two
	// implementers. Declaring any one of onto, to, or the h companion
	// installs the primary, so any of them enables the rule; read-only
	// specialists never gain external access.
	workflowFrameworkInstalled := false
	for _, resource := range e.Cfg.Frameworks {
		switch resource.Source {
		case "builtin:onto", "builtin:to", "builtin:h":
			workflowFrameworkInstalled = true
		}
	}
	if workflowFrameworkInstalled {
		// Grant only selected skill installations and their desired content roots,
		// never all of HOME or even all user-installed skills.
		if skills, err := e.Cfg.ExpandedSkillEntriesForTool("opencode"); err == nil {
			for _, skill := range skills {
				root := e.ProjectRoot
				if skill.Repo != "" {
					root = e.Cfg.RepoDirs()[skill.Repo]
				}
				installed := filepath.Join(resourcepath.Dir(resourcepath.Skill, "opencode", skill.Resource.Scope, e.Home, root), skill.Name)
				externalDirectories = append(externalDirectories, installed)
				// Resolve the parent, not a stale installed link to an old source.
				externalDirectories = append(externalDirectories, filepath.Join(permissionCanonicalPath(filepath.Dir(installed)), skill.Name))
				var source string
				switch {
				case strings.HasPrefix(skill.Resource.Source, "builtin:"):
					source = filepath.Join(e.CatalogRoot, strings.TrimPrefix(skill.Resource.Source, "builtin:"))
				case strings.HasPrefix(skill.Resource.Source, "local:"):
					source = filepath.Join(e.ContentDir, "skills", strings.TrimPrefix(skill.Resource.Source, "local:"))
				}
				if source != "" {
					externalDirectories = append(externalDirectories, source, permissionCanonicalPath(source))
				}
			}
		}
		for _, agent := range []string{"homonto", "onto-implementer", "to-implementer"} {
			externalDirectoriesByAgent[agent] = externalDirectories
		}
		externalDirectoriesByAgent["homonto"] = append(append([]string(nil), externalDirectories...), recordRoots...)
	}
	names := map[string]string{}
	hosts := map[string]string{}
	if entries, err := e.Cfg.ExpandedSubagentEntriesForTool("opencode"); err == nil {
		for _, entry := range entries {
			if source, ok := config.SubagentCatalogName(entry.Resource.Source); ok {
				names[source] = entry.Name
				if len(externalDirectoryDeniesByAgent[source]) == 0 {
					continue
				}
				// User-scoped workflow agents use this configuration's launch root;
				// a repo-targeted project install instead belongs to that repository.
				directory := e.ProjectRoot
				if entry.Resource.Scope == "project" && entry.Repo != "" {
					directory = e.Cfg.RepoDirs()[entry.Repo]
				}
				host, known := hosts[directory]
				if !known {
					var err error
					host, err = permissionHostWorktree(directory)
					if err != nil {
						return nil, err
					}
					hosts[directory] = host
				}
				for _, root := range recordRoots {
					rel, err := filepath.Rel(host, root)
					if err != nil {
						return nil, fmt.Errorf("permission host %q records %q: %w", host, root, err)
					}
					editDirectoryDeniesByAgent[source] = append(editDirectoryDeniesByAgent[source], rel)
				}
			}
		}
	}
	overrides := func(pick func(config.Subagent) config.ModelRoute) map[string]agentfm.ModelSpec {
		m := map[string]agentfm.ModelSpec{}
		for key, sa := range e.Cfg.Subagents {
			r := pick(sa)
			if !r.IsSet() {
				continue
			}
			// Resolve to the CATALOG name, which is what materialization renders
			// per file. A declared entry carries it in its builtin: source; a
			// tune-only entry names it directly (it retunes a framework's agent,
			// whose config key IS its catalog name).
			name := key
			if !sa.IsTuneOnly() {
				cat, ok := config.SubagentCatalogName(sa.Source)
				if !ok {
					continue // local:/remote: content is not catalog-keyed
				}
				name = cat
			}
			m[name] = agentfm.ModelSpec{Model: r.Model, Variant: r.Variant, Effort: r.Effort, Steps: r.Steps, BashAllowAdd: r.BashAllowAdd}
		}
		return m
	}
	return map[string]agentfm.RenderContext{
		"opencode": {
			Names:                          names,
			Overrides:                      overrides(func(s config.Subagent) config.ModelRoute { return s.OpenCode }),
			ShellProxy:                     e.Cfg.ResolvedTooling().ShellProxy,
			ExternalDirectoriesByAgent:     externalDirectoriesByAgent,
			ExternalDirectoryDeniesByAgent: externalDirectoryDeniesByAgent,
			EditDirectoryDeniesByAgent:     editDirectoryDeniesByAgent,
			Targets:                        targets["opencode"],
		},
	}, nil
}

// materializeCatalog extracts the builtin skills, commands, and subagents the
// config declares into CatalogRoot, CommandCatalogRoot, and SubagentCatalogRoot.
// It is a no-op only when planCatalog finds every input unchanged: the recorded
// catalog version matches the embedded one, the subagent render fingerprint
// matches the config's model routes, and every file a materialize would write
// already exists. The version and fingerprint are recorded (and state saved)
// only after skills, commands, AND subagents all materialize, so an interrupted
// extraction re-materializes on the next apply.
func (e *Engine) materializeCatalog() error {
	p, err := e.planCatalog()
	if err != nil {
		return err
	}
	if p == nil || p.upToDate {
		return nil
	}
	// Validate every rendered agent before publishing any catalog class. This
	// keeps malformed capability intent from leaving skills or commands newer
	// than the subagent catalog that failed to materialize.
	for _, name := range p.subagents {
		if _, err := p.cl.SubagentFiles(name, p.renderCtx); err != nil {
			return err
		}
	}
	if err := p.cl.Materialize(e.CatalogRoot, p.skills, p.shellProxy, p.codeIntel, p.tmpDir, p.workspaceRef); err != nil {
		return err
	}
	if err := p.cl.MaterializeCommands(e.CommandCatalogRoot, p.commands); err != nil {
		return err
	}
	if err := p.cl.MaterializeSubagents(e.SubagentCatalogRoot, p.subagents, p.renderCtx); err != nil {
		return err
	}
	// Bundled plugins are owned catalog content (ADR 0029): materialized with
	// the same version gate, never auto-enabled — the user opts in through
	// [plugins.opencode].
	if err := p.cl.MaterializePlugins(e.PluginCatalogRoot, p.plugins); err != nil {
		return err
	}
	// Plugin directories are replaced during materialization. Publish their
	// config-specific binding before exposing entrypoints or recording success.
	if len(p.workflowBinding) > 0 {
		if err := fsutil.WriteControlPlaneWithin(e.StateDir, e.workflowBindingPath(), p.workflowBinding, 0o644); err != nil {
			return fmt.Errorf("workflow bridge binding: %w", err)
		}
	}
	// GC: the Materialize* calls only ever WRITE declared names, so a renamed or
	// de-declared resource left its old files in the catalog roots forever. That
	// litter was live ammunition, not just clutter — the adapters prefer a
	// <name>.<tool>.md variant when one exists, so a years-old render could win
	// over a future same-named verbatim agent.
	if err := e.preflightCatalogRoots(); err != nil {
		return err
	}
	if err := gcCatalogRoots(e.CatalogRoot, e.CommandCatalogRoot, e.SubagentCatalogRoot, e.PluginCatalogRoot, p); err != nil {
		return err
	}
	e.State.SetCatalogVersion(p.cl.Version())
	e.State.SetRenderFingerprint(p.fingerprint)
	// Save immediately so a later adapter failure still records the completed
	// materialization.
	return e.State.Save(e.StateDir)
}

// ensureTmpSurface creates the declared [tmp] directory and keeps it
// gitignored (ADR 0048). A directory under .homonto/ is already covered by
// the scaffolded /.homonto/ ignore entry; any other location gets its own
// anchored entry, appended to .gitignore (created when absent) via the same
// augment-only rule homonto init uses. homonto never deletes tmp content —
// the directory outliving its files is the feature, not a leak.
func (e *Engine) ensureTmpSurface() error {
	enabled, dir := e.Cfg.ResolvedTmp()
	if !enabled || e.tmpSurfacePresent(dir) {
		return nil
	}
	if err := os.MkdirAll(filepath.Join(e.ProjectRoot, filepath.FromSlash(dir)), 0o755); err != nil {
		return fmt.Errorf("tmp dir: %w", err)
	}
	if dir == ".homonto" || strings.HasPrefix(dir, ".homonto/") {
		return nil
	}
	gitignore := filepath.Join(e.ProjectRoot, ".gitignore")
	want := "/" + dir + "/\n"
	if _, err := os.Stat(gitignore); os.IsNotExist(err) {
		return os.WriteFile(gitignore, []byte(want), 0o644)
	}
	if _, err := scaffold.AugmentGitignore(gitignore, want); err != nil {
		return fmt.Errorf("tmp gitignore: %w", err)
	}
	return nil
}

// tmpSurfacePresent reports whether the declared scratch directory exists and
// is ignored. It is the cheap existence half of the [tmp] contract; the
// content-policy half lives in the generated reference.
func (e *Engine) tmpSurfacePresent(dir string) bool {
	if fi, err := os.Stat(filepath.Join(e.ProjectRoot, filepath.FromSlash(dir))); err != nil || !fi.IsDir() {
		return false
	}
	if dir == ".homonto" || strings.HasPrefix(dir, ".homonto/") {
		return true
	}
	gi, err := os.ReadFile(filepath.Join(e.ProjectRoot, ".gitignore"))
	if err != nil {
		return false
	}
	entry := "/" + dir + "/"
	for _, l := range strings.Split(string(gi), "\n") {
		if strings.TrimSpace(l) == entry {
			return true
		}
	}
	return false
}

// allPluginDirsExist reports whether every bundled plugin directory is
// materialized — the plugin side of the materialize gate.
func allPluginDirsExist(root string, names []string, workflowContext bool) bool {
	for _, n := range names {
		files := []string{"plugin.ts"}
		if n == "permission-observer" {
			files = append(files, "index.ts", "v2.ts")
		}
		if n == workflowBridgePlugin && workflowContext {
			files = append(files, "index.ts", "tui.tsx", "rpc.ts", "v2.ts", "runner.ts", "github.ts", "authorization.ts", "compat.ts")
		}
		for _, file := range files {
			fi, err := os.Lstat(filepath.Join(root, n, file))
			if err != nil || !fi.Mode().IsRegular() {
				return false
			}
		}
	}
	return true
}

// All classes share one preflight: a later unsafe root must not be discovered
// after skills staging, plugin replacement, or another class's GC has written.
// Direct symlink entries (including staging leftovers) are foreign, not garbage
// we may unlink. RemoveAll never follows links inside an owned real directory.
func (e *Engine) preflightCatalogRoots() error {
	for _, root := range []string{e.CatalogRoot, e.CommandCatalogRoot, e.SubagentCatalogRoot, e.PluginCatalogRoot} {
		if err := fsutil.RequireRealParents(e.ProjectRoot, root); err != nil {
			return fmt.Errorf("catalog: unsafe materialization root %q: %w", root, err)
		}
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("catalog: inspecting materialization root %q: %w", root, err)
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("catalog: refusing symlinked materialization entry %q", filepath.Join(root, entry.Name()))
			}
		}
	}
	return nil
}

// gcCatalogRoots removes entries in the materialized catalog roots that no
// declared resource owns: skill directories outside p.skills, command files
// outside p.commands, and subagent files (anchor or per-tool variant) whose
// base name is outside p.subagents. The roots are control-plane directories
// under .homonto — generated content, never user-authored — so pruning the
// undeclared is safe by construction.
func gcCatalogRoots(skillRoot, cmdRoot, subRoot, pluginRoot string, p *catalogPlan) error {
	inSet := func(names []string) map[string]bool {
		m := make(map[string]bool, len(names))
		for _, n := range names {
			m[n] = true
		}
		return m
	}
	skills, cmds, subs := inSet(p.skills), inSet(p.commands), inSet(p.subagents)

	if entries, err := os.ReadDir(skillRoot); err == nil {
		for _, e := range entries {
			if !skills[e.Name()] {
				if err := os.RemoveAll(filepath.Join(skillRoot, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	if entries, err := os.ReadDir(cmdRoot); err == nil {
		for _, e := range entries {
			name := strings.TrimSuffix(e.Name(), ".md")
			if !cmds[name] {
				if err := os.RemoveAll(filepath.Join(cmdRoot, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	if entries, err := os.ReadDir(pluginRoot); err == nil {
		owned := map[string]bool{}
		for _, n := range p.plugins {
			owned[n] = true
		}
		for _, e := range entries {
			if !owned[e.Name()] {
				if err := os.RemoveAll(filepath.Join(pluginRoot, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	if entries, err := os.ReadDir(subRoot); err == nil {
		for _, e := range entries {
			// Owner name: strip .md, then an optional per-tool variant suffix.
			name := strings.TrimSuffix(e.Name(), ".md")
			name = strings.TrimSuffix(name, ".opencode")
			if !subs[name] {
				if err := os.RemoveAll(filepath.Join(subRoot, e.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// catalogPlan is what a materialize would extract, and whether it need bother.
type catalogPlan struct {
	cl        *catalog.Catalog
	skills    []string
	commands  []string
	subagents []string
	plugins   []string
	renderCtx map[string]agentfm.RenderContext
	// shellProxy/codeIntel are the resolved [tooling] providers rendered into
	// each dispatcher skill's generated tooling reference.
	shellProxy string
	codeIntel  string
	// tmpDir is the resolved [tmp] scratch directory ("" when [tmp] is not
	// declared) rendered into the dispatchers and the shared knowledge skill.
	tmpDir          string
	workspaceRef    []byte
	workflowBinding []byte
	fingerprint     string
	upToDate        bool
}

// CatalogNeedsMaterialize reports whether a materialize would do real work. The
// CLI needs this because a catalog file's symlink target is name-based, so
// stale, missing, or mis-rendered catalog content leaves the projection plan
// empty — and an empty plan otherwise skips apply entirely, stranding the
// content forever. (Same shape as the HasRemoteResources carve-out.) An error
// resolving the plan counts as "needs work" so apply runs and surfaces it,
// rather than being silently swallowed here.
func (e *Engine) CatalogNeedsMaterialize() bool {
	if !e.workflowBridgePresent() {
		return true
	}
	// An incomplete [tmp] surface forces the apply path on its own: the CLI
	// short-circuits a no-change apply before Engine.Apply runs, and the
	// surface ensure lives inside Apply (same carve-out class as the
	// symlink-blind empty plan below).
	if enabled, dir := e.Cfg.ResolvedTmp(); enabled && !e.tmpSurfacePresent(dir) {
		return true
	}
	p, err := e.planCatalog()
	if err != nil {
		return true
	}
	return p != nil && !p.upToDate
}

const workflowBridgePlugin = "homonto-workflow"

func (e *Engine) workflowBindingPath() string {
	return filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin, "binding.json")
}

func (e *Engine) workflowBinding() ([]byte, error) {
	if !filepath.IsAbs(e.ConfigPath) || filepath.Clean(e.ConfigPath) != e.ConfigPath || filepath.Dir(e.ConfigPath) != e.ProjectRoot {
		return nil, fmt.Errorf("workflow bridge: invalid selected config identity %q", e.ConfigPath)
	}
	coordinator := "homonto"
	for name, agent := range e.Cfg.Subagents {
		if catalogName, ok := config.SubagentCatalogName(agent.Source); ok && catalogName == "homonto" {
			coordinator = name
		}
	}
	h, githubEnabled := e.Cfg.Frameworks["h"]
	githubEnabled = githubEnabled && h.Source == "builtin:h"
	data, err := json.MarshalIndent(struct {
		Version       int    `json:"version"`
		ConfigPath    string `json:"configPath"`
		ConfigRoot    string `json:"configRoot"`
		Coordinator   string `json:"coordinator"`
		GithubEnabled bool   `json:"githubEnabled"`
	}{1, e.ConfigPath, e.ProjectRoot, coordinator, githubEnabled}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func (e *Engine) workflowBindingPresent(want []byte) bool {
	if len(want) == 0 {
		return true
	}
	path := e.workflowBindingPath()
	if err := fsutil.RequireRealParents(e.StateDir, filepath.Dir(path)); err != nil {
		return false
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && bytes.Equal(data, want)
}

func (e *Engine) workflowBridgeSource() string {
	return filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin, "plugin.ts")
}

func (e *Engine) workflowBridgeDestination() string {
	return filepath.Join(e.ProjectRoot, ".opencode", "plugins", workflowBridgePlugin+".ts")
}

func (e *Engine) workflowContextDestination() string {
	return filepath.Join(e.ProjectRoot, ".opencode", "plugins", workflowBridgePlugin+"-context")
}

type workflowPluginLink struct {
	source      string
	destination string
	enabled     bool
	directory   bool
}

func (e *Engine) workflowPluginLinks() []workflowPluginLink {
	return []workflowPluginLink{
		{source: e.workflowBridgeSource(), destination: e.workflowBridgeDestination(), enabled: e.Cfg.WorkflowBridgeEnabled()},
		{source: filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin, "v2.ts"), destination: e.workflowContextDestination() + ".ts"},
		{source: filepath.Join(e.PluginCatalogRoot, workflowBridgePlugin), destination: e.workflowContextDestination(), enabled: e.Cfg.WorkflowContextEnabled(), directory: true},
	}
}

// Both endpoints move with the project (ADR 0026). Keep the exact relative
// spelling as the owned target; matching a foreign path's suffix is not proof.
func (link workflowPluginLink) target() (string, error) {
	return filepath.Rel(filepath.Dir(link.destination), link.source)
}

// workflowBridgePresent reports whether the configured bridge state has
// converged. A foreign file deliberately remains "not present" so apply can
// name the conflict instead of treating an untrusted replacement as managed.
func (e *Engine) workflowBridgePresent() bool {
	for _, link := range e.workflowPluginLinks() {
		if !e.workflowPluginLinkPresent(link) {
			return false
		}
	}
	return true
}

func (e *Engine) workflowPluginLinkPresent(link workflowPluginLink) bool {
	dst := link.destination
	if err := fsutil.RequireRealParents(e.ProjectRoot, filepath.Dir(dst)); err != nil {
		return false
	}
	if !link.enabled {
		_, err := os.Lstat(dst)
		return os.IsNotExist(err)
	}
	want, err := link.target()
	if err != nil {
		return false
	}
	target, err := os.Readlink(dst)
	return err == nil && target == want
}

// ensureWorkflowBridge creates or removes only the exact symlink homonto owns.
// The source remains in the generated catalog even when disabled, so toggling
// the setting is reversible without a fetch or a copied plugin file.
func (e *Engine) ensureWorkflowBridge() error {
	links := e.workflowPluginLinks()
	for _, link := range links {
		if err := e.ensureWorkflowPluginLink(link, true); err != nil {
			return err
		}
	}
	for _, enabled := range []bool{false, true} {
		for _, link := range links {
			if link.enabled == enabled {
				if err := e.ensureWorkflowPluginLink(link, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (e *Engine) preflightWorkflowPluginDestination(link workflowPluginLink) error {
	if err := fsutil.RequireRealParents(e.ProjectRoot, filepath.Dir(link.destination)); err != nil {
		return fmt.Errorf("workflow bridge: unsafe plugin directory: %w", err)
	}
	want, err := link.target()
	if err != nil {
		return fmt.Errorf("workflow bridge: relative plugin target: %w", err)
	}
	target, err := os.Readlink(link.destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("workflow bridge: %s exists and is not a homonto-managed symlink; not overwriting", link.destination)
	}
	if target != want && target != link.source {
		return fmt.Errorf("workflow bridge: %s points outside homonto's catalog; not replacing", link.destination)
	}
	return nil
}

func (e *Engine) ensureWorkflowPluginLink(link workflowPluginLink, dryRun bool) error {
	dst := link.destination
	if err := fsutil.RequireRealParents(e.ProjectRoot, filepath.Dir(dst)); err != nil {
		return fmt.Errorf("workflow bridge: unsafe plugin directory: %w", err)
	}
	want, err := link.target()
	if err != nil {
		return fmt.Errorf("workflow bridge: relative plugin target: %w", err)
	}
	if !link.enabled {
		target, err := os.Readlink(dst)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("workflow bridge: %s exists and is not a homonto-managed symlink; not removing", dst)
		}
		if target != want && target != link.source {
			return fmt.Errorf("workflow bridge: %s points outside homonto's catalog; not removing", dst)
		}
		if dryRun {
			return nil
		}
		return os.Remove(dst)
	}
	if err := fsutil.RequireRealParents(e.ProjectRoot, filepath.Dir(link.source)); err != nil {
		return fmt.Errorf("workflow bridge: unsafe catalog source: %w", err)
	}
	info, err := os.Lstat(link.source)
	if err != nil {
		return fmt.Errorf("workflow bridge: catalog source missing: %w", err)
	}
	if (link.directory && !info.IsDir()) || (!link.directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("workflow bridge: catalog source has wrong type: %s", link.source)
	}
	if link.directory && !allPluginDirsExist(e.PluginCatalogRoot, []string{workflowBridgePlugin}, true) {
		return fmt.Errorf("workflow bridge: catalog source incomplete: %s", link.source)
	}
	if target, err := os.Readlink(dst); err == nil {
		if target == want {
			return nil
		}
		if target != link.source {
			return fmt.Errorf("workflow bridge: %s points outside homonto's catalog; not replacing", dst)
		}
		if dryRun {
			return nil
		}
		// Migrate only the exact absolute target of this current project. A stale
		// pre-move absolute target has no recorded bridge provenance to authenticate.
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("workflow bridge: migrate absolute link: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("workflow bridge: %s exists and is not a homonto-managed symlink; not overwriting", dst)
	}
	if dryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("workflow bridge: create plugin directory: %w", err)
	}
	if err := os.Symlink(want, dst); err != nil {
		return fmt.Errorf("workflow bridge: link plugin: %w", err)
	}
	return nil
}

// planCatalog resolves the builtin content the config declares and evaluates the
// materialize gate. It returns nil when nothing builtin is declared.
func (e *Engine) planCatalog() (*catalogPlan, error) {
	if err := e.preflightCatalogRoots(); err != nil {
		return nil, err
	}
	skillSet := map[string]bool{}
	cmdSet := map[string]bool{}
	subSet := map[string]bool{}
	pluginSet := map[string]bool{}
	targetedSubagents := map[string]map[string]bool{"opencode": {}}
	for _, tool := range []string{"opencode"} {
		sEntries, err := e.Cfg.ExpandedSkillEntriesForTool(tool)
		if err != nil {
			return nil, err
		}
		for _, entry := range sEntries {
			if strings.HasPrefix(entry.Resource.Source, "builtin:") {
				skillSet[strings.TrimPrefix(entry.Resource.Source, "builtin:")] = true
			}
		}
		cEntries, err := e.Cfg.ExpandedCommandEntriesForTool(tool)
		if err != nil {
			return nil, err
		}
		for _, entry := range cEntries {
			if strings.HasPrefix(entry.Resource.Source, "builtin:") {
				cmdSet[strings.TrimPrefix(entry.Resource.Source, "builtin:")] = true
			}
		}
		saEntries, err := e.Cfg.ExpandedSubagentEntriesForTool(tool)
		if err != nil {
			return nil, err
		}
		for _, entry := range saEntries {
			if strings.HasPrefix(entry.Resource.Source, "builtin:") {
				name := strings.TrimPrefix(entry.Resource.Source, "builtin:")
				subSet[name] = true
				targetedSubagents[tool][name] = true
			}
		}
	}
	// Build the catalog including the config's local frameworks so a
	// local:<path> framework's resources materialize (from their own FS) into
	// the catalog root exactly like a builtin's. With no local frameworks this
	// is the embedded singleton, identical to catalog.New().
	cl, err := e.Cfg.FrameworkCatalog()
	if err != nil {
		return nil, err
	}
	for _, name := range cl.PluginNames() {
		// Framework/resource catalogs retain their bundled plugins. Explicit
		// bundled-plugin-only configs also need content before adapter projection.
		if len(skillSet)+len(cmdSet)+len(subSet) > 0 {
			pluginSet[name] = true
		}
		for _, plugin := range e.Cfg.Plugins.OpenCode {
			if plugin.IsEnabled() && plugin.Source == name {
				pluginSet[name] = true
			}
		}
	}
	if e.Cfg.WorkflowBridgeEnabled() || e.Cfg.WorkflowContextEnabled() {
		pluginSet[workflowBridgePlugin] = true
	}
	if len(skillSet)+len(cmdSet)+len(subSet)+len(pluginSet) == 0 {
		return nil, nil
	}
	skillNames := make([]string, 0, len(skillSet))
	for n := range skillSet {
		skillNames = append(skillNames, n)
	}
	sort.Strings(skillNames)
	cmdNames := make([]string, 0, len(cmdSet))
	for n := range cmdSet {
		cmdNames = append(cmdNames, n)
	}
	sort.Strings(cmdNames)
	subNames := make([]string, 0, len(subSet))
	for n := range subSet {
		subNames = append(subNames, n)
	}
	sort.Strings(subNames)

	// Gate on every input the materialized bytes are derived from:
	//   - the base catalog version (an embedded-catalog upgrade),
	//   - the render fingerprint (model routes + per-subagent overrides — a
	//     route change would otherwise freeze rendered agents at their old
	//     model while the catalog stayed identical),
	//   - the CONTENT fingerprint (the source bytes of every declared resource
	//     — a local: framework's edited skill or a remote: framework's repinned
	//     digest changes overlay content while the version stays put; a
	//     version-only gate served the stale bytes forever, and repinning is
	//     how a patched resource ships),
	//   - and the presence of every file a materialize would write.
	renderCtx, err := e.subagentRenderContextFor(targetedSubagents)
	if err != nil {
		return nil, err
	}
	pluginNames := make([]string, 0, len(pluginSet))
	for n := range pluginSet {
		pluginNames = append(pluginNames, n)
	}
	sort.Strings(pluginNames)
	contentFP, err := cl.ContentFingerprint(skillNames, cmdNames, subNames, pluginNames)
	if err != nil {
		return nil, err
	}
	//   - and the TOOLING fingerprint (the resolved [tooling] providers plus the
	//     bytes of the two selected fragments — editing [tooling] leaves the
	//     catalog version and every resource byte untouched, so without this the
	//     gate would report up to date and serve a stale tooling reference
	//     forever, the same defect class the content fingerprint closed).
	tooling := e.Cfg.ResolvedTooling()
	toolingFP, err := cl.ToolingFingerprint(tooling.ShellProxy, tooling.CodeIntel)
	if err != nil {
		return nil, err
	}
	//   - and the TMP fingerprint (whether [tmp] is declared plus the rendered
	//     reference bytes — same staleness class the tooling fingerprint
	//     closes: a config-only edit must re-render the generated reference).
	tmpEnabled, tmpDir := e.Cfg.ResolvedTmp()
	if !tmpEnabled {
		tmpDir = ""
	}
	tmpFP := catalog.TmpFingerprint(tmpEnabled, tmpDir)
	workspaceRef := catalog.RenderWorkspace(e.WorkspaceLayout)
	fingerprint := renderFingerprint(renderCtx) + ":" + contentFP + ":" + toolingFP + ":" + tmpFP
	if len(workspaceRef) > 0 {
		workspaceFP := sha256.Sum256(workspaceRef)
		fingerprint += ":" + hex.EncodeToString(workspaceFP[:])
	}
	var workflowBinding []byte
	if pluginSet[workflowBridgePlugin] {
		workflowBinding, err = e.workflowBinding()
		if err != nil {
			return nil, err
		}
		bindingFP := sha256.Sum256(workflowBinding)
		fingerprint += ":" + hex.EncodeToString(bindingFP[:])
	}
	upToDate := e.State.CatalogVersionRecorded() == cl.Version() &&
		e.State.RenderFingerprintRecorded() == fingerprint &&
		allSkillDirsExist(e.CatalogRoot, skillNames, cl, tmpDir) &&
		allWorkspaceReferencesExist(e.CatalogRoot, skillNames, workspaceRef) &&
		allCommandFilesExist(e.CommandCatalogRoot, cmdNames) &&
		allSubagentFilesExist(e.SubagentCatalogRoot, subNames, cl, renderCtx) &&
		allPluginDirsExist(e.PluginCatalogRoot, pluginNames, e.Cfg.WorkflowContextEnabled()) &&
		e.workflowBindingPresent(workflowBinding)
	return &catalogPlan{
		cl:              cl,
		skills:          skillNames,
		commands:        cmdNames,
		subagents:       subNames,
		plugins:         pluginNames,
		renderCtx:       renderCtx,
		shellProxy:      tooling.ShellProxy,
		codeIntel:       tooling.CodeIntel,
		tmpDir:          tmpDir,
		workspaceRef:    workspaceRef,
		workflowBinding: workflowBinding,
		fingerprint:     fingerprint,
		upToDate:        upToDate,
	}, nil
}

// allSkillDirsExist reports whether every declared skill is materialized. For a
// dispatcher skill it also requires the generated tooling reference: the
// directory alone existing would otherwise mask a hand-deleted reference behind
// an up-to-date fingerprint, leaving the skill pointing at a file that is not
// there. The same holds for the generated tmp reference ([tmp] declared) on
// dispatchers and the shared knowledge skill.
func allSkillDirsExist(root string, names []string, cl *catalog.Catalog, tmpDir string) bool {
	for _, n := range names {
		fi, err := os.Stat(filepath.Join(root, n))
		if err != nil || !fi.IsDir() {
			return false
		}
		if cl.IsDispatcher(n) {
			ref := filepath.Join(root, n, filepath.FromSlash(catalog.ToolingReferencePath))
			if st, err := os.Stat(ref); err != nil || st.IsDir() {
				return false
			}
		}
		if tmpDir != "" && (cl.IsDispatcher(n) || n == catalog.SharedKnowledgeSkill) {
			ref := filepath.Join(root, n, filepath.FromSlash(catalog.TmpReferencePath))
			if st, err := os.Stat(ref); err != nil || st.IsDir() {
				return false
			}
		}
	}
	return true
}

func allWorkspaceReferencesExist(root string, names []string, ref []byte) bool {
	if len(ref) == 0 {
		return true
	}
	for _, name := range names {
		if catalog.HasWorkspaceReference(name) {
			fi, err := os.Stat(filepath.Join(root, name, filepath.FromSlash(catalog.WorkspaceReferencePath)))
			if err != nil || !fi.Mode().IsRegular() {
				return false
			}
		}
	}
	return true
}

func allCommandFilesExist(root string, names []string) bool {
	for _, n := range names {
		fi, err := os.Stat(filepath.Join(root, n+".md"))
		if err != nil || fi.IsDir() {
			return false
		}
	}
	return true
}

// renderFingerprint digests every render input — each tool's
// per-subagent overrides, model + variant + effort — deterministically, so
// the materialize gate re-renders exactly when something the agents are
// stamped from actually changed. Every field the render reads must be digested
// here: one omitted field is one config edit that silently never reaches the
// agent.
//
// Sorted keys keep it stable across map iteration order; delimited fields keep
// it unambiguous across values (an "a"+"bc" / "ab"+"c" collision would skip the
// very re-render this gate exists to trigger).
func renderFingerprint(ctx map[string]agentfm.RenderContext) string {
	h := sha256.New()
	digestSpecs := func(kind, tool string, specs map[string]agentfm.ModelSpec) {
		keys := make([]string, 0, len(specs))
		for k := range specs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s := specs[k]
			fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00", kind, tool, k, s.Model, s.Variant, s.Effort)
			if s.Steps != nil {
				fmt.Fprintf(h, "steps\x00%d\x00", *s.Steps)
			}
			for _, add := range s.BashAllowAdd {
				fmt.Fprintf(h, "bashadd\x00%s\x00", add)
			}
		}
	}
	tools := make([]string, 0, len(ctx))
	for tool := range ctx {
		tools = append(tools, tool)
	}
	sort.Strings(tools)
	for _, tool := range tools {
		names := make([]string, 0, len(ctx[tool].Names))
		for source := range ctx[tool].Names {
			names = append(names, source)
		}
		sort.Strings(names)
		for _, source := range names {
			fmt.Fprintf(h, "alias\x00%s\x00%s\x00%s\x00", tool, source, ctx[tool].Names[source])
		}
		digestSpecs("override", tool, ctx[tool].Overrides)
		fmt.Fprintf(h, "shell-proxy\x00%s\x00%s\x00", tool, ctx[tool].ShellProxy)
		for _, group := range []struct {
			kind string
			dirs map[string][]string
		}{
			{"external-dir", ctx[tool].ExternalDirectoriesByAgent},
			{"external-dir-deny", ctx[tool].ExternalDirectoryDeniesByAgent},
			{"edit-dir-deny", ctx[tool].EditDirectoryDeniesByAgent},
		} {
			agents := make([]string, 0, len(group.dirs))
			for agent := range group.dirs {
				agents = append(agents, agent)
			}
			sort.Strings(agents)
			for _, agent := range agents {
				fmt.Fprintf(h, "%s-managed\x00%s\x00%s\x00", group.kind, tool, agent)
				dirs := append([]string(nil), group.dirs[agent]...)
				sort.Strings(dirs)
				for i, dir := range dirs {
					if i > 0 && dir == dirs[i-1] {
						continue
					}
					fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00", group.kind, tool, agent, dir)
				}
			}
		}
		targets := make([]string, 0, len(ctx[tool].Targets))
		for name := range ctx[tool].Targets {
			targets = append(targets, name)
		}
		sort.Strings(targets)
		for _, name := range targets {
			fmt.Fprintf(h, "target\x00%s\x00%s\x00", tool, name)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// allSubagentFilesExist reports whether every file a materialize would write is
// present — the shared anchor AND each per-tool rendered variant. Checking only
// the <name>.md anchor would leave a deleted variant unrepaired: the anchor
// still exists, so the gate short-circuits and the tool keeps a symlink pointing
// at a file that is never rewritten.
func allSubagentFilesExist(root string, names []string, cl *catalog.Catalog, renderCtx map[string]agentfm.RenderContext) bool {
	for _, n := range names {
		files, err := cl.SubagentFiles(n, renderCtx)
		if err != nil {
			return false
		}
		for _, f := range files {
			fi, err := os.Stat(filepath.Join(root, f))
			if err != nil || fi.IsDir() {
				return false
			}
		}
	}
	return true
}
