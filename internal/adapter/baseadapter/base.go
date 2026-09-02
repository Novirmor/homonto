// Package baseadapter holds the state and behavior shared by homonto's
// Claude and OpenCode adapters. Both adapters project desired config into one
// target tool's files; they share the same per-skill/command/subagent
// file-projection machinery and the same copy-mode-subagent reconciler, and
// differ only in their structured-document namespaces, file paths, and the
// per-tool rendered subagent variant suffix.
//
// Each concrete adapter embeds Base and keeps only its tool-specific overrides
// (file paths, desired-key maps, Path funcs, Plan/Apply/ObserveHashes). Base's
// methods read the embedded fields directly, so an adapter constructs Base via
// its package's New and configures Tool + VariantSuffix once at construction.
package baseadapter

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/adapter/copyproj"
	"github.com/noviopenworks/homonto/internal/adapter/fileproj"
	"github.com/noviopenworks/homonto/internal/agentfm"
	"github.com/noviopenworks/homonto/internal/config"
	"github.com/noviopenworks/homonto/internal/copyfile"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/resourcepath"
	"github.com/noviopenworks/homonto/internal/state"
)

// Base holds the shared state of a homonto adapter that projects desired
// config into one target tool's files. The Claude and OpenCode adapters embed
// Base and keep only their tool-specific overrides.
type Base struct {
	// Tool is the adapter's tool identifier ("opencode"); it is the prefix
	// the state, fileproj, copyproj, and resourcepath namespaces key on.
	Tool string

	// RepoName, when set, puts the adapter in repo mode (ADR 0024 stage 2):
	// it projects ONLY repo-tagged project-scoped resources into the repo's
	// .opencode/ tree, skips every global namespace (settings, user-scope
	// MCPs, TUI, plugins — those belong to the config repo's adapter), and
	// Name() reports "<tool>@<repo>" so plans and warnings attribute per
	// repository. "" is the ordinary config-repo adapter.
	RepoName string

	// VariantSuffix is the per-tool rendered subagent variant suffix
	// (".claude.md" or ".opencode.md"). When non-empty, subagentSource prefers
	// the file <name><VariantSuffix> in the catalog (the agentfm tool-specific
	// render) over the shared verbatim <name>.md.
	VariantSuffix string

	Home                string
	Content             string
	ProjectRoot         string // directory of homonto.toml; used for project-scope resources
	CatalogRoot         string // materialized builtin catalog root (.homonto/catalog/skills)
	CommandCatalogRoot  string // materialized builtin command root (.homonto/catalog/commands)
	SubagentCatalogRoot string // materialized builtin subagent root (.homonto/catalog/subagents)
	RemoteSubagentRoot  string // materialized remote subagent root (.homonto/remote/subagents)

	Skills    []config.NamedResource
	Commands  []config.NamedResource
	Subagents []config.NamedResource
}

// Name returns the adapter's identifier, satisfying adapter.Adapter: the
// plain tool id for the config-repo adapter, "<tool>@<repo>" in repo mode so
// plans, warnings, and state-file matching attribute per repository.
func (b *Base) Name() string {
	if b.RepoName != "" {
		return b.Tool + "@" + b.RepoName
	}
	return b.Tool
}

// InRepo reports whether the adapter runs in repo mode targeting repo.
func (b *Base) InRepo(repo string) bool {
	return b.RepoName == repo
}

// ManagedRoots returns every content root homonto owns links into. CatalogRoot,
// CommandCatalogRoot, and SubagentCatalogRoot are included only when set:
// link.managed() treats an empty-string root as a prefix match for every
// absolute path, so passing "" here would make link calls treat any symlink as
// "ours" — an empty root must never reach link.*.
func (b *Base) ManagedRoots() []string {
	roots := []string{b.Content}
	if b.CatalogRoot != "" {
		roots = append(roots, b.CatalogRoot)
	}
	if b.CommandCatalogRoot != "" {
		roots = append(roots, b.CommandCatalogRoot)
	}
	if b.SubagentCatalogRoot != "" {
		roots = append(roots, b.SubagentCatalogRoot)
	}
	if b.RemoteSubagentRoot != "" {
		roots = append(roots, b.RemoteSubagentRoot)
	}
	return roots
}

// SkillsDir is the directory owned-skill symlinks live in for the given scope.
func (b *Base) SkillsDir(scope string) string {
	return resourcepath.Dir(resourcepath.Skill, b.Tool, scope, b.Home, b.ProjectRoot)
}

