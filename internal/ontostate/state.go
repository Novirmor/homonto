// Package ontostate models the onto-state.yaml file used by the onto
// binary's workflow tracking. It is the only package in this module
// permitted to import gopkg.in/yaml.v3.
package ontostate

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"gopkg.in/yaml.v3"
)

// validPhases enumerates the onto workflow phase set. The terminal phase is
// "close", not "archive".
var validPhases = map[string]bool{
	"open":   true,
	"design": true,
	"build":  true,
	"verify": true,
	"close":  true,
}

// CurrentSchemaVersion is the schema_version every write emits.
const CurrentSchemaVersion = 1

// enum membership sets for optional gated core fields. An empty value is
// always allowed (legacy-tolerant); a non-empty value must be a member.
var (
	validWorkflows     = map[string]bool{"full": true, "fix": true, "tweak": true}
	validIsolations    = map[string]bool{"branch": true, "worktree": true}
	validIntegrations  = map[string]bool{"merge": true, "pr": true}
	validBuildModes    = map[string]bool{"direct": true, "subagent": true}
	validTDDModes      = map[string]bool{"tdd": true, "direct": true}
	validVerifyScales  = map[string]bool{"light": true, "full": true}
	validVerifyResults = map[string]bool{"pending": true, "pass": true, "fail": true}
)

// ValidWorkflow reports whether w is a recognized workflow value.
func ValidWorkflow(w string) bool { return validWorkflows[w] }

// ValidPhase reports whether p is a recognized workflow phase. It is exported so
// commands that bake a phase into a path or decision (e.g. handoff --write) can
// reject a malformed or traversal-carrying value before using it.
func ValidPhase(p string) bool { return validPhases[p] }

// GuidesResolved reports whether the guides obligation is discharged: either
// "updated" or a "waived:<reason>". Empty and "pending" are unresolved. It
// answers the close-phase question "were the guides dealt with", distinct from
// ValidGuides which only checks the value is well-formed.
func GuidesResolved(v string) bool {
	return v == "updated" || strings.HasPrefix(v, "waived:")
}

// ValidGuides reports whether v is a recognized guides value: empty (unset),
// "pending", "updated", or any "waived:<reason>". The waived form is a prefix,
// not a fixed member, so guides cannot use the enum-setter machinery. The
// literal placeholder "<reason>" is rejected: it is the gate schema's template
// text, and accepting it verbatim would record a meaningless waiver that then
// counts as a discharged obligation.
func ValidGuides(v string) bool {
	if r, ok := strings.CutPrefix(v, "waived:"); ok {
		r = strings.TrimSpace(r)
		return r != "" && r != "<reason>"
	}
	return v == "" || v == "pending" || v == "updated"
}

// Verify holds the gated verify-phase fields.
type Verify struct {
	Scale  string `yaml:"scale,omitempty" json:"scale,omitempty"`   // light | full | ""
	Result string `yaml:"result,omitempty" json:"result,omitempty"` // pending | pass | fail
}

// Close holds the gated close-phase progress fields.
type Close struct {
	Merged bool `yaml:"merged,omitempty" json:"merged,omitempty"`
}

// Observed carries observational state. It is never gated: no field here may
// block a transition, and unknown values in it can never break a gate.
type Observed struct {
	Metrics         map[string]string `yaml:"metrics,omitempty" json:"metrics,omitempty"` // phase -> YYYY-MM-DD
	TasksTotal      int               `yaml:"tasks_total,omitempty" json:"tasks_total,omitempty"`
	VerifyRounds    int               `yaml:"verify_rounds,omitempty" json:"verify_rounds,omitempty"`
	PresetEscalated bool              `yaml:"preset_escalated,omitempty" json:"preset_escalated,omitempty"`
}

