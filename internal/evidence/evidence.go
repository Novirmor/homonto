// Package evidence is onto's structured verification record (ADR 0027):
// stable IDs for requirements and scenarios, a parser for the markdown that
// carries them, and the versioned .onto/evidence.json sidecar that records
// which command, verified how, backed which scenario — hashes only, never
// argv or output. The sidecar is optional per change; a change without one is
// legacy and reports as such, never as a failure.
package evidence

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/noviopenworks/homonto/internal/fsutil"
)

// SchemaVersion is the sidecar format version this binary writes. Loaders
// reject unknown major versions fail-closed.
const SchemaVersion = 1

// Scenario is one GIVEN/WHEN/THEN block under a requirement, with its stable
// ID when the artifact carries one.
type Scenario struct {
	Name string
	ID   string
}

// Requirement is one requirement block (delta or living spec) with its
// scenarios and its stable ID when present.
type Requirement struct {
	Name      string
	ID        string
	Section   string // ADDED | MODIFIED | REMOVED | RENAMED | "" (living spec)
	Scenarios []Scenario
}

var (
	reqHeading = regexp.MustCompile(`^### Requirement:\s*(.+?)\s*$`)
	scHeading  = regexp.MustCompile(`^#### Scenario:\s*(.+?)\s*$`)
	idLine     = regexp.MustCompile(`^(?:Requirement|Scenario)-ID:\s*(\S+)\s*$`)
	// Legacy tasks began with #N. Dotted workflow task IDs carry their stable
	// trace ID explicitly, avoiding accidental matches such as issue #123.
	legacyTaskLine = regexp.MustCompile(`^\s*-\s+\[([ xX])\]\s+#([1-9]\d*)\b`)
	traceTaskLine  = regexp.MustCompile(`^\s*-\s+\[([ xX])\]\s+\d+\.\d+\b.*\[trace\s+#([1-9]\d*)\]`)
)

// ParseRequirements extracts requirement and scenario blocks with their IDs
// from a delta or living spec. IDs come from a `Requirement-ID:` /
// `Scenario-ID:` line inside the block; a block without one carries ID "".
func ParseRequirements(doc string) []Requirement {
	var out []Requirement
	var cur *Requirement
	var curSc *Scenario
	section := ""
	for _, line := range strings.Split(doc, "\n") {
		if strings.HasPrefix(line, "## ") {
			rest := strings.TrimPrefix(line, "## ")
			if strings.HasSuffix(rest, " Requirements") {
				section = strings.TrimSuffix(rest, " Requirements")
			} else {
				section = ""
			}
		}
		if m := scHeading.FindStringSubmatch(line); m != nil {
			if cur != nil {
				cur.Scenarios = append(cur.Scenarios, Scenario{Name: m[1]})
				curSc = &cur.Scenarios[len(cur.Scenarios)-1]
			}
			continue
		}
		if m := reqHeading.FindStringSubmatch(line); m != nil {
			out = append(out, Requirement{Name: m[1], Section: section})
			cur = &out[len(out)-1]
			curSc = nil
			continue
		}
		if m := idLine.FindStringSubmatch(line); m != nil {
			if curSc != nil {
				curSc.ID = m[1]
			} else if cur != nil {
				cur.ID = m[1]
			}
		}
	}
	return out
}

// Task is one numbered tasks.md entry with its checked state.
type Task struct {
	Number  int
	Checked bool
}

// ParseTasks extracts legacy #N task checkboxes and dotted tasks with an
// explicit [trace #N] marker. A bare number elsewhere in the description is
// not a trace ID.
func ParseTasks(doc string) []Task {
	var out []Task
	seen := map[int]bool{}
	for _, line := range strings.Split(doc, "\n") {
		m := traceTaskLine.FindStringSubmatch(line)
		if m == nil {
			m = legacyTaskLine.FindStringSubmatch(line)
		}
		if m == nil {
			continue
		}
		n := 0
		for _, c := range m[2] {
			n = n*10 + int(c-'0')
		}
		if n == 0 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, Task{Number: n, Checked: m[1] != " "})
	}
	return out
}

