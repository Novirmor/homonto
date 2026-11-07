package worktask

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Novirmor/homonto/internal/capability"
	"github.com/Novirmor/homonto/internal/catalog"
	"github.com/Novirmor/homonto/internal/config"
	"github.com/Novirmor/homonto/internal/workspace"
)

// Engine is the declared contract of one installed engine, as a workspace
// task uses it (ADR 0093). Every task decision that used to branch on the
// engine's name ("no" versus onto/to) reads these properties instead.
type Engine struct {
	Name    string
	Builtin bool
	catalog.EngineContract
}

// engine resolves the installed engine selected by name: the [engines.*]
// declaration whose package name is engine, with a schema 2 [engine]
// contract.
func (s *Store) engine(name string) (Engine, error) {
	cl, err := s.Config.FrameworkCatalog()
	if err != nil {
		return Engine{}, err
	}
	keys := make([]string, 0, len(s.Config.Engines))
	for key := range s.Config.Engines {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var installed []string
	for _, key := range keys {
		res := s.Config.Engines[key]
		catName, ok := config.FrameworkCatalogName(key, res.Source)
		if !ok {
			continue
		}
		installed = append(installed, catName)
		if catName != name {
			continue
		}
		fw, ok := cl.Framework(catName)
		if !ok || fw.Engine == nil {
			return Engine{}, fmt.Errorf("engine %q has no schema 2 [engine] contract; it cannot run workspace tasks (docs/contracts/engine.md)", name)
		}
		// Selection refuses an engine a target harness cannot run, naming the
		// gap, before any task state exists (ADR 0082, ADR 0097).
		for _, tool := range res.TargetsOrAll() {
			if d, ok := capability.ForHarness(tool); ok {
				if err := d.Check(catName, fw.HarnessRequirements); err != nil {
					return Engine{}, fmt.Errorf("engine %q cannot run workspace tasks on %s: %w", name, tool, err)
				}
			}
		}
		e := Engine{Name: name, Builtin: strings.HasPrefix(res.Source, "builtin:"), EngineContract: *fw.Engine}
		// An external native engine reports its lifecycle through the record it
		// keeps under <workflow root>/engines/<name>/ and the worktree registry,
		// exactly as onto and to do under changes/ and tasks/ (#67).
		if e.OwnsLifecycle() && !e.Builtin && !workspace.External(name) {
			return Engine{}, fmt.Errorf("engine %q owns a native lifecycle but its name cannot address a lifecycle record; use a lowercase name of letters, digits and '-'", name)
		}
		return e, nil
	}
	if len(installed) == 0 {
		return Engine{}, fmt.Errorf("no engine is installed; declare one under [engines]")
	}
	return Engine{}, fmt.Errorf("select one installed engine: %s", strings.Join(installed, ", "))
}

// requireOperation fails when the task's engine does not declare op.
func (e Engine) requireOperation(op string) error {
	if !e.Supports(op) {
		return fmt.Errorf("engine %q does not support %s", e.Name, op)
	}
	return nil
}

// Engine resolves an installed engine's declared contract, for callers that
// build on workspace tasks (a swarm requires a stateless engine, ADR 0113).
func (s *Store) Engine(name string) (Engine, error) { return s.engine(name) }
