package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
)

// Typed activation errors.
var (
	// ErrWorkActive: work is in progress, so activation is refused.
	ErrWorkActive = errors.New("update: refusing to activate while work is active")
	// ErrNotStaged: the candidate is not staged.
	ErrNotStaged = errors.New("update: no staged candidate")
	// ErrRestoreFailed: a rollback could not restore an exact backup. This
	// is the one failure with no good answer, so it is reported loudly and
	// the journal is left in place.
	ErrRestoreFailed = errors.New("update: could not restore the previous installation")
)

// ActiveWork reports whether a workspace has unfinished work. Activation
// refuses while it does: replacing the binary under a running workflow
// means the next `homonto next` is answered by a different program than
// the one that issued the outstanding actions.
type ActiveWork func(ctx context.Context) (bool, error)

// Service stages and activates candidate binaries.
type Service struct {
	paths Paths
	// binary is the absolute path of the installed homonto binary — what
	// activation replaces.
	binary string
	// wrappers are the generated host files an activation refreshes.
	wrappers   []string
	activeWork ActiveWork
	now        func() time.Time
}

// Options configure a Service.
type Options struct {
	// ControlRoot is the control repository root.
	ControlRoot string
	// Binary is the installed binary's path. Empty means the running
	// executable.
	Binary string
	// Wrappers are the generated host files to refresh on activation.
	Wrappers []string
	// ActiveWork reports whether work is in progress. A nil implementation
	// means "cannot tell", and activation refuses — a service that cannot
	// check must not assume the answer it prefers.
	ActiveWork ActiveWork
	Now        func() time.Time
}

// NewService binds an update service to a workspace.
func NewService(opts Options) (*Service, error) {
	if opts.ControlRoot == "" || !filepath.IsAbs(opts.ControlRoot) {
		return nil, fmt.Errorf("update: control root %q must be an absolute path", opts.ControlRoot)
	}
	binary := opts.Binary
	if binary == "" {
		exe, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("update: locate the running binary: %w", err)
		}
		binary = exe
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		return nil, fmt.Errorf("update: resolve %s: %w", binary, err)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		paths: NewPaths(opts.ControlRoot), binary: abs,
		wrappers:   append([]string(nil), opts.Wrappers...),
		activeWork: opts.ActiveWork, now: now,
	}, nil
}

// Paths exposes the update area.
func (s *Service) Paths() Paths { return s.paths }

// StagedGeneration is a candidate written to disk and interrogated, but
// not yet installed.
type StagedGeneration struct {
	// ID names this update. It is the journal's id and the staging and
	// backup directory name, so everything about one update is findable
	// from any part of it.
	ID string
	// Path is the staged candidate binary.
	Path string
	// Metadata is what the candidate said about itself.
	Metadata CandidateMetadata
	// Release is the verified manifest it came from.
	Release VerifiedRelease
	// Compatibility is the verdict on activating it.
	Compatibility Compatibility
}

// Stage writes a candidate to disk, makes it executable, and asks it what
// it is.
//
// It replaces nothing. A staged candidate that turns out to be
// incompatible is a directory to delete, not an installation to undo,
// which is why validation happens here rather than after the binary is in
// place.
func (s *Service) Stage(ctx context.Context, release VerifiedRelease, body []byte) (StagedGeneration, error) {
	if err := VerifyChecksum(release.Artifact, body); err != nil {
		return StagedGeneration{}, err
	}
	id, err := identity.NewUUID()
	if err != nil {
		return StagedGeneration{}, err
	}
	dir := s.paths.StagedDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return StagedGeneration{}, fmt.Errorf("update: create the staging area: %w", err)
	}
	path := filepath.Join(dir, "homonto")
	if err := writeFileAtomic(path, body, 0o755); err != nil {
		return StagedGeneration{}, err
	}
	metadata, err := InspectCandidate(ctx, path)
	if err != nil {
		return StagedGeneration{}, err
	}
	if metadata.Version != release.Manifest.Version {
		return StagedGeneration{}, fmt.Errorf(
			"update: the candidate says it is %s, the manifest says %s: %w",
			metadata.Version, release.Manifest.Version, ErrCandidateMismatch)
	}
	staged := StagedGeneration{
		ID: id, Path: path, Metadata: metadata, Release: release,
		Compatibility: CheckCompatibility(LocalMetadata(), metadata, release.Rotation),
	}
	return staged, nil
}

