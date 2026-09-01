// Package tostate models the to-state.yaml file used by the to workflow
// operator. The schema is deliberately independent of ontostate — the two
// frameworks are a hard wall apart (no shared fields, no migration path) —
// and deliberately minimal: a change's identity, its phase, and the
// self-asserted done checkbox. Only the to binary may write this file.
package tostate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileName is the state file the to binary owns inside each change directory.
const FileName = "to-state.yaml"

// Phases, in traversal order. "done" and "abandoned" are terminal.
const (
	PhasePlan      = "plan"
	PhaseDo        = "do"
	PhaseDone      = "done"
	PhaseAbandoned = "abandoned"
)

var validPhases = map[string]bool{
	PhasePlan:      true,
	PhaseDo:        true,
	PhaseDone:      true,
	PhaseAbandoned: true,
}

// State is the entire schema. Git facts are never persisted: Repos records
// only the optional declared-repo scope, while live git state is inspected at
// the terminal gate.
// Verified is a self-asserted checkbox, not a guarantee; Evidence is the
// optional text asserted alongside it (`to done --evidence`), recorded
// verbatim and never checked — it makes a real verification distinguishable
// from a skipped one after the fact, nothing more.
type State struct {
	Change   string `yaml:"change" json:"change"`
	Phase    string `yaml:"phase" json:"phase"`
	Created  string `yaml:"created,omitempty" json:"created,omitempty"`
	Finished string `yaml:"finished,omitempty" json:"finished,omitempty"`
	Verified bool   `yaml:"verified,omitempty" json:"verified,omitempty"`
	Evidence string `yaml:"evidence,omitempty" json:"evidence,omitempty"`
	// Repos lists selected [repos] names. The config repository is implicit.
	Repos []string `yaml:"repos,omitempty" json:"repos,omitempty"`
}

// Validate checks the minimal shape: a change name and a known phase.
func (s State) Validate() error {
	if s.Change == "" {
		return fmt.Errorf("to-state: change is required")
	}
	if !validPhases[s.Phase] {
		return fmt.Errorf("to-state: phase %q is not one of plan|do|done|abandoned", s.Phase)
	}
	seenRepos := map[string]bool{}
	for _, repo := range s.Repos {
		if strings.TrimSpace(repo) == "" || seenRepos[repo] {
			return fmt.Errorf("to-state: repos must contain unique non-empty declared repo names")
		}
		seenRepos[repo] = true
	}
	return nil
}

// Terminal reports whether the phase permits no further transitions.
func (s State) Terminal() bool {
	return s.Phase == PhaseDone || s.Phase == PhaseAbandoned
}

// Load reads the file at path and parses it as a State.
func Load(path string) (State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, fmt.Errorf("to-state: failed to read %s: %w", path, err)
	}
	var s State
	if err := yaml.Unmarshal(b, &s); err != nil {
		return State{}, fmt.Errorf("to-state: %s: %w", path, err)
	}
	return s, nil
}

// Save writes s to path as YAML, creating parent directories as needed. It
// writes to a temp file next to path and renames it into place, removing
// the temp file if any step fails.
func Save(path string, s State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("to-state: failed to create directory for %s: %w", path, err)
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("to-state: failed to marshal %s: %w", path, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("to-state: failed to write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("to-state: failed to rename %s to %s: %w", tmp, path, err)
	}
	return nil
}
