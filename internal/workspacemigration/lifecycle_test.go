package workspacemigration

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/migrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func TestRecoverRestoreReplaysEveryInterruptedWriteToItsPreimage(t *testing.T) {
	// This fixture has one transformed active state and one byte-preserved
	// retired state. The five actual writes are registry, active state, ignore,
	// receipt, and proof; the retained state already equals its postimage.
	for failAt := 1; failAt <= 5; failAt++ {
		t.Run("write-"+string(rune('0'+failAt)), func(t *testing.T) {
			fixture := newMigrationFixture(t, true, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			initialTree := gitTextTest(t, fixture.records, "rev-parse", "HEAD^{tree}")
			activePath := filepath.Join(fixture.records, "changes", "active", "onto-state.yaml")
			retiredPath := filepath.Join(fixture.records, "changes", "active-retired", "onto-state.yaml")
			activeBefore := readFile(t, activePath)
			retiredBefore := readFile(t, retiredPath)

			previous := migrationAfterWrite
			calls := 0
			migrationAfterWrite = func(string) error {
				calls++
				if calls == failAt {
					return errors.New("interrupted migration write")
				}
				return nil
			}
			_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
			migrationAfterWrite = previous
			if err == nil || !strings.Contains(err.Error(), "interrupted migration write") {
				t.Fatalf("Apply error = %v", err)
			}
			runID := interruptedRunID(t, fixture.records)
			if !strings.Contains(err.Error(), runID) || !strings.Contains(err.Error(), "recover") {
				t.Fatalf("interrupted apply did not report recoverable run ID: %v", err)
			}

			result, err := Recover(fixture.config, runID, "restore", plan.PlanHash)
			if err != nil {
				t.Fatalf("Recover restore: %v", err)
			}
			if result.Status != "restored" {
				t.Fatalf("restore result = %+v", result)
			}
			if got := readFile(t, activePath); got != activeBefore {
				t.Fatalf("active state was not restored\nwant: %q\n got: %q", activeBefore, got)
			}
			if got := readFile(t, retiredPath); got != retiredBefore {
				t.Fatalf("retired state was not preserved\nwant: %q\n got: %q", retiredBefore, got)
			}
			for _, path := range []string{
				filepath.Join(fixture.root, ".homonto", "workflow-layout.json"),
				filepath.Join(fixture.root, ".homonto", "worktrees.json"),
			} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("restore retained generated control file %s: %v", path, err)
				}
			}
			if got := gitTextTest(t, fixture.records, "rev-parse", "HEAD^{tree}"); got != initialTree {
				t.Fatalf("restored records tree = %s, want %s", got, initialTree)
			}
			journal, err := loadJournal(fixture.records, runID)
			if err != nil || journal.Phase != "restored" {
				t.Fatalf("restored journal = %+v, error = %v", journal, err)
			}
			if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil {
				t.Fatal("restored journal resumed")
			}
		})
	}
}

