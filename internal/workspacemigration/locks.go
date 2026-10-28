package workspacemigration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func withInitialMigrationLocks(configPath string, fn func(workspace.Layout) error) error {
	configPath, err := absolutePath(configPath)
	if err != nil {
		return err
	}
	// Resolve once only to identify the records lock. The same layout is loaded
	// again after the config lock and then inventory is rebuilt under every lock.
	l, err := workspace.LoadMigration(configPath)
	if err != nil {
		return err
	}
	configLock, err := applylock.Acquire(filepath.Join(l.ConfigRoot, ".homonto"))
	if err != nil {
		return fmt.Errorf("workspace migration: %w", err)
	}
	defer configLock.Release()
	l, err = workspace.LoadMigration(configPath)
	if err != nil {
		return err
	}
	recoveryLockPath, err := migrationRecoveryLockPath(l.WorkflowRoot)
	if err != nil {
		return err
	}
	recordsLock, err := applylock.AcquirePath(recoveryLockPath, filepath.Dir(recoveryLockPath))
	if err != nil {
		return fmt.Errorf("workspace migration: records lock: %w", err)
	}
	defer recordsLock.Release()
	releaseBindings, err := workspace.LockMigrationBindings(l)
	if err != nil {
		return fmt.Errorf("workspace migration: lifecycle locks: %w", err)
	}
	defer releaseBindings()
	return fn(l)
}

type recoveryMaterial struct {
	journal   *privateJournal
	intent    *preparationIntent
	identity  *recoveryIdentity
	bootstrap *bootstrapPreparationStatusTemporary
	orphan    bool
	retired   bool
}

func loadRecoveryMaterial(root, runID string) (recoveryMaterial, error) {
	identity, identityErr := loadRecoveryIdentity(root, runID)
	if identityErr != nil && !errors.Is(identityErr, os.ErrNotExist) {
		return recoveryMaterial{}, identityErr
	}
	retired, err := recoveryPreparationRetired(root, runID)
	if err != nil {
		return recoveryMaterial{}, err
	}
	if retired {
		return recoveryMaterial{retired: true}, nil
	}
	journal, journalErr := loadJournal(root, runID)
	if journalErr == nil {
		return recoveryMaterial{journal: &journal, identity: recoveryIdentityPointer(identity, identityErr)}, nil
	}
	intent, intentErr := loadPreparationIntent(root, runID)
	if intentErr == nil {
		return recoveryMaterial{intent: &intent, identity: recoveryIdentityPointer(identity, identityErr)}, nil
	}
	if errors.Is(intentErr, os.ErrNotExist) {
		statusIntent, statusErr := preparationIntentFromStatus(root, runID)
		if statusErr == nil {
			return recoveryMaterial{intent: &statusIntent, identity: recoveryIdentityPointer(identity, identityErr)}, nil
		}
		if !errors.Is(statusErr, os.ErrNotExist) {
			return recoveryMaterial{}, statusErr
		}
	}
	if errors.Is(journalErr, os.ErrNotExist) && errors.Is(intentErr, os.ErrNotExist) {
		temporary, exists, err := loadBootstrapPreparationStatusTemporaryArtifact(root, runID)
		if err != nil {
			return recoveryMaterial{}, err
		}
		if exists {
			return recoveryMaterial{bootstrap: &temporary}, nil
		}
		orphan, orphanErr := migrationrecord.IsPreparationOrphan(root, runID)
		if orphanErr != nil {
			return recoveryMaterial{}, fmt.Errorf("workspace migration: inspect preparation artifact: %w", orphanErr)
		}
		if orphan {
			return recoveryMaterial{orphan: true}, nil
		}
		if status, statusErr := migrationrecord.LoadJournalStatus(root, runID); statusErr == nil && status.Phase == "preparing" {
			return recoveryMaterial{}, fmt.Errorf("workspace migration: preparation artifacts are not recognized; refusing automatic cleanup")
		}
	}
	if !errors.Is(journalErr, os.ErrNotExist) {
		return recoveryMaterial{}, journalErr
	}
	if !errors.Is(intentErr, os.ErrNotExist) {
		return recoveryMaterial{}, intentErr
	}
	if identityErr == nil {
		intent, err := identity.preparationIntent()
		if err != nil {
			return recoveryMaterial{}, err
		}
		return recoveryMaterial{intent: &intent, identity: &identity}, nil
	}
	return recoveryMaterial{}, intentErr
}

func recoveryIdentityPointer(identity recoveryIdentity, err error) *recoveryIdentity {
	if err != nil {
		return nil
	}
	return &identity
}

