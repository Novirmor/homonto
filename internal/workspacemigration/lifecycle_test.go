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
	"reflect"
	"strconv"
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

	t.Run("verify rejects post-migration source drift and unsupported records index flags", func(t *testing.T) {
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
		if _, err := Verify(fixture.config, result.RunID); err == nil || !strings.Contains(err.Error(), "records index flags are unsupported") {
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
	overwriteJournalPayloadForTest(t, fixture.records, runID, journal)

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
	before := migrationSnapshot(t, fixture)
	source := fixture.repos["app"]
	sourceHead := gitTextTest(t, source, "rev-parse", "HEAD^{commit}")
	sourceIndex := gitTextTest(t, source, "write-tree")
	sourceRefs, err := sourceReferenceSnapshot(source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(fixture.config, runID, "resume", strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "does not match the reviewed plan hash") {
		t.Fatalf("Recover with a different plan hash = %v", err)
	}
	if after := migrationSnapshot(t, fixture); !bytes.Equal(mustJSON(t, before), mustJSON(t, after)) {
		t.Fatalf("wrong plan hash changed authoritative files\nbefore: %#v\nafter: %#v", before, after)
	}
	if got := gitTextTest(t, source, "rev-parse", "HEAD^{commit}"); got != sourceHead {
		t.Fatalf("wrong plan hash changed source HEAD = %s, want %s", got, sourceHead)
	}
	if got := gitTextTest(t, source, "write-tree"); got != sourceIndex {
		t.Fatalf("wrong plan hash changed source index = %s, want %s", got, sourceIndex)
	}
	afterRefs, err := sourceReferenceSnapshot(source)
	if err != nil || !reflect.DeepEqual(afterRefs, sourceRefs) {
		t.Fatalf("wrong plan hash changed source refs = %+v, %v", afterRefs, err)
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
			overwriteJournalPayloadForTest(t, fixture.records, runID, journal)
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
			overwriteJournalPayloadForTest(t, fixture.records, runID, journal)
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
	overwriteJournalPayloadForTest(t, fixture.records, result.RunID, journal)
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
	previousFS := migrationFS
	previousTemporaryCreate := migrationAfterTemporaryCreate
	defer func() {
		migrationAfterPreparation = previous
		migrationAfterWrite = previousWrite
		migrationFS = previousFS
		migrationAfterTemporaryCreate = previousTemporaryCreate
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
	case "state":
		layout, err := workspace.LoadMigration(config)
		if err != nil {
			t.Fatal(err)
		}
		state := filepath.Join(layout.WorkflowRoot, "changes", "active", "onto-state.yaml")
		migrationFS = defaultMigrationFSOps
		migrationFS.sync = func(kind, path string, file *os.File) error {
			if err := file.Sync(); err != nil {
				return err
			}
			if kind != "file" || path != state {
				return nil
			}
			if _, err := fmt.Fprintln(os.Stdout, "state-durable"); err != nil {
				return err
			}
			select {}
		}
	case "state-create":
		layout, err := workspace.LoadMigration(config)
		if err != nil {
			t.Fatal(err)
		}
		state := filepath.Join(layout.WorkflowRoot, "changes", "active", "onto-state.yaml")
		migrationAfterTemporaryCreate = func(path string) error {
			if path != state {
				return nil
			}
			if _, err := fmt.Fprintln(os.Stdout, "state-create-opened"); err != nil {
				return err
			}
			select {}
		}
	case "intent-sync":
		migrationFS = defaultMigrationFSOps
		migrationFS.sync = func(kind, path string, file *os.File) error {
			if err := file.Sync(); err != nil {
				return err
			}
			if kind != "file" || filepath.Base(path) != "intent.json" {
				return nil
			}
			if _, err := fmt.Fprintln(os.Stdout, "intent-sync"); err != nil {
				return err
			}
			select {}
		}
	case "recovery-identity-sync", "recovery-descriptor-sync", "recovery-blob-sync":
		kind := strings.TrimSuffix(stage, "-sync")
		migrationFS = defaultMigrationFSOps
		migrationFS.sync = func(actualKind, path string, file *os.File) error {
			if err := file.Sync(); err != nil {
				return err
			}
			if actualKind != kind {
				return nil
			}
			if _, err := fmt.Fprintln(os.Stdout, stage+"-durable"); err != nil {
				return err
			}
			select {}
		}
	case "journal-pending-sync":
		migrationFS = defaultMigrationFSOps
		migrationFS.sync = func(kind, path string, file *os.File) error {
			if err := file.Sync(); err != nil {
				return err
			}
			if kind != "file" || filepath.Base(path) != "journal.json" {
				return nil
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if !migrationTemporaryFileName(entry.Name()) {
					continue
				}
				data, err := os.ReadFile(filepath.Join(filepath.Dir(path), entry.Name()))
				if err != nil {
					return err
				}
				var journal privateJournal
				if err := decodePrivateJSON(data, &journal); err != nil || journal.Phase != "pending-finalization" {
					continue
				}
				if _, err := fmt.Fprintln(os.Stdout, "journal-pending-sync"); err != nil {
					return err
				}
				select {}
			}
			return nil
		}
	default:
		kind, occurrence, ok := recoveryPayloadSyncStage(stage)
		if !ok {
			t.Fatalf("unsupported crash-helper stage %q", stage)
		}
		migrationFS = defaultMigrationFSOps
		seen := 0
		migrationFS.sync = func(actualKind, path string, file *os.File) error {
			if err := file.Sync(); err != nil {
				return err
			}
			if actualKind != kind {
				return nil
			}
			seen++
			if seen != occurrence {
				return nil
			}
			if _, err := fmt.Fprintln(os.Stdout, stage+"-durable"); err != nil {
				return err
			}
			select {}
		}
	}
	if _, err := Apply(config, manifest, planHash); err != nil {
		t.Fatalf("crash helper Apply: %v", err)
	}
	t.Fatal("crash helper unexpectedly completed")
}

func recoveryPayloadSyncStage(stage string) (string, int, bool) {
	for _, kind := range []string{"recovery-descriptor", "recovery-blob"} {
		prefix := kind + "-"
		if !strings.HasPrefix(stage, prefix) || !strings.HasSuffix(stage, "-sync") {
			continue
		}
		occurrence, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(stage, prefix), "-sync"))
		if err == nil && occurrence > 0 {
			return kind, occurrence, true
		}
	}
	return "", 0, false
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
	if stage == "state-create" || stage == "intent-sync" || stage == "journal-pending-sync" {
		want = stage + "\n"
		if stage == "state-create" {
			want = "state-create-opened\n"
		}
	}
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

func privateRecoveryTemporaryPath(t *testing.T, intent preparationIntent, kind, target string) string {
	t.Helper()
	identity, err := loadRecoveryIdentity(intent.Workflow, intent.RunID)
	if err != nil || !recoveryIdentityMatchesIntent(identity, intent) {
		t.Fatalf("load durable recovery identity: %v", err)
	}
	descriptor, data, exists, err := loadPrivateRecoveryPayload(identity, kind, target)
	if err != nil || !exists {
		t.Fatalf("load durable recovery payload %s: %v", kind, err)
	}
	path, err := recoveryPayloadTemporaryPath(identity, descriptor, data)
	if err != nil {
		t.Fatal(err)
	}
	return path
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

func TestMigrationRecovery_KillDuringIntentTemporarySync(t *testing.T) {
	for _, action := range []string{"resume", "restore"} {
		t.Run(action, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}

			killMigrationProcess(t, fixture, plan, "intent-sync")
			runID := interruptedRunID(t, fixture.records)
			intent, err := preparationIntentFromStatus(fixture.records, runID)
			if err != nil {
				t.Fatal(err)
			}
			intentPath, err := preparationIntentPath(fixture.records, runID)
			if err != nil {
				t.Fatal(err)
			}
			temporary := privateRecoveryTemporaryPath(t, intent, journalKindRecoveryIntent, intentPath)
			want, err := preparationIntentData(intent)
			if err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(temporary); err != nil || !bytes.Equal(data, want) {
				t.Fatalf("synced intent temporary = %q, %v", data, err)
			}
			if info, err := os.Lstat(temporary); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("synced intent temporary mode = %v, %v", info, err)
			}

			result, err := Recover(fixture.config, runID, action, plan.PlanHash)
			if err != nil {
				t.Fatalf("Recover after intent temporary SIGKILL: %v", err)
			}
			if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("intent temporary remains after recovery: %v", err)
			}
			if action == "resume" {
				if result.Status != "complete" {
					t.Fatalf("resume result = %+v", result)
				}
				if _, err := Verify(fixture.config, runID); err != nil {
					t.Fatalf("Verify after intent temporary SIGKILL resume: %v", err)
				}
				return
			}
			if result.Status != "preparation-cleared" {
				t.Fatalf("restore result = %+v", result)
			}
			if err := migrationrecord.ValidateBarrier(fixture.records); err != nil {
				t.Fatalf("migration barrier after intent temporary restore: %v", err)
			}
		})
	}
}

