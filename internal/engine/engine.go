package engine

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Novirmor/homonto/internal/adapter"
	"github.com/Novirmor/homonto/internal/adapter/claude"
	"github.com/Novirmor/homonto/internal/adapter/opencode"
	"github.com/Novirmor/homonto/internal/adapter/registry"
	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/routing"
	"github.com/Novirmor/homonto/internal/secret"
	"github.com/Novirmor/homonto/internal/state"
	"github.com/Novirmor/homonto/internal/workspace"
)

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
	// renderedExternal holds the rendered local:/remote: subagents that carry
	// a homonto: intent block, keyed by config name (ADR 0096).
	renderedExternal map[string]map[string][]byte
	// RouteDiscoverer lists the harness's models for auto routing policies
	// (ADR 0100); nil runs `opencode models` in the project root.
	RouteDiscoverer routing.Discoverer
	routeReport     *RouteReport
	routeErr        error
	// otherRoutes caches the resolved routing of every harness but OpenCode.
	otherRoutes map[string]RouteReport
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
	// Resolve any [engines.X] source="remote:<url>" through the trust pipeline
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
		// Remote packages' service requirements are known only now.
		if err := cfg.ResolvePackages(); err != nil {
			return nil, err
		}
	}
	// Fan out one adapter+state pair per declared repository (ADR 0024 stage
	// 2). Each shares the config repo's materialized catalog roots (links
	// point here, absolute) but projects into its own root with its own state
	// partition. Sorted names keep adapter order — and plan output —
	// deterministic.
	for _, name := range slices.Sorted(maps.Keys(cfg.Repos)) {
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
	e.configureHarnesses()
	if err := e.migrateRepoStateKeys(); err != nil {
		return nil, err
	}
	if err := e.validateExternalSubagents(); err != nil {
		return nil, err
	}
	return e, nil
}

// configureHarnesses keeps only the adapters of targeted harnesses (or of
// harnesses whose earlier projection still has state to prune) and binds the
// Claude Code runtime integration (M4.4). OpenCode keeps its place first.
func (e *Engine) configureHarnesses() {
	kept := e.Adapters[:0]
	for _, a := range e.Adapters {
		if a.Name() != "opencode" && !e.Cfg.TargetsHarness(a.Name()) && len(e.State.Keys(a.Name())) == 0 {
			continue
		}
		if c, ok := a.(*claude.Adapter); ok {
			e.bindClaude(c)
		}
		kept = append(kept, a)
	}
	e.Adapters = kept
	if !e.Cfg.TargetsHarness("claude") {
		return
	}
	// Repository adapters for different harnesses share one state.<repo>.json
	// partition. Reuse the OpenCode target's loaded instance so applying Claude
	// cannot later save a stale copy over the OpenCode records (or vice versa).
	repoStates := make(map[string]*state.State, len(e.RepoTargets))
	for _, target := range e.RepoTargets {
		repoStates[target.Name] = target.State
	}
	for _, name := range slices.Sorted(maps.Keys(e.Cfg.Repos)) {
		st := repoStates[name]
		if st == nil {
			var err error
			st, err = state.LoadNamed(e.StateDir, name)
			if err != nil {
				continue
			}
		}
		a := claude.New(e.Home, e.ContentDir).
			WithProjectRoot(e.Cfg.RepoDirs()[name]).
			WithCatalogRoot(e.CatalogRoot).
			WithCommandCatalogRoot(e.CommandCatalogRoot).
			WithSubagentCatalogRoot(e.SubagentCatalogRoot).
			WithRemoteSubagentRoot(filepath.Join(e.RemoteRoot, "subagents")).
			WithRepo(name)
		e.RepoTargets = append(e.RepoTargets, RepoTarget{Name: name, Dir: e.Cfg.RepoDirs()[name], Adapter: a, State: st})
	}
}

