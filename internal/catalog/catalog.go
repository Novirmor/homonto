// Package catalog loads and expands the embedded framework/skill catalog.
// It is config-agnostic: it MUST NOT import internal/config.
package catalog

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	embedded "github.com/noviopenworks/homonto/catalog"
	"github.com/noviopenworks/homonto/internal/schema"
	toml "github.com/pelletier/go-toml/v2"
)

// Framework is one catalog framework's parsed metadata.
type Framework struct {
	Name         string
	Version      string
	Description  string
	Dependencies []string // framework names (version constraints stripped)
	// DependencyConstraints maps a dependency name to its version constraint
	// (e.g. ">=0.1.0"); a dependency with no constraint is absent from the map.
	DependencyConstraints map[string]string
	// Provides / RequiredCapabilities are the capabilities (each "name@major")
	// this framework offers and depends on; resolved fail-loud at load.
	Provides             []string
	RequiredCapabilities []string
	// Compat is the [compat].homonto version constraint the framework declares
	// (empty = unconstrained). The catalog stores it version-agnostically; the
	// engine, which knows the running homonto version, enforces it.
	Compat    string
	Skills    map[string]string // skill name -> catalog-relative path ("skills/<n>")
	Commands  map[string]string // command name -> catalog-relative path ("commands/<n>.md")
	Subagents map[string]string // subagent name -> catalog-relative path ("subagents/<n>.md")
	// srcFS is the filesystem this framework was read from (the embedded base or
	// a local overlay). Resource paths are relative to it; carried so a consumer
	// can resolve overlay content later. The base's is the common case.
	srcFS fs.FS
}

// Catalog is the loaded, indexed catalog.
type Catalog struct {
	fsys       fs.FS
	frameworks map[string]Framework
	skills     map[string]string // skill name -> source-relative path (global index)
	commands   map[string]string // command name -> source-relative path (global index)
	subagents  map[string]string // subagent name -> source-relative path (global index)
	plugins    map[string]string // plugin name -> source-relative path (global index)
	// skillFS/commandFS/subagentFS map each indexed resource name to the source
	// filesystem its path is relative to. For a base-only catalog every entry is
	// the base FS (identical to reading from c.fsys); a local overlay's resources
	// carry that overlay's FS so their content resolves from the right root.
	skillFS    map[string]fs.FS
	commandFS  map[string]fs.FS
	subagentFS map[string]fs.FS
	pluginFS   map[string]fs.FS
	version    string
}

// CurrentManifestSchemaVersion is the framework.toml manifest format version
// this binary supports. A manifest declaring a higher version is rejected
// fail-closed at load, so an older binary never silently half-reads a newer
// manifest (E1 phase-1 forward-safety, mirroring the config/state schema
// versions). Absent/0 is a legacy manifest, treated as the current version.
const CurrentManifestSchemaVersion = 1

type frameworkTOML struct {
	ManifestSchema int    `toml:"manifest_schema"`
	Name           string `toml:"name"`
	Version        string `toml:"version"`
	Description    string `toml:"description"`
	Dependencies   struct {
		Frameworks   []string `toml:"frameworks"`
		Capabilities []string `toml:"capabilities"`
	} `toml:"dependencies"`
	Provides struct {
		Capabilities []string `toml:"capabilities"`
	} `toml:"provides"`
	Compat struct {
		Homonto string `toml:"homonto"`
	} `toml:"compat"`
	Skills    map[string]string `toml:"skills"`
	Commands  map[string]string `toml:"commands"`
	Subagents map[string]string `toml:"subagents"`
}

// New loads the production catalog from the embedded filesystem.
func New() (*Catalog, error) { return Load(embedded.FS) }

// NewWithLocal loads the production catalog from the embedded filesystem, then
// merges each local single-framework root in locals (keyed by framework name).
// It is LoadWithLocal with the embedded base, the constructor a config uses to
// resolve its [frameworks.X] source="local:<path>" entries.
func NewWithLocal(locals map[string]fs.FS) (*Catalog, error) {
	return LoadWithLocal(embedded.FS, locals)
}

// Load parses a single catalog source. It is LoadOverlays with no overlays.
func Load(fsys fs.FS) (*Catalog, error) { return LoadOverlays(fsys) }

