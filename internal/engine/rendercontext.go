package engine

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Novirmor/homonto/internal/agentfm"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/resourcepath"
)

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
	roles, err := agentfm.BuiltinRoles()
	if err != nil {
		return nil, err
	}
	records := e.recordRoots()
	// Native record protection follows the builtin role even when installed
	// directly rather than through a framework. It grants no external access.
	externalDenies := map[string][]string{}
	for agent, role := range roles {
		if role.Writer {
			externalDenies[agent] = records
		}
	}
	// ADR 0068: builtin workers may inspect all declared repositories. Directory
	// access does not grant shell or edit capability to read-only specialists.
	externalByAgent := map[string][]string{}
	if e.workflowFrameworkInstalled() {
		// Every builtin role may read workflow records, even outside the
		// project. A writer's record denies render last and remove that allow.
		grant := append(e.externalDirectories(), records...)
		for agent := range roles {
			externalByAgent[agent] = slices.Clone(grant)
		}
	}
	names, editDenies, err := e.installedAgentNames(records, externalDenies)
	if err != nil {
		return nil, err
	}
	openCodeRoutes, err := e.Routes()
	if err != nil {
		return nil, err
	}
	claudeRoutes, err := e.RoutesFor("claude")
	if err != nil {
		return nil, err
	}
	rules := e.Cfg.InstalledServiceRules()
	base := agentfm.RenderContext{
		ServiceRules: agentfm.ServiceRules{
			CoordinatorAsk:  rules.CoordinatorAsk,
			CoordinatorDeny: rules.CoordinatorDeny,
			WorkerDeny:      rules.WorkerDeny,
		},
		Names:                          names,
		ShellProxy:                     e.Cfg.ResolvedTooling().ShellProxy,
		ExternalDirectoriesByAgent:     externalByAgent,
		ExternalDirectoryDeniesByAgent: externalDenies,
		RuntimeProviders:               slices.Sorted(maps.Keys(e.Cfg.HostingProviders())),
	}
	openCode := base
	openCode.Overrides = e.modelOverrides(func(s config.Subagent) config.ModelRoute { return s.OpenCode }, openCodeRoutes)
	openCode.EditDirectoryDeniesByAgent = editDenies
	openCode.Targets = targets["opencode"]
	// Claude Code renders the same intent; its agents take the record roots as
	// absolute path denies (agentfm.ClaudePolicy).
	claude := base
	claude.Overrides = e.modelOverrides(func(s config.Subagent) config.ModelRoute { return s.Claude }, claudeRoutes)
	claude.EditDirectoryDeniesByAgent = map[string][]string{}
	claude.Targets = targets["claude"]
	return map[string]agentfm.RenderContext{"opencode": openCode, "claude": claude}, nil
}

// recordRoots names the workflow records directory, as configured and as
// resolved through symlinks.
func (e *Engine) recordRoots() []string {
	records := e.Cfg.Workflow.RootOrDefault()
	if e.WorkspaceLayout.ExplicitRepos() {
		records = e.WorkspaceLayout.WorkflowRoot
	} else if !filepath.IsAbs(records) {
		records = filepath.Join(e.ProjectRoot, records)
	}
	return []string{records, permissionCanonicalPath(records)}
}

func (e *Engine) workflowFrameworkInstalled() bool {
	for _, resource := range e.Cfg.Frameworks {
		switch resource.Source {
		case "builtin:onto", "builtin:to", "builtin:no":
			return true
		}
	}
	return len(e.Cfg.HostingProviders()) > 0
}

