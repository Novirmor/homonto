package workspacemigration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workspace"
)

const (
	prospectiveManifestVersion = 3
	runIDPlaceholder           = "{run_id}"

	dynamicReceiptRule     = "receipt-v1(run_id, reviewed_plan, registry, binding_token_hashes)"
	dynamicRegistryRule    = "registry-v1(reviewed_binding_identities, fresh_128_bit_owner_tokens)"
	dynamicOwnerRule       = "fresh_128_bit_owner_token"
	dynamicProofRule       = "commit_proof-v1(migration_commit, migration_parent, migration_tree)"
	dynamicCompletionRule  = "completion_witness-v1(receipt, commit_proof)"
	dynamicTempNameRule    = "migration_temp-v2(run_id, private_owner, operation, destination, preimage, postimage, modes, direction)"
	dynamicPrivateTempRule = "migration_private_temp-v3(run_id, private_owner, payload_descriptor, operation, destination, exact_payload)"

	dynamicPreparationStatusRule  = "preparation_status-v2(run_id, reviewed_plan_hash, layout, manifest, fresh_owner, guardian)"
	dynamicPreparationIntentRule  = "preparation_intent-v1(run_id, reviewed_plan_hash, layout, fresh_owner)"
	dynamicPrivateJournalRule     = "private_journal-v2(preparing_status_or_authenticated_recovery_journal)"
	dynamicRecordsBundleRule      = "records_git_bundle-v1(exact_records_head_and_refs)"
	dynamicIndexRestoreRule       = "logical_index_restore-v1(reviewed_preindex, selected_paths, no_migration_commit)"
	dynamicRecoveryIdentityRule   = "recovery_identity-v1(run_id, fresh_private_owner, reviewed_plan_hash, config_and_manifest_fingerprints, layout)"
	dynamicRecoveryDescriptorRule = "recovery_descriptor-v1(recovery_identity, purpose, exact_target, mode, payload_size, payload_sha256)"
	dynamicRecoveryBlobRule       = "recovery_blob-v1(recovery_descriptor, immutable_content_addressed_payload)"
	dynamicRecoveryRetiredRule    = "recovery_retired-v1(recovery_identity_link_digest, preparation_targets_removed)"
)

