package update

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// JournalSchema is the update journal's version.
//
// It is deliberately its own tiny schema, separate from everything else,
// because BOTH the old binary and the candidate must understand it: after
// a crash mid-activation, whichever one is on disk has to be able to read
// the journal and finish or undo the job. A journal format only one of
// them could read would be a journal that cannot be recovered by the
// binary that finds it.
const JournalSchema = 1

// Typed journal errors.
var (
	// ErrNoJournal: no update is in progress.
	ErrNoJournal = errors.New("update: no update journal")
	// ErrJournalUnreadable: a journal exists but cannot be understood.
	// Ordinary commands refuse rather than guess.
	ErrJournalUnreadable = errors.New("update: the update journal cannot be read")
	// ErrUpdateInProgress: an update was interrupted and must be
	// recovered before anything else runs.
	ErrUpdateInProgress = errors.New("update: an interrupted update must be recovered first")
)

// StepKind names one component an activation replaces.
type StepKind string

const (
	// StepBinary replaces the homonto binary itself.
	StepBinary StepKind = "binary"
	// StepState migrates the runtime database and the checkpoint.
	StepState StepKind = "state"
	// StepWrappers replaces the generated host integration files.
	StepWrappers StepKind = "wrappers"
	// StepMarker writes the activated-generation marker. It is always
	// last: the marker is what makes an activation real, so a crash before
	// it leaves an update that can be rolled back rather than a half-new
	// installation that claims to be finished.
	StepMarker StepKind = "marker"
)

// order is the sequence steps are applied in.
var order = []StepKind{StepBinary, StepState, StepWrappers, StepMarker}

// StepState is where one step stands.
type State string

const (
	// StatePending: not yet applied.
	StatePending State = "pending"
	// StateApplied: applied, and its backup is exact.
	StateApplied State = "applied"
	// StateReverted: undone from its backup.
	StateReverted State = "reverted"
)

// Step is one journaled component replacement.
type Step struct {
	Kind StepKind `json:"kind"`
	// Target is the absolute path being replaced.
	Target string `json:"target,omitempty"`
	// Backup is where the exact prior bytes live. A step with no backup
	// had nothing to preserve — the file did not exist.
	Backup string `json:"backup,omitempty"`
	// Existed records whether the target was there before. Restoring
	// something that never existed means REMOVING it, and "no backup" is
	// otherwise indistinguishable from "backup failed".
	Existed bool  `json:"existed"`
	State   State `json:"state"`
}

// Generation identifies one installed version of Homonto.
type Generation struct {
	Version            string `json:"version"`
	ProtocolVersion    int    `json:"protocol_version"`
	StoreSchemaVersion int64  `json:"store_schema_version"`
	// BinaryDigest is the sha256 of the binary, so a marker can be checked
	// against what is actually installed rather than trusted.
	BinaryDigest string `json:"binary_digest"`
}

// Journal records one activation in progress.
type Journal struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	// From and To name the generations being moved between. Both are
	// recorded so a recovery can tell which direction it is finishing.
	From      Generation `json:"from"`
	To        Generation `json:"to"`
	Steps     []Step     `json:"steps"`
	StartedAt time.Time  `json:"started_at"`
}

// Validate checks a journal this binary is about to act on.
func (j Journal) Validate() error {
	if j.SchemaVersion != JournalSchema {
		return fmt.Errorf("update: journal schema %d, want exactly %d: %w",
			j.SchemaVersion, JournalSchema, ErrJournalUnreadable)
	}
	if strings.TrimSpace(j.ID) == "" {
		return fmt.Errorf("update: journal has no id: %w", ErrJournalUnreadable)
	}
	if len(j.Steps) == 0 {
		return fmt.Errorf("update: journal records no step: %w", ErrJournalUnreadable)
	}
	for i, s := range j.Steps {
		switch s.Kind {
		case StepBinary, StepState, StepWrappers, StepMarker:
		default:
			return fmt.Errorf("update: steps[%d] kind %q is not known: %w", i, s.Kind, ErrJournalUnreadable)
		}
		switch s.State {
		case StatePending, StateApplied, StateReverted:
		default:
			return fmt.Errorf("update: steps[%d] state %q is not known: %w", i, s.State, ErrJournalUnreadable)
		}
	}
	return nil
}

// Complete reports whether every step has been applied — which, because
// the marker is last, is the same as the activation having finished.
func (j Journal) Complete() bool {
	for _, s := range j.Steps {
		if s.State != StateApplied {
			return false
		}
	}
	return true
}

