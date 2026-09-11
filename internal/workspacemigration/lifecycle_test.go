package workspacemigration

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
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

func TestRecoverRejectsRetiredReceiptDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*migrationrecord.Receipt)
	}{
		{
			name: "retiredID",
			mutate: func(receipt *migrationrecord.Receipt) {
				receipt.Retired[0].ID = "different-retired-id"
			},
		},
		{
			name: "path",
			mutate: func(receipt *migrationrecord.Receipt) {
				receipt.Retired[0].Path = "changes/different-retired"
			},
		},
		{
			name: "schema",
			mutate: func(receipt *migrationrecord.Receipt) {
				receipt.Retired[0].SchemaVersion++
			},
		},
		{
			name: "hash",
			mutate: func(receipt *migrationrecord.Receipt) {
				receipt.Retired[0].SHA256 = strings.Repeat("0", 64)
			},
		},
		{
			name: "empty",
			mutate: func(receipt *migrationrecord.Receipt) {
				receipt.Retired = []migrationrecord.RetiredRecord{}
			},
		},
		{
			name: "extra",
			mutate: func(receipt *migrationrecord.Receipt) {
				receipt.Retired = append(receipt.Retired, migrationrecord.RetiredRecord{
					Path: "changes/extra-retired", ID: "extra-retired-id", SchemaVersion: 1, SHA256: strings.Repeat("1", 64),
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMigrationFixture(t, true, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			runID := interruptAtReceiptWrite(t, fixture, plan)
			journal, err := loadJournal(fixture.records, runID)
			if err != nil {
				t.Fatal(err)
			}
			for i := range journal.Writes {
				if journal.Writes[i].Kind != journalKindReceipt {
					continue
				}
				receipt, err := migrationrecord.ParseReceipt(journal.Writes[i].Postimage)
				if err != nil {
					t.Fatal(err)
				}
				tc.mutate(&receipt)
				data, err := json.Marshal(receipt)
				if err != nil {
					t.Fatal(err)
				}
				journal.Writes[i].Postimage = append(data, '\n')
				break
			}
			overwriteJournalForTest(t, fixture.records, runID, journal)
			before := migrationSnapshot(t, fixture)
			retiredPath := filepath.Join(fixture.records, "changes", "active-retired", "onto-state.yaml")
			retiredBefore := readFile(t, retiredPath)
			previous := migrationAfterWrite
			writes := 0
			migrationAfterWrite = func(string) error {
				writes++
				return nil
			}
			t.Cleanup(func() { migrationAfterWrite = previous })
			if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "retired") {
				t.Fatalf("Recover receipt drift = %v", err)
			}
			if writes != 0 {
				t.Fatalf("receipt drift reached %d recovery writes", writes)
			}
			if got := readFile(t, retiredPath); got != retiredBefore {
				t.Fatalf("receipt drift changed retired state: %q", got)
			}
			if after := migrationSnapshot(t, fixture); !bytes.Equal(mustJSON(t, before), mustJSON(t, after)) {
				t.Fatalf("receipt drift refusal changed authoritative files\nbefore: %#v\nafter: %#v", before, after)
			}
		})
	}
}

func TestRecoverRejectsChangedRetiredPreimageBeforeWrites(t *testing.T) {
	fixture := newMigrationFixture(t, true, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	runID := interruptAtRegistryWrite(t, fixture, plan)
	retiredPath := filepath.Join(fixture.records, "changes", "active-retired", "onto-state.yaml")
	writeFile(t, retiredPath, "schema_version: 1\nchange: active\nid: retired-id\nphase: build\nabandoned: true\ntampered: true\n")
	previous := migrationAfterWrite
	writes := 0
	migrationAfterWrite = func(string) error {
		writes++
		return nil
	}
	defer func() { migrationAfterWrite = previous }()
	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "active-retired/onto-state.yaml") {
		t.Fatalf("Recover changed retired preimage = %v", err)
	}
	if writes != 0 {
		t.Fatalf("changed retired preimage reached %d recovery writes", writes)
	}
	if got := readFile(t, retiredPath); !strings.Contains(got, "tampered: true") {
		t.Fatalf("changed retired preimage was overwritten: %q", got)
	}
}

func TestMigrationRejectsSourceRefDrift(t *testing.T) {
	t.Run("tag create before apply", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		runGit(t, fixture.repos["app"], "tag", "migration-ref-drift")
		if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "stale plan hash") {
			t.Fatalf("Apply after tag create = %v", err)
		}
	})

	t.Run("tag update before apply", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		runGit(t, fixture.repos["app"], "tag", "migration-ref-drift", "HEAD")
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		target := gitTextTest(t, fixture.repos["app"], "commit-tree", "HEAD^{tree}", "-p", "HEAD", "-m", "ref-only target")
		runGit(t, fixture.repos["app"], "tag", "-f", "migration-ref-drift", target)
		if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "stale plan hash") {
			t.Fatalf("Apply after tag update = %v", err)
		}
	})

	t.Run("same commit symbolic head switch before apply", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		runGit(t, fixture.repos["app"], "branch", "same-commit", "HEAD")
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		runGit(t, fixture.repos["app"], "symbolic-ref", "HEAD", "refs/heads/same-commit")
		if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "stale plan hash") {
			t.Fatalf("Apply after symbolic HEAD switch = %v", err)
		}
	})

	t.Run("hook changes refs", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		hook := filepath.Join(fixture.records, ".git", "hooks", "pre-commit")
		writeFile(t, hook, "#!/bin/sh\ngit -C \""+fixture.repos["app"]+"\" tag -f migration-hook-ref-drift\n")
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "source refs changed") {
			t.Fatalf("Apply after hook ref change = %v", err)
		}
	})

	t.Run("hook changes same commit symbolic head", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		runGit(t, fixture.repos["app"], "branch", "same-commit", "HEAD")
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		hook := filepath.Join(fixture.records, ".git", "hooks", "pre-commit")
		writeFile(t, hook, "#!/bin/sh\ngit -C \""+fixture.repos["app"]+"\" symbolic-ref HEAD refs/heads/same-commit\n")
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "symbolic HEAD changed") {
			t.Fatalf("Apply after hook symbolic HEAD switch = %v", err)
		}
	})
}