// State models onto-state.yaml: a schema_version, a flat gated core, and a
// carried observational group. Validation inspects the core only.
type State struct {
	SchemaVersion int `yaml:"schema_version,omitempty" json:"schema_version,omitempty"`

	// gated core
	Change string `yaml:"change" json:"change"`
	// ID is a stable, name-independent identifier assigned once at `onto new` and
	// never rewritten by any later command, so a change's identity survives a
	// rename. Absent on legacy states (never retro-minted on read).
	ID       string   `yaml:"id,omitempty" json:"id,omitempty"`
	Workflow string   `yaml:"workflow,omitempty" json:"workflow,omitempty"`
	Phase    string   `yaml:"phase" json:"phase"`
	Created  string   `yaml:"created,omitempty" json:"created,omitempty"`
	BaseRef  string   `yaml:"base_ref,omitempty" json:"base_ref,omitempty"`
	Deps     []string `yaml:"deps,omitempty" json:"deps,omitempty"`
	// Supersedes lists change names this change replaces/obsoletes (a traceability
	// relationship surfaced by `onto graph`). Ungated: never blocks a transition.
	Supersedes []string `yaml:"supersedes,omitempty" json:"supersedes,omitempty"`
	// DeviatesFrom lists targets (decisions, specs, or prior changes) this change
	// knowingly diverges from — an honest record of implementation divergence
	// surfaced by `onto graph`. Ungated: never blocks a transition.
	DeviatesFrom []string `yaml:"deviates_from,omitempty" json:"deviates_from,omitempty"`
	Isolation    string   `yaml:"isolation,omitempty" json:"isolation,omitempty"`
	// Integration is how the change's git branch is integrated at close: "merge"
	// (the branch is merged into its base ref) or "pr" (a pull request is opened
	// and the branch stays for review). Empty leaves integration to the user. It
	// is a recorded choice the onto-close skill acts on (it performs the actual
	// git work); it is orthogonal to close.merged, which tracks spec-delta merging
	// and is always required at close. Ungated by the binary: never blocks a
	// transition on its own.
	Integration string `yaml:"integration,omitempty" json:"integration,omitempty"`
	BuildMode   string `yaml:"build_mode,omitempty" json:"build_mode,omitempty"`
	// BuildPause records a deliberate pause in the build phase — currently only
	// "plan-ready" (the plan is written and the user chose to stop before
	// executing, e.g. to switch models/sessions). The dispatcher resumes from it
	// instead of re-running the planning step. Empty = not paused. Ungated.
	BuildPause string `yaml:"build_pause,omitempty" json:"build_pause,omitempty"`
	TDDMode    string `yaml:"tdd_mode,omitempty" json:"tdd_mode,omitempty"`
	Verify     Verify `yaml:"verify,omitempty" json:"verify,omitempty"`
	Close      Close  `yaml:"close,omitempty" json:"close,omitempty"`
	Directive  string `yaml:"directive,omitempty" json:"directive,omitempty"`
	// Judgment-gate evidence tokens: free-form, non-empty = the gate was
	// answered (convention: "YYYY-MM-DD <summary>"). The binary checks
	// presence, never content — ceremony, not judgment (B1). Empty on states
	// written before these fields existed; no schema bump needed.
	ProposalApproved  string `yaml:"proposal_approved,omitempty" json:"proposal_approved,omitempty"`   // open's artifact-review gate (full only)
	ApproachConfirmed string `yaml:"approach_confirmed,omitempty" json:"approach_confirmed,omitempty"` // design's approach gate (full only)
	CloseConfirmed    string `yaml:"close_confirmed,omitempty" json:"close_confirmed,omitempty"`       // close's final-confirmation gate (all workflows)
	Guides            string `yaml:"guides,omitempty" json:"guides,omitempty"`                         // "" | pending | updated | waived:<reason>
	Archived          bool   `yaml:"archived,omitempty" json:"archived,omitempty"`
	// Abandoned marks the unsuccessful terminal state — a change cancelled without
	// completing, distinct from the successful archived/close terminal. Ungated:
	// set by `onto abandon`, it never blocks a transition (it makes the change
	// terminal, which `onto advance` enforces separately).
	Abandoned bool `yaml:"abandoned,omitempty" json:"abandoned,omitempty"`

	// observational (carried, never gated)
	Observed Observed `yaml:"observed,omitempty" json:"observed,omitempty"`
}

// Parse decodes raw YAML bytes into a State. It never panics; malformed
// input is returned as an error whose message identifies the onto-state
// source.
func Parse(b []byte) (state State, err error) {
	defer func() {
		if r := recover(); r != nil {
			state = State{}
			err = fmt.Errorf("onto-state: parse panic: %v", r)
		}
	}()

	if unmarshalErr := yaml.Unmarshal(b, &state); unmarshalErr != nil {
		return State{}, fmt.Errorf("onto-state: %w", unmarshalErr)
	}
	return state, nil
}

