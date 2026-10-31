package engine

import (
	"fmt"

	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/config"
)

// checkFrameworkCompat enforces each declared framework's [compat].homonto range
// against the running homonto version (HomontoVersion), fail-closed, before any
// projection. A framework with no [compat] is unconstrained; an empty
// HomontoVersion (unstamped build / tests) skips the check.
func (e *Engine) checkFrameworkCompat() error {
	if e.HomontoVersion == "" {
		return nil
	}
	cl, err := e.Cfg.FrameworkCatalog()
	if err != nil {
		return err
	}
	for fwName, res := range e.Cfg.Frameworks {
		catName, ok := config.FrameworkCatalogName(fwName, res.Source)
		if !ok {
			continue
		}
		fw, ok := cl.Framework(catName)
		if !ok || fw.Compat == "" {
			continue
		}
		okv, err := catalog.SatisfiesLoose(e.HomontoVersion, fw.Compat)
		if err != nil {
			return fmt.Errorf("framework %q: invalid [compat].homonto %q: %w", fwName, fw.Compat, err)
		}
		if !okv {
			return fmt.Errorf("framework %q requires homonto %s, but this build is %s", fwName, fw.Compat, e.HomontoVersion)
		}
	}
	return nil
}
