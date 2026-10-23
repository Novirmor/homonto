package workspacemigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func newPreparationIntent(layout workspace.Layout, plan Plan) (preparationIntent, error) {
	if err := validateReadyPlan(plan); err != nil {
		return preparationIntent{}, err
	}
	runID, err := newRunID()
	if err != nil {
		return preparationIntent{}, err
	}
	owner, err := newMigrationOwner()
	if err != nil {
		return preparationIntent{}, err
	}
	intent := preparationIntent{
		Version:      migrationPreparationIntentVersion,
		RunID:        runID,
		Phase:        "preparing",
		PlanHash:     plan.PlanHash,
		ConfigPath:   layout.ConfigPath,
		ConfigRoot:   layout.ConfigRoot,
		Workflow:     layout.WorkflowRoot,
		ManifestPath: plan.Manifest.Path,
		Owner:        owner,
		Guardian:     migrationGuardianIdentity,
	}
	if err := intent.validate(); err != nil {
		return preparationIntent{}, err
	}
	return intent, nil
}

func preparationIntentFromStatus(root, runID string) (preparationIntent, error) {
	status, err := migrationrecord.LoadJournalStatus(root, runID)
	if err != nil {
		return preparationIntent{}, os.ErrNotExist
	}
	intent, err := preparationIntentFromJournalStatus(status, root, runID)
	if err != nil {
		return preparationIntent{}, err
	}
	identity, identityErr := loadRecoveryIdentity(root, runID)
	if identityErr == nil {
		path, err := journalStatusPath(root, runID)
		if err != nil {
			return preparationIntent{}, err
		}
		descriptor, expected, found, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryStatus, path)
		data, dataErr := journalStatusData(intent)
		actual, mode, exists, actualErr := readMigrationOptionalRegular(root, path)
		if err != nil || dataErr != nil || actualErr != nil || !found || !exists || descriptor.Target != path || !bytes.Equal(expected, data) || mode.Perm() != os.FileMode(descriptor.Mode).Perm() || !bytes.Equal(actual, expected) {
			return preparationIntent{}, fmt.Errorf("workspace migration: preparation status differs from durable recovery payload")
		}
	} else if !errors.Is(identityErr, os.ErrNotExist) {
		return preparationIntent{}, identityErr
	}
	return intent, nil
}

func preparationIntentFromJournalStatus(status migrationrecord.JournalStatus, root, runID string) (preparationIntent, error) {
	if status.Phase != "preparing" || status.Preparation == nil {
		return preparationIntent{}, os.ErrNotExist
	}
	preparation := status.Preparation
	intent := preparationIntent{
		Version:      migrationPreparationIntentVersion,
		RunID:        runID,
		Phase:        "preparing",
		PlanHash:     preparation.PlanHash,
		ConfigPath:   preparation.ConfigPath,
		ConfigRoot:   preparation.ConfigRoot,
		Workflow:     preparation.WorkflowRoot,
		ManifestPath: preparation.ManifestPath,
		Owner:        preparation.Owner,
		Guardian:     preparation.Guardian,
	}
	if err := intent.validate(); err != nil {
		return preparationIntent{}, err
	}
	return intent, nil
}

func (intent preparationIntent) validate() error {
	if intent.Version != migrationPreparationIntentVersion || intent.Phase != "preparing" || !migrationrecord.SafeRunID(intent.RunID) || !validDigest(intent.PlanHash) || !validMigrationOwner(intent.Owner) || intent.Guardian != migrationGuardianIdentity {
		return fmt.Errorf("workspace migration: invalid preparation intent identity")
	}
	for _, path := range []string{intent.ConfigPath, intent.ConfigRoot, intent.Workflow, intent.ManifestPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("workspace migration: invalid preparation intent path")
		}
	}
	if filepath.Dir(intent.ConfigPath) != intent.ConfigRoot {
		return fmt.Errorf("workspace migration: invalid preparation intent layout")
	}
	return nil
}

