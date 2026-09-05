package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/noviopenworks/homonto/internal/agentfm"
	"github.com/noviopenworks/homonto/internal/fsutil"
)

// Materialize extracts each named builtin skill from the embedded FS into
// dstRoot/<name>/, removing any existing per-skill directory first so a stale
// file from a previous version cannot survive an upgrade. It is the caller's
// job (engine) to gate this on the catalog version.
//
// shellProxy and codeIntel are the resolved tooling providers. Every skill that
// is a framework DISPATCHER — by convention the skill named after its own
// framework — additionally receives a generated ToolingReferencePath describing
// exactly those providers. The write lands in the staging directory before the
// atomic swap, so a crash never leaves a half-written reference in place.
func (c *Catalog) Materialize(dstRoot string, skillNames []string, shellProxy, codeIntel string) error {
	for _, name := range skillNames {
		sp, ok := c.skills[name]
		if !ok {
			return fmt.Errorf("catalog: unknown skill %q", name)
		}
		sub, err := fs.Sub(c.skillFS[name], sp)
		if err != nil {
			return fmt.Errorf("catalog: sub %q: %w", sp, err)
		}
		dstDir := filepath.Join(dstRoot, name)
		// Stage-then-swap so a read error, full disk, or crash mid-walk never
		// leaves a partially-written skill dir (which allSkillDirsExist would
		// mistake for complete and never repair). Write into a sibling staging
		// dir, then atomically swap it into place only after the whole walk
		// succeeds. Discard any leftover staging from a prior crashed run first.
		staging := dstDir + ".staging"
		if err := os.RemoveAll(staging); err != nil {
			return err
		}
		err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			target := filepath.Join(staging, filepath.FromSlash(p))
			if d.IsDir() {
				return os.MkdirAll(target, 0o755)
			}
			data, err := fs.ReadFile(sub, p)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			// Catalog files live under .homonto (control plane); write no-follow
			// so a planted symlink cannot redirect materialization.
			return fsutil.WriteControlPlane(target, data, 0o644)
		})
		if err != nil {
			// dstDir is untouched (still the prior complete version); drop the
			// partial staging so the next run starts clean.
			_ = os.RemoveAll(staging)
			return err
		}
		// A dispatcher skill carries the generated tooling reference. Writing it
		// into staging (not dstDir) keeps the same all-or-nothing guarantee the
		// verbatim walk above has.
		if c.IsDispatcher(name) {
			ref, rerr := c.RenderTooling(shellProxy, codeIntel)
			if rerr != nil {
				_ = os.RemoveAll(staging)
				return rerr
			}
			target := filepath.Join(staging, filepath.FromSlash(ToolingReferencePath))
			if mkerr := os.MkdirAll(filepath.Dir(target), 0o755); mkerr != nil {
				_ = os.RemoveAll(staging)
				return mkerr
			}
			if werr := fsutil.WriteControlPlane(target, ref, 0o644); werr != nil {
				_ = os.RemoveAll(staging)
				return werr
			}
		}
		// Swap: remove the old dir, then rename staging into place. A crash in
		// this window leaves dstDir absent (not partial), so the next run
		// re-materializes rather than trusting a half-written directory.
		if err := os.RemoveAll(dstDir); err != nil {
			_ = os.RemoveAll(staging)
			return err
		}
		if err := os.Rename(staging, dstDir); err != nil {
			return err
		}
	}
	return nil
}

