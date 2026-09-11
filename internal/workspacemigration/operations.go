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
	prospectiveManifestVersion = 1
	runIDPlaceholder           = "{run_id}"

	dynamicReceiptRule    = "receipt-v1(run_id, reviewed_plan, registry, binding_token_hashes)"
	dynamicRegistryRule   = "registry-v1(reviewed_binding_identities, fresh_128_bit_owner_tokens)"
	dynamicOwnerRule      = "fresh_128_bit_owner_token"
	dynamicProofRule      = "commit_proof-v1(migration_commit, migration_parent, migration_tree)"
	dynamicCompletionRule = "completion_witness-v1(receipt, commit_proof)"
)

// buildProspectiveManifest derives all mutation authority from a ready plan.
// It deliberately has no run ID or token input: those values stay dynamic and
// are constrained by a named rule rather than being exposed in plan JSON.
func buildProspectiveManifest(plan Plan) (ProspectiveManifest, error) {
	if plan.Layout.ConfigRoot == "" || plan.Layout.WorkflowRoot == "" || !canonicalCommit.MatchString(plan.RecordsGit.Head) {
		return ProspectiveManifest{}, fmt.Errorf("workspace migration: prospective operations require a complete layout and records head")
	}
	operations := make([]ProspectiveOperation, 0, len(plan.RecordWrites)+5)
	for _, write := range plan.RecordWrites {
		if !filepath.IsAbs(write.Path) || filepath.Clean(write.Path) != write.Path || !pathWithin(plan.Layout.WorkflowRoot, write.Path) || !validDigest(write.PreSHA256) || !validDigest(write.PostSHA256) {
			return ProspectiveManifest{}, fmt.Errorf("workspace migration: invalid planned record operation")
		}
		mode, err := plannedRegularMode(write.Path)
		if err != nil {
			return ProspectiveManifest{}, err
		}
		operations = append(operations, ProspectiveOperation{
			Scope:      journalScopeRecords,
			Kind:       journalKindState,
			Path:       write.Path,
			Intent:     write.Action,
			PreExists:  true,
			PreSHA256:  write.PreSHA256,
			PreMode:    mode,
			PostExists: true,
			PostSHA256: write.PostSHA256,
			PostMode:   mode,
		})
	}

	ignorePath := filepath.Join(plan.Layout.WorkflowRoot, ".workflow", "migrations", ".gitignore")
	operations = append(operations, ProspectiveOperation{
		Scope:      journalScopeRecords,
		Kind:       journalKindIgnore,
		Path:       ignorePath,
		Intent:     "create_migration_ignore",
		PostExists: true,
		PostSHA256: migrationDigest([]byte(migrationIgnore)),
		PostMode:   0o644,
	})

	operations = append(operations,
		ProspectiveOperation{
			Scope:       journalScopeRecords,
			Kind:        journalKindReceipt,
			Path:        plannedRunPath(plan.Layout.WorkflowRoot, "receipt.json"),
			Intent:      "create_public_receipt",
			PostExists:  true,
			PostMode:    0o644,
			DynamicRule: dynamicReceiptRule,
		},
		ProspectiveOperation{
			Scope:       journalScopeRecords,
			Kind:        journalKindProof,
			Path:        plannedRunPath(plan.Layout.WorkflowRoot, "commit-proof.json"),
			Intent:      "create_commit_proof",
			PostExists:  true,
			PostMode:    0o644,
			DynamicRule: dynamicProofRule,
		},
		ProspectiveOperation{
			Scope:       journalScopeRecords,
			Kind:        journalKindCompletion,
			Path:        plannedRunPath(plan.Layout.WorkflowRoot, "private", "completion.json"),
			Intent:      "create_completion_witness",
			PostExists:  true,
			PostMode:    0o600,
			DynamicRule: dynamicCompletionRule,
		},
		ProspectiveOperation{
			Scope:       journalScopeConfig,
			Kind:        journalKindRegistry,
			Path:        filepath.Join(plan.Layout.ConfigRoot, ".homonto", "worktrees.json"),
			Intent:      "create_migration_registry",
			PostExists:  true,
			PostMode:    0o600,
			DynamicRule: dynamicRegistryRule,
		},
	)

	marker, err := plannedMarkerData(plan.Layout)
	if err != nil {
		return ProspectiveManifest{}, err
	}
	operations = append(operations, ProspectiveOperation{
		Scope:      journalScopeConfig,
		Kind:       journalKindMarker,
		Path:       filepath.Join(plan.Layout.ConfigRoot, ".homonto", workflowroot.LayoutMarkerFile),
		Intent:     "activate_schema_2_layout_last",
		PostExists: true,
		PostSHA256: migrationDigest(marker),
		PostMode:   0o644,
	})

	bindings, err := plannedBindingTemplates(plan)
	if err != nil {
		return ProspectiveManifest{}, err
	}
	for _, binding := range bindings {
		operations = append(operations, ProspectiveOperation{
			Scope:       journalScopeOwner,
			Kind:        journalKindOwner,
			Path:        filepath.Join(binding.GitDir, "homonto-owner"),
			Intent:      "create_legacy_execution_owner_token",
			PostExists:  true,
			PostMode:    0o600,
			DynamicRule: dynamicOwnerRule,
		})
	}
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
	expected, err := buildProspectiveManifest(plan)
	if err != nil {
		return err
	}
	if plan.Prospective.Version != prospectiveManifestVersion || !reflect.DeepEqual(plan.Prospective, expected) {
		return fmt.Errorf("workspace migration: prospective operation authority differs from the reviewed plan")
	}
	return nil
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