// buildProspectiveManifest derives all mutation authority from a ready plan.
// It deliberately has no run ID or token input: those values stay dynamic and
// are constrained by a named rule rather than being exposed in plan JSON.
func buildProspectiveManifest(plan Plan) (ProspectiveManifest, error) {
	if plan.Layout.ConfigRoot == "" || plan.Layout.WorkflowRoot == "" || !canonicalCommit.MatchString(plan.RecordsGit.Head) {
		return ProspectiveManifest{}, fmt.Errorf("workspace migration: prospective operations require a complete layout and records head")
	}
	operations := make([]ProspectiveOperation, 0, len(plan.RecordWrites)+11)
	for _, write := range plan.RecordWrites {
		if !filepath.IsAbs(write.Path) || filepath.Clean(write.Path) != write.Path || !pathWithin(plan.Layout.WorkflowRoot, write.Path) || !validDigest(write.PreSHA256) || !validDigest(write.PostSHA256) {
			return ProspectiveManifest{}, fmt.Errorf("workspace migration: invalid planned record operation")
		}
		mode, err := plannedRegularMode(write.Path)
		if err != nil {
			return ProspectiveManifest{}, err
		}
		operations = append(operations, ProspectiveOperation{
			Scope:        journalScopeRecords,
			Kind:         journalKindState,
			Path:         write.Path,
			Intent:       write.Action,
			PreExists:    true,
			PreSHA256:    write.PreSHA256,
			PreMode:      mode,
			PostExists:   true,
			PostSHA256:   write.PostSHA256,
			PostMode:     mode,
			TempParent:   filepath.Dir(write.Path),
			TempNameRule: dynamicTempNameRule,
		})
	}

	ignorePath := filepath.Join(plan.Layout.WorkflowRoot, ".workflow", "migrations", ".gitignore")
	operations = append(operations, ProspectiveOperation{
		Scope:        journalScopeRecords,
		Kind:         journalKindIgnore,
		Path:         ignorePath,
		Intent:       "create_migration_ignore",
		PostExists:   true,
		PostSHA256:   migrationDigest([]byte(migrationIgnore)),
		PostMode:     0o644,
		TempParent:   filepath.Dir(ignorePath),
		TempNameRule: dynamicTempNameRule,
	})

	operations = append(operations,
		ProspectiveOperation{
			Scope:        journalScopeRecords,
			Kind:         journalKindReceipt,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "receipt.json"),
			Intent:       "create_public_receipt",
			PostExists:   true,
			PostMode:     0o644,
			DynamicRule:  dynamicReceiptRule,
			TempParent:   plannedRunPath(plan.Layout.WorkflowRoot),
			TempNameRule: dynamicTempNameRule,
		},
		ProspectiveOperation{
			Scope:        journalScopeRecords,
			Kind:         journalKindProof,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "commit-proof.json"),
			Intent:       "create_commit_proof",
			PostExists:   true,
			PostMode:     0o644,
			DynamicRule:  dynamicProofRule,
			TempParent:   plannedRunPath(plan.Layout.WorkflowRoot),
			TempNameRule: dynamicTempNameRule,
		},
		ProspectiveOperation{
			Scope:        journalScopeRecords,
			Kind:         journalKindCompletion,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "private", "completion.json"),
			Intent:       "create_completion_witness",
			PostExists:   true,
			PostMode:     0o600,
			DataClass:    "private_completion_witness",
			Mutation:     "create_or_update",
			DynamicRule:  dynamicCompletionRule,
			TempParent:   plannedRunPath(plan.Layout.WorkflowRoot, "private"),
			TempNameRule: dynamicTempNameRule,
		},
		ProspectiveOperation{
			Scope:        journalScopeConfig,
			Kind:         journalKindRegistry,
			Path:         filepath.Join(plan.Layout.ConfigRoot, ".homonto", "worktrees.json"),
			Intent:       "create_migration_registry",
			PostExists:   true,
			PostMode:     0o600,
			DynamicRule:  dynamicRegistryRule,
			TempParent:   filepath.Join(plan.Layout.ConfigRoot, ".homonto"),
			TempNameRule: dynamicTempNameRule,
		},
	)
	operations = append(operations, plannedPrivateRecoveryOperations(plan)...)

	marker, err := plannedMarkerData(plan.Layout)
	if err != nil {
		return ProspectiveManifest{}, err
	}
	operations = append(operations, ProspectiveOperation{
		Scope:        journalScopeConfig,
		Kind:         journalKindMarker,
		Path:         filepath.Join(plan.Layout.ConfigRoot, ".homonto", workflowroot.LayoutMarkerFile),
		Intent:       "activate_schema_2_layout_last",
		PostExists:   true,
		PostSHA256:   migrationDigest(marker),
		PostMode:     0o644,
		TempParent:   filepath.Join(plan.Layout.ConfigRoot, ".homonto"),
		TempNameRule: dynamicTempNameRule,
	})

	bindings, err := plannedBindingTemplates(plan)
	if err != nil {
		return ProspectiveManifest{}, err
	}
	for _, binding := range bindings {
		operations = append(operations, ProspectiveOperation{
			Scope:        journalScopeOwner,
			Kind:         journalKindOwner,
			Path:         filepath.Join(binding.GitDir, "homonto-owner"),
			Intent:       "create_legacy_execution_owner_token",
			PostExists:   true,
			PostMode:     0o600,
			DynamicRule:  dynamicOwnerRule,
			TempParent:   binding.GitDir,
			TempNameRule: dynamicTempNameRule,
		})
	}
	indexPaths, err := plannedJournalIndexPaths(plan)
	if err != nil {
		return ProspectiveManifest{}, err
	}
	operations = append(operations, ProspectiveOperation{
		Scope:       journalScopeRecordsIndex,
		Kind:        journalKindIndexRestore,
		Path:        filepath.Join(plan.RecordsGit.GitCommonDir, "index"),
		Paths:       indexPaths,
		Intent:      "restore_authenticated_original_logical_index",
		PreExists:   true,
		PreSHA256:   plan.RecordsGit.LogicalIndexSHA256,
		PostExists:  true,
		PostSHA256:  plan.RecordsGit.LogicalIndexSHA256,
		DataClass:   "records_logical_index",
		Mutation:    "restore_selected_preimage_if_uncommitted",
		DynamicRule: dynamicIndexRestoreRule,
	})
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].Scope != operations[j].Scope {
			return operations[i].Scope < operations[j].Scope
		}
		if operations[i].Kind != operations[j].Kind {
			return operations[i].Kind < operations[j].Kind
		}
		return operations[i].Path < operations[j].Path
	})

	migrationPaths, err := plannedMigrationCommitPaths(plan)
	if err != nil {
		return ProspectiveManifest{}, err
	}
	return ProspectiveManifest{
		Version:    prospectiveManifestVersion,
		Operations: operations,
		Commits: []ProspectiveCommitBoundary{
			{
				Kind:            "migration",
				Parent:          plan.RecordsGit.Head,
				ParentRule:      "exact_plan_records_head",
				Paths:           migrationPaths,
				MessageTemplate: "Migrate legacy workflow records to schema-2 ownership ({run_id})",
				TreeRule:        "git_write_tree_after_exact_paths",
			},
			{
				Kind:            "proof",
				Parent:          "{migration_commit}",
				ParentRule:      "migration_commit",
				Paths:           []string{plannedRunRelativePath("commit-proof.json")},
				MessageTemplate: "Record schema-2 migration commit proof ({run_id})",
				TreeRule:        "git_write_tree_after_exact_paths",
			},
			{
				Kind:            "restore",
				Parent:          "{latest_records_migration_commit}",
				ParentRule:      "migration_or_proof_commit",
				Paths:           append(append([]string{}, migrationPaths...), plannedRunRelativePath("commit-proof.json")),
				MessageTemplate: "Restore interrupted schema-2 migration ({run_id})",
				TreeRule:        "git_write_tree_after_exact_paths",
			},
		},
	}, nil
}