func TestMigrationVerifyWithoutExternalManifest(t *testing.T) {
	t.Run("completed verification", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		result, err := Apply(fixture.config, fixture.manifest, plan.PlanHash)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(fixture.manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(fixture.config, result.RunID); err != nil {
			t.Fatalf("Verify without external manifest: %v", err)
		}
	})

	t.Run("recovery", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		runID := interruptAtRegistryWrite(t, fixture, plan)
		if err := os.Remove(fixture.manifest); err != nil {
			t.Fatal(err)
		}
		result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
		if err != nil || result.Status != "complete" {
			t.Fatalf("Recover without external manifest = %+v, %v", result, err)
		}
	})
}

func TestMigrationVerifyRejectsModifiedManifestBackup(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Apply(fixture.config, fixture.manifest, plan.PlanHash)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := loadJournal(fixture.records, result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range journal.Snapshots {
		if journal.Snapshots[i].Path == journal.Plan.Manifest.Path {
			journal.Snapshots[i].Data = append(journal.Snapshots[i].Data, []byte("tampered\n")...)
			break
		}
	}
	overwriteJournalForTest(t, fixture.records, result.RunID, journal)
	if err := os.Remove(fixture.manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(fixture.config, result.RunID); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Fatalf("Verify with tampered manifest backup = %v", err)
	}
}

func TestMigrationRobustness_StagedOnlyConflict(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	runID := interruptAtReceiptWrite(t, fixture, plan)
	foreign := filepath.Join(t.TempDir(), "staged-only-conflict")
	writeFile(t, foreign, "operator staged blob\n")
	blob := gitTextTest(t, fixture.records, "hash-object", "-w", foreign)
	state := "changes/active/onto-state.yaml"
	runGit(t, fixture.records, "update-index", "--add", "--cacheinfo", "100644,"+blob+","+state)
	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "records index") {
		t.Fatalf("Recover staged-only conflict = %v", err)
	}
	if entry := gitTextTest(t, fixture.records, "ls-files", "--stage", "--", state); !strings.Contains(entry, blob) {
		t.Fatalf("operator staged blob was overwritten: %q", entry)
	}
}

func TestMigrationRobustness_ParentSubstitution(t *testing.T) {
	if err := requireMigrationFilesystemSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	parent := filepath.Join(root, "records", "state")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "journal.json")
	replaced := parent + "-replaced"
	previous := migrationAfterParentPin
	migrationAfterParentPin = func(_, path string) error {
		if path != target {
			return nil
		}
		if err := os.Rename(parent, replaced); err != nil {
			return err
		}
		return os.Mkdir(parent, 0o755)
	}
	t.Cleanup(func() { migrationAfterParentPin = previous })

	err := writeMigrationRegular(root, target, []byte("journal\n"), 0o600)
	if err == nil || !strings.Contains(err.Error(), "parent directory changed") {
		t.Fatalf("write after parent substitution = %v", err)
	}
	for _, path := range []string{target, filepath.Join(replaced, "journal.json")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("parent substitution wrote %s: %v", path, err)
		}
	}
}

