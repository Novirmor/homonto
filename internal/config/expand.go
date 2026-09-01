package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/noviopenworks/homonto/internal/catalog"
	"github.com/noviopenworks/homonto/internal/remote"
)

var (
	catalogOnce sync.Once
	catalogInst *catalog.Catalog
	catalogErr  error
)

// loadedCatalog lazily builds the singleton embedded catalog (cheap to index).
func loadedCatalog() (*catalog.Catalog, error) {
	catalogOnce.Do(func() { catalogInst, catalogErr = catalog.New() })
	return catalogInst, catalogErr
}

// frameworkCatalog returns the catalog used to expand this config's frameworks:
// the embedded builtin catalog overlaid with each [frameworks.X]
// source="local:<path>" as a local single-framework root keyed by X (its path
// resolved relative to the config's baseDir). When the config declares no local
// frameworks it returns the cached embedded singleton unchanged, so a
// builtin-only config expands EXACTLY as before (no per-call re-indexing, no
// behavior change). A local framework's resources index and materialize as
// builtin:<name>, reusing the whole projection path.
func (c *Config) FrameworkCatalog() (*catalog.Catalog, error) {
	locals := map[string]fs.FS{}
	for name, fw := range c.Frameworks {
		p, ok := strings.CutPrefix(fw.Source, "local:")
		if !ok {
			continue
		}
		root := p
		if !filepath.IsAbs(root) {
			root = filepath.Join(c.baseDir, p)
		}
		locals[name] = os.DirFS(root)
	}
	// Overlay each remote framework's verified cache dir keyed by its config name,
	// exactly like a local framework root. The engine resolved and digest-verified
	// the dir through the trust pipeline before injecting it, so this path adds no
	// fetch/verify logic — it merges an already-trusted framework root.
	for name, dir := range c.remoteFrameworkDirs {
		locals[name] = os.DirFS(dir)
	}
	if len(locals) == 0 {
		return loadedCatalog()
	}
	return catalog.NewWithLocal(locals)
}

// frameworkCatalogName maps a [frameworks.X] declaration to the catalog
// framework name to expand, and reports whether it is expandable. A builtin:<n>
// source expands framework n from the embedded catalog; a local:<path> or
// remote:<url> source expands the framework keyed by the config name X
// (frameworkCatalog indexed the local/remote root under X). Any other source is
// not expandable (false); validation already rejected it at load, so this is
// defensive.
func FrameworkCatalogName(fwName, source string) (string, bool) {
	if n, ok := strings.CutPrefix(source, "builtin:"); ok && n != "" {
		return n, true
	}
	if strings.HasPrefix(source, "local:") {
		return fwName, true
	}
	// A remote framework expands by its config-key name (FrameworkCatalog overlaid
	// its verified cache dir under X), exactly like a local one; its resources are
	// tagged builtin:<name> through the same projection.
	if remote.IsRemoteSource(source) {
		return fwName, true
	}
	return "", false
}

func sameResource(a, b Resource) bool {
	return a.Source == b.Source && a.Scope == b.Scope && slices.Equal(a.Targets, b.Targets)
}