// Record is one verification claim: the task and scenario it backs, the
// executable that was run (named, never its argv), a hash of the command, the
// commit it ran at, exit status, and hashes of the output and of the
// verification.md artifact at record time. Staleness is detectable (hashes,
// commit) without storing reproducible secrets or command text.
type Record struct {
	Task         int    `json:"task"`
	Scenario     string `json:"scenario"`
	Executable   string `json:"executable"`
	CommandHash  string `json:"commandHash"`
	Repo         string `json:"repo,omitempty"`
	Commit       string `json:"commit"`
	OperationID  string `json:"operationId"`
	ExitStatus   int    `json:"exitStatus"`
	OutputHash   string `json:"outputHash"`
	ArtifactHash string `json:"artifactHash"`
	At           string `json:"at"`
}

// Sidecar is the per-change evidence file at
// docs/changes/<name>/.onto/evidence.json.
type Sidecar struct {
	SchemaVersion int      `json:"schemaVersion"`
	Change        string   `json:"change"`
	Records       []Record `json:"records"`
}

// New returns an empty sidecar for a change.
func New(change string) *Sidecar {
	return &Sidecar{SchemaVersion: SchemaVersion, Change: change, Records: []Record{}}
}

// Path is the sidecar's location inside a change workspace directory.
func Path(changeDir string) string {
	return filepath.Join(changeDir, ".onto", "evidence.json")
}

// Load reads a sidecar. A missing file returns nil, false, nil — legacy, not
// an error. A malformed file, an unknown schema major, or a change mismatch
// is a hard error: guessing at evidence would defeat the point.
func Load(change, path string) (*Sidecar, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var sc Sidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, false, fmt.Errorf("evidence: %s is not a valid sidecar: %w", path, err)
	}
	if sc.SchemaVersion <= 0 {
		return nil, false, fmt.Errorf("evidence: %s has no schemaVersion", path)
	}
	if sc.SchemaVersion > SchemaVersion {
		return nil, false, fmt.Errorf("evidence: %s schemaVersion %d is newer than this binary supports (%d)", path, sc.SchemaVersion, SchemaVersion)
	}
	if sc.Change != change {
		return nil, false, fmt.Errorf("evidence: %s records change %q, expected %q", path, sc.Change, change)
	}
	return &sc, true, nil
}

// Save persists the sidecar with confinement: on first write an existing file
// without the expected schema and change is refused (a foreign file is never
// adopted); on every write symlinked destinations and symlinked parents up to
// the change directory are refused, and the write itself is atomic no-follow.
func Save(changeDir string, sc *Sidecar) error {
	if sc.SchemaVersion != SchemaVersion {
		return fmt.Errorf("evidence: refusing to write schemaVersion %d", sc.SchemaVersion)
	}
	path := Path(changeDir)
	if err := requireRealParents(changeDir, filepath.Dir(path)); err != nil {
		return err
	}
	if data, err := os.ReadFile(path); err == nil {
		var probe Sidecar
		if json.Unmarshal(data, &probe) != nil || probe.SchemaVersion != sc.SchemaVersion || probe.Change != sc.Change {
			return fmt.Errorf("evidence: %s exists and is not this change's sidecar; refusing to overwrite", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("evidence: %s is a symlink; refusing", path)
	}
	data, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return fsutil.WriteControlPlane(path, data, 0o644)
}

// requireRealParents verifies every component from root through dir is a
// real directory — no symlinks anywhere on the path, so a planted link cannot
// redirect the sidecar outside the workspace.
func requireRealParents(root, dir string) error {
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("evidence: %s is outside the change workspace %s", dir, root)
	}
	cur := root
	for _, c := range strings.Split(filepath.ToSlash(rel), "/") {
		if c == "." || c == "" {
			continue
		}
		cur = filepath.Join(cur, c)
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil // absent parents are created real by MkdirAll
			}
			return err
		}
		if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("evidence: %s is not a real directory (symlinked parents are refused)", cur)
		}
	}
	return nil
}

// ValidateSchema is the envelope check shared with loaders that receive a
// bare version.
func ValidateSchema(v int) error {
	if v <= 0 {
		return fmt.Errorf("evidence: no schemaVersion")
	}
	if v > SchemaVersion {
		return fmt.Errorf("evidence: schemaVersion %d is newer than this binary supports (%d)", v, SchemaVersion)
	}
	return nil
}
