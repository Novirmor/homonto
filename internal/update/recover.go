package update

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// RecoverPending finishes or undoes an interrupted activation.
//
// The direction is decided by the MARKER, not by how many steps happen to
// be applied. Past the marker the new binary is the installed one and the
// activation is finished forward; before it, nothing has committed and
// everything applied is undone from its exact backups. That is the whole
// reason the marker is written last: it is the single bit that says which
// installation this machine is running.
//
// Both the old binary and the candidate can run this, which is what makes
// it a recovery rather than a hope — whichever one survives the crash
// finds the journal and knows what to do with it.
func (s *Service) RecoverPending(ctx context.Context) error {
	journal, err := ReadJournal(s.paths)
	if errors.Is(err, ErrNoJournal) {
		return nil
	}
	if err != nil {
		return err
	}
	if journal.MarkerApplied() {
		return s.rollForward(journal)
	}
	return s.Rollback(ctx, journal)
}

// Pending reports whether an interrupted update is waiting to be
// recovered. Ordinary commands consult it and refuse rather than run
// against a half-replaced installation.
func Pending(controlRoot string) (bool, error) {
	_, err := ReadJournal(NewPaths(controlRoot))
	if errors.Is(err, ErrNoJournal) {
		return false, nil
	}
	if err != nil {
		// A journal that exists and cannot be read is the worst case, and
		// it is still "pending": refusing is right, because the one thing
		// that must not happen is running as if no update were underway.
		return true, err
	}
	return true, nil
}

// rollForward finishes an activation that had already committed.
func (s *Service) rollForward(journal Journal) error {
	// The marker is applied, so the candidate is the installed binary.
	// What can remain is bookkeeping: the journal itself and the staging
	// directory.
	if err := s.finish(journal); err != nil {
		return fmt.Errorf("update: finish the interrupted activation: %w", err)
	}
	return nil
}

// Rollback restores the previous installation from its exact backups.
//
// Steps are reverted in REVERSE order, so the marker goes first and the
// binary last: a crash mid-rollback must never leave a machine with the
// new marker and the old binary, which would tell the next invocation that
// an activation it never finished had succeeded.
//
// A backup that cannot be restored is reported and the journal is LEFT IN
// PLACE. There is no good answer to a failed restore, and the worst one
// would be to clear the record and let ordinary commands resume against an
// installation nobody can describe.
func (s *Service) Rollback(ctx context.Context, journal Journal) error {
	var failures []string
	for i := len(journal.Steps) - 1; i >= 0; i-- {
		step := &journal.Steps[i]
		if step.State != StateApplied {
			continue
		}
		if err := restore(*step); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		step.State = StateReverted
		if err := WriteJournal(s.paths, journal); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("update: %v: %w", failures, ErrRestoreFailed)
	}
	if err := RemoveJournal(s.paths); err != nil {
		return err
	}
	return os.RemoveAll(s.paths.StagedDir(journal.ID))
}

// restore puts one step's target back exactly as it was.
func restore(step Step) error {
	if step.Target == "" {
		return nil
	}
	if !step.Existed {
		// The target did not exist before this update, so restoring it
		// means removing it. Leaving a file the previous installation
		// never had would be a rollback that did not roll all the way
		// back.
		if err := os.Remove(step.Target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove %s: %v", step.Target, err)
		}
		return nil
	}
	body, err := os.ReadFile(step.Backup)
	if err != nil {
		return fmt.Errorf("read the backup of %s: %v", step.Target, err)
	}
	info, err := os.Stat(step.Backup)
	if err != nil {
		return fmt.Errorf("stat the backup of %s: %v", step.Target, err)
	}
	if err := writeFileAtomic(step.Target, body, info.Mode().Perm()); err != nil {
		return fmt.Errorf("restore %s: %v", step.Target, err)
	}
	return nil
}