func plannedRegularMode(path string) (uint32, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("workspace migration: planned state is no longer a real regular file: %s", path)
	}
	return uint32(info.Mode().Perm()), nil
}

func plannedRunPath(root string, suffix ...string) string {
	parts := append([]string{root, ".workflow", "migrations", runIDPlaceholder}, suffix...)
	return filepath.Join(parts...)
}

func plannedRunRelativePath(suffix ...string) string {
	parts := append([]string{".workflow", "migrations", runIDPlaceholder}, suffix...)
	return filepath.ToSlash(filepath.Join(parts...))
}

func plannedMarkerData(layout Layout) ([]byte, error) {
	data, err := json.MarshalIndent(workflowroot.LayoutMarker{
		SchemaVersion: 2,
		ConfigPath:    layout.ConfigPath,
		WorkflowRoot:  layout.WorkflowRoot,
		GitMode:       "existing",
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func plannedMigrationCommitPaths(plan Plan) ([]string, error) {
	paths := make([]string, 0, len(plan.RecordWrites)+2)
	for _, write := range plan.RecordWrites {
		if write.PreSHA256 == write.PostSHA256 {
			continue
		}
		rel, err := filepath.Rel(plan.Layout.WorkflowRoot, write.Path)
		if err != nil || !safeGitRelativePath(filepath.ToSlash(rel)) {
			return nil, fmt.Errorf("workspace migration: unsafe planned records commit path")
		}
		paths = append(paths, filepath.ToSlash(rel))
	}
	paths = append(paths, ".workflow/migrations/.gitignore", plannedRunRelativePath("receipt.json"))
	sort.Strings(paths)
	return paths, nil
}

// plannedLogicalRecordsIndexPaths is the fixed pre-migration subset of the
// records index. Dynamic migration files are necessarily absent before a fresh
// run ID is allocated, so they are not part of this plan-bound logical snapshot.
func plannedLogicalRecordsIndexPaths(plan Plan) ([]string, error) {
	paths := make([]string, 0, len(plan.RecordWrites))
	seen := map[string]bool{}
	for _, write := range plan.RecordWrites {
		rel, err := filepath.Rel(plan.Layout.WorkflowRoot, write.Path)
		if err != nil || !safeGitRelativePath(filepath.ToSlash(rel)) || seen[filepath.ToSlash(rel)] {
			return nil, fmt.Errorf("workspace migration: unsafe planned records index path")
		}
		seen[filepath.ToSlash(rel)] = true
		paths = append(paths, filepath.ToSlash(rel))
	}
	sort.Strings(paths)
	return paths, nil
}

// plannedJournalIndexPaths includes the generated records paths that can be
// staged during the transaction as well as the static state paths. It is used
// by the typed index-recovery declaration; its run ID is resolved only by the
// journal authority.
func plannedJournalIndexPaths(plan Plan) ([]string, error) {
	paths, err := plannedLogicalRecordsIndexPaths(plan)
	if err != nil {
		return nil, err
	}
	paths = append(paths,
		".workflow/migrations/.gitignore",
		plannedRunRelativePath("receipt.json"),
		plannedRunRelativePath("commit-proof.json"),
	)
	sort.Strings(paths)
	return paths, nil
}

func plannedPrivateRecoveryOperations(plan Plan) []ProspectiveOperation {
	run := plannedRunPath(plan.Layout.WorkflowRoot)
	private := plannedRunPath(plan.Layout.WorkflowRoot, "private")
	store := plannedRunPath(plan.Layout.WorkflowRoot, "private", recoveryStoreName)
	blobs := plannedRunPath(plan.Layout.WorkflowRoot, "private", recoveryStoreName, recoveryBlobDirectoryName)
	descriptors := plannedRunPath(plan.Layout.WorkflowRoot, "private", recoveryStoreName, recoveryDescriptorDirectoryName)
	return []ProspectiveOperation{
		{
			Scope:      journalScopeRecovery,
			Kind:       journalKindRecoveryRunDirectory,
			Path:       run,
			Intent:     "create_private_recovery_run_directory",
			PostExists: true,
			PostMode:   0o700,
			DataClass:  "private_recovery_directory",
			Mutation:   "create_or_validate",
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryPrivateDirectory,
			Path:         private,
			Intent:       "create_private_recovery_directory",
			PostExists:   true,
			PostMode:     0o700,
			DataClass:    "private_recovery_directory",
			Mutation:     "create_or_validate",
			ArtifactType: "directory",
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryIdentity,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "private", recoveryIdentityName),
			Intent:       "publish_atomic_recovery_identity_before_payload_preparation",
			PostExists:   true,
			DataClass:    "private_recovery_identity",
			ArtifactType: "opaque_symlink",
			Mutation:     "create_or_validate",
			DynamicRule:  dynamicRecoveryIdentityRule,
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryStoreDirectory,
			Path:         store,
			Intent:       "create_private_content_addressed_recovery_store",
			PostExists:   true,
			PostMode:     0o700,
			DataClass:    "private_recovery_store",
			ArtifactType: "directory",
			Mutation:     "create_or_validate",
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryBlobDirectory,
			Path:         blobs,
			Intent:       "create_private_immutable_payload_blob_directory",
			PostExists:   true,
			PostMode:     0o700,
			DataClass:    "private_recovery_store",
			ArtifactType: "directory",
			Mutation:     "create_or_validate",
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryDescriptorDir,
			Path:         descriptors,
			Intent:       "create_private_payload_descriptor_directory",
			PostExists:   true,
			PostMode:     0o700,
			DataClass:    "private_recovery_store",
			ArtifactType: "directory",
			Mutation:     "create_or_validate",
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryDescriptor,
			Path:         descriptors,
			Paths:        []string{"status", "intent", "bundle", "journal", "completion"},
			Intent:       "publish_payload_descriptor_before_private_target_replacement",
			PostExists:   true,
			DataClass:    "private_recovery_payload_descriptor",
			ArtifactType: "opaque_symlink",
			Mutation:     "create_or_update",
			DynamicRule:  dynamicRecoveryDescriptorRule,
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryBlob,
			Path:         blobs,
			Intent:       "persist_immutable_content_addressed_private_payload",
			PostExists:   true,
			PostMode:     recoveryBlobMode,
			DataClass:    "private_recovery_payload_blob",
			ArtifactType: "regular_file",
			Mutation:     "create_or_validate",
			DynamicRule:  dynamicRecoveryBlobRule,
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryStatus,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "private", "journal.json"),
			Intent:       "publish_preparation_status",
			PostExists:   true,
			PostMode:     0o600,
			DataClass:    "private_preparation_status",
			ArtifactType: "regular_file",
			Mutation:     "create_or_update",
			DynamicRule:  dynamicPreparationStatusRule,
			TempParent:   private,
			TempNameRule: dynamicPrivateTempRule,
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryJournal,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "private", "journal.json"),
			Intent:       "publish_private_recovery_journal",
			PostExists:   true,
			PostMode:     0o600,
			DataClass:    "private_recovery_journal",
			ArtifactType: "regular_file",
			Mutation:     "create_or_update",
			DynamicRule:  dynamicPrivateJournalRule,
			TempParent:   private,
			TempNameRule: dynamicPrivateTempRule,
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryIntent,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "private", "intent.json"),
			Intent:       "publish_then_retire_preparation_intent",
			PostExists:   true,
			PostMode:     0o600,
			DataClass:    "private_preparation_intent",
			ArtifactType: "regular_file",
			Mutation:     "create_then_remove",
			DynamicRule:  dynamicPreparationIntentRule,
			TempParent:   private,
			TempNameRule: dynamicPrivateTempRule,
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryBundle,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "private", "records.git.bundle"),
			Intent:       "create_private_records_git_backup",
			PostExists:   true,
			PostMode:     0o600,
			DataClass:    "private_records_git_bundle",
			ArtifactType: "regular_file",
			Mutation:     "create_or_validate",
			DynamicRule:  dynamicRecordsBundleRule,
			TempParent:   private,
			TempNameRule: dynamicPrivateTempRule,
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryCompletion,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "private", "completion.json"),
			Intent:       "publish_private_completion_witness_with_durable_payload_authority",
			PostExists:   true,
			PostMode:     0o600,
			DataClass:    "private_completion_witness",
			ArtifactType: "regular_file",
			Mutation:     "create_or_update",
			DynamicRule:  dynamicCompletionRule,
			TempParent:   private,
			TempNameRule: dynamicPrivateTempRule,
		},
		{
			Scope:        journalScopeRecovery,
			Kind:         journalKindRecoveryRetired,
			Path:         plannedRunPath(plan.Layout.WorkflowRoot, "private", recoveryRetiredName),
			Intent:       "retain_recovery_evidence_after_preparation_restore",
			PostExists:   true,
			DataClass:    "private_recovery_retirement_marker",
			ArtifactType: "opaque_symlink",
			Mutation:     "create_or_validate",
			DynamicRule:  dynamicRecoveryRetiredRule,
		},
	}
}