// LoadOverlays loads the base catalog source, then merges each overlay source
// over it. Every source is validated through the same checks (manifest schema,
// name==directory, resource-path existence). An overlay that redefines a
// resource name already provided by an earlier source with a different path is a
// strict conflict (the shared-index guard); an identical mapping collapses.
// version.txt is read from the base only; dependency-range validation runs once
// after all sources are indexed so a cross-source dependency is checked.
func LoadOverlays(base fs.FS, overlays ...fs.FS) (*Catalog, error) {
	c, err := newBaseCatalog(base)
	if err != nil {
		return nil, err
	}
	for _, src := range append([]fs.FS{base}, overlays...) {
		if err := c.mergeSource(src); err != nil {
			return nil, err
		}
	}
	if err := c.validateDependencyRanges(); err != nil {
		return nil, err
	}
	if err := c.validateCapabilities(); err != nil {
		return nil, err
	}
	return c, nil
}

// LoadWithLocal loads the base catalog via mergeSource, then merges each local
// single-framework root in locals (keyed by framework name) via
// mergeFrameworkRoot. A local root's framework.toml declares one framework at
// the root with framework-root-relative resource paths; its resources index and
// materialize as builtin:<name>, reusing the whole projection path. Passing an
// empty locals map is identical to Load(base). Dependency-range validation runs
// once after every source is indexed so a cross-source dependency is checked.
func LoadWithLocal(base fs.FS, locals map[string]fs.FS) (*Catalog, error) {
	c, err := newBaseCatalog(base)
	if err != nil {
		return nil, err
	}
	if err := c.mergeSource(base); err != nil {
		return nil, err
	}
	// Deterministic merge order for stable conflict error messages.
	names := make([]string, 0, len(locals))
	for name := range locals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := c.mergeFrameworkRoot(name, locals[name]); err != nil {
			return nil, err
		}
	}
	if err := c.validateDependencyRanges(); err != nil {
		return nil, err
	}
	if err := c.validateCapabilities(); err != nil {
		return nil, err
	}
	return c, nil
}

// newBaseCatalog allocates a Catalog over base and reads version.txt (base
// only). It indexes nothing; the caller merges sources.
func newBaseCatalog(base fs.FS) (*Catalog, error) {
	c := &Catalog{
		fsys:       base,
		frameworks: map[string]Framework{},
		skills:     map[string]string{},
		commands:   map[string]string{},
		subagents:  map[string]string{},
		plugins:    map[string]string{},
		skillFS:    map[string]fs.FS{},
		commandFS:  map[string]fs.FS{},
		subagentFS: map[string]fs.FS{},
		pluginFS:   map[string]fs.FS{},
	}
	vb, err := fs.ReadFile(base, "version.txt")
	if err != nil {
		return nil, fmt.Errorf("catalog: read version.txt: %w", err)
	}
	c.version = strings.TrimSpace(string(vb))
	return c, nil
}