// InactiveSkillsDir is the other scope's skills directory — where a link may
// linger after a per-resource scope switch. It returns "" when there is nothing
// meaningful to relocate from: no project root is known, or the two scopes
// resolve to the same directory.
func (b *Base) InactiveSkillsDir(scope string) string {
	if b.ProjectRoot == "" {
		return ""
	}
	d := resourcepath.Dir(resourcepath.Skill, b.Tool, resourcepath.OtherScope(scope), b.Home, b.ProjectRoot)
	if d == b.SkillsDir(scope) {
		return ""
	}
	return d
}

// linkTarget spells a symlink target for dst (ADR 0026): relative when the
// source and the destination move together — both under the one project root
// that holds .opencode/ and .homonto//homonto/ — and absolute otherwise
// (user scope lives under $HOME, which does not move with the repo; a
// cross-repo link's source sits in the config repo while its destination sits
// in another). A relative spelling that would need to escape to another
// volume's root (Windows drive letters) has no filepath.Rel; that error is
// returned so the caller fails before writing a broken link.
// linkDomain is the relocation domain for links this adapter builds: the
// project root, and only for the config repo's own adapter (a repo adapter's
// destinations live in a repository that moves independently).
func (b *Base) linkDomain() string {
	if b.RepoName != "" {
		return ""
	}
	return b.ProjectRoot
}

func (b *Base) linkTarget(src, dst string) (string, error) {
	domain := b.ProjectRoot
	if b.RepoName != "" {
		// Repo mode: the destination lives in the declared repo, which moves
		// independently of the config repo — not a relocation domain.
		return src, nil
	}
	if domain == "" {
		return src, nil
	}
	relSrc, srcErr := filepath.Rel(domain, src)
	relDst, dstErr := filepath.Rel(domain, dst)
	if srcErr != nil || dstErr != nil {
		// Cross-volume (or otherwise unrepresentable): keep absolute.
		return src, nil
	}
	if strings.HasPrefix(relSrc, "..") || strings.HasPrefix(relDst, "..") {
		return src, nil // source (e.g. $HOME content) outside the domain: absolute
	}
	rel, err := filepath.Rel(filepath.Dir(dst), src)
	if err != nil {
		return "", fmt.Errorf("link target for %s: %w", dst, err)
	}
	return rel, nil
}

// SkillFileLinks builds the desired managed skill symlinks for the fileproj
// contract: destination, content source, state key, and the same-named link at
// the other scope (Inactive is "" when there is nothing to relocate from).
func (b *Base) SkillFileLinks() []fileproj.Link {
	var out []fileproj.Link
	for _, e := range b.Skills {
		inact := ""
		if d := b.InactiveSkillsDir(e.Resource.Scope); d != "" {
			inact = filepath.Join(d, e.Name)
		}
		src, err := b.linkTarget(b.skillSource(e), filepath.Join(b.SkillsDir(e.Resource.Scope), e.Name))
		if err != nil {
			continue // unrepresentable target: the plan surfaces the conflict later
		}
		out = append(out, fileproj.Link{
			Dst:      filepath.Join(b.SkillsDir(e.Resource.Scope), e.Name),
			Src:      src,
			Key:      "skill." + e.Name,
			Inactive: inact,
			Domain:   b.linkDomain(),
		})
	}
	return out
}

// skillSource resolves a skill entry's on-disk content directory by source
// scheme: builtin:<n> from the materialized catalog root, otherwise the local
// content dir.
func (b *Base) skillSource(entry config.NamedResource) string {
	if s := entry.Resource.Source; strings.HasPrefix(s, "builtin:") {
		return filepath.Join(b.CatalogRoot, strings.TrimPrefix(s, "builtin:"))
	}
	return filepath.Join(b.Content, "skills", LocalSourceName(entry.Resource.Source, entry.Name))
}

// CommandsDir is the directory owned-command symlinks live in for the scope.
func (b *Base) CommandsDir(scope string) string {
	return resourcepath.Dir(resourcepath.Command, b.Tool, scope, b.Home, b.ProjectRoot)
}

// InactiveCommandsDir is the other scope's commands directory — where a link
// may linger after a per-resource scope switch. It returns "" when nothing
// meaningful can be relocated (no project root, or both scopes resolve equal).
func (b *Base) InactiveCommandsDir(scope string) string {
	if b.ProjectRoot == "" {
		return ""
	}
	d := resourcepath.Dir(resourcepath.Command, b.Tool, resourcepath.OtherScope(scope), b.Home, b.ProjectRoot)
	if d == b.CommandsDir(scope) {
		return ""
	}
	return d
}

