package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
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
	ProjectRoot         string // directory of homonto.toml; skill-scope project root
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
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, err
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
		ProjectRoot:         projectRoot,
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
// because materialization writes one rendered file per catalog name — two
// declarations of the same builtin share that file. Config validation rejects
// conflicting overrides on one source, so resolving by catalog name here is
// unambiguous by the time we run.
func (e *Engine) subagentRenderContext() map[string]agentfm.RenderContext {
	return e.subagentRenderContextFor(nil)
}

func (e *Engine) subagentRenderContextFor(targets map[string]map[string]bool) map[string]agentfm.RenderContext {
	overrides := func(pick func(config.Subagent) config.ModelRoute) map[string]agentfm.ModelSpec {
		m := map[string]agentfm.ModelSpec{}
		for key, sa := range e.Cfg.Subagents {
			r := pick(sa)
			if r.Model == "" && r.Variant == "" && r.Effort == "" {
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
			m[name] = agentfm.ModelSpec{Model: r.Model, Variant: r.Variant, Effort: r.Effort, BashAllowAdd: r.BashAllowAdd}
		}
		return m
	}
	return map[string]agentfm.RenderContext{
		"opencode": {Overrides: overrides(func(s config.Subagent) config.ModelRoute { return s.OpenCode }), Targets: targets["opencode"]},
	}
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
	if err := p.cl.Materialize(e.CatalogRoot, p.skills, p.shellProxy, p.codeIntel); err != nil {
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
	// GC: the Materialize* calls only ever WRITE declared names, so a renamed or
	// de-declared resource left its old files in the catalog roots forever. That
	// litter was live ammunition, not just clutter — the adapters prefer a
	// <name>.<tool>.md variant when one exists, so a years-old render could win
	// over a future same-named verbatim agent.
	if err := gcCatalogRoots(e.CatalogRoot, e.CommandCatalogRoot, e.SubagentCatalogRoot, e.PluginCatalogRoot, p); err != nil {
		return err
	}
	e.State.SetCatalogVersion(p.cl.Version())
	e.State.SetRenderFingerprint(p.fingerprint)
	// Save immediately so a later adapter failure still records the completed
	// materialization.
	return e.State.Save(e.StateDir)
}

// allPluginDirsExist reports whether every bundled plugin directory is
// materialized — the plugin side of the materialize gate.
func allPluginDirsExist(root string, names []string) bool {
	for _, n := range names {
		fi, err := os.Stat(filepath.Join(root, n, "plugin.ts"))
		if err != nil || fi.IsDir() {
			return false
		}
	}
	return true
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
	shellProxy  string
	codeIntel   string
	fingerprint string
	upToDate    bool
}

// CatalogNeedsMaterialize reports whether a materialize would do real work. The
// CLI needs this because a catalog file's symlink target is name-based, so
// stale, missing, or mis-rendered catalog content leaves the projection plan
// empty — and an empty plan otherwise skips apply entirely, stranding the
// content forever. (Same shape as the HasRemoteResources carve-out.) An error
// resolving the plan counts as "needs work" so apply runs and surfaces it,
// rather than being silently swallowed here.
func (e *Engine) CatalogNeedsMaterialize() bool {
	p, err := e.planCatalog()
	if err != nil {
		return true
	}
	return p != nil && !p.upToDate
}

// planCatalog resolves the builtin content the config declares and evaluates the
// materialize gate. It returns nil when nothing builtin is declared.
func (e *Engine) planCatalog() (*catalogPlan, error) {
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
	// Bundled plugins ride with the catalog: any catalog materialization
	// materializes them (they are version-gated content, not per-config
	// declarations). When the config declares no builtin content at all,
	// there is no catalog to materialize alongside.
	if len(skillSet) == 0 && len(cmdSet) == 0 && len(subSet) == 0 {
		return nil, nil
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
		pluginSet[name] = true
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
	renderCtx := e.subagentRenderContextFor(targetedSubagents)
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
	fingerprint := renderFingerprint(renderCtx) + ":" + contentFP + ":" + toolingFP
	upToDate := e.State.CatalogVersionRecorded() == cl.Version() &&
		e.State.RenderFingerprintRecorded() == fingerprint &&
		allSkillDirsExist(e.CatalogRoot, skillNames, cl) &&
		allCommandFilesExist(e.CommandCatalogRoot, cmdNames) &&
		allSubagentFilesExist(e.SubagentCatalogRoot, subNames, cl, renderCtx) &&
		allPluginDirsExist(e.PluginCatalogRoot, pluginNames)
	return &catalogPlan{
		cl:          cl,
		skills:      skillNames,
		commands:    cmdNames,
		subagents:   subNames,
		plugins:     pluginNames,
		renderCtx:   renderCtx,
		shellProxy:  tooling.ShellProxy,
		codeIntel:   tooling.CodeIntel,
		fingerprint: fingerprint,
		upToDate:    upToDate,
	}, nil
}

// allSkillDirsExist reports whether every declared skill is materialized. For a
// dispatcher skill it also requires the generated tooling reference: the
// directory alone existing would otherwise mask a hand-deleted reference behind
// an up-to-date fingerprint, leaving the skill pointing at a file that is not
// there.
func allSkillDirsExist(root string, names []string, cl *catalog.Catalog) bool {
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
		digestSpecs("override", tool, ctx[tool].Overrides)
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