// mergeSource indexes one catalog source's frameworks and loose resources into
// c, validating each and enforcing the strict cross-source conflict policy via
// the shared index. fs operations use src, so resource paths are validated in
// the source they belong to.
func (c *Catalog) mergeSource(src fs.FS) error {
	// readOptionalDir returns the entries of a directory that may legitimately
	// be absent (subagents/, skills/, commands/ are all optional). A real I/O
	// error (permission denied, corrupt FS) is surfaced rather than collapsed
	// into "missing" — a corrupted overlay must not silently mask itself.
	readOptionalDir := func(name string) ([]fs.DirEntry, error) {
		entries, err := fs.ReadDir(src, name)
		if err == nil {
			return entries, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("catalog: read %s: %w", name, err)
	}

	// Loose (framework-agnostic) subagents: every "<n>.md" file directly under
	// subagents/ is indexed by base name, independent of any framework
	// declaring it. Unlike skills/commands, subagents are designed to include
	// standalone builtins (e.g. onto-reviewer, onto-explorer) referenced
	// directly by an explicit [subagents.X] config entry with no framework
	// home. The subagents/ directory is optional — fixtures/tests that don't
	// exercise subagents need not provide one.
	subagentEntries, err := readOptionalDir("subagents")
	if err != nil {
		return err
	}
	for _, e := range subagentEntries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == e.Name() {
			continue // not a ".md" file
		}
		c.subagents[name] = path.Join("subagents", e.Name())
		c.subagentFS[name] = src
	}

	dirs, err := fs.ReadDir(src, "frameworks")
	if err != nil {
		return fmt.Errorf("catalog: read frameworks: %w", err)
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := d.Name()
		tp := path.Join("frameworks", dir, "framework.toml")
		b, err := fs.ReadFile(src, tp)
		if err != nil {
			return fmt.Errorf("catalog: read %s: %w", tp, err)
		}
		var ft frameworkTOML
		if err := toml.Unmarshal(b, &ft); err != nil {
			return fmt.Errorf("catalog: parse %s: %w", tp, err)
		}
		// Forward-safety: refuse a manifest from a newer schema before indexing
		// any of its resources, so an older binary never silently half-reads a
		// newer framework manifest. Absent/0 is a legacy manifest (current).
		if ft.ManifestSchema > CurrentManifestSchemaVersion {
			return fmt.Errorf("catalog: framework %q manifest_schema %d is newer than this binary supports (up to %d) — upgrade homonto: %w", dir, ft.ManifestSchema, CurrentManifestSchemaVersion, schema.ErrTooNew)
		}
		if ft.Name != dir {
			return fmt.Errorf("catalog: framework %q declares name %q; name must equal directory", dir, ft.Name)
		}
		if err := c.indexFramework(dir, src, ft); err != nil {
			return err
		}
	}
	// Loose (framework-agnostic) skills and commands: a skills/<dir> holding a
	// SKILL.md, or a commands/<n>.md file, not already claimed by a framework is
	// indexed by name so it installs as builtin:<name> with no framework home
	// (mirrors loose subagents). Framework declarations take precedence.
	skillEntries, err := readOptionalDir("skills")
	if err != nil {
		return err
	}
	for _, e := range skillEntries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, ok := c.skills[name]; ok {
			continue // a framework already declares this skill
		}
		sp := path.Join("skills", name)
		if _, err := fs.Stat(src, path.Join(sp, "SKILL.md")); err != nil {
			continue // a skill directory must hold a SKILL.md
		}
		c.skills[name] = sp
		c.skillFS[name] = src
	}
	// Bundled plugins: every plugins/<name>/ entry holding a plugin entry
	// file (plugin.ts) is indexed like a skill directory — owned catalog
	// content, materialized and version-gated, but never auto-enabled.
	pluginEntries, err := readOptionalDir("plugins")
	if err != nil {
		return err
	}
	for _, e := range pluginEntries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if _, ok := c.plugins[name]; ok {
			continue // a framework or earlier source already declares it
		}
		sp := path.Join("plugins", name)
		if _, err := fs.Stat(src, path.Join(sp, "plugin.ts")); err != nil {
			continue // a plugin directory must hold a plugin.ts
		}
		c.plugins[name] = sp
		c.pluginFS[name] = src
	}
	commandEntries, err := readOptionalDir("commands")
	if err != nil {
		return err
	}
	for _, e := range commandEntries {
		if e.IsDir() {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == e.Name() {
			continue // not a ".md" file
		}
		if _, ok := c.commands[name]; ok {
			continue // a framework already declares this command
		}
		c.commands[name] = path.Join("commands", e.Name())
		c.commandFS[name] = src
	}
	return nil
}

// validResourceName rejects a resource name that is not a plain single path
// component. A framework-manifest key (skill/command/subagent name) later becomes
// a local filesystem path element during materialization and linking —
// filepath.Join(root, name) followed by os.RemoveAll / os.Rename / symlink
// creation — so an empty, ".", "..", or separator-bearing name could escape the
// managed root and delete or overwrite arbitrary files (a pinned-remote or shared
// local framework would otherwise be an arbitrary-write vector). Mirrors
// config.validateResourceName; duplicated here because catalog must not import
// config (see the package doc).
func validResourceName(kind, name string) error {
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) || name != path.Base(name) {
		return fmt.Errorf("catalog: %s name %q is not a plain name (path traversal rejected)", kind, name)
	}
	return nil
}

