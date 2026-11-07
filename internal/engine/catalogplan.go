package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/Novirmor/homonto/internal/agentfm"
	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/fsutil"
	"github.com/Novirmor/homonto/internal/scaffold"
)

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
	p.cl.SetHarnessReference(p.harnessRef)
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
		files := []string{"index.ts"}
		if n == "permission-observer" {
			files = append(files, "v2.ts")
		}
		if n == workflowBridgePlugin {
			files = append(files, "continuation.ts")
		}
		if n == workflowBridgePlugin && workflowContext {
			files = append(files, "tui.tsx", "rpc.ts", "v2.ts", "runner.ts", "github.ts", "authorization.ts", "compat.ts", "core.ts", "workflow.ts", "snapshot.ts")
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
	same := func(entry string) string { return entry }
	if err := pruneRoot(skillRoot, p.skills, same); err != nil {
		return err
	}
	if err := pruneRoot(cmdRoot, p.commands, func(entry string) string { return strings.TrimSuffix(entry, ".md") }); err != nil {
		return err
	}
	if err := pruneRoot(pluginRoot, p.plugins, same); err != nil {
		return err
	}
	// Subagent owner name: strip .md, then an optional per-tool variant suffix.
	return pruneRoot(subRoot, p.subagents, func(entry string) string {
		name := strings.TrimSuffix(entry, ".md")
		for _, tool := range config.HarnessIDs() {
			name = strings.TrimSuffix(name, "."+tool)
		}
		return name
	})
}

// pruneRoot removes every entry of root whose owner is not in keep. A root
// that cannot be read has nothing to prune.
func pruneRoot(root string, keep []string, owner func(entry string) string) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if !slices.Contains(keep, owner(e.Name())) {
			if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
				return err
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
	harnessRef      []byte
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

// planCatalog resolves the builtin content the config declares and evaluates the
// materialize gate. It returns nil when nothing builtin is declared.
func (e *Engine) planCatalog() (*catalogPlan, error) {
	if err := e.preflightCatalogRoots(); err != nil {
		return nil, err
	}
	skillSet, cmdSet, subSet, targetedSubagents, err := e.declaredBuiltins()
	if err != nil {
		return nil, err
	}
	// Build the catalog including the config's local frameworks so a
	// local:<path> framework's resources materialize (from their own FS) into
	// the catalog root exactly like a builtin's. With no local frameworks this
	// is the embedded singleton, identical to catalog.New().
	cl, err := e.Cfg.FrameworkCatalog()
	if err != nil {
		return nil, err
	}
	pluginSet := e.bundledPlugins(cl, len(skillSet)+len(cmdSet)+len(subSet) > 0)
	if len(skillSet)+len(cmdSet)+len(subSet)+len(pluginSet) == 0 {
		return nil, nil
	}
	skillNames := slices.Sorted(maps.Keys(skillSet))
	cmdNames := slices.Sorted(maps.Keys(cmdSet))
	subNames := slices.Sorted(maps.Keys(subSet))

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
	pluginNames := slices.Sorted(maps.Keys(pluginSet))
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
	harnessRef := catalog.RenderHarness(e.harnessDeclarations())
	fingerprint := renderFingerprint(renderCtx) + ":" + contentFP + ":" + toolingFP + ":" + tmpFP
	if len(harnessRef) > 0 {
		harnessFP := sha256.Sum256(harnessRef)
		fingerprint += ":h" + hex.EncodeToString(harnessFP[:])
	}
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
		allHarnessReferencesExist(e.CatalogRoot, skillNames, harnessRef) &&
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
		harnessRef:      harnessRef,
		workflowBinding: workflowBinding,
		fingerprint:     fingerprint,
		upToDate:        upToDate,
	}, nil
}

// declaredBuiltins names the builtin skills, commands and subagents any
// harness installs, and the subagents each harness targets.
func (e *Engine) declaredBuiltins() (skillSet, cmdSet, subSet map[string]bool, targeted map[string]map[string]bool, err error) {
	skillSet = map[string]bool{}
	cmdSet = map[string]bool{}
	subSet = map[string]bool{}
	targeted = map[string]map[string]bool{}
	for _, tool := range config.HarnessIDs() {
		targeted[tool] = map[string]bool{}
		sEntries, err := e.Cfg.ExpandedSkillEntriesForTool(tool)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		for _, entry := range sEntries {
			if strings.HasPrefix(entry.Resource.Source, "builtin:") {
				skillSet[strings.TrimPrefix(entry.Resource.Source, "builtin:")] = true
			}
		}
		cEntries, err := e.Cfg.ExpandedCommandEntriesForTool(tool)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		for _, entry := range cEntries {
			if strings.HasPrefix(entry.Resource.Source, "builtin:") {
				cmdSet[strings.TrimPrefix(entry.Resource.Source, "builtin:")] = true
			}
		}
		saEntries, err := e.Cfg.ExpandedSubagentEntriesForTool(tool)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		for _, entry := range saEntries {
			if strings.HasPrefix(entry.Resource.Source, "builtin:") {
				name := strings.TrimPrefix(entry.Resource.Source, "builtin:")
				subSet[name] = true
				targeted[tool][name] = true
			}
		}
	}
	return skillSet, cmdSet, subSet, targeted, nil
}

// bundledPlugins are the catalog plugins to materialize: every one when any
// builtin resource is declared (framework catalogs retain their bundled
// plugins), each explicitly enabled one (bundled-plugin-only configs also need
// content before adapter projection), and the workflow bridge when the
// workflow context is on.
func (e *Engine) bundledPlugins(cl *catalog.Catalog, anyResource bool) map[string]bool {
	pluginSet := map[string]bool{}
	for _, name := range cl.PluginNames() {
		if anyResource {
			pluginSet[name] = true
		}
		for _, plugin := range e.Cfg.Plugins.OpenCode {
			if plugin.IsEnabled() && plugin.Source == name {
				pluginSet[name] = true
			}
		}
	}
	if e.Cfg.WorkflowContextEnabled() {
		pluginSet[workflowBridgePlugin] = true
	}
	if e.Cfg.WorkflowContextEnabled() {
		pluginSet[workflowBridgePlugin] = true
	}
	return pluginSet
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

func allHarnessReferencesExist(root string, names []string, ref []byte) bool {
	if len(ref) == 0 {
		return true
	}
	for _, name := range names {
		if catalog.HasHarnessReference(name) {
			fi, err := os.Stat(filepath.Join(root, name, filepath.FromSlash(catalog.HarnessReferencePath)))
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
			kind     string
			patterns []string
		}{
			{"service-coordinator-ask", ctx[tool].ServiceRules.CoordinatorAsk},
			{"service-coordinator-deny", ctx[tool].ServiceRules.CoordinatorDeny},
			{"service-worker-deny", ctx[tool].ServiceRules.WorkerDeny},
		} {
			for _, pattern := range group.patterns {
				fmt.Fprintf(h, "%s\x00%s\x00%s\x00", group.kind, tool, pattern)
			}
		}
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
