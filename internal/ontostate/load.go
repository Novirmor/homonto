package ontostate

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/schema"
	"gopkg.in/yaml.v3"
)

// parseCurrent accepts only the live state schema (ADR 0081). Older shapes —
// unversioned files, earlier schema versions, the agent-managed state.yaml —
// fail with the fix instead of being converted on read.
func parseCurrent(b []byte, sourceName string) (State, error) {
	var probe struct {
		SchemaVersion *int   `yaml:"schema_version"`
		Workflow      string `yaml:"workflow"`
	}
	if err := yaml.Unmarshal(b, &probe); err != nil {
		return State{}, fmt.Errorf("onto-state: %s: %w", sourceName, err)
	}
	if err := schema.ValidateOntoState(probe.SchemaVersion, probe.Workflow, sourceName); err != nil {
		return State{}, err
	}
	return Parse(b)
}

// LoadChange loads a change's state from changeDir's onto-state.yaml.
func LoadChange(changeDir string) (State, error) {
	path := filepath.Join(changeDir, "onto-state.yaml")
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if !fileMissing(filepath.Join(changeDir, "state.yaml")) {
			return State{}, fmt.Errorf("onto-state: %s holds only a pre-binary state.yaml, which is no longer supported; finish or abandon the change with the onto release that created it", changeDir)
		}
		return State{}, fmt.Errorf("onto-state: no onto-state.yaml in %s", changeDir)
	}
	if err != nil {
		return State{}, fmt.Errorf("onto-state: failed to read %s: %w", path, err)
	}
	return parseCurrent(b, path)
}

// Classify loads and validates a change directory, returning one of "valid",
// "malformed", or "missing-state". A directory with no state file at all is
// "missing-state" (not an error) so a deleted state file is reported rather
// than silently skipped (F14). A lone legacy state.yaml is malformed, not missing.
func Classify(changeDir string) (State, string, error) {
	if fileMissing(filepath.Join(changeDir, "onto-state.yaml")) && fileMissing(filepath.Join(changeDir, "state.yaml")) {
		return State{}, "missing-state", nil
	}
	st, err := LoadChange(changeDir)
	if err != nil {
		return State{}, "malformed", err
	}
	if err := st.Validate(); err != nil {
		return State{}, "malformed", err
	}
	return st, "valid", nil
}

func fileMissing(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}
