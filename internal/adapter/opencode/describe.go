package opencode

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/adapter/fileproj"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/state"
)

// Describe enumerates every managed key this adapter currently projects, with
// its kind, name, destination, and declaration origin. It mirrors the same
// desired-key construction Plan uses (desiredMCPs/desiredProjectMCPs/
// desiredSettings/desiredTUI, the plugin loop, and the file/copy link
// builders), so there is no second mapping to drift. An expansion failure
// yields nil — explain then falls back to state-only facts rather than
// failing on a broken tool file.
func (a *Adapter) Describe(c *config.Config) []adapter.ManagedResource {
	if err := a.Expand(c); err != nil {
		return nil
	}
	global := a.RepoName == ""
	out := []adapter.ManagedResource{}
	add := func(kind, name, key, dst string, origin *state.Origin) {
		out = append(out, adapter.ManagedResource{
			Tool: a.Name(), Key: key, Kind: kind, Name: name, Destination: dst, Origin: origin,
		})
	}
	entryOrigin := func(e config.NamedResource, repo string) *state.Origin {
		if len(e.Origins) == 0 {
			return nil
		}
		o := e.Origins[0]
		if repo != "" {
			r := o // copy; the repo-tagged view names the partition
			r.Repo = repo
			return &r
		}
		return &o
	}

	if global {
		for name, m := range c.MCPs {
			if m.ScopeOrDefault() == "project" && a.ProjectRoot != "" {
				continue
			}
			if _, ok := mcpValue(m); ok {
				add("mcp", name, "mcp."+name, a.cfgFile(), &state.Origin{Kind: "direct", Scope: m.ScopeOrDefault()})
			}
		}
		for k := range c.Settings.OpenCode {
			add("setting", k, "setting."+k, a.cfgFile(), &state.Origin{Kind: "direct"})
		}
		for k := range c.TUI.OpenCode {
			add("tui", k, "tui."+k, a.tuiFile(), &state.Origin{Kind: "direct"})
		}
		for _, pl := range c.Plugins.OpenCode {
			if pl.IsEnabled() {
				add("plugin", pl.Source, "plugin."+pl.Source, a.cfgFile(), &state.Origin{Kind: "direct"})
			}
		}
	}
	if a.ProjectRoot != "" {
		for name, m := range c.MCPs {
			if m.ScopeOrDefault() != "project" || m.Repo != a.RepoName {
				continue
			}
			if _, ok := mcpValue(m); ok {
				add("projmcp", name, "projmcp."+name, a.projectCfgFile(), &state.Origin{Kind: "direct", Scope: "project", Repo: m.Repo})
			}
		}
	}

	linkKind := func(links []fileLink, kind string, entries []config.NamedResource) {
		byName := map[string]config.NamedResource{}
		for _, e := range entries {
			byName[e.Name] = e
		}
		for _, l := range links {
			e, ok := byName[l.name]
			o := (*state.Origin)(nil)
			if ok {
				o = entryOrigin(e, a.RepoName)
			}
			add(kind, l.name, l.key, l.dst, o)
		}
	}
	linkKind(collectLinks(a.SkillFileLinks()), "skill", a.Skills)
	linkKind(collectLinks(a.CommandFileLinks()), "command", a.Commands)

	linkEntries := map[string]config.NamedResource{}
	for _, e := range a.Subagents {
		linkEntries[e.Name] = e
	}
	for _, l := range collectLinks(a.SubagentFileLinks()) {
		e := linkEntries[l.name]
		add("subagent", l.name, l.key, l.dst, entryOrigin(e, a.RepoName))
	}
	for _, e := range a.Subagents {
		if e.Mode != "copy" || a.SkipsSubagent(e) {
			continue
		}
		dst := filepath.Join(a.SubagentsDir(e.Resource.Scope), e.Name+".md")
		add("subagentcopy", e.Name, "subagentcopy."+e.Name, dst, entryOrigin(e, a.RepoName))
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// fileLink flattens fileproj.Link to the three fields Describe consumes.
type fileLink struct{ dst, key, name string }

func collectLinks(links []fileproj.Link) []fileLink {
	out := make([]fileLink, 0, len(links))
	for _, l := range links {
		out = append(out, fileLink{dst: l.Dst, key: l.Key, name: keyName(l.Key)})
	}
	return out
}

// keyName strips a state key's "<kind>." prefix to recover the resource name.
func keyName(key string) string {
	if _, name, ok := strings.Cut(key, "."); ok {
		return name
	}
	return key
}