// DiscardStaged removes a staged candidate.
func (s *Service) DiscardStaged(staged StagedGeneration) error {
	if staged.ID == "" {
		return nil
	}
	if err := os.RemoveAll(s.paths.StagedDir(staged.ID)); err != nil {
		return fmt.Errorf("update: discard the staged candidate: %w", err)
	}
	return nil
}

// Activate installs a staged candidate under a journal.
//
// The order is fixed and the marker is last. Each component is replaced by
// one atomic per-file operation with its exact prior bytes preserved
// first, and the journal records each transition before and after it
// happens. A crash anywhere leaves a journal that says exactly how far it
// got — which is what makes the difference between recovering and
// guessing.
//
// No cross-filesystem atomic transaction is claimed. What is claimed is
// that every individual replacement is atomic, that every replaced file
// has an exact backup, and that the marker distinguishes "finished" from
// "in progress".
func (s *Service) Activate(ctx context.Context, staged StagedGeneration) (err error) {
	if staged.ID == "" || staged.Path == "" {
		return fmt.Errorf("update: %w", ErrNotStaged)
	}
	if !staged.Compatibility.OK() {
		return fmt.Errorf("update: %v: %w", staged.Compatibility.Reasons, ErrIncompatible)
	}
	if err := s.requireIdle(ctx); err != nil {
		return err
	}
	if _, err := ReadJournal(s.paths); err == nil {
		return fmt.Errorf("update: %w", ErrUpdateInProgress)
	} else if !errors.Is(err, ErrNoJournal) {
		return err
	}

	journal := Journal{
		SchemaVersion: JournalSchema,
		ID:            staged.ID,
		From:          s.currentGeneration(),
		To:            generationOf(staged),
		StartedAt:     s.now().UTC(),
	}
	for _, kind := range order {
		journal.Steps = append(journal.Steps, Step{Kind: kind, State: StatePending})
	}
	if err := WriteJournal(s.paths, journal); err != nil {
		return err
	}
	// Every failure from here on rolls the journal back rather than
	// leaving a half-installed binary: an activation that stopped partway
	// is not a state anything can run from.
	defer func() {
		if err == nil {
			return
		}
		if rollbackErr := s.Rollback(ctx, journal); rollbackErr != nil {
			err = fmt.Errorf("%w (rollback also failed: %v)", err, rollbackErr)
		}
	}()

	if err := s.applyBinary(&journal, staged); err != nil {
		return err
	}
	if err := s.applyState(&journal); err != nil {
		return err
	}
	if err := s.applyWrappers(&journal); err != nil {
		return err
	}
	if err := s.applyMarker(&journal); err != nil {
		return err
	}
	return s.finish(journal)
}

// requireIdle refuses activation while work is in progress.
func (s *Service) requireIdle(ctx context.Context) error {
	if s.activeWork == nil {
		return fmt.Errorf(
			"update: cannot tell whether work is active, so activation is refused: %w", ErrWorkActive)
	}
	active, err := s.activeWork(ctx)
	if err != nil {
		return err
	}
	if active {
		return fmt.Errorf(
			"update: finish or abandon the active work first: %w", ErrWorkActive)
	}
	return nil
}

// applyBinary replaces the installed binary.
func (s *Service) applyBinary(j *Journal, staged StagedGeneration) error {
	body, err := os.ReadFile(staged.Path)
	if err != nil {
		return fmt.Errorf("update: read the staged candidate: %w", err)
	}
	return s.applyFile(j, StepBinary, s.binary, body, 0o755)
}

// applyState migrates the runtime database and the checkpoint.
//
// There is nothing to migrate here yet: a candidate whose store schema is
// newer migrates on its first read-write open, and a candidate whose
// schema matches needs nothing. The step exists in the journal so a
// migration that DOES need work has a recorded place to happen, with its
// backups already taken — adding it later must not mean changing the
// journal format both binaries have to understand.
func (s *Service) applyState(j *Journal) error {
	step := j.step(StepState)
	step.State = StateApplied
	return WriteJournal(s.paths, *j)
}

