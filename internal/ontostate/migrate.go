package ontostate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/schema"
	"gopkg.in/yaml.v3"
)

// legacyState is a permissive superset that captures BOTH legacy shapes: the
// flat 7-field binary onto-state.yaml and the rich skill state.yaml. Fields
// absent from a given file simply stay zero.
type legacyState struct {
	Change   string   `yaml:"change"`
	Workflow string   `yaml:"workflow"`
	Phase    string   `yaml:"phase"`
	Created  string   `yaml:"created"`
	BaseRef  string   `yaml:"base_ref"`
	Deps     []string `yaml:"deps"`
	Archived bool     `yaml:"archived"`

	Decisions struct {
		Isolation string `yaml:"isolation"`
		Execution string `yaml:"execution"`
		TDD       string `yaml:"tdd"`
		Directive string `yaml:"directive"`
	} `yaml:"decisions"`

	Verify struct {
		Mode   string `yaml:"mode"`
		Result string `yaml:"result"`
	} `yaml:"verify"`

	Close struct {
		Merged bool `yaml:"merged"`
	} `yaml:"close"`

	Metrics struct {
		Phases       map[string]string `yaml:"phases"`
		TasksTotal   int               `yaml:"tasks_total"`
		VerifyRounds int               `yaml:"verify_rounds"`
		Upgraded     bool              `yaml:"upgraded"`
	} `yaml:"metrics"`
}

// isLegacy reports whether b carries no schema_version key.
func isLegacy(b []byte) bool {
	var probe struct {
		SchemaVersion *int `yaml:"schema_version"`
	}
	if err := yaml.Unmarshal(b, &probe); err != nil {
		return false // let parseAndMigrate surface the parse error
	}
	return probe.SchemaVersion == nil
}

// migrateLegacy maps a legacy shape onto the current State. Fields the legacy
// file lacks map to their zero value, which the schema treats as "unset".
// Renames: execution->build_mode, tdd->tdd_mode, verify.mode->verify.scale,
// metrics.upgraded->preset_escalated. "guides" is now a gated field but a
// legacy file's guides value is intentionally not carried: it re-resolves at
// close (onto-close sets it), and empty is a valid guides shape.
func migrateLegacy(l legacyState) State {
	return State{
		SchemaVersion: CurrentSchemaVersion,
		Change:        l.Change,
		Workflow:      l.Workflow,
		Phase:         l.Phase,
		Created:       l.Created,
		BaseRef:       l.BaseRef,
		Deps:          l.Deps,
		Isolation:     l.Decisions.Isolation,
		BuildMode:     l.Decisions.Execution,
		TDDMode:       l.Decisions.TDD,
		Verify:        Verify{Scale: l.Verify.Mode, Result: l.Verify.Result},
		Close:         Close{Merged: l.Close.Merged},
		Directive:     l.Decisions.Directive,
		Archived:      l.Archived,
		Observed: Observed{
			Metrics:         l.Metrics.Phases,
			TasksTotal:      l.Metrics.TasksTotal,
			VerifyRounds:    l.Metrics.VerifyRounds,
			PresetEscalated: l.Metrics.Upgraded,
		},
	}
}

// parseAndMigrate decodes raw state bytes, up-migrating a legacy (unversioned)
// shape to the current schema. A current-version doc is decoded as-is. The
// result always carries the current schema_version. sourceName names the file
// in any error. Migration is ordered and idempotent.
func parseAndMigrate(b []byte, sourceName string) (State, error) {
	if isLegacy(b) {
		var l legacyState
		if err := yaml.Unmarshal(b, &l); err != nil {
			return State{}, fmt.Errorf("onto-state: %s: %w", sourceName, err)
		}
		return migrateLegacy(l), nil
	}
	st, err := Parse(b)
	if err != nil {
		return State{}, err
	}
	if st.SchemaVersion > CurrentSchemaVersion {
		return State{}, fmt.Errorf("onto-state: %s: unknown schema_version %d (this binary supports up to %d) — upgrade onto: %w", sourceName, st.SchemaVersion, CurrentSchemaVersion, schema.ErrTooNew)
	}
	if st.SchemaVersion < 2 {
		st.IntegrationRequired = false
	}
	st.SchemaVersion = CurrentSchemaVersion
	return st, nil
}