func TestMigrationRecovery_DurablePayloadBootstrapProcessKills(t *testing.T) {
	for _, stage := range []string{"recovery-identity-sync", "recovery-descriptor-sync", "recovery-blob-sync"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			killMigrationProcess(t, fixture, plan, stage)
			runID := interruptedRunID(t, fixture.records)
			result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
			if err != nil || result.Status != "complete" {
				t.Fatalf("Recover after %s SIGKILL = %+v, %v", stage, result, err)
			}
			if _, err := Verify(fixture.config, runID); err != nil {
				t.Fatalf("Verify after %s SIGKILL: %v", stage, err)
			}
		})
	}
}

func TestMigrationRecovery_DurablePayloadStagesProcessKills(t *testing.T) {
	stages := []string{
		"recovery-descriptor-2-sync", "recovery-blob-2-sync",
		"recovery-descriptor-3-sync", "recovery-blob-3-sync",
		"recovery-descriptor-4-sync", "recovery-blob-4-sync",
		"recovery-descriptor-5-sync", "recovery-blob-5-sync",
		"recovery-descriptor-6-sync", "recovery-blob-6-sync",
	}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			killMigrationProcess(t, fixture, plan, stage)
			runID := interruptedRunID(t, fixture.records)
			result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
			if err != nil || result.Status != "complete" {
				t.Fatalf("Recover after %s SIGKILL = %+v, %v", stage, result, err)
			}
			if _, err := Verify(fixture.config, runID); err != nil {
				t.Fatalf("Verify after %s SIGKILL: %v", stage, err)
			}
		})
	}
}

func TestMigrationRecovery_DurablePayloadPromotedJournalAndCompletionProcessKills(t *testing.T) {
	for _, stage := range []string{
		// The promoted applying-journal descriptor retains the initial journal as
		// history until its target replacement is complete.
		"recovery-descriptor-13-sync",
		// The separate completion witness is also private recovery payload.
		"recovery-blob-13-sync",
	} {
		t.Run(stage, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			killMigrationProcess(t, fixture, plan, stage)
			runID := interruptedRunID(t, fixture.records)
			result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
			if err != nil || result.Status != "complete" {
				t.Fatalf("Recover after %s SIGKILL = %+v, %v", stage, result, err)
			}
			if _, err := Verify(fixture.config, runID); err != nil {
				t.Fatalf("Verify after %s SIGKILL: %v", stage, err)
			}
		})
	}
}

func TestMigrationRecovery_RefusesAlteredIntentTemporary(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	killMigrationProcess(t, fixture, plan, "intent-sync")
	runID := interruptedRunID(t, fixture.records)
	intent, err := preparationIntentFromStatus(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	intentPath, err := preparationIntentPath(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	temporary := privateRecoveryTemporaryPath(t, intent, journalKindRecoveryIntent, intentPath)
	altered := []byte("altered intent temporary\n")
	if err := os.WriteFile(temporary, altered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "foreign or altered temporary file") {
		t.Fatalf("Recover with altered intent temporary = %v", err)
	}
	if data, err := os.ReadFile(temporary); err != nil || !bytes.Equal(data, altered) {
		t.Fatalf("altered intent temporary changed: %q, %v", data, err)
	}
}

func TestMigrationRecovery_KillDuringPendingJournalTemporarySync(t *testing.T) {
	for _, action := range []string{"resume", "restore"} {
		t.Run(action, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(fixture.records, "changes", "active", "onto-state.yaml")
			stateBefore := readFile(t, state)

			killMigrationProcess(t, fixture, plan, "journal-pending-sync")
			runID := interruptedRunID(t, fixture.records)
			journal, err := loadJournal(fixture.records, runID)
			if err != nil {
				t.Fatal(err)
			}
			if journal.Phase != "applying" || journal.MigrationCommit == "" || journal.ProofCommit == "" {
				t.Fatalf("durable journal before pending replacement = %+v", journal)
			}
			intent, err := journalPreparationIntent(journal)
			if err != nil {
				t.Fatal(err)
			}
			journalPath, err := journalPath(fixture.records, runID)
			if err != nil {
				t.Fatal(err)
			}
			temporary := privateRecoveryTemporaryPath(t, intent, journalKindRecoveryJournal, journalPath)
			data, mode, exists, err := readMigrationOptionalRegular(fixture.records, temporary)
			if err != nil || !exists || mode.Perm() != 0o600 {
				t.Fatalf("synced journal temporary = exists:%v mode:%v err:%v", exists, mode, err)
			}
			candidate, err := parsePrivateJournal(fixture.records, runID, journalPath, data, mode)
			if err != nil || candidate.Phase != "pending-finalization" {
				t.Fatalf("pending journal temporary = %+v, %v", candidate, err)
			}

			result, err := Recover(fixture.config, runID, action, plan.PlanHash)
			if err != nil {
				t.Fatalf("Recover after pending journal temporary SIGKILL: %v", err)
			}
			if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("journal temporary remains after recovery: %v", err)
			}
			if action == "resume" {
				if result.Status != "complete" {
					t.Fatalf("resume result = %+v", result)
				}
				if _, err := Verify(fixture.config, runID); err != nil {
					t.Fatalf("Verify after pending journal temporary SIGKILL resume: %v", err)
				}
				return
			}
			if result.Status != "restored" {
				t.Fatalf("restore result = %+v", result)
			}
			if got := readFile(t, state); got != stateBefore {
				t.Fatalf("restored state = %q, want %q", got, stateBefore)
			}
		})
	}
}