// MarkerApplied reports whether the activated-generation marker was
// written. It is what a recovery uses to choose its direction: past the
// marker, roll forward; before it, roll back.
func (j Journal) MarkerApplied() bool {
	for _, s := range j.Steps {
		if s.Kind == StepMarker {
			return s.State == StateApplied
		}
	}
	return false
}

// step returns a mutable pointer to one step.
func (j *Journal) step(kind StepKind) *Step {
	for i := range j.Steps {
		if j.Steps[i].Kind == kind {
			return &j.Steps[i]
		}
	}
	return nil
}

// Paths locates the update area inside a control directory.
type Paths struct {
	// Dir is <control>/.homonto/update.
	Dir string
}

// NewPaths derives the update area from a control root.
func NewPaths(controlRoot string) Paths {
	return Paths{Dir: filepath.Join(controlRoot, ".homonto", "update")}
}

// JournalPath is where the journal lives.
func (p Paths) JournalPath() string { return filepath.Join(p.Dir, "journal.json") }

// MarkerPath is where the activated-generation marker lives.
func (p Paths) MarkerPath() string { return filepath.Join(p.Dir, "generation.json") }

// StagedDir is where a candidate is staged.
func (p Paths) StagedDir(id string) string { return filepath.Join(p.Dir, "staged", id) }

// BackupDir is where exact pre-update copies live.
func (p Paths) BackupDir(id string) string { return filepath.Join(p.Dir, "backup", id) }

// ReadJournal loads the journal, if there is one.
func ReadJournal(p Paths) (Journal, error) {
	body, err := os.ReadFile(p.JournalPath())
	if errors.Is(err, fs.ErrNotExist) {
		return Journal{}, ErrNoJournal
	}
	if err != nil {
		return Journal{}, fmt.Errorf("update: read the journal: %w: %w", ErrJournalUnreadable, err)
	}
	var j Journal
	if err := json.Unmarshal(body, &j); err != nil {
		return Journal{}, fmt.Errorf("update: decode the journal: %w: %w", ErrJournalUnreadable, err)
	}
	if err := j.Validate(); err != nil {
		return Journal{}, err
	}
	return j, nil
}

// WriteJournal persists the journal atomically. Every state change goes
// through here and lands whole: a torn journal is a journal that cannot
// direct a recovery, which is the one job it has.
func WriteJournal(p Paths, j Journal) error {
	if err := j.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("update: create the update area: %w", err)
	}
	body, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("update: encode the journal: %w", err)
	}
	return writeFileAtomic(p.JournalPath(), append(body, '\n'), 0o600)
}

// RemoveJournal clears a finished update.
func RemoveJournal(p Paths) error {
	if err := os.Remove(p.JournalPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("update: clear the journal: %w", err)
	}
	return nil
}

// ReadMarker loads the activated-generation marker.
func ReadMarker(p Paths) (Generation, error) {
	body, err := os.ReadFile(p.MarkerPath())
	if errors.Is(err, fs.ErrNotExist) {
		return Generation{}, ErrNoJournal
	}
	if err != nil {
		return Generation{}, fmt.Errorf("update: read the generation marker: %w", err)
	}
	var g Generation
	if err := json.Unmarshal(body, &g); err != nil {
		return Generation{}, fmt.Errorf("update: decode the generation marker: %w", err)
	}
	return g, nil
}

// WriteMarker records the activated generation. It is always the last
// thing an activation does.
func WriteMarker(p Paths, g Generation) error {
	if err := os.MkdirAll(p.Dir, 0o700); err != nil {
		return fmt.Errorf("update: create the update area: %w", err)
	}
	body, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return fmt.Errorf("update: encode the generation marker: %w", err)
	}
	return writeFileAtomic(p.MarkerPath(), append(body, '\n'), 0o600)
}

// writeFileAtomic writes a file through a temporary name and a rename, so
// a reader sees the old content or the new one and never a partial write.
func writeFileAtomic(path string, body []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".homonto-update-*")
	if err != nil {
		return fmt.Errorf("update: stage %s: %w", path, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return fmt.Errorf("update: write %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("update: sync %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("update: close %s: %w", path, err)
	}
	if err := os.Chmod(name, mode); err != nil {
		return fmt.Errorf("update: chmod %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("update: install %s: %w", path, err)
	}
	return syncDir(dir)
}

// syncDir flushes a directory entry so a rename survives a crash.
func syncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("update: open %s: %w", dir, err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return fmt.Errorf("update: sync %s: %w", dir, err)
	}
	return nil
}