// externalDirectories are the directories outside the project a builtin agent
// may read: every declared repository and its worktrees, then the selected
// skill installations and their content roots — never all of HOME or even all
// user-installed skills.
func (e *Engine) externalDirectories() []string {
	dirs := slices.Collect(maps.Values(e.Cfg.RepoDirs()))
	if e.WorkspaceLayout.ExplicitRepos() && e.WorkspaceLayout.WorktreesDir != "" {
		for name := range e.WorkspaceLayout.Repos {
			dirs = append(dirs, filepath.Join(e.WorkspaceLayout.WorktreesDir, name))
		}
	}
	slices.Sort(dirs)
	skills, err := e.Cfg.ExpandedSkillEntriesForTool("opencode")
	if err != nil {
		return dirs
	}
	for _, skill := range skills {
		root := e.ProjectRoot
		if skill.Repo != "" {
			root = e.Cfg.RepoDirs()[skill.Repo]
		}
		installed := filepath.Join(resourcepath.Dir(resourcepath.Skill, "opencode", skill.Resource.Scope, e.Home, root), skill.Name)
		// Resolve the parent, not a stale installed link to an old source.
		dirs = append(dirs, installed, filepath.Join(permissionCanonicalPath(filepath.Dir(installed)), skill.Name))
		var source string
		switch {
		case strings.HasPrefix(skill.Resource.Source, "builtin:"):
			source = filepath.Join(e.CatalogRoot, strings.TrimPrefix(skill.Resource.Source, "builtin:"))
		case strings.HasPrefix(skill.Resource.Source, "local:"):
			source = filepath.Join(e.ContentDir, "skills", strings.TrimPrefix(skill.Resource.Source, "local:"))
		}
		if source != "" {
			dirs = append(dirs, source, permissionCanonicalPath(source))
		}
	}
	return dirs
}

// installedAgentNames maps each catalog agent to its installed alias, and gives
// every record-protected agent the records as edit denies relative to the
// directory it is installed for.
func (e *Engine) installedAgentNames(records []string, externalDenies map[string][]string) (map[string]string, map[string][]string, error) {
	names := map[string]string{}
	editDenies := map[string][]string{}
	entries, err := e.Cfg.ExpandedSubagentEntriesForTool("opencode")
	if err != nil {
		return names, editDenies, nil
	}
	for _, entry := range entries {
		source, ok := config.SubagentCatalogName(entry.Resource.Source)
		if !ok {
			continue
		}
		names[source] = entry.Name
		if len(externalDenies[source]) == 0 {
			continue
		}
		// User-scoped workflow agents use this configuration's launch root;
		// a repo-targeted project install instead belongs to that repository.
		directory := e.ProjectRoot
		if entry.Resource.Scope == "project" && entry.Repo != "" {
			directory = e.Cfg.RepoDirs()[entry.Repo]
		}
		for _, root := range records {
			// V2 checks internal edits relative to the active Location, and
			// external files by absolute canonical path (already included in
			// records). Mixing in V1 worktree-relative paths would deny
			// unrelated source files in nested layouts.
			for _, base := range []string{directory, permissionCanonicalPath(directory)} {
				rel, err := filepath.Rel(base, root)
				if err != nil {
					return nil, nil, fmt.Errorf("permission host %q records %q: %w", base, root, err)
				}
				if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue
				}
				editDenies[source] = append(editDenies[source], rel)
			}
		}
	}
	return names, editDenies, nil
}

// modelOverrides resolves each catalog agent's model spec for one harness.
func (e *Engine) modelOverrides(pick func(config.Subagent) config.ModelRoute, routes RouteReport) map[string]agentfm.ModelSpec {
	m := map[string]agentfm.ModelSpec{}
	// Policy-routed agents first (ADR 0099): a pin without a model still
	// contributes its steps and bash additions below, and a pin with one
	// replaces the policy's choice entirely.
	for _, r := range routes.Routes {
		if r.Selection != nil && r.CatalogKeyed {
			m[r.Agent] = agentfm.ModelSpec{Model: r.Selection.Model, Variant: r.Selection.Variant}
		}
	}
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
		spec := agentfm.ModelSpec{Model: r.Model, Variant: r.Variant, Effort: r.Effort, Steps: r.Steps, BashAllowAdd: r.BashAllowAdd}
		if spec.Model == "" {
			if routed, ok := m[name]; ok {
				spec.Model, spec.Variant = routed.Model, routed.Variant
			}
		}
		m[name] = spec
	}
	return m
}