// Load reads the file at path and parses it as a State.
func Load(path string) (State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return State{}, fmt.Errorf("onto-state: failed to read %s: %w", path, err)
	}
	state, err := parseAndMigrate(b, path)
	if err != nil {
		return State{}, fmt.Errorf("%s: %w", path, err)
	}
	return state, nil
}

// Validate checks presence and shape of the gated core: Change and Phase must
// be present/known, and every optional enum, when non-empty, must be a member
// of its set. It never inspects Observed (B1: shape, not judgment).
func (s State) Validate() error {
	if s.Change == "" {
		return fmt.Errorf("onto-state: change is required")
	}
	if !validPhases[s.Phase] {
		return fmt.Errorf("onto-state: phase %q is not one of open|design|build|verify|close", s.Phase)
	}
	if s.Workflow != "" && !validWorkflows[s.Workflow] {
		return fmt.Errorf("onto-state: workflow %q is not one of full|fix|tweak", s.Workflow)
	}
	if s.Isolation != "" && !validIsolations[s.Isolation] {
		return fmt.Errorf("onto-state: isolation %q is not one of branch|worktree", s.Isolation)
	}
	if s.Integration != "" && !validIntegrations[s.Integration] {
		return fmt.Errorf("onto-state: integration %q is not one of merge|pr", s.Integration)
	}
	if s.BuildMode != "" && !validBuildModes[s.BuildMode] {
		return fmt.Errorf("onto-state: build_mode %q is not one of direct|subagent", s.BuildMode)
	}
	if s.TDDMode != "" && !validTDDModes[s.TDDMode] {
		return fmt.Errorf("onto-state: tdd_mode %q is not one of tdd|direct", s.TDDMode)
	}
	if s.Verify.Scale != "" && !validVerifyScales[s.Verify.Scale] {
		return fmt.Errorf("onto-state: verify.scale %q is not one of light|full", s.Verify.Scale)
	}
	if s.Verify.Result != "" && !validVerifyResults[s.Verify.Result] {
		return fmt.Errorf("onto-state: verify.result %q is not one of pending|pass|fail", s.Verify.Result)
	}
	if !ValidGuides(s.Guides) {
		return fmt.Errorf("onto-state: guides %q is not one of pending|updated|waived:<reason>", s.Guides)
	}
	return nil
}

// DerivePhase validates the State and returns its Phase.
func (s State) DerivePhase() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	return s.Phase, nil
}

// Marshal encodes a State to YAML bytes.
func Marshal(s State) ([]byte, error) {
	return yaml.Marshal(s)
}

// Save writes s to path as YAML, creating parent directories as needed. It is
// written through fsutil.WriteControlPlane: a symlink planted at the
// destination is refused (never followed), a unique temp file in the target
// directory is fsynced and renamed into place, and an existing file's mode is
// preserved. This matches the durability and no-follow guarantees homonto's
// other control-plane files (state.json, remote.lock.json) already enjoy, so a
// malicious or untrusted workspace cannot redirect an onto state write outside
// the project, and two concurrent writers cannot collide on a shared temp name
// (F8).
func Save(path string, s State) error {
	s.SchemaVersion = CurrentSchemaVersion
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("onto-state: failed to create directory for %s: %w", path, err)
	}
	b, err := Marshal(s)
	if err != nil {
		return fmt.Errorf("onto-state: failed to marshal %s: %w", path, err)
	}
	if err := fsutil.WriteControlPlane(path, b, 0o644); err != nil {
		return fmt.Errorf("onto-state: failed to write %s: %w", path, err)
	}
	return nil
}

// orderedPhases lists the onto workflow phases in traversal order. "close"
// is the terminal phase.
var orderedPhases = []string{"open", "design", "build", "verify", "close"}