// expandEntriesForTool is the generic per-kind framework-expansion pipeline
// (F43): explicit [<kind>s.X] entries plus, for each framework declaration
// targeting the tool, its transitively expanded resources of the kind — tagged
// builtin:<name> with the framework's scope/targets, merged with the same
// explicit-clash and conflicting-scope/targets rules for every kind. kind fills
// the error text ("skill"/"command"/"subagent"); expand adapts the per-kind
// catalog Expand method to the resource names.
func (c *Config) expandEntriesForTool(tool, kind string, base []NamedResource, expand func(*catalog.Catalog, string) ([]string, error)) ([]NamedResource, error) {
	byName := map[string]NamedResource{}
	explicitNames := map[string]bool{}
	for _, e := range base {
		byName[e.Name] = e
		explicitNames[e.Name] = true
	}
	// Deterministic framework iteration order for stable error messages.
	fwNames := make([]string, 0, len(c.Frameworks))
	for name := range c.Frameworks {
		fwNames = append(fwNames, name)
	}
	sort.Strings(fwNames)
	var cl *catalog.Catalog
	for _, fwName := range fwNames {
		fwRes := c.Frameworks[fwName]
		catName, ok := FrameworkCatalogName(fwName, fwRes.Source)
		if !ok {
			continue
		}
		if !slices.Contains(fwRes.TargetsOrAll(), tool) {
			continue
		}
		if cl == nil {
			var err error
			if cl, err = c.FrameworkCatalog(); err != nil {
				return nil, err
			}
		}
		names, err := expand(cl, catName)
		if err != nil {
			return nil, fmt.Errorf("config: framework %q: %w", fwName, err)
		}
		for _, name := range names {
			if explicitNames[name] {
				return nil, fmt.Errorf("config: %s %q is declared both explicitly in [%ss] and by framework %q", kind, name, kind, fwName)
			}
			nr := NamedResource{
				Name: name,
				Resource: Resource{
					Source:  "builtin:" + name,
					Scope:   fwRes.Scope,
					Targets: fwRes.Targets,
				},
				Mode: "link", // framework-expanded resources project as symlinks
			}
			if prev, ok := byName[name]; ok {
				if !sameResource(prev.Resource, nr.Resource) {
					return nil, fmt.Errorf("config: %s %q expanded by multiple frameworks with conflicting scope/targets (framework %q)", kind, name, fwName)
				}
				continue
			}
			byName[name] = nr
		}
	}
	out := make([]NamedResource, 0, len(byName))
	for _, nr := range byName {
		out = append(out, nr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// skillNames/commandNames/subagentNames extract the expanded resource names (the
// only field the pipeline uses) from each kind's catalog Expand result.
func skillNames(e []catalog.ExpandedSkill) []string {
	out := make([]string, len(e))
	for i, x := range e {
		out[i] = x.Name
	}
	return out
}
func commandNames(e []catalog.ExpandedCommand) []string {
	out := make([]string, len(e))
	for i, x := range e {
		out[i] = x.Name
	}
	return out
}
func subagentNames(e []catalog.ExpandedSubagent) []string {
	out := make([]string, len(e))
	for i, x := range e {
		out[i] = x.Name
	}
	return out
}

// ExpandedSkillEntriesForTool returns the effective skills for a tool: explicit
// [skills.X] entries plus, for each [frameworks.<fw>] source="builtin:<fw>"
// targeting the tool, its transitively expanded skills. Each expanded skill
// inherits the framework declaration's scope and targets. A framework skill
// whose name collides with an explicit [skills.X] entry, or with another
// framework's skill under a conflicting declaration, is an error, as is a
// dependency cycle (surfaced from catalog.Expand).
func (c *Config) ExpandedSkillEntriesForTool(tool string) ([]NamedResource, error) {
	return c.expandEntriesForTool(tool, "skill", c.SkillEntriesForTool(tool),
		func(cl *catalog.Catalog, n string) ([]string, error) {
			e, err := cl.Expand([]string{n})
			return skillNames(e), err
		})
}

// ExpandedCommandEntriesForTool returns the effective commands for a tool:
// explicit [commands.X] entries plus, for each [frameworks.<fw>]
// source="builtin:<fw>" targeting the tool, its transitively expanded commands.
// Each expanded command inherits the framework declaration's scope and targets.
// A framework command whose name collides with an explicit [commands.X] entry,
// or with another framework's command under a conflicting declaration, is an
// error, as is a dependency cycle (surfaced from catalog.ExpandCommands).
// Collision is command-vs-command only: a command may share a name with a skill.
func (c *Config) ExpandedCommandEntriesForTool(tool string) ([]NamedResource, error) {
	return c.expandEntriesForTool(tool, "command", c.CommandEntriesForTool(tool),
		func(cl *catalog.Catalog, n string) ([]string, error) {
			e, err := cl.ExpandCommands([]string{n})
			return commandNames(e), err
		})
}

// ExpandedSubagentEntriesForTool returns the effective subagents for a tool:
// explicit [subagents.X] entries plus, for each [frameworks.<fw>]
// source="builtin:<fw>" targeting the tool, its transitively expanded
// subagents. Each expanded subagent inherits the framework declaration's scope
// and targets. A framework subagent whose name collides with an explicit
// [subagents.X] entry, or with another framework's subagent under a conflicting
// declaration, is an error, as is a dependency cycle (surfaced from
// catalog.ExpandSubagents). Collision is subagent-vs-subagent only.
func (c *Config) ExpandedSubagentEntriesForTool(tool string) ([]NamedResource, error) {
	return c.expandEntriesForTool(tool, "subagent", c.SubagentEntriesForTool(tool),
		func(cl *catalog.Catalog, n string) ([]string, error) {
			e, err := cl.ExpandSubagents([]string{n})
			return subagentNames(e), err
		})
}

// EnabledModelTools returns the tools for which model routing is currently
// active — every tool targeted by an installed builtin/framework that expands
// model-routed content, plus any tool with explicit [settings.<tool>.model]
// routing. Used by validateModels to scope its checks.
func (c *Config) EnabledModelTools() []string {
	seen := map[string]bool{}
	// Builtin frameworks enable model routing for their targeted tools (they
	// expand model-routed commands/agents). A local:<path> framework may
	// contribute only skills — like a skills-only config, which needs no models
	// — so it does not by itself force model routes; its expanded resources are
	// validated on their own where model routing actually applies. This keeps the
	// builtin path identical while letting a skill-only local framework load
	// without a [models] block.
	for _, r := range c.Frameworks {
		if !strings.HasPrefix(r.Source, "builtin:") {
			continue
		}
		for _, target := range r.TargetsOrAll() {
			seen[target] = true
		}
	}
	for _, r := range c.Commands {
		for _, target := range r.TargetsOrAll() {
			seen[target] = true
		}
	}
	for _, s := range c.Subagents {
		// A tune-only entry projects no agent, so it enables no tool: it only
		// retunes an agent something else already installed. Counting it would
		// demand a model block for a tool nothing actually targets — e.g.
		// tuning the Claude side of an agent would start requiring
		// [subagents.<name>.opencode].
		if s.IsTuneOnly() {
			continue
		}
		for _, target := range s.TargetsOrAll() {
			seen[target] = true
		}
	}
	out := make([]string, 0, len(seen))
	for tool := range seen {
		out = append(out, tool)
	}
	sort.Strings(out)
	return out
}

func entriesForTool(resources map[string]Resource, tool string) []NamedResource {
	var out []NamedResource
	for name, r := range resources {
		if slices.Contains(r.TargetsOrAll(), tool) {
			out = append(out, NamedResource{Name: name, Resource: r, Repo: r.Repo})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