// MaterializeCommands writes each named builtin command from the embedded FS to
// dstRoot/<name>.md (a single file), replacing any existing file. Unlike
// Materialize (per-skill directories), no RemoveAll is needed — a single-file
// overwrite fully replaces prior content on upgrade. It is the caller's job
// (engine) to gate this on the catalog version.
func (c *Catalog) MaterializeCommands(dstRoot string, names []string) error {
	for _, name := range names {
		cp, ok := c.commands[name]
		if !ok {
			return fmt.Errorf("catalog: unknown command %q", name)
		}
		data, err := fs.ReadFile(c.commandFS[name], cp)
		if err != nil {
			return fmt.Errorf("catalog: read %q: %w", cp, err)
		}
		if err := os.MkdirAll(dstRoot, 0o755); err != nil {
			return err
		}
		if err := fsutil.WriteControlPlane(filepath.Join(dstRoot, name+".md"), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// MaterializePlugins writes each named bundled plugin directory from the
// embedded FS to dstRoot/<name>/ — owned catalog content (ADR 0029), replaced
// byte-for-byte on upgrade, never executed by homonto itself.
func (c *Catalog) MaterializePlugins(dstRoot string, names []string) error {
	for _, name := range names {
		pp, ok := c.plugins[name]
		if !ok {
			return fmt.Errorf("catalog: unknown plugin %q", name)
		}
		sub, err := fs.Sub(c.pluginFS[name], pp)
		if err != nil {
			return fmt.Errorf("catalog: sub %q: %w", pp, err)
		}
		dst := filepath.Join(dstRoot, name)
		if err := os.RemoveAll(dst); err != nil {
			return err
		}
		if err := fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			rel := filepath.FromSlash(p)
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			data, err := fs.ReadFile(sub, p)
			if err != nil {
				return err
			}
			return fsutil.WriteControlPlane(filepath.Join(dst, rel), data, 0o644)
		}); err != nil {
			return fmt.Errorf("catalog: materialize plugin %q: %w", name, err)
		}
	}
	return nil
}

// PluginNames lists the bundled plugin names.
func (c *Catalog) PluginNames() []string {
	return sortedCopy(mapKeys(c.plugins))
}

func mapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ContentFingerprint digests the SOURCE content of the named skills, commands,
// and subagents — every byte a materialize would extract — deterministically
// (sorted names, path+content per file, NUL-delimited).
//
// The engine's materialize gate needs this because the embedded catalog's
// version.txt only identifies the BASE catalog: a `local:` framework's edited
// skill or a `remote:` framework's repinned digest changes overlay content
// while the version stays put, so a version-only gate served the stale bytes
// forever ("No changes. Everything up to date.") — repinning is how a patched
// resource ships, which made the staleness security-relevant.
func (c *Catalog) ContentFingerprint(skills, commands, subagents, plugins []string) (string, error) {
	h := sha256.New()
	hashFile := func(kind string, src fs.FS, p string) error {
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return fmt.Errorf("catalog: fingerprint %s %q: %w", kind, p, err)
		}
		fmt.Fprintf(h, "%s\x00%s\x00%d\x00", kind, p, len(data))
		h.Write(data)
		return nil
	}
	for _, name := range sortedCopy(skills) {
		sp, ok := c.skills[name]
		if !ok {
			return "", fmt.Errorf("catalog: unknown skill %q", name)
		}
		sub, err := fs.Sub(c.skillFS[name], sp)
		if err != nil {
			return "", fmt.Errorf("catalog: sub %q: %w", sp, err)
		}
		// WalkDir visits lexically, so the traversal is already deterministic.
		err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return walkErr
			}
			return hashFile("skill:"+name, sub, p)
		})
		if err != nil {
			return "", err
		}
	}
	for _, name := range sortedCopy(commands) {
		cp, ok := c.commands[name]
		if !ok {
			return "", fmt.Errorf("catalog: unknown command %q", name)
		}
		if err := hashFile("command:"+name, c.commandFS[name], cp); err != nil {
			return "", err
		}
	}
	for _, name := range sortedCopy(subagents) {
		sp, ok := c.subagents[name]
		if !ok {
			return "", fmt.Errorf("catalog: unknown subagent %q", name)
		}
		if err := hashFile("subagent:"+name, c.subagentFS[name], sp); err != nil {
			return "", err
		}
	}
	for _, name := range sortedCopy(plugins) {
		sp, ok := c.plugins[name]
		if !ok {
			return "", fmt.Errorf("catalog: unknown plugin %q", name)
		}
		sub, err := fs.Sub(c.pluginFS[name], sp)
		if err != nil {
			return "", err
		}
		err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return walkErr
			}
			return hashFile("plugin:"+name, sub, p)
		})
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func sortedCopy(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// SubagentFiles returns the file names MaterializeSubagents writes for name
// under dstRoot, given renderCtx: the shared <name>.md anchor, plus the
// <name>.opencode.md variant when the source carries a homonto: block and the
// render context targets the agent; a verbatim subagent yields the anchor
// alone. The engine's version gate uses this to check every file a
// materialize would produce — checking the anchor alone would let a deleted or
// stale variant survive as a dangling link forever.
func (c *Catalog) SubagentFiles(name string, renderCtx map[string]agentfm.RenderContext) ([]string, error) {
	sp, ok := c.subagents[name]
	if !ok {
		return nil, fmt.Errorf("catalog: unknown subagent %q", name)
	}
	data, err := fs.ReadFile(c.subagentFS[name], sp)
	if err != nil {
		return nil, fmt.Errorf("catalog: read %q: %w", sp, err)
	}
	files := []string{name + ".md"}
	if !agentfm.NeedsTransform(data) {
		return files, nil
	}
	for _, tool := range []string{"opencode"} {
		ctx, targeted := renderContextForTool(renderCtx, tool, name)
		if !targeted {
			continue
		}
		if _, rerr := agentfm.Render(name, data, tool, ctx); rerr != nil {
			return nil, fmt.Errorf("catalog: render subagent %q for %s: %w", name, tool, rerr)
		}
		files = append(files, name+"."+tool+".md")
	}
	return files, nil
}

// MaterializeSubagents writes the named subagents' content into dstRoot,
// rendering the OpenCode variant via renderCtx when the source carries a
// homonto: frontmatter block. Mirrors Materialize/MaterializeCommands: one
// file per name, removing any stale variant a previous verbatim projection
// left behind.
// MaterializeSubagents writes each named builtin subagent from the embedded FS
// to dstRoot/<name>.md, replacing any existing file byte-for-byte. When a
// subagent has a neutral `homonto:` access block, it also renders the OpenCode
// variant <name>.opencode.md from renderCtx; a non-targeted agent has a stale
// variant removed. The shared file remains the version-gate anchor and
// fallback for verbatim subagents.
func (c *Catalog) MaterializeSubagents(dstRoot string, names []string, renderCtx map[string]agentfm.RenderContext) error {
	for _, name := range names {
		sp, ok := c.subagents[name]
		if !ok {
			return fmt.Errorf("catalog: unknown subagent %q", name)
		}
		data, err := fs.ReadFile(c.subagentFS[name], sp)
		if err != nil {
			return fmt.Errorf("catalog: read %q: %w", sp, err)
		}
		if err := os.MkdirAll(dstRoot, 0o755); err != nil {
			return err
		}
		if err := fsutil.WriteControlPlane(filepath.Join(dstRoot, name+".md"), data, 0o644); err != nil {
			return err
		}
		if !agentfm.NeedsTransform(data) {
			// A catalog upgrade can turn a rendered agent verbatim (its homonto:
			// block removed). Remove any stale variant: OpenCode prefers a
			// <name>.<tool>.md when it exists, so a leftover render from the
			// previous version would silently win over the new content forever —
			// invisible to the gate (SubagentFiles no longer claims the variant)
			// and to doctor (which mirrors the same preference).
			for _, tool := range []string{"opencode"} {
				stale := filepath.Join(dstRoot, name+"."+tool+".md")
				if err := os.Remove(stale); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			continue
		}
		for _, tool := range []string{"opencode"} {
			ctx, targeted := renderContextForTool(renderCtx, tool, name)
			if !targeted {
				variant := filepath.Join(dstRoot, name+"."+tool+".md")
				if err := os.Remove(variant); err != nil && !os.IsNotExist(err) {
					return err
				}
				continue
			}
			rendered, rerr := agentfm.Render(name, data, tool, ctx)
			if rerr != nil {
				return fmt.Errorf("catalog: render subagent %q for %s: %w", name, tool, rerr)
			}
			variant := filepath.Join(dstRoot, name+"."+tool+".md")
			if err := fsutil.WriteControlPlane(variant, rendered, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderContextForTool preserves nil as the catalog-only, model-free rendering
// mode. Production contexts name their target agents; unselected tool variants
// are skipped, while a selected agent with no override gets a non-nil context
// that agentfm rejects instead of omitting a model line.
func renderContextForTool(renderCtx map[string]agentfm.RenderContext, tool, name string) (*agentfm.RenderContext, bool) {
	if renderCtx == nil {
		return nil, true
	}
	ctx := renderCtx[tool]
	if ctx.Targets != nil && !ctx.Targets[name] {
		return nil, false
	}
	return &ctx, true
}