func preparationIntentData(intent preparationIntent) ([]byte, error) {
	data, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func preparationStatusTempAuthority(intent preparationIntent, data []byte) (migrationTempAuthority, error) {
	path, err := journalStatusPath(intent.Workflow, intent.RunID)
	if err != nil {
		return migrationTempAuthority{}, err
	}
	return migrationTempAuthority{
		root:      intent.Workflow,
		runID:     intent.RunID,
		scope:     journalScopeRecovery,
		kind:      journalKindRecoveryStatus,
		target:    path,
		direction: "publish",
		data:      append([]byte(nil), data...),
		mode:      0o600,
		bootstrap: true,
	}, nil
}

func bootstrapPreparationStatusTempAuthority(root, runID string) (migrationTempAuthority, error) {
	path, err := journalStatusPath(root, runID)
	if err != nil {
		return migrationTempAuthority{}, err
	}
	return migrationTempAuthority{
		root:      root,
		runID:     runID,
		scope:     journalScopeRecovery,
		kind:      journalKindRecoveryStatus,
		target:    path,
		direction: "publish",
		mode:      0o600,
		bootstrap: true,
	}, nil
}

func cleanupPreparationStatusTemporaryArtifact(intent preparationIntent) error {
	data, err := journalStatusData(intent)
	if err != nil {
		return err
	}
	authority, err := preparationStatusTempAuthority(intent, data)
	if err != nil {
		return err
	}
	return removeMigrationAuthorizedTemp(authority)
}

func loadBootstrapPreparationStatusTemporaryArtifact(root, runID string) (bootstrapPreparationStatusTemporary, bool, error) {
	authority, err := bootstrapPreparationStatusTempAuthority(root, runID)
	if err != nil {
		return bootstrapPreparationStatusTemporary{}, false, err
	}
	name, err := authority.name()
	if err != nil {
		return bootstrapPreparationStatusTemporary{}, false, err
	}
	data, mode, exists, err := readMigrationOptionalRegular(root, filepath.Join(filepath.Dir(authority.target), name))
	if err != nil || !exists {
		return bootstrapPreparationStatusTemporary{}, exists, err
	}
	return bootstrapPreparationStatusTemporary{data: data, mode: mode}, true, nil
}

func bootstrapPreparationStatusTemporaryConflict() error {
	return fmt.Errorf("workspace migration: recovery conflict: bootstrap preparation status temporary is not recognized")
}

func cleanupBootstrapPreparationStatusTemporaryArtifact(layout workspace.Layout, runID, expectedPlanHash string, temporary bootstrapPreparationStatusTemporary) error {
	if temporary.mode.Perm() != 0o600 {
		return bootstrapPreparationStatusTemporaryConflict()
	}
	status, err := migrationrecord.ParseJournalStatus(temporary.data, layout.WorkflowRoot, runID)
	if err != nil {
		return bootstrapPreparationStatusTemporaryConflict()
	}
	intent, err := preparationIntentFromJournalStatus(status, layout.WorkflowRoot, runID)
	if err != nil || validatePreparationIntentLayout(intent, layout) != nil || intent.PlanHash != expectedPlanHash {
		return bootstrapPreparationStatusTemporaryConflict()
	}
	data, err := journalStatusData(intent)
	if err != nil || !bytes.Equal(temporary.data, data) {
		return bootstrapPreparationStatusTemporaryConflict()
	}
	return cleanupPreparationStatusTemporaryArtifact(intent)
}

func journalPreparationIntent(j privateJournal) (preparationIntent, error) {
	intent := preparationIntent{
		Version:      migrationPreparationIntentVersion,
		RunID:        j.RunID,
		Phase:        "preparing",
		PlanHash:     j.PlanHash,
		ConfigPath:   j.ConfigPath,
		ConfigRoot:   j.ConfigRoot,
		Workflow:     j.Workflow,
		ManifestPath: j.Plan.Manifest.Path,
		Owner:        j.IntentOwner,
		Guardian:     migrationGuardianIdentity,
	}
	if err := intent.validate(); err != nil {
		return preparationIntent{}, err
	}
	return intent, nil
}

func validatePreparationIntentLayout(intent preparationIntent, layout workspace.Layout) error {
	if err := intent.validate(); err != nil {
		return err
	}
	if intent.ConfigPath != layout.ConfigPath || intent.ConfigRoot != layout.ConfigRoot || intent.Workflow != layout.WorkflowRoot {
		return fmt.Errorf("workspace migration: preparation intent does not belong to this workspace layout")
	}
	return nil
}

func savePreparationIntent(intent preparationIntent) error {
	if err := intent.validate(); err != nil {
		return err
	}
	// Publish the content-free pending status first so an interrupted intent is
	// discoverable by ordinary loaders before any backup work can begin.
	if err := saveJournalStatus(intent); err != nil {
		return err
	}
	path, err := ensurePrivateJournalDirectory(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	intentPath, err := preparationIntentPath(intent.Workflow, intent.RunID)
	if err != nil {
		return err
	}
	if filepath.Dir(path) != filepath.Dir(intentPath) {
		return fmt.Errorf("workspace migration: preparation intent directory differs from journal directory")
	}
	data, err := preparationIntentData(intent)
	if err != nil {
		return err
	}
	identity, err := loadRecoveryIdentity(intent.Workflow, intent.RunID)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		return fmt.Errorf("workspace migration: durable recovery identity is missing or differs from preparation intent")
	}
	return writePrivateRecoveryPayload(identity, journalKindRecoveryIntent, intentPath, data)
}

func loadPreparationIntent(root, runID string) (preparationIntent, error) {
	path, err := preparationIntentPath(root, runID)
	if err != nil {
		return preparationIntent{}, err
	}
	data, mode, exists, err := readMigrationOptionalRegular(root, path)
	if err != nil {
		return preparationIntent{}, err
	}
	if !exists {
		return preparationIntent{}, os.ErrNotExist
	}
	if err := requirePrivateRecoveryDirectories(root, runID); err != nil {
		return preparationIntent{}, err
	}
	if mode.Perm()&0o077 != 0 {
		return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent is not a 0600 regular file")
	}
	var intent preparationIntent
	if err := decodePrivateJSON(data, &intent); err != nil {
		return preparationIntent{}, fmt.Errorf("workspace migration: invalid preparation intent: %w", err)
	}
	if err := intent.validate(); err != nil {
		return preparationIntent{}, err
	}
	if intent.RunID != runID || intent.Workflow != root {
		return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent does not match its requested recovery directory")
	}
	if expected, err := preparationIntentPath(intent.Workflow, intent.RunID); err != nil || expected != path {
		return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent path does not match its identity")
	}
	identity, identityErr := loadRecoveryIdentity(root, runID)
	if identityErr == nil {
		descriptor, expected, found, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryIntent, path)
		if err != nil || !found || descriptor.Target != path || !bytes.Equal(data, expected) || mode.Perm() != os.FileMode(descriptor.Mode).Perm() || !recoveryIdentityMatchesIntent(identity, intent) {
			return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent differs from durable recovery payload")
		}
	} else if !errors.Is(identityErr, os.ErrNotExist) {
		return preparationIntent{}, identityErr
	}
	statusIntent, err := preparationIntentFromStatus(root, runID)
	if err != nil || statusIntent != intent {
		return preparationIntent{}, fmt.Errorf("workspace migration: preparation intent status is missing or invalid")
	}
	return intent, nil
}