// indexFramework indexes one framework's declared resources and metadata into c,
// reading paths relative to src and recording src as each resource's source FS.
// Each resource path is validated to exist in src, and the strict shared-index
// conflict guard rejects a name already mapped to a different path by an earlier
// source. Shared by mergeSource (base/overlay frameworks under frameworks/<dir>)
// and mergeFrameworkRoot (a local single-framework root).
func (c *Catalog) indexFramework(name string, src fs.FS, ft frameworkTOML) error {
	for skill, sp := range ft.Skills {
		if err := validResourceName("skill", skill); err != nil {
			return err
		}
		if _, err := fs.Stat(src, sp); err != nil {
			return fmt.Errorf("catalog: framework %q skill %q path %q missing from catalog", name, skill, sp)
		}
		if prev, ok := c.skills[skill]; ok && prev != sp {
			return fmt.Errorf("catalog: skill %q mapped to both %q and %q", skill, prev, sp)
		}
		c.skills[skill] = sp
		c.skillFS[skill] = src
	}
	for command, cp := range ft.Commands {
		if err := validResourceName("command", command); err != nil {
			return err
		}
		if _, err := fs.Stat(src, cp); err != nil {
			return fmt.Errorf("catalog: framework %q command %q path %q missing from catalog", name, command, cp)
		}
		if prev, ok := c.commands[command]; ok && prev != cp {
			return fmt.Errorf("catalog: command %q mapped to both %q and %q", command, prev, cp)
		}
		c.commands[command] = cp
		c.commandFS[command] = src
	}
	for subagent, sap := range ft.Subagents {
		if err := validResourceName("subagent", subagent); err != nil {
			return err
		}
		if _, err := fs.Stat(src, sap); err != nil {
			return fmt.Errorf("catalog: framework %q subagent %q path %q missing from catalog", name, subagent, sap)
		}
		if prev, ok := c.subagents[subagent]; ok && prev != sap {
			return fmt.Errorf("catalog: subagent %q mapped to both %q and %q", subagent, prev, sap)
		}
		c.subagents[subagent] = sap
		c.subagentFS[subagent] = src
	}
	// Split each dependency "name@constraint" into the graph name (used for
	// transitive resolution and cycle detection) and its version constraint
	// (validated once every framework is indexed).
	depNames := make([]string, 0, len(ft.Dependencies.Frameworks))
	var depConstraints map[string]string
	for _, entry := range ft.Dependencies.Frameworks {
		dep, constraint := parseDep(entry)
		depNames = append(depNames, dep)
		if constraint != "" {
			if depConstraints == nil {
				depConstraints = map[string]string{}
			}
			depConstraints[dep] = constraint
		}
	}
	c.frameworks[name] = Framework{
		srcFS:                 src,
		Name:                  ft.Name,
		Version:               ft.Version,
		Description:           ft.Description,
		Dependencies:          depNames,
		DependencyConstraints: depConstraints,
		Provides:              ft.Provides.Capabilities,
		RequiredCapabilities:  ft.Dependencies.Capabilities,
		Compat:                ft.Compat.Homonto,
		Skills:                ft.Skills,
		Commands:              ft.Commands,
		Subagents:             ft.Subagents,
	}
	return nil
}

// mergeFrameworkRoot indexes a local single-framework root: <src>/framework.toml
// declares exactly one framework whose name must equal the given name, with
// framework-root-relative resource paths (validated to exist in src). Resources
// index with FS=src so their content materializes from the local root, exactly
// like a builtin framework's from the embedded base.
func (c *Catalog) mergeFrameworkRoot(name string, src fs.FS) error {
	b, err := fs.ReadFile(src, "framework.toml")
	if err != nil {
		return fmt.Errorf("catalog: read local framework %q framework.toml: %w", name, err)
	}
	var ft frameworkTOML
	if err := toml.Unmarshal(b, &ft); err != nil {
		return fmt.Errorf("catalog: parse local framework %q framework.toml: %w", name, err)
	}
	// Forward-safety mirrors mergeSource: refuse a newer manifest schema before
	// indexing any resource.
	if ft.ManifestSchema > CurrentManifestSchemaVersion {
		return fmt.Errorf("catalog: local framework %q manifest_schema %d is newer than this binary supports (up to %d) — upgrade homonto: %w", name, ft.ManifestSchema, CurrentManifestSchemaVersion, schema.ErrTooNew)
	}
	if ft.Name != name {
		return fmt.Errorf("catalog: local framework %q declares name %q; name must equal the framework key", name, ft.Name)
	}
	return c.indexFramework(name, src, ft)
}