func cleanupRecoveryMaterial(layout workspace.Layout, runID, expectedPlanHash string, material *recoveryMaterial) error {
	if material.journal != nil {
		intent, err := journalPreparationIntent(*material.journal)
		if err != nil {
			return err
		}
		journal, err := cleanupPrivateRecoveryTemporaryArtifacts(layout, intent, material.journal)
		if err != nil {
			return err
		}
		if journal != nil {
			material.journal = journal
		}
		if err := cleanupPreparationStatusTemporaryArtifact(intent); err != nil {
			return err
		}
		return cleanupJournalTemporaryArtifacts(*material.journal)
	}
	if material.intent != nil {
		journal, err := cleanupPrivateRecoveryTemporaryArtifacts(layout, *material.intent, nil)
		if err != nil {
			return err
		}
		if err := cleanupPreparationStatusTemporaryArtifact(*material.intent); err != nil {
			return err
		}
		if journal != nil {
			material.journal = journal
			return cleanupJournalTemporaryArtifacts(*journal)
		}
		return nil
	}
	if material.bootstrap != nil {
		if err := cleanupBootstrapPreparationStatusTemporaryArtifact(layout, runID, expectedPlanHash, *material.bootstrap); err != nil {
			return err
		}
		material.orphan = true
	}
	return nil
}

func withRecoveryMigrationLocks(configPath, runID, expectedPlanHash string, fn func(workspace.Layout, recoveryMaterial) error) error {
	configPath, err := absolutePath(configPath)
	if err != nil {
		return err
	}
	if !migrationrecord.SafeRunID(runID) {
		return fmt.Errorf("workspace migration: invalid run ID")
	}
	if !validDigest(expectedPlanHash) {
		return fmt.Errorf("workspace migration: recovery requires the reviewed --plan-hash")
	}
	configRoot := filepath.Dir(configPath)
	configLock, err := applylock.Acquire(filepath.Join(configRoot, ".homonto"))
	if err != nil {
		return fmt.Errorf("workspace migration: %w", err)
	}
	defer configLock.Release()
	// This recovery-specific loader repeats normal schema/path/repository
	// validation while permitting only a matching legacy or pending marker.
	// It never converts a generic workspace.Load failure into permission to act.
	l, err := workspace.LoadMigrationRecovery(configPath)
	if err != nil {
		return fmt.Errorf("workspace migration: recovery layout validation failed: %w", err)
	}
	if l.ConfigPath != configPath || l.ConfigRoot != configRoot {
		return fmt.Errorf("workspace migration: recovery layout identity changed")
	}
	recoveryLockPath, err := migrationRecoveryLockPath(l.WorkflowRoot)
	if err != nil {
		return err
	}
	recordsLock, err := applylock.AcquirePath(recoveryLockPath, filepath.Dir(recoveryLockPath))
	if err != nil {
		return fmt.Errorf("workspace migration: records lock: %w", err)
	}
	defer recordsLock.Release()
	releaseBindings, err := workspace.LockMigrationBindings(l)
	if err != nil {
		return fmt.Errorf("workspace migration: lifecycle locks: %w", err)
	}
	defer releaseBindings()
	material, err := loadRecoveryMaterial(l.WorkflowRoot, runID)
	if err != nil {
		return err
	}
	if material.identity != nil {
		if err := validateRecoveryIdentityLayout(*material.identity, l); err != nil {
			return err
		}
		if material.identity.PlanHash != expectedPlanHash {
			return fmt.Errorf("workspace migration: durable recovery identity does not match the reviewed plan hash")
		}
		// Once a full journal exists, it contains the immutable manifest snapshot
		// used by resume and restore. Requiring the external manifest to remain
		// present would make a valid interrupted migration unrecoverable.
		if material.journal == nil {
			if err := validateRecoveryIdentityInputs(*material.identity); err != nil {
				return err
			}
		}
	}
	if material.journal != nil {
		if err := validateJournalLayout(*material.journal, l); err != nil {
			return err
		}
		if material.journal.PlanHash != expectedPlanHash {
			return fmt.Errorf("workspace migration: recovery journal does not match the reviewed plan hash")
		}
	}
	if material.intent != nil {
		if err := validatePreparationIntentLayout(*material.intent, l); err != nil {
			return err
		}
		if material.intent.PlanHash != expectedPlanHash {
			return fmt.Errorf("workspace migration: recovery preparation intent does not match the reviewed plan hash")
		}
	}
	if err := cleanupRecoveryMaterial(l, runID, expectedPlanHash, &material); err != nil {
		return err
	}
	// Descriptor publication can legitimately precede a target replacement. Load
	// the material again after cleanup so a recovered journal descriptor becomes
	// the sole authority used by resume or restore.
	material, err = loadRecoveryMaterial(l.WorkflowRoot, runID)
	if err != nil {
		return err
	}
	if material.identity != nil {
		if err := validateRecoveryIdentityLayout(*material.identity, l); err != nil || material.identity.PlanHash != expectedPlanHash {
			if err != nil {
				return err
			}
			return fmt.Errorf("workspace migration: durable recovery identity does not match the reviewed plan hash")
		}
	}
	return fn(l, material)
}
