package catalog

import (
	"io/fs"
	"strings"
	"testing"

	embedded "github.com/noviopenworks/homonto/catalog"
)

// Shipped catalog content must name no tooling provider: provider prose lives
// in catalog/tooling/ fragments and reaches a skill only through the generated
// reference. A name that creeps back into a skill, command, or subagent would
// tell every user about a tool they may not run and may not want — the exact
// problem [tooling] exists to fix.
//
// Scoped to catalog/ deliberately. docs/ legitimately names providers when
// documenting the config, and so does this repository's own AGENTS.md.
func TestCatalogContentNamesNoToolingProvider(t *testing.T) {
	providers := []string{"rtk", "graphify", "codegraph", "okf"}
	scanned := 0
	err := fs.WalkDir(embedded.FS, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		// The fragments are where provider names are supposed to live.
		if strings.HasPrefix(p, "tooling/") {
			return nil
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		data, err := fs.ReadFile(embedded.FS, p)
		if err != nil {
			return err
		}
		scanned++
		body := strings.ToLower(string(data))
		for _, name := range providers {
			if strings.Contains(body, name) {
				t.Errorf("%s names the tooling provider %q; move that prose into catalog/tooling/ and point at the generated reference instead", p, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk embedded catalog: %v", err)
	}
	// Guard against a vacuous pass: a walk that visited nothing (bad embed
	// pattern, moved catalog root) would otherwise report success forever.
	if scanned < 20 {
		t.Fatalf("only scanned %d catalog markdown files; the walk is not covering the catalog", scanned)
	}
}