func (j privateJournal) validate() error {
	if j.Version != migrationJournalVersion || !migrationrecord.SafeRunID(j.RunID) || !validJournalPhase(j.Phase) || !validDigest(j.PlanHash) {
		return fmt.Errorf("workspace migration: invalid private journal identity")
	}
	for _, path := range []string{j.ConfigPath, j.ConfigRoot, j.Workflow} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("workspace migration: invalid private journal path")
		}
	}
	if filepath.Dir(j.ConfigPath) != j.ConfigRoot || j.Plan.PlanHash != j.PlanHash {
		return fmt.Errorf("workspace migration: invalid private journal plan binding")
	}
	if err := validateReadyPlan(j.Plan); err != nil {
		return err
	}
	if !canonicalCommit.MatchString(j.RecordsParent) || !validDigest(j.RecordsIndexSHA256) || (j.IntentOwner != "" && !validMigrationOwner(j.IntentOwner)) || strings.TrimSpace(j.MigrationMessage) == "" || !validDigest(j.MigrationMessageSHA256) || strings.TrimSpace(j.ProofMessage) == "" || strings.TrimSpace(j.RestoreMessage) == "" || !validDigest(j.RestoreMessageSHA256) {
		return fmt.Errorf("workspace migration: invalid private journal records identity")
	}
	if j.MigrationMessageSHA256 != migrationDigest([]byte(j.MigrationMessage+"\n")) {
		return fmt.Errorf("workspace migration: invalid private journal migration message")
	}
	if j.RestoreMessageSHA256 != migrationDigest([]byte(j.RestoreMessage+"\n")) {
		return fmt.Errorf("workspace migration: invalid private journal restore message")
	}
	for _, tree := range []string{j.ExpectedMigrationTree, j.ExpectedProofTree, j.ExpectedRestoreTree} {
		if tree != "" && !canonicalCommit.MatchString(tree) {
			return fmt.Errorf("workspace migration: invalid private journal Git tree")
		}
	}
	for _, commit := range []string{j.MigrationCommit, j.ProofCommit, j.RestoreParent, j.RestoreCommit} {
		if commit != "" && !canonicalCommit.MatchString(commit) {
			return fmt.Errorf("workspace migration: invalid private journal Git commit")
		}
	}
	if (j.ExpectedMigrationTree != "" && j.MigrationCommit == "" && j.Phase != "prepared" && j.Phase != "applying" && j.Phase != "restoring" && j.Phase != "restored") || (j.ProofCommit != "" && (j.MigrationCommit == "" || j.ExpectedProofTree == "")) || (j.ExpectedRestoreTree != "" && j.RestoreParent == "") || (j.RestoreCommit != "" && (j.RestoreParent == "" || j.ExpectedRestoreTree == "")) {
		return fmt.Errorf("workspace migration: incomplete private journal commit state")
	}
	seenWrites := map[string]bool{}
	for _, write := range j.Writes {
		if err := write.validate(j); err != nil {
			return err
		}
		key := write.Scope + "\x00" + write.Path
		if seenWrites[key] {
			return fmt.Errorf("workspace migration: duplicate private journal write")
		}
		seenWrites[key] = true
	}
	seenSnapshots := map[string]bool{}
	for _, snapshot := range j.Snapshots {
		if !filepath.IsAbs(snapshot.Path) || filepath.Clean(snapshot.Path) != snapshot.Path || !validDigest(snapshot.SHA256) || snapshot.SHA256 != migrationDigest(snapshot.Data) || snapshot.Mode == 0 || seenSnapshots[snapshot.Path] {
			return fmt.Errorf("workspace migration: invalid private backup snapshot")
		}
		seenSnapshots[snapshot.Path] = true
	}
	if err := validateRecordsGitBackup(j); err != nil {
		return err
	}
	if err := validateJournalIndexAuthority(j); err != nil {
		return err
	}
	seenBindings := map[string]bool{}
	for _, binding := range j.Bindings {
		key := binding.Workflow + "\x00" + binding.Change + "\x00" + binding.Repo
		if key == "\x00\x00" || seenBindings[key] || binding.Ownership == "" {
			return fmt.Errorf("workspace migration: invalid private binding")
		}
		seenBindings[key] = true
	}
	return validateJournalAuthority(j)
}

type bootstrapPreparationStatusTemporary struct {
	data []byte
	mode os.FileMode
}
