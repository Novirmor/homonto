package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultTmpDir is the scratch directory used when [tmp] is declared without
// a dir (ADR 0048): repo-root level, gitignored by apply, never cleaned by
// homonto.
const DefaultTmpDir = ".tmp"

// tmpTable is the raw [tmp] table — a name→string map so an unknown key
// survives decode and can be rejected by name (the same philosophy as
// [tooling]). It hangs off Config as a POINTER so a bare `[tmp]` table
// (feature on, all defaults) is distinguishable from no table at all.
type tmpTable = map[string]string

// ResolvedTmp reports whether [tmp] is declared, and the validated,
// slash-separated, workspace-relative scratch directory (DefaultTmpDir when
// the table carries no dir).
func (c *Config) ResolvedTmp() (bool, string) {
	if c.Tmp == nil {
		return false, ""
	}
	dir := DefaultTmpDir
	if raw := strings.TrimSpace((*c.Tmp)["dir"]); raw != "" {
		dir = filepath.ToSlash(filepath.Clean(raw))
	}
	return true, dir
}

// validateTmp rejects unknown keys and unusable directories: the dir must be
// relative, must stay below the workspace root, must name a dedicated
// subdirectory, and must stay disjoint from every tree homonto or the
// workflows own — a colliding scratch dir (the workflow records root, the
// projection target, the local skills root, the managed .homonto subtrees)
// would be gitignored or rebuilt over, taking real content with it.
func validateTmp(c *Config) error {
	t := c.Tmp
	if t == nil {
		return nil
	}
	keys := make([]string, 0, len(*t))
	for key := range *t {
		keys = append(keys, key)
	}
	sort.Strings(keys) // deterministic offender, like every other validator
	for _, key := range keys {
		if key != "dir" {
			return fmt.Errorf("parse config: tmp.%s is an unknown key — [tmp] takes only dir", key)
		}
	}
	_, dir := (&Config{Tmp: t}).ResolvedTmp()
	switch {
	case dir == "" || dir == ".":
		return fmt.Errorf("parse config: [tmp] dir must name a dedicated subdirectory, not the workspace root")
	case filepath.IsAbs(dir):
		return fmt.Errorf("parse config: [tmp] dir %q must be a relative path below the workspace root", dir)
	case dir == ".." || strings.HasPrefix(dir, "../"):
		return fmt.Errorf("parse config: [tmp] dir %q must stay below the workspace root", dir)
	case dir == ".git" || strings.HasPrefix(dir, ".git/"):
		return fmt.Errorf("parse config: [tmp] dir %q may not live inside .git", dir)
	}
	// Disjoint from the workflow records tree: a scratch dir equal to, above,
	// or inside the records root would gitignore real workflow records (dir ==
	// root writes "/root/" into .gitignore) or bury scratch inside the
	// audited tree the dirt gates and discovery watch.
	root := c.Workflow.RootOrDefault()
	if pathOverlaps(dir, root) {
		return fmt.Errorf("parse config: [tmp] dir %q overlaps the workflow records root %q", dir, root)
	}
	for _, reserved := range []string{".opencode", "homonto", ".homonto/catalog", ".homonto/remote", ".homonto/cache"} {
		if pathOverlaps(dir, reserved) {
			return fmt.Errorf("parse config: [tmp] dir %q overlaps %q, which homonto owns (projection, local skills, or materialized state)", dir, reserved)
		}
	}
	return nil
}

// pathOverlaps reports whether a and b are the same path or one contains the
// other (slash-boundary comparison so "doc" never matches "docs").
func pathOverlaps(a, b string) bool {
	if a == b {
		return true
	}
	if strings.HasPrefix(a, b+"/") || strings.HasPrefix(b, a+"/") {
		return true
	}
	return false
}