func TestRecoverRestoreRefusesAConflictingJournaledPath(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	previous := migrationAfterWrite
	migrationAfterWrite = func(path string) error {
		if path == filepath.Join(fixture.root, ".homonto", "worktrees.json") {
			return errors.New("interrupted registry write")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterWrite = previous
	if err == nil {
		t.Fatal("Apply unexpectedly completed")
	}
	if !strings.Contains(err.Error(), "interrupted registry write") {
		t.Fatalf("Apply error = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	registry := filepath.Join(fixture.root, ".homonto", "worktrees.json")
	if err := os.WriteFile(registry, []byte("operator edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(fixture.config, runID, "restore", plan.PlanHash); err == nil || !strings.Contains(err.Error(), registry) {
		t.Fatalf("Recover restore error = %v, want conflict at %s", err, registry)
	}
	if got := readFile(t, registry); got != "operator edit\n" {
		t.Fatalf("conflicting registry was overwritten: %q", got)
	}
}

func TestRecoverRestoreRefusesAChangedJournaledMode(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	previous := migrationAfterWrite
	migrationAfterWrite = func(path string) error {
		if path == filepath.Join(fixture.root, ".homonto", "worktrees.json") {
			return errors.New("interrupted registry write")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterWrite = previous
	if err == nil {
		t.Fatal("Apply unexpectedly completed")
	}
	runID := interruptedRunID(t, fixture.records)
	registry := filepath.Join(fixture.root, ".homonto", "worktrees.json")
	if err := os.Chmod(registry, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(fixture.config, runID, "restore", plan.PlanHash); err == nil || !strings.Contains(err.Error(), registry) {
		t.Fatalf("Recover restore error = %v, want conflict at %s", err, registry)
	}
	info, err := os.Lstat(registry)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("conflicting registry mode was overwritten: info=%v error=%v", info, err)
	}
}

func TestRecoverResumeCompletesAnInterruptedMigration(t *testing.T) {
	fixture := newMigrationFixture(t, true, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	previous := migrationAfterWrite
	fail := true
	migrationAfterWrite = func(path string) error {
		if fail && path == filepath.Join(fixture.root, ".homonto", "worktrees.json") {
			fail = false
			return errors.New("interrupted registry write")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterWrite = previous
	if err == nil {
		t.Fatal("Apply unexpectedly completed")
	}
	if !strings.Contains(err.Error(), "interrupted registry write") {
		t.Fatalf("Apply error = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)

	result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil {
		t.Fatalf("Recover resume: %v", err)
	}
	if result.Status != "complete" {
		t.Fatalf("resume result = %+v", result)
	}
	verification, err := Verify(fixture.config, runID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verification.Status != "complete" || verification.RecordWrites != 2 || verification.RetiredRecords != 1 {
		t.Fatalf("verification = %+v", verification)
	}
	commits := gitTextTest(t, fixture.records, "rev-list", "--count", "HEAD")
	again, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || again.Status != "complete" || gitTextTest(t, fixture.records, "rev-list", "--count", "HEAD") != commits {
		t.Fatalf("repeated resume = %+v, error = %v", again, err)
	}
}

func TestRecoverResumeCompletesAfterMarkerBeforeJournalCompletion(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	previous := migrationAfterMarker
	migrationAfterMarker = func() error { return errors.New("interrupted marker finalization") }
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterMarker = previous
	if err == nil || !strings.Contains(err.Error(), "interrupted marker finalization") {
		t.Fatalf("Apply error = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil || journal.Phase != "pending-finalization" {
		t.Fatalf("interrupted journal = %+v, error = %v", journal, err)
	}
	if _, err := workspace.Load(fixture.config); err == nil || !strings.Contains(err.Error(), "workspace migration pending") {
		t.Fatalf("ordinary layout load = %v, want pending migration", err)
	}
	commits := gitTextTest(t, fixture.records, "rev-list", "--count", "HEAD")

	result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || result.Status != "complete" {
		t.Fatalf("Recover resume = %+v, %v", result, err)
	}
	if _, err := Verify(fixture.config, runID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if _, err := workspace.Load(fixture.config); err != nil {
		t.Fatalf("completed schema-2 layout load: %v", err)
	}
	again, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || again.Status != "complete" || gitTextTest(t, fixture.records, "rev-list", "--count", "HEAD") != commits {
		t.Fatalf("repeated resume = %+v, error = %v", again, err)
	}
}

func TestApplyAdoptsLegacyExecutionCheckoutWithoutChangingIt(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	execution := filepath.Join(fixture.root, ".legacy-worktrees", "active")
	runGit(t, fixture.repos["app"], "worktree", "add", "-b", "migration-active", execution, fixture.bases["app"])
	fixture.input.Records[0].Sources[0].ExecutionPath = execution
	writeManifest(t, fixture)

	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := findRecord(t, plan, "active-id").Sources[0].Execution; got == nil || got.Path != execution {
		t.Fatalf("planned execution checkout = %+v", got)
	}
	beforeHead := gitTextTest(t, execution, "rev-parse", "HEAD")
	beforeTree := gitTextTest(t, execution, "rev-parse", "HEAD^{tree}")
	beforeBranch := gitTextTest(t, execution, "branch", "--show-current")
	beforeMain := gitTextTest(t, fixture.repos["app"], "rev-parse", "refs/heads/main")
	gitDir := gitTextTest(t, execution, "rev-parse", "--absolute-git-dir")

	result, err := Apply(fixture.config, fixture.manifest, plan.PlanHash)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if result.Status != "complete" {
		t.Fatalf("Apply result = %+v", result)
	}
	verification, err := Verify(fixture.config, result.RunID)
	if err != nil || verification.Bindings != 1 {
		t.Fatalf("Verify = %+v, %v", verification, err)
	}
	if got := gitTextTest(t, execution, "rev-parse", "HEAD"); got != beforeHead {
		t.Fatalf("execution HEAD = %s, want %s", got, beforeHead)
	}
	if got := gitTextTest(t, execution, "rev-parse", "HEAD^{tree}"); got != beforeTree {
		t.Fatalf("execution tree = %s, want %s", got, beforeTree)
	}
	if got := gitTextTest(t, execution, "branch", "--show-current"); got != beforeBranch {
		t.Fatalf("execution branch = %s, want %s", got, beforeBranch)
	}
	if got := gitTextTest(t, fixture.repos["app"], "rev-parse", "refs/heads/main"); got != beforeMain {
		t.Fatalf("source main = %s, want %s", got, beforeMain)
	}
	runGit(t, execution, "diff", "--quiet")
	runGit(t, execution, "diff", "--cached", "--quiet")
	owner, err := os.Lstat(filepath.Join(gitDir, "homonto-owner"))
	if err != nil || owner.Mode().Perm() != 0o600 {
		t.Fatalf("migration owner token = %v, %v", owner, err)
	}

	layout, err := workspace.Load(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := workspace.ListWorktrees(layout)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ListWorktrees = %+v, %v", entries, err)
	}
	if entry := entries[0]; entry.Origin != "legacy-migration" || entry.MigrationRunID != result.RunID || entry.Path != execution {
		t.Fatalf("adopted worktree = %+v", entry)
	}
}

func TestApplyCommitsPreparedLegacyConfigInPublicReceipt(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	statePath := filepath.Join(fixture.records, "changes", "active", "onto-state.yaml")
	writeFile(t, statePath, "schema_version: 2\nchange: active\nid: active-id\nworkflow: full\nphase: build\nbase_ref: "+fixture.bases["app"]+"\nbase_branch: main\nrepos: [app]\n")
	runGit(t, fixture.records, "add", "changes/active/onto-state.yaml")
	runGit(t, fixture.records, "commit", "-m", "legacy scalar state")
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	planned := findRecordWrite(t, plan, statePath)
	if planned.LegacyConfig == nil || planned.LegacyConfig.BaseRef == "" || planned.LegacyConfig.BaseBranch == "" {
		t.Fatalf("prepared legacy config = %+v", planned.LegacyConfig)
	}
	result, err := Apply(fixture.config, fixture.manifest, plan.PlanHash)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	verification, err := Verify(fixture.config, result.RunID)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	receiptPath, err := migrationrecord.ReceiptPath(fixture.records, result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	receiptRel, err := filepath.Rel(fixture.records, receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	committed, err := migrationGit(fixture.records, "show", verification.MigrationCommit+":"+filepath.ToSlash(receiptRel))
	if err != nil {
		t.Fatalf("read committed receipt: %v", err)
	}
	receipt, err := migrationrecord.ParseReceipt(committed)
	if err != nil {
		t.Fatalf("parse committed receipt: %v", err)
	}
	for _, write := range receipt.RecordWrites {
		if write.Path != statePath {
			continue
		}
		if write.LegacyConfig == nil || write.LegacyConfig.BaseRef != planned.LegacyConfig.BaseRef || write.LegacyConfig.BaseBranch != planned.LegacyConfig.BaseBranch || write.LegacyConfig.Provenance != migrationrecord.LegacyConfigProvenance {
			t.Fatalf("committed legacy config = %+v, want %+v", write.LegacyConfig, planned.LegacyConfig)
		}
		return
	}
	t.Fatalf("receipt omitted transformed state %s", statePath)
}

func TestApplyPreservesEveryCoResidentRetiredStateFile(t *testing.T) {
	fixture := newMigrationFixture(t, true, false)
	retiredDir := filepath.Join(fixture.records, "changes", "active-retired")
	ontoState := filepath.Join(retiredDir, "onto-state.yaml")
	state := filepath.Join(retiredDir, "state.yaml")
	writeFile(t, state, "# legacy co-resident formatting\n"+readFile(t, ontoState))
	runGit(t, fixture.records, "add", "changes/active-retired/state.yaml")
	runGit(t, fixture.records, "commit", "-m", "co-resident retired state")
	beforeOnto := readFile(t, ontoState)
	beforeState := readFile(t, state)

	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	result, err := Apply(fixture.config, fixture.manifest, plan.PlanHash)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := Verify(fixture.config, result.RunID); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	retired, err := migrationrecord.IsRetired(fixture.records, retiredDir, "retired-id")
	if err != nil || !retired {
		t.Fatalf("IsRetired = %t, %v", retired, err)
	}
	if got := readFile(t, ontoState); got != beforeOnto {
		t.Fatalf("onto state changed\nwant: %q\n got: %q", beforeOnto, got)
	}
	if got := readFile(t, state); got != beforeState {
		t.Fatalf("co-resident state changed\nwant: %q\n got: %q", beforeState, got)
	}

	writeFile(t, state, beforeState+"changed: true\n")
	if _, err := migrationrecord.IsRetired(fixture.records, retiredDir, "retired-id"); err == nil {
		t.Fatal("IsRetired accepted a changed co-resident retired state")
	}
}

func TestApplyRefusesAStaleSourceFingerprintBeforeWritingJournal(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	writeFile(t, filepath.Join(fixture.repos["app"], "untracked-after-plan"), "changed\n")
	if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "stale plan hash") {
		t.Fatalf("Apply stale source error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.records, ".workflow", "migrations")); !os.IsNotExist(err) {
		t.Fatalf("stale apply created a private journal: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(fixture.root, ".homonto", "workflow-layout.json")); !os.IsNotExist(err) {
		t.Fatalf("stale apply created a layout marker: %v", err)
	}
}

func TestMigrationRejectsSameStatusContentDrift(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, fixture *migrationFixture)
		mutate  func(t *testing.T, fixture *migrationFixture)
	}{
		{
			name: "tracked",
			prepare: func(t *testing.T, fixture *migrationFixture) {
				writeFile(t, filepath.Join(fixture.repos["app"], "tracked"), "dirty before plan\n")
			},
			mutate: func(t *testing.T, fixture *migrationFixture) {
				writeFile(t, filepath.Join(fixture.repos["app"], "tracked"), "dirty after plan\n")
			},
		},
		{
			name: "untracked",
			prepare: func(t *testing.T, fixture *migrationFixture) {
				writeFile(t, filepath.Join(fixture.repos["app"], "notes.txt"), "before plan\n")
			},
			mutate: func(t *testing.T, fixture *migrationFixture) {
				writeFile(t, filepath.Join(fixture.repos["app"], "notes.txt"), "after plan\n")
			},
		},
		{
			name: "untracked mode",
			prepare: func(t *testing.T, fixture *migrationFixture) {
				writeFile(t, filepath.Join(fixture.repos["app"], "notes.txt"), "before plan\n")
			},
			mutate: func(t *testing.T, fixture *migrationFixture) {
				if err := os.Chmod(filepath.Join(fixture.repos["app"], "notes.txt"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "untracked replaced by symlink",
			prepare: func(t *testing.T, fixture *migrationFixture) {
				writeFile(t, filepath.Join(fixture.repos["app"], "notes.txt"), "before plan\n")
			},
			mutate: func(t *testing.T, fixture *migrationFixture) {
				path := filepath.Join(fixture.repos["app"], "notes.txt")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("tracked", path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			},
		},
		{
			name: "ignored",
			prepare: func(t *testing.T, fixture *migrationFixture) {
				writeFile(t, filepath.Join(fixture.repos["app"], ".git", "info", "exclude"), "ignored.txt\n")
				writeFile(t, filepath.Join(fixture.repos["app"], "ignored.txt"), "before plan\n")
			},
			mutate: func(t *testing.T, fixture *migrationFixture) {
				writeFile(t, filepath.Join(fixture.repos["app"], "ignored.txt"), "after plan\n")
			},
		},
		{
			name: "execution checkout",
			prepare: func(t *testing.T, fixture *migrationFixture) {
				execution := filepath.Join(fixture.root, ".legacy-worktrees", "active")
				runGit(t, fixture.repos["app"], "worktree", "add", "-b", "migration-active", execution, fixture.bases["app"])
				fixture.input.Records[0].Sources[0].ExecutionPath = execution
				writeManifest(t, fixture)
				writeFile(t, filepath.Join(execution, "tracked"), "dirty before plan\n")
			},
			mutate: func(t *testing.T, fixture *migrationFixture) {
				execution := fixture.input.Records[0].Sources[0].ExecutionPath
				writeFile(t, filepath.Join(execution, "tracked"), "dirty after plan\n")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			tc.prepare(t, fixture)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			tc.mutate(t, fixture)

			if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "stale plan hash") {
				t.Fatalf("Apply after same-status source content drift = %v", err)
			}
			if _, err := os.Lstat(filepath.Join(fixture.records, ".workflow", "migrations")); !os.IsNotExist(err) {
				t.Fatalf("stale Apply created a private journal: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(fixture.root, ".homonto", "workflow-layout.json")); !os.IsNotExist(err) {
				t.Fatalf("stale Apply created a layout marker: %v", err)
			}
		})
	}
}

func TestMigrationFinalizationChecksSourcePreimages(t *testing.T) {
	t.Run("records hook changes a same-status source file", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		sourcePath := filepath.Join(fixture.repos["app"], "tracked")
		writeFile(t, sourcePath, "dirty before plan\n")
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		hook := filepath.Join(fixture.records, ".git", "hooks", "pre-commit")
		writeFile(t, hook, "#!/bin/sh\nprintf 'dirty after hook\\n' > \""+sourcePath+"\"\n")
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}

		if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "source worktree content changed") {
			t.Fatalf("Apply after records hook source mutation = %v", err)
		}
		runID := interruptedRunID(t, fixture.records)
		journal, err := loadJournal(fixture.records, runID)
		if err != nil || journal.Phase != "pending-finalization" {
			t.Fatalf("journal after finalization refusal = %+v, %v", journal, err)
		}
		if _, err := os.Lstat(filepath.Join(fixture.root, ".homonto", "workflow-layout.json")); !os.IsNotExist(err) {
			t.Fatalf("source drift activated the layout marker: %v", err)
		}
	})

	t.Run("records hook changes historic markdown", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		handoff := filepath.Join(fixture.records, "changes", "active", ".onto", "handoff.md")
		hook := filepath.Join(fixture.records, ".git", "hooks", "pre-commit")
		writeFile(t, hook, "#!/bin/sh\nprintf 'historic hook mutation\\n' > \""+handoff+"\"\n")
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "unrelated changed path") {
			t.Fatalf("Apply after records hook markdown mutation = %v", err)
		}
		runID := interruptedRunID(t, fixture.records)
		journal, err := loadJournal(fixture.records, runID)
		if err != nil || journal.Phase == "complete" {
			t.Fatalf("journal after markdown finalization refusal = %+v, %v", journal, err)
		}
		if _, err := os.Lstat(filepath.Join(fixture.root, ".homonto", "workflow-layout.json")); !os.IsNotExist(err) {
			t.Fatalf("markdown drift activated the layout marker: %v", err)
		}
	})

	t.Run("verify rejects post-migration source and preserved-record drift", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		sourcePath := filepath.Join(fixture.repos["app"], "tracked")
		writeFile(t, sourcePath, "dirty before plan\n")
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		result, err := Apply(fixture.config, fixture.manifest, plan.PlanHash)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		writeFile(t, sourcePath, "dirty after migration\n")
		if _, err := Verify(fixture.config, result.RunID); err == nil || !strings.Contains(err.Error(), "source worktree content changed") {
			t.Fatalf("Verify after source drift = %v", err)
		}

		writeFile(t, sourcePath, "dirty before plan\n")
		handoff := filepath.Join(fixture.records, "changes", "active", ".onto", "handoff.md")
		writeFile(t, handoff, "historic evidence changed\n")
		runGit(t, fixture.records, "update-index", "--assume-unchanged", "changes/active/.onto/handoff.md")
		if _, err := Verify(fixture.config, result.RunID); err == nil || !strings.Contains(err.Error(), "input changed after journal creation") {
			t.Fatalf("Verify after preserved record drift = %v", err)
		}
	})
}

func TestMigrationBackupContainsRecordsGit(t *testing.T) {
	fixture := newMigrationFixture(t, true, false)
	runGit(t, fixture.records, "branch", "legacy-preserved")
	runGit(t, fixture.records, "tag", "-a", "legacy-preserved-tag", "-m", "legacy backup coverage")
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	beforeHead := gitTextTest(t, fixture.records, "rev-parse", "HEAD")
	indexPath := gitTextTest(t, fixture.records, "rev-parse", "--path-format=absolute", "--git-path", "index")
	beforeIndex, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeRefs, err := recordsGitReferences(fixture.records)
	if err != nil {
		t.Fatal(err)
	}

	runID := interruptAtRegistryWrite(t, fixture, plan)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil {
		t.Fatalf("load journal: %v", err)
	}
	backup := journal.RecordsBackup
	if backup.Head != beforeHead || backup.IndexPath != indexPath || !bytes.Equal(backup.IndexData, beforeIndex) || backup.IndexSHA256 != migrationDigest(beforeIndex) {
		t.Fatalf("records Git backup identity = %+v", backup)
	}
	if len(backup.Refs) != len(beforeRefs) {
		t.Fatalf("records Git backup refs = %+v, want %+v", backup.Refs, beforeRefs)
	}
	for i := range beforeRefs {
		if backup.Refs[i] != beforeRefs[i] {
			t.Fatalf("records Git backup ref %d = %+v, want %+v", i, backup.Refs[i], beforeRefs[i])
		}
	}
	info, err := os.Lstat(backup.BundlePath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("records Git bundle permissions = %v, %v", info, err)
	}
	if err := verifyRecordsGitBackup(fixture.records, backup); err != nil {
		t.Fatalf("verify records Git backup: %v", err)
	}

	restored := filepath.Join(t.TempDir(), "restored-records")
	if err := os.MkdirAll(restored, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, restored, "init", "-b", "main")
	if _, err := migrationGit(restored, "bundle", "unbundle", backup.BundlePath); err != nil {
		t.Fatalf("unbundle records backup: %v", err)
	}
	if _, err := migrationGit(restored, "cat-file", "-e", backup.Head+"^{commit}"); err != nil {
		t.Fatalf("restored backup lacks HEAD: %v", err)
	}
	for _, ref := range backup.Refs {
		if _, err := migrationGit(restored, "cat-file", "-e", ref.Object+"^{object}"); err != nil {
			t.Fatalf("restored backup lacks %s: %v", ref.Name, err)
		}
	}
}

func TestMigrationRecoveryRejectsMissingOrCorruptRecordsGitBackupBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		action string
		mutate func(t *testing.T, path string)
	}{
		{
			name:   "missing during resume",
			action: "resume",
			mutate: func(t *testing.T, path string) {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:   "corrupt during restore",
			action: "restore",
			mutate: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("corrupt backup\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			runID := interruptAtRegistryWrite(t, fixture, plan)
			journal, err := loadJournal(fixture.records, runID)
			if err != nil {
				t.Fatalf("load journal: %v", err)
			}
			before := migrationSnapshot(t, fixture)
			tc.mutate(t, journal.RecordsBackup.BundlePath)

			if _, err := Recover(fixture.config, runID, tc.action, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "private records Git backup") {
				t.Fatalf("Recover with %s backup = %v", tc.name, err)
			}
			if after := migrationSnapshot(t, fixture); !bytes.Equal(mustJSON(t, before), mustJSON(t, after)) {
				t.Fatalf("backup refusal changed authoritative files\nbefore: %#v\nafter: %#v", before, after)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestRecoverResumeRefusesAnUnrelatedRecordsEdit(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	previous := migrationAfterWrite
	migrationAfterWrite = func(path string) error {
		if path == filepath.Join(fixture.root, ".homonto", "worktrees.json") {
			return errors.New("interrupted registry write")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterWrite = previous
	if err == nil {
		t.Fatal("Apply unexpectedly completed")
	}
	runID := interruptedRunID(t, fixture.records)
	foreign := filepath.Join(fixture.records, "changes", "foreign.md")
	writeFile(t, foreign, "unrelated\n")
	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "unrelated changed path") {
		t.Fatalf("Recover resume error = %v", err)
	}
	if got := readFile(t, foreign); got != "unrelated\n" {
		t.Fatalf("foreign records edit was changed: %q", got)
	}
}

func TestRecoverRefusesJournalWriteOutsideTheReviewedPlan(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	runID := interruptAtRegistryWrite(t, fixture, plan)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil {
		t.Fatalf("load journal: %v", err)
	}
	roguePath := filepath.Join(fixture.records, "changes", "rogue", "onto-state.yaml")
	journal.Writes = append(journal.Writes, privateWrite{
		Scope:      journalScopeRecords,
		Kind:       journalKindState,
		Path:       roguePath,
		PostExists: true,
		PostMode:   0o644,
		Postimage:  []byte("rogue\n"),
	})
	overwriteJournalForTest(t, fixture.records, runID, journal)

	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil {
		t.Fatal("Recover accepted a journal write outside the reviewed plan")
	}
	if _, err := os.Lstat(roguePath); !os.IsNotExist(err) {
		t.Fatalf("rogue journal write reached the records tree: %v", err)
	}
}

func TestRecoverRequiresTheJournalPlanHash(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	runID := interruptAtRegistryWrite(t, fixture, plan)
	if _, err := Recover(fixture.config, runID, "resume", strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "does not match the reviewed plan hash") {
		t.Fatalf("Recover with a different plan hash = %v", err)
	}
}

func TestRecoverRefusesAChangedDeclaredSource(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	runID := interruptAtRegistryWrite(t, fixture, plan)
	writeFile(t, filepath.Join(fixture.repos["app"], "changed-after-journal"), "changed\n")
	runGit(t, fixture.repos["app"], "add", "changed-after-journal")
	runGit(t, fixture.repos["app"], "commit", "-m", "changed after migration journal")
	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "source HEAD changed") {
		t.Fatalf("Recover after source change = %v", err)
	}
}

func TestRecoverRefusesAlteredDynamicJournalOutputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, journal *privateJournal)
	}{
		{
			name: "receipt",
			mutate: func(t *testing.T, journal *privateJournal) {
				t.Helper()
				for i := range journal.Writes {
					if journal.Writes[i].Kind != journalKindReceipt {
						continue
					}
					receipt, err := migrationrecord.ParseReceipt(journal.Writes[i].Postimage)
					if err != nil {
						t.Fatal(err)
					}
					receipt.PlanHash = strings.Repeat("0", 64)
					data, err := json.Marshal(receipt)
					if err != nil {
						t.Fatal(err)
					}
					journal.Writes[i].Postimage = append(data, '\n')
					return
				}
				t.Fatal("receipt write is absent")
			},
		},
		{
			name: "registry",
			mutate: func(t *testing.T, journal *privateJournal) {
				t.Helper()
				for i := range journal.Writes {
					if journal.Writes[i].Kind == journalKindRegistry {
						journal.Writes[i].Postimage = []byte("{}\n")
						return
					}
				}
				t.Fatal("registry write is absent")
			},
		},
		{
			name: "proof",
			mutate: func(t *testing.T, journal *privateJournal) {
				t.Helper()
				for i := range journal.Writes {
					if journal.Writes[i].Kind == journalKindProof {
						journal.Writes[i].Postimage = []byte("{}\n")
						return
					}
				}
				t.Fatal("proof write is absent")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			runID := interruptAtRegistryWrite(t, fixture, plan)
			journal, err := loadJournal(fixture.records, runID)
			if err != nil {
				t.Fatalf("load journal: %v", err)
			}
			tc.mutate(t, &journal)
			overwriteJournalForTest(t, fixture.records, runID, journal)
			if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil {
				t.Fatal("Recover accepted an altered dynamic journal output")
			}
		})
	}
}

func interruptAtRegistryWrite(t *testing.T, fixture *migrationFixture, plan Plan) string {
	t.Helper()
	previous := migrationAfterWrite
	migrationAfterWrite = func(path string) error {
		if path == filepath.Join(fixture.root, ".homonto", "worktrees.json") {
			return errors.New("interrupted registry write")
		}
		return nil
	}
	defer func() { migrationAfterWrite = previous }()
	if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "interrupted registry write") {
		t.Fatalf("Apply error = %v", err)
	}
	return interruptedRunID(t, fixture.records)
}

func overwriteJournalForTest(t *testing.T, records, runID string, journal privateJournal) {
	t.Helper()
	path, err := journalPath(records, runID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func interruptedRunID(t *testing.T, records string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(records, ".workflow", "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "migration-") {
			return entry.Name()
		}
	}
	t.Fatal("interrupted migration did not create a journal")
	return ""
}
