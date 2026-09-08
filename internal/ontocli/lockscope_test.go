package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

func TestLegacyLockCommandsIgnoreUnselectedOfflineRepo(t *testing.T) {
	for _, version := range []int{0, 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			root := prepWorkspace(t)
			writeFile(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("schema_version=%d\n[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[repos]\noffline='missing-repo'\n", version))
			commitAll(t, root, "declare offline repository")
			base := seedBaseBranch(t, root)
			head, err := resolveCommit(root, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			seedCloseState(t, root, ontostate.State{
				Change: "local", Workflow: "tweak", Phase: "close", BaseRef: head, BaseBranch: base,
				Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed", Integration: "merge",
			})
			checkoutChangeBranch(t, root, "local")
			changeDir := filepath.Join(root, "docs", "changes", "local")
			writeFile(t, filepath.Join(changeDir, "specs", "local.md"), "## ADDED Requirements\n\n### Requirement: Local\n\nThe system SHALL work locally.\n")

			// Both actual command handlers must contend on config Git, not on
			// records paths or any declared source's Git directory.
			runLockedCommand := func(args ...string) {
				t.Helper()
				lockDir := filepath.Join(root, ".git", "homonto-onto-merge")
				lock, err := applylock.AcquireProcess(lockDir)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if lock != nil {
						_ = lock.Release()
					}
				}()
				args = append(args, "--dir", root)
				if _, err := runOnto(t, args...); err == nil || !strings.Contains(err.Error(), "spec merge lock") || !strings.Contains(err.Error(), filepath.Join(lockDir, "apply.lock")) {
					t.Fatalf("command %v did not contend on config Git: %v", args, err)
				}
				if err := lock.Release(); err != nil {
					t.Fatal(err)
				}
				lock = nil
				if out, err := runOnto(t, args...); err != nil {
					t.Fatalf("command %v rejected an unused offline declaration: %v\n%s", args, err, out)
				}
			}
			runLockedCommand("merge-deltas", "local")
			st, err := ontostate.LoadChange(changeDir)
			if err != nil || !st.Close.Merged {
				t.Fatalf("merge state: %+v %v", st, err)
			}
			if _, err := os.Stat(filepath.Join(root, "docs", "specs", "local.md")); err != nil {
				t.Fatal(err)
			}
			commitAll(t, root, "local change")
			if _, err := runOnto(t, "close", "local", "--dir", root); err != nil {
				t.Fatal(err)
			}
			commitAll(t, root, "archive local change")
			merge := mergeChangeBranch(t, root, base, "change/local")
			runLockedCommand("complete-integration", "local", "--receipt", "merge:"+merge)
			archive, st, err := locateArchive(root, "local")
			if err != nil || !ontostate.ArchiveIntegrationComplete(archive, st) {
				t.Fatalf("integration did not complete: %+v %v", st, err)
			}
		})
	}
}

func TestLegacyLockCommandsStillRejectSelectedOfflineRepo(t *testing.T) {
	for _, command := range []string{"merge-deltas", "complete-integration"} {
		t.Run(command, func(t *testing.T) {
			root := prepWorkspace(t)
			writeFile(t, filepath.Join(root, "homonto.toml"), "schema_version=1\n[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[repos]\noffline='missing-repo'\n")
			base := seedBaseBranch(t, root)
			st := ontostate.State{
				Change: "scoped", Workflow: "tweak", Phase: "close", Repos: []string{"offline"},
				Verify: ontostate.Verify{Result: "pass"}, CloseConfirmed: "reviewed", Integration: "pr", BaseBranch: base,
			}
			args := []string{command, "scoped", "--dir", root}
			changeDir := filepath.Join(root, "docs", "changes", "scoped")
			if command == "merge-deltas" {
				seedCloseState(t, root, st)
			} else {
				changeDir = filepath.Join(root, "docs", "changes", "archive", "2026-09-07-scoped")
				st.Archived, st.IntegrationRequired = true, true
				if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
					t.Fatal(err)
				}
				record := integrationrecord.NewPending("scoped", "pr", base, []integrationrecord.Entry{syntheticEntry(t, root, ""), syntheticEntry(t, root, "offline")})
				if err := integrationrecord.Save(changeDir, record); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--receipt", "pr:https://example.test/pull/1")
			}
			if _, err := runOnto(t, args...); err == nil || !strings.Contains(err.Error(), "offline") {
				t.Fatalf("selected missing repository accepted: %v", err)
			}
			if command == "merge-deltas" {
				if _, err := os.Stat(mergeReceiptPath(changeDir)); !os.IsNotExist(err) {
					t.Fatalf("rejected merge wrote receipt: %v", err)
				}
			} else {
				record, _, err := integrationrecord.Load(changeDir, "scoped")
				if err != nil || record.Status != integrationrecord.StatusPending || record.Repositories[0].Receipt != "" {
					t.Fatalf("rejected integration wrote receipt: %+v %v", record, err)
				}
			}
		})
	}
}