// plannedBindingTemplates identifies only execution checkouts selected by a
// reviewed active onto record. Ownership tokens are deliberately absent here;
// they are fresh private values generated only after this exact set is fixed.
func plannedBindingTemplates(plan Plan) ([]workspace.MigrationBinding, error) {
	var bindings []workspace.MigrationBinding
	seen := map[string]bool{}
	seenPaths := map[string]bool{}
	for _, record := range plan.Records {
		if record.Lifecycle != "active" || record.Framework != "onto" {
			continue
		}
		if record.ID == "" {
			return nil, fmt.Errorf("workspace migration: active record %s lacks a stable ID for worktree adoption", record.Path)
		}
		for _, source := range record.Sources {
			if source.Execution == nil {
				continue
			}
			execution := source.Execution
			if source.Alias == "" || source.BaseRef == "" || source.BaseBranch == "" || source.GitCommonDir == "" || execution.Path == "" || execution.GitCommonDir != source.GitCommonDir || execution.GitDir == "" || !filepath.IsAbs(execution.GitDir) || filepath.Clean(execution.GitDir) != execution.GitDir {
				return nil, fmt.Errorf("workspace migration: execution binding is incomplete")
			}
			key := "onto\x00" + record.Name + "\x00" + source.Alias
			if seen[key] || seenPaths[execution.Path] {
				return nil, fmt.Errorf("workspace migration: execution checkout is selected more than once")
			}
			seen[key], seenPaths[execution.Path] = true, true
			bindings = append(bindings, workspace.MigrationBinding{
				Workflow:   "onto",
				Change:     record.Name,
				StateID:    "id:" + record.ID,
				Repo:       source.Alias,
				Path:       execution.Path,
				CommonDir:  execution.GitCommonDir,
				GitDir:     execution.GitDir,
				Branch:     execution.Branch,
				BaseRef:    source.BaseRef,
				BaseTarget: "refs/heads/" + source.BaseBranch,
				BaseCommit: source.BaseRef,
			})
		}
	}
	sort.Slice(bindings, func(i, j int) bool {
		return bindings[i].Workflow+"/"+bindings[i].Change+"/"+bindings[i].Repo < bindings[j].Workflow+"/"+bindings[j].Change+"/"+bindings[j].Repo
	})
	return bindings, nil
}

