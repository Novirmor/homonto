package engine

import (
	"fmt"
	"strings"

	"github.com/Novirmor/homonto/internal/adapter"
	"github.com/Novirmor/homonto/internal/state"
)

// stateKey is the namespace an adapter records under inside its state
// partition: its harness id (ADR 0116). A repository adapter's label
// ("claude@svc") names the repository; the partition file already scopes it.
// An adapter without a harness id records under its label minus the repo.
func stateKey(a adapter.Adapter, repo string) string {
	if h, ok := a.(interface{ HarnessID() string }); ok {
		return h.HarnessID()
	}
	return strings.TrimSuffix(a.Name(), "@"+repo)
}

// Partition resolves an adapter label to the state it records in, the key it
// records under there, and its repository ("" for the config repository).
func (e *Engine) Partition(label string) (st *state.State, key, repo string) {
	for _, t := range e.RepoTargets {
		if t.Adapter.Name() == label {
			return t.State, stateKey(t.Adapter, t.Name), t.Name
		}
	}
	return e.State, label, ""
}

// migrateRepoStateKeys moves records an older release kept under a
// repository adapter's label to its harness id (ADR 0116). The move lives in
// memory until the next save.
func (e *Engine) migrateRepoStateKeys() error {
	for _, t := range e.RepoTargets {
		if err := t.State.Rekey(t.Adapter.Name(), stateKey(t.Adapter, t.Name)); err != nil {
			return fmt.Errorf("repo %s: %w", t.Name, err)
		}
	}
	return nil
}