// RequiredArtifacts returns the filenames that must exist to LEAVE phase for a
// change of the given workflow. Requirements are cumulative and workflow-aware:
//
//   - full (and "" / unknown → strictest): the task list is derived from the
//     confirmed design, so tasks.md gates the DESIGN exit — not the open exit —
//     and design.md + plan.md are required as before.
//   - fix / tweak presets: they skip design and plan entirely, so tasks.md is
//     the open-lite checklist (required from the open exit on) and neither
//     design.md nor plan.md is ever demanded. This is also what lets a preset
//     advance mechanically past design/build (the former N2 gap).
//
// The returned slice is a fresh copy on every call.
func RequiredArtifacts(phase, workflow string) []string {
	base := func(extra ...string) []string {
		return append([]string{"onto-state.yaml", "proposal.md"}, extra...)
	}
	if workflow == "fix" || workflow == "tweak" {
		switch phase {
		case "verify", "close":
			return base("tasks.md", "verification.md")
		default:
			return base("tasks.md")
		}
	}
	switch phase {
	case "design":
		return base("tasks.md", "design.md")
	case "build":
		return base("tasks.md", "design.md", "plan.md")
	case "verify", "close":
		return base("tasks.md", "design.md", "plan.md", "verification.md")
	default: // open
		return base()
	}
}

// NextPhase returns the phase that follows phase in the ordered onto
// workflow sequence ["open","design","build","verify","close"], and true.
// It returns ("", false) when phase is "close" (the terminal phase) or is
// not a recognized phase.
func NextPhase(phase string) (string, bool) {
	for i, p := range orderedPhases {
		if p == phase {
			if i+1 < len(orderedPhases) {
				return orderedPhases[i+1], true
			}
			return "", false
		}
	}
	return "", false
}

// taskItemNum matches a tasks.md checklist item's number: "- [ ] 2.3 …".
var taskItemNum = regexp.MustCompile(`^- \[[ xX]\]\s+(\d+\.\d+)`)

// planTaskNum matches a plan.md detail heading's number: "## Task 2.3 — …".
var planTaskNum = regexp.MustCompile(`^## Task\s+(\d+\.\d+)`)

// TaskPlanDrift is the divergence between a change's tasks.md checklist and
// its plan.md detail blocks (ADR 0018: tasks.md is the single checkoff,
// plan.md carries the detail under a matching "## Task N.M" heading).
type TaskPlanDrift struct {
	// MissingFromPlan holds tasks.md item numbers with no plan.md heading.
	MissingFromPlan []string
	// MissingFromTasks holds plan.md heading numbers with no tasks.md item.
	MissingFromTasks []string
	// PlanCheckboxes counts checkbox lines in plan.md. Any is a defect:
	// completion state lives in tasks.md alone, and a second checkbox is how
	// the two lists silently disagree about what is done.
	PlanCheckboxes int
}

// Empty reports whether the two files correspond exactly.
func (d TaskPlanDrift) Empty() bool {
	return len(d.MissingFromPlan) == 0 && len(d.MissingFromTasks) == 0 && d.PlanCheckboxes == 0
}

// CheckTaskPlan compares a change's tasks.md against its plan.md and reports
// where they diverge. It exists because the pairing is what makes a change
// resumable by someone who was not there: a fresh session resumes at the first
// unchecked tasks.md item and reads its detail under the matching plan.md
// heading. A number in one file and not the other breaks that handoff, and
// until now nothing but a prose checklist at close looked for it.
//
// A missing plan.md is not drift — presets legitimately run without one — so
// the check reports an empty result. A missing or unreadable tasks.md is an
// error.
func CheckTaskPlan(tasksPath, planPath string) (TaskPlanDrift, error) {
	var drift TaskPlanDrift

	if _, err := os.Stat(planPath); err != nil {
		if os.IsNotExist(err) {
			return drift, nil
		}
		return drift, fmt.Errorf("onto-state: failed to stat %s: %w", planPath, err)
	}

	taskNums, err := scanNumbers(tasksPath, taskItemNum, nil)
	if err != nil {
		return drift, err
	}
	var planCheckboxes int
	planNums, err := scanNumbers(planPath, planTaskNum, &planCheckboxes)
	if err != nil {
		return drift, err
	}
	drift.PlanCheckboxes = planCheckboxes

	for _, n := range sortedKeys(taskNums) {
		if !planNums[n] {
			drift.MissingFromPlan = append(drift.MissingFromPlan, n)
		}
	}
	for _, n := range sortedKeys(planNums) {
		if !taskNums[n] {
			drift.MissingFromTasks = append(drift.MissingFromTasks, n)
		}
	}
	return drift, nil
}