// applyWrappers refreshes the generated host files.
func (s *Service) applyWrappers(j *Journal) error {
	step := j.step(StepWrappers)
	// Wrappers are regenerated by the candidate on its next `host
	// install`, not copied here; what this step preserves is the ability
	// to put the OLD ones back, which matters because a wrapper that
	// invokes a rolled-back binary with a newer protocol would fail in a
	// way nobody could read.
	for i, path := range s.wrappers {
		backup := filepath.Join(s.paths.BackupDir(j.ID), fmt.Sprintf("wrapper-%d", i))
		existed, err := backupFile(path, backup)
		if err != nil {
			return err
		}
		if existed {
			step.Target = path
			step.Backup = backup
			step.Existed = true
		}
	}
	step.State = StateApplied
	return WriteJournal(s.paths, *j)
}

// applyMarker writes the activated-generation marker, last.
func (s *Service) applyMarker(j *Journal) error {
	step := j.step(StepMarker)
	backup := filepath.Join(s.paths.BackupDir(j.ID), "generation.json")
	existed, err := backupFile(s.paths.MarkerPath(), backup)
	if err != nil {
		return err
	}
	step.Target = s.paths.MarkerPath()
	step.Backup = backup
	step.Existed = existed
	if err := WriteMarker(s.paths, j.To); err != nil {
		return err
	}
	step.State = StateApplied
	return WriteJournal(s.paths, *j)
}

// applyFile backs a file up and replaces it atomically, recording both
// transitions in the journal.
func (s *Service) applyFile(j *Journal, kind StepKind, target string, body []byte, mode fs.FileMode) error {
	step := j.step(kind)
	backup := filepath.Join(s.paths.BackupDir(j.ID), string(kind))
	existed, err := backupFile(target, backup)
	if err != nil {
		return err
	}
	step.Target = target
	step.Backup = backup
	step.Existed = existed
	// The backup is journaled BEFORE the replacement, so a crash between
	// them leaves a recovery that knows where the old bytes are.
	if err := WriteJournal(s.paths, *j); err != nil {
		return err
	}
	if err := writeFileAtomic(target, body, mode); err != nil {
		return err
	}
	step.State = StateApplied
	return WriteJournal(s.paths, *j)
}

// finish clears a completed activation.
func (s *Service) finish(j Journal) error {
	if err := RemoveJournal(s.paths); err != nil {
		return err
	}
	// The staged candidate is now the installed binary; the backups stay.
	// Keeping them is the point: "retains exact pre-update backups" is
	// what makes a later manual restore possible.
	return os.RemoveAll(s.paths.StagedDir(j.ID))
}

// currentGeneration describes the binary being replaced.
func (s *Service) currentGeneration() Generation {
	meta := LocalMetadata()
	return Generation{
		Version:            meta.Version,
		ProtocolVersion:    meta.ProtocolVersion,
		StoreSchemaVersion: meta.StoreSchemaVersion,
		BinaryDigest:       fileDigest(s.binary),
	}
}

// generationOf describes a staged candidate.
func generationOf(staged StagedGeneration) Generation {
	return Generation{
		Version:            staged.Metadata.Version,
		ProtocolVersion:    staged.Metadata.ProtocolVersion,
		StoreSchemaVersion: staged.Metadata.StoreSchemaVersion,
		BinaryDigest:       fileDigest(staged.Path),
	}
}

// backupFile copies a file's exact bytes and mode aside, reporting whether
// it was there at all.
func backupFile(target, backup string) (bool, error) {
	src, err := os.Open(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("update: read %s: %w", target, err)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return false, fmt.Errorf("update: stat %s: %w", target, err)
	}
	if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
		return false, fmt.Errorf("update: create the backup area: %w", err)
	}
	dst, err := os.OpenFile(backup, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return false, fmt.Errorf("update: write the backup of %s: %w", target, err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return false, fmt.Errorf("update: copy %s: %w", target, err)
	}
	if err := dst.Sync(); err != nil {
		dst.Close()
		return false, fmt.Errorf("update: sync the backup of %s: %w", target, err)
	}
	if err := dst.Close(); err != nil {
		return false, fmt.Errorf("update: close the backup of %s: %w", target, err)
	}
	return true, syncDir(filepath.Dir(backup))
}

// fileDigest returns a file's sha256, or "" when it cannot be read. It is
// informational — a marker that can be checked against reality — so a
// failure here is not worth failing an update over.
func fileDigest(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