func TestMigrationRecovery_RefusesInvalidCompletePrivateJournalTemporary(t *testing.T) {
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
	if err != nil || journal.Phase != "pending-finalization" {
		t.Fatalf("pending journal = %+v, %v", journal, err)
	}
	intent, err := journalPreparationIntent(journal)
	if err != nil {
		t.Fatal(err)
	}
	journalPath, err := journalPath(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	temporary := privateRecoveryTemporaryPath(t, intent, journalKindRecoveryJournal, journalPath)
	journal.Phase = "complete"
	tampered, err := journalData(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "foreign or altered temporary file") {
		t.Fatalf("Recover with invalid complete journal temporary = %v", err)
	}
	if data, err := os.ReadFile(temporary); err != nil || !bytes.Equal(data, tampered) {
		t.Fatalf("invalid complete journal temporary changed: %q, %v", data, err)
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

func TestMigrationRecovery_KillDuringStateReplacement(t *testing.T) {
	for _, action := range []string{"resume", "restore"} {
		t.Run(action, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			unrelated := filepath.Join(fixture.records, "unrelated.md")
			writeFile(t, unrelated, "unrelated records file\n")
			runGit(t, fixture.records, "add", "unrelated.md")
			runGit(t, fixture.records, "commit", "-m", "retain unrelated record")
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(fixture.records, "changes", "active", "onto-state.yaml")
			stateBefore := readFile(t, state)

			killMigrationProcess(t, fixture, plan, "state")
			foundTemp := false
			entries, err := os.ReadDir(filepath.Dir(state))
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if migrationTemporaryFileName(entry.Name()) {
					foundTemp = true
					break
				}
			}
			if !foundTemp {
				t.Fatal("SIGKILL did not leave the synced state replacement temporary")
			}

			runID := interruptedRunID(t, fixture.records)
			result, err := Recover(fixture.config, runID, action, plan.PlanHash)
			if err != nil {
				t.Fatalf("Recover after state replacement SIGKILL: %v", err)
			}
			if action == "resume" {
				if result.Status != "complete" {
					t.Fatalf("resume result = %+v", result)
				}
				if _, err := Verify(fixture.config, runID); err != nil {
					t.Fatalf("Verify after state replacement resume: %v", err)
				}
			} else if result.Status != "restored" {
				t.Fatalf("restore result = %+v", result)
			} else if got := readFile(t, state); got != stateBefore {
				t.Fatalf("restored state = %q, want %q", got, stateBefore)
			}
			if got := readFile(t, unrelated); got != "unrelated records file\n" {
				t.Fatalf("unrelated records file changed: %q", got)
			}
		})
	}
}

func TestMigrationRecovery_KillDuringStateTemporaryCreation(t *testing.T) {
	for _, action := range []string{"resume", "restore"} {
		t.Run(action, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(fixture.records, "changes", "active", "onto-state.yaml")
			stateBefore := readFile(t, state)

			killMigrationProcess(t, fixture, plan, "state-create")
			stateDir := filepath.Dir(state)
			entries, err := os.ReadDir(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			temporary := ""
			for _, entry := range entries {
				if migrationTemporaryFileName(entry.Name()) {
					temporary = filepath.Join(stateDir, entry.Name())
					break
				}
			}
			if temporary == "" {
				t.Fatal("SIGKILL did not leave the creation-stage state temporary")
			}
			if data, err := os.ReadFile(temporary); err != nil || len(data) != 0 {
				t.Fatalf("creation-stage temporary = %q, %v", data, err)
			}
			if info, err := os.Lstat(temporary); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("creation-stage temporary mode = %v, %v", info, err)
			}

			runID := interruptedRunID(t, fixture.records)
			result, err := Recover(fixture.config, runID, action, plan.PlanHash)
			if err != nil {
				t.Fatalf("Recover after creation-stage SIGKILL: %v", err)
			}
			if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("creation-stage temporary remains after recovery: %v", err)
			}
			if action == "resume" {
				if result.Status != "complete" {
					t.Fatalf("resume result = %+v", result)
				}
				if _, err := Verify(fixture.config, runID); err != nil {
					t.Fatalf("Verify after creation-stage SIGKILL resume: %v", err)
				}
				return
			}
			if result.Status != "restored" {
				t.Fatalf("restore result = %+v", result)
			}
			if got := readFile(t, state); got != stateBefore {
				t.Fatalf("restored state = %q, want %q", got, stateBefore)
			}
		})
	}
}

func TestMigrationTempAuthorityPrivateSlotRequiresExpectedPayload(t *testing.T) {
	authority := migrationTempAuthority{privateSlot: true, mode: 0o600}
	if authority.matchesPartial(nil, 0o600) || authority.matchesPartial([]byte("arbitrary private bytes"), 0o600) {
		t.Fatal("private slot without an expected payload accepted arbitrary temporary data")
	}
	authority.data = []byte("expected private payload")
	if !authority.matchesPartial(nil, 0o600) || !authority.matchesPartial([]byte("expected private"), 0o600) {
		t.Fatal("private slot rejected the empty creation image or expected payload prefix")
	}
	if authority.matchesPartial([]byte("altered private payload"), 0o600) || authority.matchesPartial([]byte("expected private"), 0o640) {
		t.Fatal("private slot accepted altered temporary data or mode")
	}
}

