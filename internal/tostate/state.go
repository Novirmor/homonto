// Package tostate models the to-state.yaml file used by the to workflow
// operator. Its lifecycle is independent of ontostate; source provenance and
// optional conversion anchors survive promotion and demotion.
package tostate

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/schema"
	"gopkg.in/yaml.v3"
)

// FileName is the state file the to binary owns inside each change directory.
const FileName = "to-state.yaml"

const CurrentSchemaVersion = 1

const (
	SourceLegacy   = "legacy"
	SourceExplicit = "explicit"
)

// RepoBase pins source authority and explicit changes' immutable start anchors.
// Legacy anchors remain optional; to does not impose onto's integration gates.
type RepoBase struct {
	BaseRef      string `yaml:"base_ref,omitempty" json:"base_ref,omitempty"`
	BaseBranch   string `yaml:"base_branch,omitempty" json:"base_branch,omitempty"`
	GitCommonDir string `yaml:"git_common_dir" json:"git_common_dir"`
}

var canonicalCommit = regexp.MustCompile(`^[0-9a-f]{40}$|^[0-9a-f]{64}$`)

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

// State records source authority, not mutable Git facts such as HEAD or dirt.
// Verified is a self-asserted checkbox, not a guarantee; Evidence is the
// optional text asserted alongside it (`to done --evidence`), recorded
// verbatim and never checked — it makes a real verification distinguishable
// from a skipped one after the fact, nothing more.
type State struct {
	SchemaVersion int    `yaml:"schema_version,omitempty" json:"schema_version,omitempty"`
	ID            string `yaml:"id,omitempty" json:"id,omitempty"`
	Change        string `yaml:"change" json:"change"`
	Phase         string `yaml:"phase" json:"phase"`
	Created       string `yaml:"created,omitempty" json:"created,omitempty"`
	Finished      string `yaml:"finished,omitempty" json:"finished,omitempty"`
	Verified      bool   `yaml:"verified,omitempty" json:"verified,omitempty"`
	Evidence      string `yaml:"evidence,omitempty" json:"evidence,omitempty"`
	// Unversioned records retain legacy scope. Versioned records pin both the
	// scope model and each selected authority's canonical Git common directory.
	Repos     []string            `yaml:"repos,omitempty" json:"repos,omitempty"`
	RepoMode  string              `yaml:"repo_mode,omitempty" json:"repo_mode,omitempty"`
	RepoBases map[string]RepoBase `yaml:"repo_bases,omitempty" json:"repo_bases,omitempty"`
}

// Validate checks the minimal shape: a change name and a known phase.
func (s State) Validate() error {
	if err := s.validateSourceScope(); err != nil {
		return err
	}
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

func (s State) validateSourceScope() error {
	if s.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("to-state: unknown schema_version %d (supports up to %d); upgrade to: %w", s.SchemaVersion, CurrentSchemaVersion, schema.ErrTooNew)
	}
	if s.SchemaVersion < 0 {
		return fmt.Errorf("to-state: schema_version must be non-negative")
	}
	if s.SchemaVersion == 0 {
		if s.RepoMode != "" || len(s.RepoBases) != 0 {
			return fmt.Errorf("to-state: source provenance requires schema_version %d", CurrentSchemaVersion)
		}
		return nil
	}
	if s.RepoMode != SourceExplicit && s.RepoMode != SourceLegacy {
		return fmt.Errorf("to-state: repo_mode must be explicit or legacy")
	}
	if s.RepoMode == SourceExplicit && len(s.Repos) == 0 {
		return fmt.Errorf("to-state: explicit source scope requires selected repos")
	}
	expected := map[string]bool{}
	for _, repo := range s.Repos {
		if strings.TrimSpace(repo) == "" || expected[repo] {
			return fmt.Errorf("to-state: repos must contain unique non-empty declared repo names")
		}
		expected[repo] = true
	}
	if s.RepoMode == SourceLegacy && len(s.Repos) > 0 {
		expected[""] = true
	}
	if len(expected) != len(s.RepoBases) {
		return fmt.Errorf("to-state: repo_bases must identify exactly the recorded source scope")
	}
	for repo := range expected {
		base := s.RepoBases[repo]
		path := base.GitCommonDir
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("to-state: repo_bases[%q] requires an absolute canonical Git identity", repo)
		}
		if s.RepoMode == SourceExplicit && (!canonicalCommit.MatchString(base.BaseRef) || strings.TrimSpace(base.BaseBranch) == "") {
			return fmt.Errorf("to-state: repo_bases[%q] requires a canonical base_ref and non-empty base_branch captured at creation", repo)
		}
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
	if err := s.validateSourceScope(); err != nil {
		return State{}, err
	}
	if s.SchemaVersion != 0 {
		dec := yaml.NewDecoder(bytes.NewReader(b))
		dec.KnownFields(true)
		if err := dec.Decode(&s); err != nil {
			return State{}, fmt.Errorf("to-state: %s: %w", path, err)
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return State{}, fmt.Errorf("to-state: %s: unexpected trailing YAML document", path)
		}
	}
	return s, nil
}

// Save writes s to path as YAML, creating parent directories as needed through
// the shared no-follow control-plane writer.
func Save(path string, s State) error {
	if err := s.validateSourceScope(); err != nil {
		return err
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	b, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("to-state: failed to marshal %s: %w", path, err)
	}
	// Save also serves conversion and external records-root callers, so it must
	// validate parents without assuming the state lives below the config repo.
	if err := fsutil.WriteControlPlaneWithin(filepath.VolumeName(path)+string(filepath.Separator), path, b, 0o644); err != nil {
		return fmt.Errorf("to-state: failed to write %s: %w", path, err)
	}
	return nil
}