// validateCapabilities resolves every framework's required capabilities against
// the capabilities provided across all indexed frameworks (a capability is an
// interface, so multiple providers are fine). An unresolved requirement, or a
// malformed capability string, fails loud. Runs after all sources are merged.
func (c *Catalog) validateCapabilities() error {
	provided := map[string]bool{}
	for name, fw := range c.frameworks {
		for _, cap := range fw.Provides {
			if _, _, err := parseCapability(cap); err != nil {
				return fmt.Errorf("catalog: framework %q provides malformed capability %q: %w", name, cap, err)
			}
			provided[cap] = true
		}
	}
	for name, fw := range c.frameworks {
		for _, req := range fw.RequiredCapabilities {
			if _, _, err := parseCapability(req); err != nil {
				return fmt.Errorf("catalog: framework %q requires malformed capability %q: %w", name, req, err)
			}
			if !provided[req] {
				return fmt.Errorf("catalog: framework %q requires capability %q, but no loaded framework provides it", name, req)
			}
		}
	}
	return nil
}

// validateDependencyRanges checks every framework's constrained dependencies
// against the versions of the indexed frameworks, after all sources are merged.
func (c *Catalog) validateDependencyRanges() error {
	// Validate dependency version ranges now that every framework is indexed: a
	// constrained dependency must resolve to a known framework whose version
	// satisfies the constraint, else fail loud (E1 compatibility gate).
	for name, fw := range c.frameworks {
		for dep, constraint := range fw.DependencyConstraints {
			target, ok := c.frameworks[dep]
			if !ok {
				return fmt.Errorf("catalog: framework %q depends on %q@%s, but %q is not a known framework", name, dep, constraint, dep)
			}
			ok, err := satisfies(target.Version, constraint)
			if err != nil {
				return fmt.Errorf("catalog: framework %q dependency %q: %w", name, dep, err)
			}
			if !ok {
				return fmt.Errorf("catalog: framework %q requires %q@%s, but %q is version %s", name, dep, constraint, dep, target.Version)
			}
		}
	}
	return nil
}

// Version returns the catalog version string from version.txt.
func (c *Catalog) Version() string { return c.version }

// FrameworkVersion returns the declared version of a builtin framework and
// whether it exists in the catalog.
func (c *Catalog) FrameworkVersion(name string) (string, bool) {
	fw, ok := c.frameworks[name]
	if !ok {
		return "", false
	}
	return fw.Version, true
}

// Framework returns the indexed framework and whether it exists.
func (c *Catalog) Framework(name string) (Framework, bool) {
	f, ok := c.frameworks[name]
	return f, ok
}

// SkillPath returns a skill's catalog-relative path ("skills/<n>") and whether
// it is known.
func (c *Catalog) SkillPath(name string) (string, bool) {
	p, ok := c.skills[name]
	return p, ok
}

// CommandPath returns a command's catalog-relative path ("commands/<n>.md") and
// whether it is known.
func (c *Catalog) CommandPath(name string) (string, bool) {
	p, ok := c.commands[name]
	return p, ok
}

// SubagentPath returns a subagent's catalog-relative path ("subagents/<n>.md")
// and whether it is known.
func (c *Catalog) SubagentPath(name string) (string, bool) {
	p, ok := c.subagents[name]
	return p, ok
}

// SubagentContent returns a builtin subagent/agent's content by name from the
// catalog filesystem, and whether the name is known.
func (c *Catalog) SubagentContent(name string) ([]byte, bool, error) {
	p, ok := c.subagents[name]
	if !ok {
		return nil, false, nil
	}
	b, err := fs.ReadFile(c.subagentFS[name], p)
	return b, true, err
}