// commandSource resolves a command entry's on-disk file by source scheme:
// builtin:<n> from the materialized command root (<n>.md), otherwise the local
// content dir (homonto/commands/<n>.md).
func (b *Base) commandSource(entry config.NamedResource) string {
	if s := entry.Resource.Source; strings.HasPrefix(s, "builtin:") {
		return filepath.Join(b.CommandCatalogRoot, strings.TrimPrefix(s, "builtin:")+".md")
	}
	return filepath.Join(b.Content, "commands", LocalSourceName(entry.Resource.Source, entry.Name)+".md")
}

// CommandFileLinks builds the desired managed command symlinks for the fileproj
// contract (destination is <name>.md).
func (b *Base) CommandFileLinks() []fileproj.Link {
	var out []fileproj.Link
	for _, e := range b.Commands {
		inact := ""
		if d := b.InactiveCommandsDir(e.Resource.Scope); d != "" {
			inact = filepath.Join(d, e.Name+".md")
		}
		dst := filepath.Join(b.CommandsDir(e.Resource.Scope), e.Name+".md")
		src, err := b.linkTarget(b.commandSource(e), dst)
		if err != nil {
			continue
		}
		out = append(out, fileproj.Link{
			Dst:      dst,
			Src:      src,
			Key:      "command." + e.Name,
			Inactive: inact,
			Domain:   b.linkDomain(),
		})
	}
	return out
}

// SubagentsDir is the directory owned-subagent symlinks live in for the scope.
func (b *Base) SubagentsDir(scope string) string {
	return resourcepath.Dir(resourcepath.Subagent, b.Tool, scope, b.Home, b.ProjectRoot)
}

// InactiveSubagentsDir is the other scope's subagent directory — where a link
// may linger after a per-resource scope switch. It returns "" when nothing
// meaningful can be relocated (no project root, or both scopes resolve equal).
func (b *Base) InactiveSubagentsDir(scope string) string {
	if b.ProjectRoot == "" {
		return ""
	}
	d := resourcepath.Dir(resourcepath.Subagent, b.Tool, resourcepath.OtherScope(scope), b.Home, b.ProjectRoot)
	if d == b.SubagentsDir(scope) {
		return ""
	}
	return d
}

// subagentSource resolves a subagent entry's on-disk file by source scheme:
// builtin:<n> from the materialized subagent root (<n>.md, preferring the
// tool-specific <n><VariantSuffix> render when present), remote:<url> from the
// materialized remote root (<name>.md), otherwise the local content dir
// (homonto/subagents/<n>.md).
func (b *Base) subagentSource(entry config.NamedResource) string {
	if s := entry.Resource.Source; strings.HasPrefix(s, "builtin:") {
		name := strings.TrimPrefix(s, "builtin:")
		// Prefer the tool-rendered frontmatter variant when the subagent
		// declared a neutral homonto: block (materialize wrote
		// <name><VariantSuffix>); fall back to the shared verbatim file.
		if b.VariantSuffix != "" {
			if variant := filepath.Join(b.SubagentCatalogRoot, name+b.VariantSuffix); fsutil.FileExists(variant) {
				return variant
			}
		}
		return filepath.Join(b.SubagentCatalogRoot, name+".md")
	}
	if strings.HasPrefix(entry.Resource.Source, "remote:") {
		return filepath.Join(b.RemoteSubagentRoot, entry.Name+".md")
	}
	return filepath.Join(b.Content, "subagents", LocalSourceName(entry.Resource.Source, entry.Name)+".md")
}

// skipsSubagent reports whether a builtin subagent must NOT be projected for
// this tool: it carries a neutral homonto: block (so it is rendered per tool)
// but has no <name><VariantSuffix> variant — agentfm skipped this tool's
// render, so the tool simply does not project it. A verbatim subagent (no
// block) is always projected.
func (b *Base) SkipsSubagent(e config.NamedResource) bool {
	name, ok := strings.CutPrefix(e.Resource.Source, "builtin:")
	if !ok {
		return false
	}
	if b.VariantSuffix != "" && fsutil.FileExists(filepath.Join(b.SubagentCatalogRoot, name+b.VariantSuffix)) {
		return false
	}
	data, err := os.ReadFile(filepath.Join(b.SubagentCatalogRoot, name+".md"))
	return err == nil && agentfm.NeedsTransform(data)
}

