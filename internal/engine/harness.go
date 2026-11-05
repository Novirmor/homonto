package engine

import (
	"fmt"
	"sort"

	"github.com/Novirmor/homonto/internal/capability"
	"github.com/Novirmor/homonto/internal/config"
)

// checkHarnessCapabilities fails plan closed when a declared package requires
// a harness capability that one of its target adapters does not provide as
// enforced (ADR 0082, ADR 0097). The error names the package, the gap and the
// harness, before anything is projected. An adapter that declares nothing
// provides nothing.
func (e *Engine) checkHarnessCapabilities() error {
	cl, err := e.Cfg.FrameworkCatalog()
	if err != nil {
		return err
	}
	declarations := map[string]capability.Declaration{}
	for _, a := range e.Adapters {
		d := capability.Declaration{Harness: a.Name(), Version: "(no capability declaration)"}
		if declarer, ok := a.(capability.Declarer); ok {
			d = declarer.Capabilities()
		}
		declarations[a.Name()] = d
	}
	names := make([]string, 0, len(e.Cfg.Frameworks))
	for name := range e.Cfg.Frameworks {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		res := e.Cfg.Frameworks[name]
		catName, ok := config.FrameworkCatalogName(name, res.Source)
		if !ok {
			continue
		}
		fw, ok := cl.Framework(catName)
		if !ok || len(fw.HarnessRequirements) == 0 {
			continue
		}
		for _, tool := range res.TargetsOrAll() {
			d, ok := declarations[tool]
			if !ok {
				continue
			}
			if err := d.Check(name, fw.HarnessRequirements); err != nil {
				return fmt.Errorf("plan: %w", err)
			}
		}
	}
	return nil
}

// harnessDeclarations lists the capability declaration of every harness this
// config targets, once per harness (repo-mode adapters share their harness's
// declaration), in adapter order.
func (e *Engine) harnessDeclarations() []capability.Declaration {
	var out []capability.Declaration
	seen := map[string]bool{}
	for _, a := range e.Adapters {
		declarer, ok := a.(capability.Declarer)
		if !ok {
			continue
		}
		d := declarer.Capabilities()
		if seen[d.Harness] {
			continue
		}
		seen[d.Harness] = true
		out = append(out, d)
	}
	return out
}