func TestMigrationRobustness_DurableDirectoriesAndUnlinks(t *testing.T) {
	if err := requireMigrationFilesystemSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	target := filepath.Join(root, "migration", "private", "journal.json")
	previous := migrationFS
	calls := []string{}
	migrationFS = defaultMigrationFSOps
	migrationFS.sync = func(kind, _ string, file *os.File) error {
		calls = append(calls, kind)
		return file.Sync()
	}
	t.Cleanup(func() { migrationFS = previous })
	saw := func(kind string) bool {
		for _, call := range calls {
			if call == kind {
				return true
			}
		}
		return false
	}

	if err := writeMigrationRegular(root, target, []byte("durable\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !saw("file") || !saw("directory") {
		t.Fatalf("durable write syncs = %v, want file and directory", calls)
	}
	calls = nil
	if err := removeMigrationOptionalRegular(root, target); err != nil {
		t.Fatal(err)
	}
	if !saw("directory") {
		t.Fatalf("durable unlink syncs = %v, want parent directory", calls)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("removed migration file still exists: %v", err)
	}
}

func TestMigrationRobustness_ProcessKillCrashHelper(t *testing.T) {
	config := os.Getenv("HOMONTO_MIGRATION_KILL_CONFIG")
	if config == "" {
		return
	}
	manifest := os.Getenv("HOMONTO_MIGRATION_KILL_MANIFEST")
	planHash := os.Getenv("HOMONTO_MIGRATION_KILL_PLAN_HASH")
	stage := os.Getenv("HOMONTO_MIGRATION_KILL_STAGE")
	if stage == "" {
		stage = "intent"
	}
	previous := migrationAfterPreparation
	previousWrite := migrationAfterWrite
	defer func() {
		migrationAfterPreparation = previous
		migrationAfterWrite = previousWrite
	}()
	switch stage {
	case "intent":
		migrationAfterPreparation = func(boundary string) error {
			if boundary != "intent" {
				return nil
			}
			if _, err := fmt.Fprintln(os.Stdout, "intent-durable"); err != nil {
				return err
			}
			select {}
		}
	case "registry":
		registry := filepath.Join(filepath.Dir(config), ".homonto", "worktrees.json")
		migrationAfterWrite = func(path string) error {
			if path != registry {
				return nil
			}
			if _, err := fmt.Fprintln(os.Stdout, "registry-durable"); err != nil {
				return err
			}
			select {}
		}
	default:
		t.Fatalf("unsupported crash-helper stage %q", stage)
	}
	if _, err := Apply(config, manifest, planHash); err != nil {
		t.Fatalf("crash helper Apply: %v", err)
	}
	t.Fatal("crash helper unexpectedly completed")
}

func killMigrationProcess(t *testing.T, fixture *migrationFixture, plan Plan, stage string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrationRobustness_ProcessKillCrashHelper$")
	cmd.Env = append(os.Environ(),
		"HOMONTO_MIGRATION_KILL_CONFIG="+fixture.config,
		"HOMONTO_MIGRATION_KILL_MANIFEST="+fixture.manifest,
		"HOMONTO_MIGRATION_KILL_PLAN_HASH="+plan.PlanHash,
		"HOMONTO_MIGRATION_KILL_STAGE="+stage,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	want := stage + "-durable\n"
	if err != nil || line != want {
		_ = cmd.Process.Kill()
		rest, _ := io.ReadAll(stdout)
		_ = cmd.Wait()
		t.Fatalf("crash helper readiness = %q, %v, stdout=%s, stderr=%s", line, err, string(rest), stderr.String())
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("crash helper exited successfully after SIGKILL")
	}
}

func TestMigrationRobustness_ProcessKillRecovery(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	killMigrationProcess(t, fixture, plan, "intent")

	runID := interruptedRunID(t, fixture.records)
	if _, err := workspace.Load(fixture.config); err == nil {
		t.Fatal("ordinary load accepted a workspace with a pending migration")
	}
	if err := migrationrecord.ValidateBarrier(fixture.records); err == nil || !strings.Contains(err.Error(), "workspace migration pending") {
		t.Fatalf("migration barrier after SIGKILL = %v", err)
	}
	result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || result.Status != "complete" {
		t.Fatalf("Recover after SIGKILL = %+v, %v", result, err)
	}
	if _, err := Verify(fixture.config, runID); err != nil {
		t.Fatalf("Verify after SIGKILL: %v", err)
	}
}

func TestMigrationRobustness_ProcessKillAfterRegistryWriteRecovery(t *testing.T) {
	for _, action := range []string{"resume", "restore"} {
		t.Run(action, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			killMigrationProcess(t, fixture, plan, "registry")

			registry := filepath.Join(fixture.root, ".homonto", "worktrees.json")
			if data, err := os.ReadFile(registry); err != nil || len(data) == 0 {
				t.Fatalf("registry was not durably written before SIGKILL: data=%q err=%v", data, err)
			}
			runID := interruptedRunID(t, fixture.records)
			result, err := Recover(fixture.config, runID, action, plan.PlanHash)
			if err != nil {
				t.Fatalf("fresh %s after registry SIGKILL: %v", action, err)
			}
			if action == "resume" {
				if result.Status != "complete" {
					t.Fatalf("resume result = %+v", result)
				}
				if _, err := Verify(fixture.config, runID); err != nil {
					t.Fatalf("Verify after registry SIGKILL resume: %v", err)
				}
				return
			}
			if result.Status != "restored" {
				t.Fatalf("restore result = %+v", result)
			}
			for _, path := range []string{registry, filepath.Join(fixture.root, ".homonto", "workflow-layout.json")} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("restore retained generated control file %s: %v", path, err)
				}
			}
		})
	}
}