func validateReadyPlan(plan Plan) error {
	if plan.Version != PlanVersion || !plan.ReadOnly || plan.Status != "ready" || !validDigest(plan.PlanHash) || plan.PlanHash != planHash(plan) {
		return fmt.Errorf("workspace migration: invalid ready plan authority")
	}
	if err := validatePlanLogicalIndex(plan); err != nil {
		return err
	}
	expected, err := buildProspectiveManifest(plan)
	if err != nil {
		return err
	}
	if plan.Prospective.Version != prospectiveManifestVersion || !reflect.DeepEqual(plan.Prospective, expected) {
		return fmt.Errorf("workspace migration: prospective operation authority differs from the reviewed plan")
	}
	return nil
}

func validatePlanLogicalIndex(plan Plan) error {
	paths, err := plannedLogicalRecordsIndexPaths(plan)
	if err != nil {
		return err
	}
	if !validDigest(plan.RecordsGit.LogicalIndexSHA256) || plan.RecordsGit.LogicalIndexSHA256 != logicalRecordsIndexDigest(plan.RecordsGit.LogicalIndex) {
		return fmt.Errorf("workspace migration: plan logical records index digest is invalid")
	}
	return validateJournalIndexSnapshot(paths, plan.RecordsGit.LogicalIndex, true)
}

func plannedSnapshotRefs(plan Plan) []FileFingerprint {
	refs := append([]FileFingerprint{plan.Config, plan.Manifest}, plan.ControlFiles...)
	refs = append(refs, plan.Files...)
	seen := map[string]bool{}
	out := make([]FileFingerprint, 0, len(refs))
	for _, ref := range refs {
		if ref.Path == "" || seen[ref.Path] {
			continue
		}
		seen[ref.Path] = true
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func resolvePlannedOperationPath(path, runID string) (string, error) {
	if strings.Count(path, runIDPlaceholder) > 1 || (strings.Contains(path, runIDPlaceholder) && !migrationrecord.SafeRunID(runID)) {
		return "", fmt.Errorf("workspace migration: invalid dynamic operation path")
	}
	return filepath.Clean(strings.ReplaceAll(path, runIDPlaceholder, runID)), nil
}