// coreAgrees reports an error if the gated core fields that matter for the
// dual-legacy conflict policy — phase, workflow, archived — disagree.
func coreAgrees(a, b State) error {
	var diffs []string
	if a.Phase != b.Phase {
		diffs = append(diffs, fmt.Sprintf("phase (%q vs %q)", a.Phase, b.Phase))
	}
	if a.Workflow != b.Workflow {
		diffs = append(diffs, fmt.Sprintf("workflow (%q vs %q)", a.Workflow, b.Workflow))
	}
	if a.Archived != b.Archived {
		diffs = append(diffs, fmt.Sprintf("archived (%v vs %v)", a.Archived, b.Archived))
	}
	if len(diffs) > 0 {
		return fmt.Errorf("disagreeing legacy files: %s", strings.Join(diffs, ", "))
	}
	return nil
}

// mergeObserved unions two Observed groups; the skill file's richer per-field
// value wins when set.
func mergeObserved(base, skill Observed) Observed {
	out := base
	if len(skill.Metrics) > 0 {
		if out.Metrics == nil {
			out.Metrics = map[string]string{}
		}
		for k, v := range skill.Metrics {
			out.Metrics[k] = v
		}
	}
	if skill.TasksTotal != 0 {
		out.TasksTotal = skill.TasksTotal
	}
	if skill.VerifyRounds != 0 {
		out.VerifyRounds = skill.VerifyRounds
	}
	if skill.PresetEscalated {
		out.PresetEscalated = true
	}
	return out
}

// LoadChange loads a change's state from changeDir, resolving the canonical
// onto-state.yaml and a co-resident legacy state.yaml. If both are present and
// both legacy, disagreeing gated core (phase/workflow/archived) is malformed;
// otherwise Observed is merged onto the canonical state.
func LoadChange(changeDir string) (State, error) {
	ontoPath := filepath.Join(changeDir, "onto-state.yaml")
	skillPath := filepath.Join(changeDir, "state.yaml")

	ontoB, ontoErr := os.ReadFile(ontoPath)
	skillB, skillErr := os.ReadFile(skillPath)

	switch {
	case ontoErr == nil && skillErr == nil:
		ontoState, err := parseAndMigrate(ontoB, ontoPath)
		if err != nil {
			return State{}, err
		}
		skillState, err := parseAndMigrate(skillB, skillPath)
		if err != nil {
			return State{}, err
		}
		if isLegacy(ontoB) && isLegacy(skillB) {
			if err := coreAgrees(ontoState, skillState); err != nil {
				return State{}, fmt.Errorf("%s: %w", changeDir, err)
			}
		}
		ontoState.Observed = mergeObserved(ontoState.Observed, skillState.Observed)
		return ontoState, nil
	case ontoErr == nil:
		return parseAndMigrate(ontoB, ontoPath)
	case skillErr == nil:
		return parseAndMigrate(skillB, skillPath)
	default:
		return State{}, fmt.Errorf("onto-state: no state file (onto-state.yaml or state.yaml) in %s", changeDir)
	}
}

// Classify loads and validates a change directory, returning one of "valid",
// "malformed", or "missing-state". A directory with no state file at all is
// "missing-state" (not an error) so a deleted state file is reported rather
// than silently skipped (F14).
func Classify(changeDir string) (State, string, error) {
	ontoMissing := fileMissing(filepath.Join(changeDir, "onto-state.yaml"))
	skillMissing := fileMissing(filepath.Join(changeDir, "state.yaml"))
	if ontoMissing && skillMissing {
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