func TestMigrationRobustness_BackupPreparationFailure(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationAfterPreparation
	migrationAfterPreparation = func(stage string) error {
		if stage == "backup" {
			return errors.New("interrupted after durable backup")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterPreparation = previous
	if err == nil || !strings.Contains(err.Error(), "interrupted after durable backup") {
		t.Fatalf("Apply after backup interruption = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	backupPath, err := recordsGitBackupPath(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(backupPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("durable backup = %v, %v", info, err)
	}
	if _, err := loadJournal(fixture.records, runID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("journal exists after backup preparation interruption: %v", err)
	}
	intent, err := loadPreparationIntent(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := workspace.LoadMigrationRecovery(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	replanned, err := buildLockedIgnoring(layout.ConfigPath, intent.ManifestPath, migrationPreparationIgnored(layout, intent.RunID))
	if err != nil || replanned.Status != "ready" || replanned.PlanHash != plan.PlanHash {
		t.Fatalf("replan after backup interruption = status=%s hash=%s blockers=%+v error=%v", replanned.Status, replanned.PlanHash, replanned.Blockers, err)
	}
	result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || result.Status != "complete" {
		t.Fatalf("Recover after backup interruption = %+v, %v", result, err)
	}
	if _, err := Verify(fixture.config, runID); err != nil {
		t.Fatalf("Verify after backup interruption: %v", err)
	}
}

func TestMigrationRobustness_InterruptedPreparationRecovery(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	const runID = "migration-12345678"
	private := filepath.Join(fixture.records, ".workflow", "migrations", runID, "private")
	if err := os.MkdirAll(private, 0o700); err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(private, ".homonto-migration-0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(temp, []byte("partial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrationrecord.ValidateBarrier(fixture.records); err == nil || !strings.Contains(err.Error(), "workspace migration pending (preparing)") {
		t.Fatalf("migration barrier before interrupted preparation recovery = %v", err)
	}
	result, err := Recover(fixture.config, runID, "restore", plan.PlanHash)
	if err != nil || result.Status != "preparation-cleared" {
		t.Fatalf("Recover interrupted preparation = %+v, %v", result, err)
	}
	if err := migrationrecord.ValidateBarrier(fixture.records); err != nil {
		t.Fatalf("migration barrier after interrupted preparation recovery: %v", err)
	}
	if _, err := os.Lstat(temp); !os.IsNotExist(err) {
		t.Fatalf("partial preparation temp remains after recovery: %v", err)
	}
	if rebuilt, err := Build(fixture.config, fixture.manifest); err != nil {
		t.Fatalf("Build after interrupted preparation recovery: %v; blockers=%+v", err, rebuilt.Blockers)
	}
}

func TestMigrationRobustness_PreparationTempRecovery(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationAfterPreparation
	migrationAfterPreparation = func(stage string) error {
		if stage == "backup" {
			return errors.New("interrupted after durable backup")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterPreparation = previous
	if err == nil || !strings.Contains(err.Error(), "interrupted after durable backup") {
		t.Fatalf("Apply after backup interruption = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	journalFile, err := journalPath(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(filepath.Dir(journalFile), ".homonto-migration-0123456789abcdef0123456789abcdef")
	if err := os.WriteFile(temp, []byte("partial private write\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || result.Status != "complete" {
		t.Fatalf("Recover after preparation temp = %+v, %v", result, err)
	}
	if _, err := os.Lstat(temp); !os.IsNotExist(err) {
		t.Fatalf("preparation temp remains after recovery: %v", err)
	}
	if _, err := Verify(fixture.config, runID); err != nil {
		t.Fatalf("Verify after preparation temp recovery: %v", err)
	}
}

func TestRecordsIndexMatchesWorktreeRejectsStagedModeConflict(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	state := "changes/active/onto-state.yaml"
	baseline, err := captureRecordsIndex(fixture.records, []string{state})
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline) != 1 || baseline[0].Mode != 0o100644 {
		t.Fatalf("baseline index = %+v", baseline)
	}
	if info, err := os.Lstat(filepath.Join(fixture.records, filepath.FromSlash(state))); err != nil || info.Mode()&0o111 != 0 {
		t.Fatalf("worktree mode before staged conflict: info=%v err=%v", info, err)
	}
	runGit(t, fixture.records, "update-index", "--chmod=+x", "--", state)
	actual, err := captureRecordsIndex(fixture.records, []string{state})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 1 || actual[0].Mode != 0o100755 {
		t.Fatalf("staged mode conflict = %+v", actual)
	}
	matches, err := recordsIndexMatchesWorktree(fixture.records, actual, baseline, []string{state})
	if err != nil {
		t.Fatal(err)
	}
	if matches {
		t.Fatal("staged executable mode matched a non-executable worktree file")
	}
}

func TestMigrationRobustness_RestoreHookAddsForeignPath(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationAfterMarker
	migrationAfterMarker = func() error { return errors.New("interrupt after marker") }
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterMarker = previous
	if err == nil || !strings.Contains(err.Error(), "interrupt after marker") {
		t.Fatalf("Apply = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	beforeHead := journal.ProofCommit
	hook := filepath.Join(fixture.records, ".git", "hooks", "pre-commit")
	writeFile(t, hook, "#!/bin/sh\ncd \"$(git rev-parse --show-toplevel)\" || exit 1\nprintf 'foreign restore hook\\n' > foreign-after-restore.md\ngit add -- foreign-after-restore.md\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(fixture.config, runID, "restore", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "restoration commit") {
		t.Fatalf("Recover restore with foreign hook path = %v", err)
	}
	if head := gitTextTest(t, fixture.records, "rev-parse", "HEAD"); head == beforeHead {
		t.Fatal("bad restoration commit was not retained for diagnosis")
	}
	if paths := gitTextTest(t, fixture.records, "show", "--format=", "--name-only", "HEAD"); !strings.Contains(paths, "foreign-after-restore.md") {
		t.Fatalf("foreign hook path was not committed: %q", paths)
	}
	journal, err = loadJournal(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Phase == "restored" {
		t.Fatalf("bad restoration commit reported restored: %+v", journal)
	}
}

func TestMigrationRobustness_ForgedCompleteBarrier(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationAfterMarker
	migrationAfterMarker = func() error { return errors.New("interrupt after marker") }
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterMarker = previous
	if err == nil || !strings.Contains(err.Error(), "interrupt after marker") {
		t.Fatalf("Apply = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Phase != "pending-finalization" {
		t.Fatalf("interrupted phase = %q", journal.Phase)
	}
	journal.Phase = "complete"
	overwriteJournalForTest(t, fixture.records, runID, journal)
	if _, err := workspace.Load(fixture.config); err == nil || !strings.Contains(err.Error(), "authorization") {
		t.Fatalf("ordinary load accepted forged completion phase: %v", err)
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

func interruptAtReceiptWrite(t *testing.T, fixture *migrationFixture, plan Plan) string {
	t.Helper()
	previous := migrationAfterWrite
	migrationAfterWrite = func(path string) error {
		rel, err := filepath.Rel(fixture.records, path)
		if err == nil && strings.HasPrefix(filepath.ToSlash(rel), ".workflow/migrations/") && filepath.Base(rel) == "receipt.json" {
			return errors.New("interrupted receipt write")
		}
		return nil
	}
	defer func() { migrationAfterWrite = previous }()
	if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "interrupted receipt write") {
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