// scanNumbers collects the capture group of re over each line of path. When
// checkboxes is non-nil it also counts checkbox lines, which is how the plan is
// caught carrying completion state it must not have.
func scanNumbers(path string, re *regexp.Regexp, checkboxes *int) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("onto-state: failed to read %s: %w", path, err)
	}
	defer f.Close()

	found := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := re.FindStringSubmatch(line); m != nil {
			found[m[1]] = true
		}
		if checkboxes != nil && (strings.HasPrefix(line, "- [ ]") ||
			strings.HasPrefix(line, "- [x]") || strings.HasPrefix(line, "- [X]")) {
			*checkboxes++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("onto-state: failed to read %s: %w", path, err)
	}
	return found, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TasksAllChecked reads the file at tasksPath and reports whether every
// checkbox line ("- [ ]" or "- [x]"/"- [X]", after trimming leading
// whitespace) is checked. It returns false if the file contains no
// checkbox lines. It returns an error if the file cannot be read.
func TasksAllChecked(tasksPath string) (bool, error) {
	f, err := os.Open(tasksPath)
	if err != nil {
		return false, fmt.Errorf("onto-state: failed to read %s: %w", tasksPath, err)
	}
	defer f.Close()

	sawCheckbox := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch {
		case strings.HasPrefix(line, "- [ ]"):
			return false, nil
		case strings.HasPrefix(line, "- [x]"), strings.HasPrefix(line, "- [X]"):
			sawCheckbox = true
		}
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("onto-state: failed to read %s: %w", tasksPath, err)
	}
	return sawCheckbox, nil
}

// archiveDatePrefix matches the dated archive-directory prefix
// ("YYYY-MM-DD-") that onto close puts in front of the change name.
var archiveDatePrefix = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}-`)

// DepsResolved reports which of deps are not yet archived under root. A dep is
// resolved iff docs/changes/archive/ contains a directory named exactly
// <YYYY-MM-DD>-<dep>. The comparison is an exact string match on the name after
// the date prefix — NOT a glob. The previous `*-<dep>` glob had two holes: any
// archive whose name merely ENDED with "-<dep>" resolved it (dep "auth"
// satisfied by "2026-07-10-refactor-auth"), and glob metacharacters in a dep
// name matched anything. The returned slice contains the unresolved subset of
// deps, in input order. A nil or empty deps returns an empty (len 0) slice.
func DepsResolved(root string, deps []string) []string {
	// Note: both os.ReadDir calls below intentionally collapse "missing" and
	// "unreadable" into "no entries". For a workflow gate that is the safe
	// direction: an unreadable archive/changes dir leaves every dep
	// unresolved, so the gate refuses to advance rather than greenlighting an
	// unverified dependency. Do not "fix" this by surfacing the error — it
	// would change the gate-closed contract.
	archived := map[string]bool{}
	if entries, err := os.ReadDir(filepath.Join(root, "docs", "changes", "archive")); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if p := archiveDatePrefix.FindString(e.Name()); p != "" {
				archived[e.Name()[len(p):]] = true
			}
		}
	}
	// An active workspace with the dep's name overrides any archive hit: a
	// reused name in flight is not archived. This matches the dispatcher's
	// documented dependency contract (see onto/SKILL.md §2). Without it, a
	// new change reusing an old name would falsely resolve a dep that is
	// actually still being worked on (F10).
	active := map[string]bool{}
	if entries, err := os.ReadDir(filepath.Join(root, "docs", "changes")); err == nil {
		for _, e := range entries {
			if !e.IsDir() || e.Name() == "archive" {
				continue
			}
			active[e.Name()] = true
		}
	}
	unresolved := make([]string, 0, len(deps))
	for _, dep := range deps {
		if archived[dep] && !active[dep] {
			continue
		}
		unresolved = append(unresolved, dep)
	}
	return unresolved
}

// ValidateSkeleton loads onto-state.yaml from changeDir, derives its phase,
// and checks that every artifact RequiredArtifacts(phase) names is present
// in changeDir. It returns an error naming the first missing artifact, or
// nil if all are present.
func ValidateSkeleton(changeDir string) error {
	state, err := Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		return err
	}
	phase, err := state.DerivePhase()
	if err != nil {
		return err
	}
	for _, name := range RequiredArtifacts(phase, state.Workflow) {
		if _, err := os.Stat(filepath.Join(changeDir, name)); err != nil {
			return fmt.Errorf("onto-state: missing required artifact %s in %s: %w", name, changeDir, err)
		}
	}
	return nil
}