func TestMigrationRecovery_StateTemporaryArtifactsAreAuthenticated(t *testing.T) {
	for _, scenario := range []struct {
		name         string
		mutate       func(t *testing.T, stateDir, owned string)
		wantErr      string
		wantContents string
		wantMode     os.FileMode
	}{
		{
			name: "foreign similar name is preserved and blocks recovery",
			mutate: func(t *testing.T, stateDir, _ string) {
				t.Helper()
				writeFile(t, filepath.Join(stateDir, ".homonto-migration-ffffffffffffffffffffffffffffffff"), "foreign temporary\n")
			},
			wantErr: "unrelated changed path",
		},
		{
			name: "altered owned temporary is preserved and blocks recovery",
			mutate: func(t *testing.T, _ string, owned string) {
				t.Helper()
				if err := os.WriteFile(owned, []byte("altered temporary\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(owned, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantErr:      "foreign or altered temporary file",
			wantContents: "altered temporary\n",
			wantMode:     0o600,
		},
		{
			name: "empty creation-stage temporary is retired before resume",
			mutate: func(t *testing.T, _ string, owned string) {
				t.Helper()
				if err := os.Truncate(owned, 0); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(owned, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "empty temporary with an altered mode is preserved and blocks recovery",
			mutate: func(t *testing.T, _ string, owned string) {
				t.Helper()
				if err := os.Truncate(owned, 0); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(owned, 0o640); err != nil {
					t.Fatal(err)
				}
			},
			wantErr:  "foreign or altered temporary file",
			wantMode: 0o640,
		},
		{
			name: "owned partial temporary is retired before resume",
			mutate: func(t *testing.T, _ string, owned string) {
				t.Helper()
				data, err := os.ReadFile(owned)
				if err != nil {
					t.Fatal(err)
				}
				if len(data) < 2 {
					t.Fatalf("temporary data too short: %q", data)
				}
				if err := os.WriteFile(owned, data[:len(data)/2], 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			stateDir := filepath.Join(fixture.records, "changes", "active")
			killMigrationProcess(t, fixture, plan, "state")
			entries, err := os.ReadDir(stateDir)
			if err != nil {
				t.Fatal(err)
			}
			owned := ""
			for _, entry := range entries {
				if migrationTemporaryFileName(entry.Name()) {
					owned = filepath.Join(stateDir, entry.Name())
					break
				}
			}
			if owned == "" {
				t.Fatal("SIGKILL did not leave the owned state temporary")
			}
			scenario.mutate(t, stateDir, owned)

			runID := interruptedRunID(t, fixture.records)
			result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
			if scenario.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), scenario.wantErr) {
					t.Fatalf("Recover error = %v, want %q", err, scenario.wantErr)
				}
				if scenario.wantContents != "" {
					if got := readFile(t, owned); got != scenario.wantContents {
						t.Fatalf("altered owned temporary was changed: %q", got)
					}
				}
				if scenario.wantMode != 0 {
					if info, err := os.Lstat(owned); err != nil || info.Mode().Perm() != scenario.wantMode {
						t.Fatalf("altered owned temporary mode = %v, %v", info, err)
					}
				} else if _, err := os.Lstat(filepath.Join(stateDir, ".homonto-migration-ffffffffffffffffffffffffffffffff")); err != nil {
					t.Fatalf("foreign similar temporary was removed: %v", err)
				}
				return
			}
			if err != nil || result.Status != "complete" {
				t.Fatalf("Recover partial temporary = %+v, %v", result, err)
			}
			if _, err := os.Lstat(owned); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("owned partial temporary remains: %v", err)
			}
			if _, err := Verify(fixture.config, runID); err != nil {
				t.Fatalf("Verify after partial temporary resume: %v", err)
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

func TestMigrationRecovery_PreparationRestoreInterrupted(t *testing.T) {
	prepare := func(t *testing.T) (*migrationFixture, Plan, string, string, string) {
		t.Helper()
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
		intentPath, err := preparationIntentPath(fixture.records, runID)
		if err != nil {
			t.Fatal(err)
		}
		bundlePath, err := recordsGitBackupPath(fixture.records, runID)
		if err != nil {
			t.Fatal(err)
		}
		return fixture, plan, runID, intentPath, bundlePath
	}

	t.Run("bundle removal keeps intent recoverable", func(t *testing.T) {
		fixture, plan, runID, intentPath, bundlePath := prepare(t)
		intentBefore, err := os.ReadFile(intentPath)
		if err != nil {
			t.Fatal(err)
		}
		bundleBefore, err := os.ReadFile(bundlePath)
		if err != nil {
			t.Fatal(err)
		}
		statePath := filepath.Join(fixture.records, "changes", "active", "onto-state.yaml")
		stateBefore := readFile(t, statePath)
		sourceHead := gitTextTest(t, fixture.repos["app"], "rev-parse", "HEAD")
		sourceIndex := gitTextTest(t, fixture.repos["app"], "write-tree")

		previous := migrationFS
		migrationFS = defaultMigrationFSOps
		failed := false
		migrationFS.remove = func(parent *os.Root, name string) error {
			if !failed && name == "records.git.bundle" {
				failed = true
				return errors.New("interrupted bundle removal")
			}
			return defaultMigrationFSOps.remove(parent, name)
		}
		t.Cleanup(func() { migrationFS = previous })

		if _, err := Recover(fixture.config, runID, "restore", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "interrupted bundle removal") {
			t.Fatalf("first recovery restore = %v", err)
		}
		if !failed {
			t.Fatal("bundle-removal seam did not run")
		}
		if actual, err := os.ReadFile(intentPath); err != nil || !bytes.Equal(actual, intentBefore) {
			t.Fatalf("intent changed after failed bundle removal: %v", err)
		}
		if actual, err := os.ReadFile(bundlePath); err != nil || !bytes.Equal(actual, bundleBefore) {
			t.Fatalf("bundle changed after failed removal: %v", err)
		}
		if got := readFile(t, statePath); got != stateBefore {
			t.Fatalf("state changed during preparation restore: %q", got)
		}
		if got := gitTextTest(t, fixture.repos["app"], "rev-parse", "HEAD"); got != sourceHead {
			t.Fatalf("source HEAD changed during preparation restore: %s", got)
		}
		if got := gitTextTest(t, fixture.repos["app"], "write-tree"); got != sourceIndex {
			t.Fatalf("source index changed during preparation restore: %s", got)
		}

		result, err := Recover(fixture.config, runID, "restore", plan.PlanHash)
		if err != nil || result.Status != "preparation-cleared" {
			t.Fatalf("retry recovery restore = %+v, %v", result, err)
		}
		for _, path := range []string{intentPath, bundlePath} {
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("preparation artifact remains at %s: %v", path, err)
			}
		}
		retired, err := migrationrecord.IsRetiredPreparation(fixture.records, runID)
		if err != nil || !retired {
			t.Fatalf("IsRetiredPreparation after restore = %t, %v", retired, err)
		}
		if err := migrationrecord.ValidateBarrier(fixture.records); err != nil {
			t.Fatalf("migration barrier after retired preparation = %v", err)
		}
		result, err = Recover(fixture.config, runID, "restore", plan.PlanHash)
		if err != nil || result.Status != "preparation-cleared" {
			t.Fatalf("idempotent preparation restore = %+v, %v", result, err)
		}
	})

	t.Run("bundle removed before intent remains recoverable", func(t *testing.T) {
		fixture, plan, runID, intentPath, bundlePath := prepare(t)
		previous := migrationFS
		migrationFS = defaultMigrationFSOps
		failed := false
		migrationFS.remove = func(parent *os.Root, name string) error {
			if !failed && name == "intent.json" {
				failed = true
				return errors.New("interrupted intent removal")
			}
			return defaultMigrationFSOps.remove(parent, name)
		}
		t.Cleanup(func() { migrationFS = previous })

		if _, err := Recover(fixture.config, runID, "restore", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "interrupted intent removal") {
			t.Fatalf("first recovery restore = %v", err)
		}
		if !failed {
			t.Fatal("intent-removal seam did not run")
		}
		if _, err := os.Lstat(bundlePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("bundle remained after its successful removal: %v", err)
		}
		if _, err := loadPreparationIntent(fixture.records, runID); err != nil {
			t.Fatalf("intent was not retained after bundle removal: %v", err)
		}
		result, err := Recover(fixture.config, runID, "restore", plan.PlanHash)
		if err != nil || result.Status != "preparation-cleared" {
			t.Fatalf("retry recovery restore = %+v, %v", result, err)
		}
		if _, err := os.Lstat(intentPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("intent remains after retry: %v", err)
		}
		retired, err := migrationrecord.IsRetiredPreparation(fixture.records, runID)
		if err != nil || !retired {
			t.Fatalf("IsRetiredPreparation after retry = %t, %v", retired, err)
		}
		if err := migrationrecord.ValidateBarrier(fixture.records); err != nil {
			t.Fatalf("migration barrier after retired preparation retry = %v", err)
		}
	})

	t.Run("retired evidence tampering remains blocked", func(t *testing.T) {
		fixture, plan, runID, _, _ := prepare(t)
		result, err := Recover(fixture.config, runID, "restore", plan.PlanHash)
		if err != nil || result.Status != "preparation-cleared" {
			t.Fatalf("preparation restore = %+v, %v", result, err)
		}
		identity, err := loadRecoveryIdentity(fixture.records, runID)
		if err != nil {
			t.Fatal(err)
		}
		descriptor, err := recoveryPayloadDescriptorPath(identity, journalKindRecoveryStatus)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(descriptor); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("tampered-descriptor", descriptor); err != nil {
			t.Fatal(err)
		}
		if err := migrationrecord.ValidateBarrier(fixture.records); err == nil {
			t.Fatal("migration barrier accepted tampered retired recovery evidence")
		}
		if _, err := Recover(fixture.config, runID, "restore", plan.PlanHash); err == nil {
			t.Fatal("Recover accepted tampered retired recovery evidence")
		}
	})
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
	if _, err := os.Lstat(private); !os.IsNotExist(err) {
		t.Fatalf("orphan preparation scaffolding remains after recovery: %v", err)
	}
	if rebuilt, err := Build(fixture.config, fixture.manifest); err != nil {
		t.Fatalf("Build after interrupted preparation recovery: %v; blockers=%+v", err, rebuilt.Blockers)
	}
}

func TestMigrationRecovery_BootstrapPreparationStatusTemporaryArtifacts(t *testing.T) {
	prepare := func(t *testing.T) (*migrationFixture, Plan, string, preparationIntent, string, []byte) {
		t.Helper()
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		layout, err := workspace.LoadMigration(fixture.config)
		if err != nil {
			t.Fatal(err)
		}
		const runID = "migration-12345678"
		intent := preparationIntent{
			Version:      migrationPreparationIntentVersion,
			RunID:        runID,
			Phase:        "preparing",
			PlanHash:     plan.PlanHash,
			ConfigPath:   layout.ConfigPath,
			ConfigRoot:   layout.ConfigRoot,
			Workflow:     layout.WorkflowRoot,
			ManifestPath: plan.Manifest.Path,
			Owner:        strings.Repeat("a", 32),
			Guardian:     migrationGuardianIdentity,
		}
		if err := intent.validate(); err != nil {
			t.Fatal(err)
		}
		statusPath, err := ensurePrivateJournalDirectory(fixture.records, runID)
		if err != nil {
			t.Fatal(err)
		}
		data, err := journalStatusData(intent)
		if err != nil {
			t.Fatal(err)
		}
		authority, err := preparationStatusTempAuthority(intent, data)
		if err != nil {
			t.Fatal(err)
		}
		name, err := authority.name()
		if err != nil {
			t.Fatal(err)
		}
		return fixture, plan, runID, intent, filepath.Join(filepath.Dir(statusPath), name), data
	}

	t.Run("foreign exact name remains and blocks recovery", func(t *testing.T) {
		fixture, plan, runID, _, temporary, _ := prepare(t)
		foreign := []byte("foreign bootstrap temporary\n")
		if err := os.WriteFile(temporary, foreign, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "recovery conflict") {
			t.Fatalf("Recover with foreign bootstrap temporary = %v", err)
		}
		if data, err := os.ReadFile(temporary); err != nil || !bytes.Equal(data, foreign) {
			t.Fatalf("foreign bootstrap temporary changed: %q, %v", data, err)
		}
	})

	t.Run("partial exact name remains and blocks recovery", func(t *testing.T) {
		fixture, plan, runID, _, temporary, _ := prepare(t)
		if err := os.WriteFile(temporary, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Recover(fixture.config, runID, "restore", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "recovery conflict") {
			t.Fatalf("Recover with partial bootstrap temporary = %v", err)
		}
		if data, err := os.ReadFile(temporary); err != nil || len(data) != 0 {
			t.Fatalf("partial bootstrap temporary changed: %q, %v", data, err)
		}
	})

	t.Run("canonical status temporary is recovered", func(t *testing.T) {
		fixture, plan, runID, _, temporary, data := prepare(t)
		if err := os.WriteFile(temporary, data, 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := Recover(fixture.config, runID, "restore", plan.PlanHash)
		if err != nil || result.Status != "preparation-cleared" {
			t.Fatalf("Recover canonical bootstrap temporary = %+v, %v", result, err)
		}
		if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("canonical bootstrap temporary remains: %v", err)
		}
		if err := migrationrecord.ValidateBarrier(fixture.records); err != nil {
			t.Fatalf("migration barrier after bootstrap recovery: %v", err)
		}
	})
}

func TestMigrationRobustness_PreparationTempRecovery(t *testing.T) {
	prepare := func(t *testing.T) (*migrationFixture, Plan, string, preparationIntent, string) {
		t.Helper()
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
		intent, err := loadPreparationIntent(fixture.records, runID)
		if err != nil {
			t.Fatal(err)
		}
		bundlePath, err := recordsGitBackupPath(fixture.records, runID)
		if err != nil {
			t.Fatal(err)
		}
		return fixture, plan, runID, intent, bundlePath
	}

	t.Run("arbitrary private-slot data is preserved and blocks recovery", func(t *testing.T) {
		fixture, plan, runID, intent, bundlePath := prepare(t)
		temp := privateRecoveryTemporaryPath(t, intent, journalKindRecoveryBundle, bundlePath)
		if err := os.WriteFile(temp, []byte("partial private write\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
		if err == nil || result.Status != "" {
			t.Fatalf("Recover with arbitrary private-slot temp = %+v, %v", result, err)
		}
		if got := readFile(t, temp); got != "partial private write\n" {
			t.Fatalf("arbitrary private-slot temp was changed: %q", got)
		}
		if err := migrationrecord.ValidateBarrier(fixture.records); err == nil || !strings.Contains(err.Error(), "workspace migration pending (preparing)") {
			t.Fatalf("migration barrier after arbitrary private-slot temp = %v", err)
		}
	})

	t.Run("unrecognized temporary is preserved and blocks recovery", func(t *testing.T) {
		fixture, plan, runID, _, bundlePath := prepare(t)
		temp := filepath.Join(filepath.Dir(bundlePath), ".homonto-migration-0123456789abcdef0123456789abcdef")
		if err := os.WriteFile(temp, []byte("foreign private write\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if result, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || result.Status != "" {
			t.Fatalf("Recover with unrecognized preparation temp = %+v, %v", result, err)
		}
		if got := readFile(t, temp); got != "foreign private write\n" {
			t.Fatalf("unrecognized preparation temp was changed: %q", got)
		}
		if err := migrationrecord.ValidateBarrier(fixture.records); err == nil || !strings.Contains(err.Error(), "workspace migration pending (preparing)") {
			t.Fatalf("migration barrier after unrecognized preparation temp = %v", err)
		}
	})
}

func TestMigrationRecovery_RejectsWrongPlanHashBeforeTemporaryCleanup(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	stateDir := filepath.Join(fixture.records, "changes", "active")
	killMigrationProcess(t, fixture, plan, "state")
	temporary := ""
	entries, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if migrationTemporaryFileName(entry.Name()) {
			temporary = filepath.Join(stateDir, entry.Name())
			break
		}
	}
	if temporary == "" {
		t.Fatal("SIGKILL did not leave a synced state temporary")
	}
	before, err := os.ReadFile(temporary)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(temporary)
	if err != nil {
		t.Fatal(err)
	}
	wrongPlanHash := strings.Repeat("f", 64)
	if wrongPlanHash == plan.PlanHash {
		wrongPlanHash = strings.Repeat("e", 64)
	}
	runID := interruptedRunID(t, fixture.records)
	if _, err := Recover(fixture.config, runID, "resume", wrongPlanHash); err == nil || !strings.Contains(err.Error(), "reviewed plan hash") {
		t.Fatalf("Recover with wrong plan hash = %v", err)
	}
	if data, err := os.ReadFile(temporary); err != nil || !bytes.Equal(data, before) {
		t.Fatalf("synced temporary changed after rejected recovery: %q, %v", data, err)
	}
	if after, err := os.Lstat(temporary); err != nil || after.Mode().Perm() != info.Mode().Perm() {
		t.Fatalf("synced temporary mode after rejected recovery = %v, %v", after, err)
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

func TestMigrationJournalWriteRejectsInPlaceDriftAfterClassification(t *testing.T) {
	if err := requireMigrationFilesystemSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "control.json")
	writeFile(t, path, "before\n")
	previous := migrationAfterParentPin
	migrationAfterParentPin = func(_, pinnedPath string) error {
		if pinnedPath != path {
			return nil
		}
		return os.WriteFile(path, []byte("raced\n"), 0o644)
	}
	t.Cleanup(func() { migrationAfterParentPin = previous })

	journal, write := journalWriteWithTemporaryAuthority(root, path)
	if _, err := applyJournalWrite(journal, write, true); err == nil || !strings.Contains(err.Error(), "recovery conflict") {
		t.Fatalf("apply journal write after in-place drift = %v", err)
	}
	if got := readFile(t, path); got != "raced\n" {
		t.Fatalf("raced content was overwritten: %q", got)
	}
}

func TestMigrationCompletionPayloadRejectsTargetRace(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previousMarker := migrationAfterMarker
	migrationAfterMarker = func() error { return errors.New("interrupt after marker") }
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterMarker = previousMarker
	if err == nil || !strings.Contains(err.Error(), "interrupt after marker") {
		t.Fatalf("Apply = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	completion, err := migrationrecord.CompletionWitnessPath(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationAfterParentPin
	wrote := false
	migrationAfterParentPin = func(_, path string) error {
		if wrote || path != completion {
			return nil
		}
		wrote = true
		return os.WriteFile(completion, []byte("foreign completion\n"), 0o600)
	}
	t.Cleanup(func() { migrationAfterParentPin = previous })
	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "target appeared after classification") {
		t.Fatalf("Recover after completion target race = %v", err)
	}
	if !wrote {
		t.Fatal("completion target race seam did not run")
	}
	if got := readFile(t, completion); got != "foreign completion\n" {
		t.Fatalf("foreign completion was overwritten: %q", got)
	}
}

func journalWriteWithTemporaryAuthority(root, path string) (privateJournal, privateWrite) {
	write := privateWrite{
		Scope:      journalScopeConfig,
		Kind:       journalKindRegistry,
		Path:       path,
		PreExists:  true,
		PreMode:    0o644,
		Preimage:   []byte("before\n"),
		PostExists: true,
		PostMode:   0o644,
		Postimage:  []byte("after\n"),
	}
	return privateJournal{
		RunID:       "migration-12345678",
		ConfigRoot:  root,
		IntentOwner: strings.Repeat("a", 32),
		Plan: Plan{Prospective: ProspectiveManifest{
			Version: prospectiveManifestVersion,
			Operations: []ProspectiveOperation{{
				Scope:        write.Scope,
				Kind:         write.Kind,
				Path:         write.Path,
				PreExists:    write.PreExists,
				PreSHA256:    migrationDigest(write.Preimage),
				PreMode:      write.PreMode,
				PostExists:   write.PostExists,
				PostSHA256:   migrationDigest(write.Postimage),
				PostMode:     write.PostMode,
				TempParent:   filepath.Dir(write.Path),
				TempNameRule: dynamicTempNameRule,
			}},
		}},
	}, write
}

func TestMigrationRecoveryRerunBeforePostIndexSave(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationBeforePostIndexSave
	migrationBeforePostIndexSave = func(stage string) error {
		if stage == "migration" {
			return errors.New("interrupted before migration index journal save")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationBeforePostIndexSave = previous
	if err == nil || !strings.Contains(err.Error(), "before migration index journal save") {
		t.Fatalf("Apply = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil || journal.MigrationIndexPost != nil {
		t.Fatalf("journal before postindex save = %+v, error=%v", journal, err)
	}
	result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || result.Status != "complete" {
		t.Fatalf("Recover resume = %+v, %v", result, err)
	}
	if _, err := Verify(fixture.config, runID); err != nil {
		t.Fatalf("Verify after postindex rerun: %v", err)
	}
}

func TestMigrationRecoveryRerunAfterPostIndexSave(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationAfterPostIndexSave
	migrationAfterPostIndexSave = func(stage string) error {
		if stage == "migration" {
			return errors.New("interrupted after migration index journal save")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterPostIndexSave = previous
	if err == nil || !strings.Contains(err.Error(), "after migration index journal save") {
		t.Fatalf("Apply = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil || journal.MigrationIndexPost == nil {
		t.Fatalf("journal after postindex save = %+v, error=%v", journal, err)
	}
	result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || result.Status != "complete" {
		t.Fatalf("Recover resume = %+v, %v", result, err)
	}
	if _, err := Verify(fixture.config, runID); err != nil {
		t.Fatalf("Verify after postindex rerun: %v", err)
	}
}

func TestMigrationRestoreReinstatesLogicalIndexWithoutCommit(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationBeforePostIndexSave
	migrationBeforePostIndexSave = func(stage string) error {
		if stage == "migration" {
			return errors.New("interrupted before migration index journal save")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationBeforePostIndexSave = previous
	if err == nil || !strings.Contains(err.Error(), "before migration index journal save") {
		t.Fatalf("Apply = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil || journal.MigrationCommit != "" {
		t.Fatalf("uncommitted journal = %+v, error=%v", journal, err)
	}
	result, err := Recover(fixture.config, runID, "restore", plan.PlanHash)
	if err != nil || result.Status != "restored" {
		t.Fatalf("Recover restore = %+v, %v", result, err)
	}
	paths, err := plannedLogicalRecordsIndexPaths(plan)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := captureRecordsIndex(fixture.records, paths)
	if err != nil || !sameRecordsIndex(actual, plan.RecordsGit.LogicalIndex) {
		t.Fatalf("restored logical index = %+v, error=%v, want=%+v", actual, err, plan.RecordsGit.LogicalIndex)
	}
	if staged := gitTextTest(t, fixture.records, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("restore retained staged records paths: %q", staged)
	}
}

func TestMigrationRecoveryRejectsSwappedRecoveryPayloads(t *testing.T) {
	t.Run("journal", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		runID := interruptAtRegistryWrite(t, fixture, plan)
		original, err := journalPath(fixture.records, runID)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		other := "migration-0123456789abcdef0123456789abcdef"
		copyPath, err := ensurePrivateJournalDirectory(fixture.records, other)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(copyPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Recover(fixture.config, other, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "does not match its requested recovery directory") {
			t.Fatalf("Recover with swapped journal = %v", err)
		}
	})

	t.Run("intent", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		layout, err := workspace.LoadMigration(fixture.config)
		if err != nil {
			t.Fatal(err)
		}
		intent, err := newPreparationIntent(layout, plan)
		if err != nil {
			t.Fatal(err)
		}
		identity, err := newRecoveryIdentity(intent, plan)
		if err != nil {
			t.Fatal(err)
		}
		if err := publishRecoveryIdentity(identity); err != nil {
			t.Fatal(err)
		}
		if err := savePreparationIntent(intent); err != nil {
			t.Fatal(err)
		}
		original, err := preparationIntentPath(fixture.records, intent.RunID)
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		other := "migration-fedcba9876543210fedcba9876543210"
		if _, err := ensurePrivateJournalDirectory(fixture.records, other); err != nil {
			t.Fatal(err)
		}
		copyPath, err := preparationIntentPath(fixture.records, other)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(copyPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadPreparationIntent(fixture.records, other); err == nil || !strings.Contains(err.Error(), "does not match its requested recovery directory") {
			t.Fatalf("load swapped intent = %v", err)
		}
	})
}

func TestMigrationRecoveryRejectsTamperedDurablePayloadsBeforeWrites(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, recoveryIdentity, string)
	}{
		{
			name: "journal target",
			mutate: func(t *testing.T, _ recoveryIdentity, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("tampered journal\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "journal descriptor",
			mutate: func(t *testing.T, identity recoveryIdentity, _ string) {
				t.Helper()
				path, err := recoveryPayloadDescriptorPath(identity, journalKindRecoveryJournal)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("tampered-descriptor", path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "journal blob",
			mutate: func(t *testing.T, identity recoveryIdentity, path string) {
				t.Helper()
				descriptor, _, exists, err := loadPrivateRecoveryPayload(identity, journalKindRecoveryJournal, path)
				if err != nil || !exists {
					t.Fatalf("load journal descriptor = %+v, %t, %v", descriptor, exists, err)
				}
				blob, err := recoveryPayloadBlobPath(identity, descriptor.SHA256)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(blob, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(blob, []byte("tampered blob\n"), recoveryBlobMode); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(blob, recoveryBlobMode); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "extra blob",
			mutate: func(t *testing.T, identity recoveryIdentity, _ string) {
				t.Helper()
				blob, err := recoveryPayloadBlobPath(identity, strings.Repeat("0", 64))
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(blob, []byte("foreign blob\n"), recoveryBlobMode); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newMigrationFixture(t, false, false)
			plan, err := Build(fixture.config, fixture.manifest)
			if err != nil {
				t.Fatal(err)
			}
			runID := interruptAtRegistryWrite(t, fixture, plan)
			identity, err := loadRecoveryIdentity(fixture.records, runID)
			if err != nil {
				t.Fatal(err)
			}
			journalPath, err := journalPath(fixture.records, runID)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, identity, journalPath)
			before := migrationSnapshot(t, fixture)
			previous := migrationAfterWrite
			writes := 0
			migrationAfterWrite = func(string) error {
				writes++
				return nil
			}
			t.Cleanup(func() { migrationAfterWrite = previous })
			if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil {
				t.Fatal("Recover accepted a tampered durable payload")
			}
			if writes != 0 {
				t.Fatalf("tampered payload reached %d recovery writes", writes)
			}
			if after := migrationSnapshot(t, fixture); !bytes.Equal(mustJSON(t, before), mustJSON(t, after)) {
				t.Fatalf("tampered payload refusal changed authoritative files\nbefore: %#v\nafter: %#v", before, after)
			}
		})
	}
}

func TestMigrationFinalMarkerRejectsParentSubstitution(t *testing.T) {
	if err := requireMigrationFilesystemSupport(); err != nil {
		t.Skip(err)
	}
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(fixture.root, ".homonto", "workflow-layout.json")
	control := filepath.Dir(marker)
	replaced := control + "-replaced"
	previous := migrationAfterParentPin
	migrationAfterParentPin = func(_, pinnedPath string) error {
		if pinnedPath != marker {
			return nil
		}
		if err := os.Rename(control, replaced); err != nil {
			return err
		}
		return os.Mkdir(control, 0o755)
	}
	t.Cleanup(func() { migrationAfterParentPin = previous })
	if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "parent directory changed") {
		t.Fatalf("Apply after marker parent substitution = %v", err)
	}
	for _, path := range []string{marker, filepath.Join(replaced, "workflow-layout.json")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("marker was written through substituted parent at %s: %v", path, err)
		}
	}
}

func TestMigrationRecoveryRerunRejectsPreCommitHookSourceMutation(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	source := fixture.repos["app"]
	hook := filepath.Join(fixture.records, ".git", "hooks", "pre-commit")
	writeFile(t, hook, "#!/bin/sh\nprintf 'hook source mutation\\n' > "+filepath.Join(source, "tracked")+"\ngit -C "+source+" branch migration-hook-ref\nexit 1\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "records migration commit remains pending") {
		t.Fatalf("Apply with rejecting hook = %v", err)
	}
	runID := interruptedRunID(t, fixture.records)
	if _, err := Recover(fixture.config, runID, "resume", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "source refs changed") {
		t.Fatalf("Recover after hook source mutation = %v", err)
	}
	journal, err := loadJournal(fixture.records, runID)
	if err != nil || journal.Phase != "applying" || journal.MigrationCommit != "" {
		t.Fatalf("journal after rejected hook rerun = %+v, error=%v", journal, err)
	}
	if got := readFile(t, filepath.Join(source, "tracked")); got != "hook source mutation\n" {
		t.Fatalf("hook source worktree mutation missing: %q", got)
	}
}

func TestMigrationRecoveryRerun_IndexAuthorityTamper(t *testing.T) {
	for _, tc := range []struct {
		name      string
		interrupt func(*testing.T, *migrationFixture, Plan) string
	}{
		{"receipt", interruptAtReceiptWrite},
		{"registry", interruptAtRegistryWrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testMigrationRecoveryRerunIndexAuthorityTamper(t, tc.interrupt)
		})
	}
}

func testMigrationRecoveryRerunIndexAuthorityTamper(t *testing.T, interrupt func(*testing.T, *migrationFixture, Plan) string) {
	t.Helper()
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	runID := interrupt(t, fixture, plan)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := journalWrite(journal, journalKindState)
	if !ok {
		t.Fatal("journal state write is absent")
	}
	statePath, err := filepath.Rel(fixture.records, state.Path)
	if err != nil {
		t.Fatal(err)
	}
	statePath = filepath.ToSlash(statePath)
	if err := os.WriteFile(state.Path, []byte("operator third staged blob\n"), os.FileMode(state.PostMode)); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.records, "add", "--", statePath)
	operatorIndex, err := captureRecordsIndex(fixture.records, []string{statePath})
	if err != nil || len(operatorIndex) != 1 {
		t.Fatalf("capture operator index = %+v, %v", operatorIndex, err)
	}
	if err := os.WriteFile(state.Path, state.Preimage, os.FileMode(state.PreMode)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(state.Path, os.FileMode(state.PreMode)); err != nil {
		t.Fatal(err)
	}
	found := false
	for i := range journal.RecordsIndexPre {
		if journal.RecordsIndexPre[i].Path == statePath {
			journal.RecordsIndexPre[i].Object = operatorIndex[0].Object
			found = true
		}
	}
	if !found || journal.PlanHash != plan.PlanHash {
		t.Fatalf("tampered journal authority = %+v", journal.RecordsIndexPre)
	}
	backupBefore, err := os.ReadFile(journal.RecordsBackup.BundlePath)
	if err != nil {
		t.Fatal(err)
	}
	overwriteJournalPayloadForTest(t, fixture.records, runID, journal)
	journalFile, err := journalPath(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	journalBefore, err := os.ReadFile(journalFile)
	if err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(state.Path)
	if err != nil {
		t.Fatal(err)
	}
	headBefore := gitTextTest(t, fixture.records, "rev-parse", "HEAD^{commit}")

	for _, action := range []string{"resume", "restore"} {
		if _, err := Recover(fixture.config, runID, action, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "records preindex differs from the reviewed logical index") {
			t.Fatalf("Recover %s with tampered index authority = %v", action, err)
		}
		if actual, err := captureRecordsIndex(fixture.records, []string{statePath}); err != nil || !sameRecordsIndex(actual, operatorIndex) {
			t.Fatalf("operator index after rejected %s = %+v, %v", action, actual, err)
		}
		if actual, err := os.ReadFile(state.Path); err != nil || !bytes.Equal(actual, stateBefore) {
			t.Fatalf("state worktree after rejected %s = %q, %v", action, actual, err)
		}
		if actual, err := os.ReadFile(journalFile); err != nil || !bytes.Equal(actual, journalBefore) {
			t.Fatalf("journal after rejected %s changed: %v", action, err)
		}
		if actual, err := os.ReadFile(journal.RecordsBackup.BundlePath); err != nil || !bytes.Equal(actual, backupBefore) {
			t.Fatalf("records backup after rejected %s changed: %v", action, err)
		}
		if head := gitTextTest(t, fixture.records, "rev-parse", "HEAD^{commit}"); head != headBefore {
			t.Fatalf("records head after rejected %s = %q, want %q", action, head, headBefore)
		}
	}
}

func TestMigrationRecoveryRerun_PreCommitRestore(t *testing.T) {
	t.Run("after migration index save before migration tree", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		previous := migrationAfterPostIndexSave
		migrationAfterPostIndexSave = func(stage string) error {
			if stage == "migration" {
				return errors.New("interrupted after migration index journal save")
			}
			return nil
		}
		_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
		migrationAfterPostIndexSave = previous
		if err == nil || !strings.Contains(err.Error(), "after migration index journal save") {
			t.Fatalf("Apply = %v", err)
		}
		runID := interruptedRunID(t, fixture.records)
		journal, err := loadJournal(fixture.records, runID)
		if err != nil || journal.MigrationIndexPost == nil || journal.ExpectedMigrationTree != "" || journal.MigrationCommit != "" {
			t.Fatalf("pre-tree journal = %+v, error=%v", journal, err)
		}
		assertUncommittedMigrationRestored(t, fixture, plan, runID, journal.RecordsParent)
	})

	t.Run("rejecting first commit hook", func(t *testing.T) {
		fixture := newMigrationFixture(t, false, false)
		plan, err := Build(fixture.config, fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		hook := filepath.Join(fixture.records, ".git", "hooks", "pre-commit")
		writeFile(t, hook, "#!/bin/sh\nexit 1\n")
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
		if err == nil || !strings.Contains(err.Error(), "records migration commit remains pending") {
			t.Fatalf("Apply with rejecting hook = %v", err)
		}
		runID := interruptedRunID(t, fixture.records)
		journal, err := loadJournal(fixture.records, runID)
		if err != nil || journal.MigrationIndexPost == nil || journal.ExpectedMigrationTree == "" || journal.MigrationCommit != "" {
			t.Fatalf("hook-rejected journal = %+v, error=%v", journal, err)
		}
		hookSentinel := filepath.Join(t.TempDir(), "restore-hook-ran")
		writeFile(t, hook, fmt.Sprintf("#!/bin/sh\n: > %q\nexit 1\n", hookSentinel))
		if err := os.Chmod(hook, 0o755); err != nil {
			t.Fatal(err)
		}
		assertUncommittedMigrationRestored(t, fixture, plan, runID, journal.RecordsParent)
		if _, err := os.Lstat(hookSentinel); !os.IsNotExist(err) {
			t.Fatalf("restore invoked the rejecting commit hook: %v", err)
		}
	})
}

func TestMigrationRecoveryRerun_WitnessPublicationFailure(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationAfterParentPin
	fired := false
	migrationAfterParentPin = func(_, pinnedPath string) error {
		if !fired && filepath.Base(pinnedPath) == "completion.json" {
			fired = true
			return errors.New("interrupted completion witness publication")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterParentPin = previous
	if err == nil || !strings.Contains(err.Error(), "interrupted completion witness publication") || !fired {
		t.Fatalf("Apply during completion witness publication = %v, fired=%t", err, fired)
	}
	runID := interruptedRunID(t, fixture.records)
	journal, err := loadJournal(fixture.records, runID)
	if err != nil {
		t.Fatal(err)
	}
	completion, ok := journalWrite(journal, journalKindCompletion)
	if !ok || journal.Phase != "pending-finalization" || len(completion.Postimage) == 0 {
		t.Fatalf("journal before completion recovery = %+v", journal)
	}
	if _, err := os.Lstat(completion.Path); !os.IsNotExist(err) {
		t.Fatalf("completion witness exists after interrupted publication: %v", err)
	}
	if _, err := workspace.Load(fixture.config); err == nil {
		t.Fatal("ordinary workspace load crossed the incomplete completion barrier")
	}
	headBefore := gitTextTest(t, fixture.records, "rev-parse", "HEAD^{commit}")
	migrationCommit, proofCommit := journal.MigrationCommit, journal.ProofCommit
	result, err := Recover(fixture.config, runID, "resume", plan.PlanHash)
	if err != nil || result.Status != "complete" {
		t.Fatalf("Recover witness publication = %+v, %v", result, err)
	}
	completed, err := loadJournal(fixture.records, runID)
	if err != nil || completed.MigrationCommit != migrationCommit || completed.ProofCommit != proofCommit {
		t.Fatalf("completion recovery commits = %+v, error=%v", completed, err)
	}
	if head := gitTextTest(t, fixture.records, "rev-parse", "HEAD^{commit}"); head != headBefore {
		t.Fatalf("completion recovery created another records commit: %q != %q", head, headBefore)
	}
	if _, err := workspace.Load(fixture.config); err != nil {
		t.Fatalf("ordinary workspace load after completion recovery: %v", err)
	}
	if _, err := Verify(fixture.config, runID); err != nil {
		t.Fatalf("Verify after completion recovery: %v", err)
	}
}

func TestMigrationRecoveryRerun_IntentRunIDMismatch(t *testing.T) {
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	previous := migrationAfterPreparation
	migrationAfterPreparation = func(stage string) error {
		if stage == "intent" {
			return errors.New("interrupted after durable intent")
		}
		return nil
	}
	_, err = Apply(fixture.config, fixture.manifest, plan.PlanHash)
	migrationAfterPreparation = previous
	if err == nil || !strings.Contains(err.Error(), "interrupted after durable intent") {
		t.Fatalf("Apply = %v", err)
	}
	runA := interruptedRunID(t, fixture.records)
	intent, err := loadPreparationIntent(fixture.records, runA)
	if err != nil {
		t.Fatal(err)
	}
	runB := "migration-0123456789abcdef0123456789abcdef"
	bJournal, err := ensurePrivateJournalDirectory(fixture.records, runB)
	if err != nil {
		t.Fatal(err)
	}
	bBackup, err := recordsGitBackupPath(fixture.records, runB)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bJournal, []byte("sentinel B journal\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bBackup, []byte("sentinel B backup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bJournalBefore, err := os.ReadFile(bJournal)
	if err != nil {
		t.Fatal(err)
	}
	bBackupBefore, err := os.ReadFile(bBackup)
	if err != nil {
		t.Fatal(err)
	}
	intent.RunID = runB
	if intent.PlanHash != plan.PlanHash {
		t.Fatalf("intent plan hash = %q, want %q", intent.PlanHash, plan.PlanHash)
	}
	intentData, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	intentPath, err := preparationIntentPath(fixture.records, runA)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(intentPath, append(intentData, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	aIntentBefore, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	aStatusPath, err := journalStatusPath(fixture.records, runA)
	if err != nil {
		t.Fatal(err)
	}
	aStatusBefore, err := os.ReadFile(aStatusPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Recover(fixture.config, runA, "restore", plan.PlanHash); err == nil || !strings.Contains(err.Error(), "preparation intent does not match its requested recovery directory") {
		t.Fatalf("Recover A restore with mismatched intent run ID = %v", err)
	}
	for _, check := range []struct {
		name string
		path string
		want []byte
	}{
		{"A intent", intentPath, aIntentBefore},
		{"A status", aStatusPath, aStatusBefore},
		{"B journal", bJournal, bJournalBefore},
		{"B backup", bBackup, bBackupBefore},
	} {
		actual, err := os.ReadFile(check.path)
		if err != nil || !bytes.Equal(actual, check.want) {
			t.Fatalf("%s changed after rejected recovery: %v", check.name, err)
		}
	}
}

func TestMigrationJournalRestoreWriteRejectsInPlaceDriftAfterClassification(t *testing.T) {
	if err := requireMigrationFilesystemSupport(); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "control.json")
	writeFile(t, path, "after\n")
	previous := migrationAfterParentPin
	migrationAfterParentPin = func(_, pinnedPath string) error {
		if pinnedPath != path {
			return nil
		}
		return os.WriteFile(path, []byte("raced restore\n"), 0o644)
	}
	t.Cleanup(func() { migrationAfterParentPin = previous })

	journal, write := journalWriteWithTemporaryAuthority(root, path)
	if _, err := applyJournalWrite(journal, write, false); err == nil || !strings.Contains(err.Error(), "recovery conflict") {
		t.Fatalf("restore journal write after in-place drift = %v", err)
	}
	if got := readFile(t, path); got != "raced restore\n" {
		t.Fatalf("raced restore content was overwritten: %q", got)
	}
}

func TestMigrationFinalMarkerRejectsOutsideSymlinkSubstitution(t *testing.T) {
	if err := requireMigrationFilesystemSupport(); err != nil {
		t.Skip(err)
	}
	fixture := newMigrationFixture(t, false, false)
	plan, err := Build(fixture.config, fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(fixture.root, ".homonto", "workflow-layout.json")
	control := filepath.Dir(marker)
	replaced := control + "-replaced"
	outside := filepath.Join(t.TempDir(), "outside-control")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "sentinel")
	writeFile(t, sentinel, "outside sentinel\n")
	previous := migrationAfterParentPin
	migrationAfterParentPin = func(_, pinnedPath string) error {
		if pinnedPath != marker {
			return nil
		}
		if err := os.Rename(control, replaced); err != nil {
			return err
		}
		return os.Symlink(outside, control)
	}
	t.Cleanup(func() { migrationAfterParentPin = previous })
	if _, err := Apply(fixture.config, fixture.manifest, plan.PlanHash); err == nil || !strings.Contains(err.Error(), "parent directory changed") && !strings.Contains(err.Error(), "parent component is not a real directory") {
		t.Fatalf("Apply after marker outside symlink substitution = %v", err)
	}
	if got := readFile(t, sentinel); got != "outside sentinel\n" {
		t.Fatalf("outside sentinel changed: %q", got)
	}
	for _, path := range []string{filepath.Join(outside, "workflow-layout.json"), filepath.Join(replaced, "workflow-layout.json")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("marker was written through substituted parent at %s: %v", path, err)
		}
	}
}

func assertUncommittedMigrationRestored(t *testing.T, fixture *migrationFixture, plan Plan, runID, parent string) {
	t.Helper()
	result, err := Recover(fixture.config, runID, "restore", plan.PlanHash)
	if err != nil || result.Status != "restored" {
		t.Fatalf("Recover restore = %+v, %v", result, err)
	}
	journal, err := loadJournal(fixture.records, runID)
	if err != nil || journal.MigrationCommit != "" || journal.ProofCommit != "" || journal.RestoreCommit != "" {
		t.Fatalf("restored uncommitted journal = %+v, error=%v", journal, err)
	}
	if head := gitTextTest(t, fixture.records, "rev-parse", "HEAD^{commit}"); head != parent {
		t.Fatalf("restore created a records commit: %q != %q", head, parent)
	}
	if err := verifyJournalWrites(journal, false); err != nil {
		t.Fatalf("restored worktree differs from journal preimages: %v", err)
	}
	paths, err := plannedLogicalRecordsIndexPaths(plan)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := captureRecordsIndex(fixture.records, paths)
	if err != nil || !sameRecordsIndex(actual, plan.RecordsGit.LogicalIndex) {
		t.Fatalf("restored logical index = %+v, error=%v, want=%+v", actual, err, plan.RecordsGit.LogicalIndex)
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

func overwriteJournalPayloadForTest(t *testing.T, records, runID string, journal privateJournal) {
	t.Helper()
	identity, err := loadRecoveryIdentity(records, runID)
	if err != nil {
		t.Fatal(err)
	}
	path, err := journalPath(records, runID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := journalData(journal)
	if err != nil {
		t.Fatal(err)
	}
	if err := writePrivateRecoveryPayload(identity, journalKindRecoveryJournal, path, data); err != nil {
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