// SubagentFileLinks builds the desired managed subagent symlinks for the
// fileproj contract. Copy-mode subagents are projected as content files (not
// links), so they are skipped here and reconciled by ApplyCopySubagents.
func (b *Base) SubagentFileLinks() []fileproj.Link {
	var out []fileproj.Link
	for _, e := range b.Subagents {
		if e.Mode == "copy" || b.SkipsSubagent(e) {
			continue
		}
		inact := ""
		if d := b.InactiveSubagentsDir(e.Resource.Scope); d != "" {
			inact = filepath.Join(d, e.Name+".md")
		}
		dst := filepath.Join(b.SubagentsDir(e.Resource.Scope), e.Name+".md")
		src, err := b.linkTarget(b.subagentSource(e), dst)
		if err != nil {
			continue
		}
		out = append(out, fileproj.Link{
			Dst:      dst,
			Src:      src,
			Key:      "subagent." + e.Name,
			Inactive: inact,
			Domain:   b.linkDomain(),
		})
	}
	return out
}

// copySubagentDesired returns dst -> resolved content for each copy-mode
// subagent (a real managed file rather than a symlink).
func (b *Base) copySubagentDesired() (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, entry := range b.Subagents {
		if entry.Mode != "copy" || b.SkipsSubagent(entry) {
			continue
		}
		content, err := os.ReadFile(b.subagentSource(entry))
		if err != nil {
			return nil, err
		}
		out[filepath.Join(b.SubagentsDir(entry.Resource.Scope), entry.Name+".md")] = content
	}
	return out, nil
}

// PlanCopyOps computes the reconciler ops for copy-mode subagents against state
// through the shared copyproj core.
func (b *Base) PlanCopyOps(st *state.State) ([]copyfile.Op, error) {
	desired, err := b.copySubagentDesired()
	if err != nil {
		return nil, err
	}
	return copyproj.Plan(b.Tool, desired, st)
}

// ApplyCopySubagents reconciles copy-mode subagent content files through the
// shared copyproj core (write/update/prune + local-edit .bak backup + state,
// conflict abort, F7 prune-root guard).
func (b *Base) ApplyCopySubagents(st *state.State) error {
	desired, err := b.copySubagentDesired()
	if err != nil {
		return err
	}
	return copyproj.Apply(b.Tool, desired, st, b.copyPruneRoots())
}

// copyPruneRoots are the directories a copy-mode subagent file may legitimately
// live in (user + project agent dirs). copyfile.Apply refuses to delete a prune
// destination — reconstructed from an untrusted state entry — that resolves
// outside these roots, so a tampered state.json path cannot delete an arbitrary
// file (F7). The project dir is included only when a project root is known.
func (b *Base) copyPruneRoots() []string {
	roots := []string{b.SubagentsDir("user")}
	if b.ProjectRoot != "" {
		roots = append(roots, b.SubagentsDir("project"))
	}
	return roots
}

// Expand resolves the config's skill/command/subagent entries for this tool
// into the Base's instance fields. Both Plan and Apply call it first so Apply's
// file entries derive from the supplied config rather than a prior Plan. In
// repo mode only the entries tagged with this adapter's repo resolve — the
// config-repo adapter sees exactly the untagged ones, so no resource is
// projected twice or into the wrong repository.
func (b *Base) Expand(c *config.Config) error {
	inRepo := func(entries []config.NamedResource) []config.NamedResource {
		// Exact match both ways: the config-repo adapter (RepoName "")
		// excludes repo-tagged entries; a repo adapter sees only its own.
		var out []config.NamedResource
		for _, e := range entries {
			if e.Repo == b.RepoName {
				out = append(out, e)
			}
		}
		return out
	}
	skills, err := c.ExpandedSkillEntriesForTool(b.Tool)
	if err != nil {
		return err
	}
	b.Skills = inRepo(skills)
	commands, err := c.ExpandedCommandEntriesForTool(b.Tool)
	if err != nil {
		return err
	}
	b.Commands = inRepo(commands)
	subagents, err := c.ExpandedSubagentEntriesForTool(b.Tool)
	if err != nil {
		return err
	}
	b.Subagents = inRepo(subagents)
	return nil
}

// LocalSourceName resolves a skill resource's content subdirectory: a local:
// source names that directory directly; any other source falls back to the
// skill's declared name.
func LocalSourceName(source, fallback string) string {
	if strings.HasPrefix(source, "local:") {
		return strings.TrimPrefix(source, "local:")
	}
	return fallback
}