// bindClaude enables the agent-policy hook whenever Claude Code is targeted,
// and the full workflow integration when the workflow runtime is enabled.
func (e *Engine) bindClaude(c *claude.Adapter) {
	if !e.Cfg.TargetsHarness("claude") {
		return
	}
	c.WithPolicyHook(true)
	if !e.Cfg.WorkflowContextEnabled() {
		return
	}
	hosts := slices.Sorted(maps.Keys(e.Cfg.HostingProviders()))
	coordinator := ""
	if entries, err := e.Cfg.ExpandedSubagentEntriesForTool("claude"); err == nil {
		for _, entry := range entries {
			if name, ok := config.SubagentCatalogName(entry.Resource.Source); ok && name == catalog.SharedCoordinatorAgent {
				coordinator = entry.Name
			}
		}
	}
	c.WithWorkflow(&claude.Workflow{ConfigPath: e.ConfigPath, Hosts: hosts, Coordinator: coordinator})
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
	if err := e.checkHarnessCapabilities(); err != nil {
		return nil, err
	}
	e.Warnings = nil
	// Rendered external agents may obtain their model from an auto policy.
	// Resolve that policy as part of planning, not engine construction, so
	// non-planning commands never invoke model discovery.
	if err := e.refreshExternalRenders(); err != nil {
		return nil, err
	}
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
	routes, err := e.Routes()
	if err != nil {
		return nil, err
	}
	e.Warnings = append(e.Warnings, routes.routeWarnings()...)
	return sets, nil
}

// Apply is two-phase: resolve every non-noop change's secrets first (abort
// before any write on error), then apply each adapter, saving state after each
// successful adapter so a later failure never loses an earlier one's record.
// The context bounds remote-source fetches during materializeRemotes so the
// caller (typically a cobra command) can interrupt a hung network operation.
func (e *Engine) Apply(ctx context.Context, sets []adapter.ChangeSet) error {
	if err := e.preflightApply(sets); err != nil {
		return err
	}
	// Resolve, verify, and materialize remote sources before any adapter links
	// them. This fetches → validates → pin-matches → caches, aborting the whole
	// apply before any adapter write if any remote resource fails closed.
	if err := e.materializeRemotes(ctx); err != nil {
		return err
	}
	// Remote subagent content exists only now; render any that carry intent.
	if err := e.refreshExternalRenders(); err != nil {
		return err
	}
	// Materialize builtin skills before any adapter links them, so no symlink is
	// created ahead of its target.
	if err := e.materializeCatalog(); err != nil {
		return err
	}
	if err := e.writeRenderedSubagents(); err != nil {
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
	if err := e.applyAdapters(sets); err != nil {
		return err
	}
	e.recordVersions()
	if err := e.recordRoutes(); err != nil {
		return fmt.Errorf("record routing: %w", err)
	}
	return e.State.Save(e.StateDir)
}

// preflightApply fails closed before any side effect: on a malformed plan (an
// unknown tool, otherwise silently skipped, or an undefined action, otherwise
// a silent no-op), a redirected catalog or plugin destination, or a secret
// that does not resolve.
func (e *Engine) preflightApply(sets []adapter.ChangeSet) error {
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
	return nil
}

// applyAdapters hands each planned set to its adapter and saves state after
// every write, so a partial apply keeps the record of each adapter that wrote.
func (e *Engine) applyAdapters(sets []adapter.ChangeSet) error {
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
		st, save := e.State, func() error { return e.State.Save(e.StateDir) }
		if t, isRepo := pair[cs.Tool]; isRepo {
			st, save = t.State, func() error { return t.State.SaveNamed(e.StateDir, t.Name) }
		}
		post := enrich(cs, st)
		// Name the tool in every per-adapter failure: with several adapters an
		// unwrapped error leaves the user guessing which file broke.
		if err := a.Apply(e.Cfg, cs, e.Resolver, st); err != nil {
			return fmt.Errorf("%s: %w", cs.Tool, err)
		}
		post()
		// Persist immediately, a repo target into its own partition.
		if err := save(); err != nil {
			return fmt.Errorf("%s: save state: %w", cs.Tool, err)
		}
	}
	return nil
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
