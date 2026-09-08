package ontocli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/evidence"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

func TestAuditNoSpecRepeatedVerificationAndUnchangedCompletion(t *testing.T) {
	l := explicitWorkspace(t)
	root, name := l.ConfigRoot, "retry"
	run := func(args ...string) string {
		t.Helper()
		out, err := runOnto(t, append(args, "--dir", root)...)
		if err != nil {
			t.Fatalf("onto %v: %v\n%s", args, err, out)
		}
		return out
	}
	run("new", name, "--workflow", "tweak", "--repo", "a,b")
	changeDir := filepath.Join(l.WorkflowRoot, "changes", name)
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "Fix a small source defect; b is inspected but unchanged.\n")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [ ] 1.1 Correct and verify [trace #1]\n\nScenario-ID: SC-retry\nThe corrected source must pass its regression check.\n")
	run("set", "isolation", name, "branch")
	run("advance", name, "--to", "build")
	runGit(t, l.Repos["a"], "checkout", "-b", "work/retry")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] 1.1 Correct and verify [trace #1]\n\nScenario-ID: SC-retry\nThe corrected source must pass its regression check.\n")
	run("advance", name)
	record := func(alias string, exit int) {
		t.Helper()
		run("evidence", "record", name, "--repo", alias, "--task", "1", "--scenario", "SC-retry", "--exec", "go", "--cmd-hash", cmdHash, "--exit", fmt.Sprint(exit))
	}
	for round := 1; round <= 3; round++ {
		writeFile(t, filepath.Join(l.Repos["a"], "feature"), fmt.Sprintf("fix attempt %d\n", round))
		commitAll(t, l.Repos["a"], fmt.Sprintf("attempt %d", round))
		writeFile(t, filepath.Join(changeDir, "verification.md"), fmt.Sprintf("Result: fail\nAttempt %d failed regression.\n", round))
		record("a", 1)
		run("set", "verify-result", name, "fail")
		if findings, _ := evidenceFindings(NewRootCmd(), root, changeDir, name); len(findings) != 0 {
			t.Fatalf("superseded reports stayed stale: %v", findings)
		}
	}
	if out, err := runOnto(t, "doctor", "--dir", root); err == nil || !strings.Contains(out, "3 failed verify rounds") {
		t.Fatalf("unresolved repeated failure not reported: %s %v", out, err)
	}
	writeFile(t, filepath.Join(l.Repos["a"], "feature"), "corrected and verified\n")
	commitAll(t, l.Repos["a"], "correct defect")
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\nRegression passed; b needs no source edits.\n")
	if findings, _ := evidenceFindings(NewRootCmd(), root, changeDir, name); len(findings) == 0 {
		t.Fatal("changed report accepted before fresh evidence")
	}
	record("a", 0)
	record("b", 0)
	run("set", "verify-result", name, "pass")
	if out := run("doctor"); !strings.Contains(out, "healthy") || strings.Contains(out, "failed verify rounds") {
		t.Fatalf("resolved failures remain findings: %s", out)
	}
	sc, _, err := evidence.Load(name, evidence.Path(changeDir))
	if err != nil || len(sc.Records) != 5 || sc.Records[0].ExitStatus != 1 {
		t.Fatalf("lost audit history: %+v %v", sc, err)
	}
	trace := run("trace", name, "--json")
	if !strings.Contains(trace, `"superseded-by"`) || !strings.Contains(trace, `"kind": "scenario"`) {
		t.Fatalf("trace lost current/history or preset scenario: %s", trace)
	}
	run("advance", name)
	run("set", "integration", name, "merge")
	run("set", "close-confirmed", name, "reviewed")
	run("merge-deltas", name)
	run("close", name)
	var view struct {
		IntegrationRecord integrationrecord.Record `json:"integration_record"`
	}
	if err := json.Unmarshal([]byte(run("state", name, "--json")), &view); err != nil || len(view.IntegrationRecord.Repositories) != 2 {
		t.Fatalf("pinned candidate API: %+v %v", view, err)
	}
	for _, entry := range view.IntegrationRecord.Repositories {
		receipt := "unchanged:" + entry.BaseCommit
		if entry.Alias == "a" {
			receipt = "merge:" + mergeChangeBranch(t, l.Repos["a"], "main", entry.SourceBranch)
		}
		run("complete-integration", name, "--repo", entry.Alias, "--receipt", receipt)
	}
	archive, st, err := locateArchive(root, name)
	if err != nil || !ontostate.ArchiveIntegrationComplete(archive, st) || st.Observed.VerifyRounds != 3 {
		t.Fatalf("completion or failure audit lost: %+v %v", st, err)
	}
	if files, err := filepath.Glob(filepath.Join(archive, "specs", "*.md")); err != nil || len(files) != 0 {
		t.Fatalf("no-spec workflow invented specs: %v %v", files, err)
	}
}

func TestAuditEvidenceSupersessionDoesNotHideOtherClaims(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := seedEvidenceChange(t, root, "ev")
	seedGitRepo(t, root)
	record := func(task string) {
		t.Helper()
		if _, err := runOnto(t, "evidence", "record", "ev", "--dir", root, "--task", task, "--scenario", "SC-reset-expired", "--exec", "go", "--cmd-hash", cmdHash); err != nil {
			t.Fatal(err)
		}
	}
	record("1")
	record("2")
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\nNew report\n")
	record("2")
	if findings, _ := evidenceFindings(NewRootCmd(), root, changeDir, "ev"); len(findings) != 1 || !strings.Contains(findings[0], "evidence[1]") {
		t.Fatalf("unrefreshed claim disappeared: %v", findings)
	}
	record("1")
	if findings, _ := evidenceFindings(NewRootCmd(), root, changeDir, "ev"); len(findings) != 0 {
		t.Fatalf("refreshed claims stay stale: %v", findings)
	}
}

func TestAuditPresetScenarioContractSources(t *testing.T) {
	root := prepWorkspace(t)
	changeDir := filepath.Join(changesDir(root), "contract")
	st := ontostate.State{Change: "contract", Phase: "verify", Workflow: "fix"}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] 1.1 Verify [trace #1]\nScenario-ID: SC-task\n")
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\nScenario-ID: SC-report\n")
	writeFile(t, filepath.Join(changeDir, "specs", "README.md"), "No delta specs are needed.\n")
	if got, err := loadScenarioIndex(changeDir, st); err != nil || len(got) != 2 || len(got["SC-task"]) != 1 || len(got["SC-report"]) != 1 {
		t.Fatalf("preset contract sources: %v %v", got, err)
	}
	if out, err := runOnto(t, "trace", "contract", "--json", "--dir", root); err != nil || !strings.Contains(out, "SC-report") {
		t.Fatalf("contract missing before evidence recording: %s %v", out, err)
	}
	for _, scenario := range []string{"SC-task", "SC-report"} {
		if _, err := runOnto(t, "evidence", "record", "contract", "--dir", root, "--task", "1", "--scenario", scenario, "--exec", "go", "--cmd-hash", cmdHash); err != nil {
			t.Fatal(err)
		}
	}
	if findings, _ := evidenceFindings(NewRootCmd(), root, changeDir, "contract"); len(findings) != 0 {
		t.Fatalf("declared preset evidence rejected: %v", findings)
	}
	st.Workflow = "full"
	if got, err := loadScenarioIndex(changeDir, st); err != nil || len(got) != 0 {
		t.Fatalf("full workflow bypassed delta contract: %v %v", got, err)
	}
	st.Workflow = "tweak"
	writeFile(t, filepath.Join(changeDir, "specs", "contract.md"), reviewDelta)
	if got, err := loadScenarioIndex(changeDir, st); err != nil || len(got) != 0 {
		t.Fatalf("preset with deltas bypassed delta contract: %v %v", got, err)
	}
}

func TestAuditMergeRejectsUnverifiedDescendant(t *testing.T) {
	for _, combined := range []bool{false, true} {
		for _, reverted := range []bool{false, true} {
			t.Run(fmt.Sprintf("combined=%t/reverted=%t", combined, reverted), func(t *testing.T) {
				root := prepWorkspace(t)
				var archive, source, base string
				name := "demo"
				if combined {
					archive, base = archiveClosedChange(t, root, name)
					source = root
					commitAll(t, root, "archive")
				} else {
					l := explicitWorkspace(t)
					root, name = l.ConfigRoot, "review"
					reviewCloseReady(t, l, false)
					for _, command := range []string{"merge-deltas", "close"} {
						if _, err := runOnto(t, command, name, "--dir", root); err != nil {
							t.Fatal(err)
						}
					}
					archive, _, _ = locateArchive(root, name)
					source, base = l.Repos["a"], "main"
				}
				r, _, err := integrationrecord.Load(archive, name)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(source, "unverified"), "unverified source\n")
				commitAll(t, source, "unverified commit")
				if reverted {
					runGit(t, source, "revert", "--no-edit", "HEAD")
				}
				sha := mergeChangeBranch(t, source, base, r.Repositories[0].SourceBranch)
				args := []string{"complete-integration", name, "--receipt", "merge:" + sha, "--dir", root}
				if _, err := runOnto(t, args...); err == nil || !strings.Contains(err.Error(), "unverified source parent") {
					t.Fatalf("unverified descendant accepted: %v", err)
				}
				if _, err := runOnto(t, "complete-integration", name, "--receipt", "unchanged:"+sha, "--dir", root); err == nil {
					t.Fatal("unchanged receipt laundered an unverified merge")
				}
				after, _, err := integrationrecord.Load(archive, name)
				if err != nil || !reflect.DeepEqual(after, r) {
					t.Fatalf("rejection mutated receipt: %+v %v", after, err)
				}
			})
		}
	}
}

func TestAuditExplicitPRCandidateConfirmation(t *testing.T) {
	l := explicitWorkspace(t)
	changeDir := reviewCloseReady(t, l, false)
	if _, err := runOnto(t, "set", "integration", "review", "pr", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"merge-deltas", "close"} {
		if _, err := runOnto(t, command, "review", "--dir", l.ConfigRoot); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(changeDir); !os.IsNotExist(err) {
		t.Fatalf("not archived: %v", err)
	}
	archive, _, _ := locateArchive(l.ConfigRoot, "review")
	r, _, _ := integrationrecord.Load(archive, "review")
	args := []string{"complete-integration", "review", "--receipt", "pr:https://example.test/pull/1", "--dir", l.ConfigRoot}
	if _, err := runOnto(t, args...); err == nil || !strings.Contains(err.Error(), "requires --head") {
		t.Fatalf("URL alone accepted: %v", err)
	}
	writeFile(t, filepath.Join(l.Repos["a"], "unverified"), "late source\n")
	commitAll(t, l.Repos["a"], "late source")
	head, _ := resolveCommit(l.Repos["a"], "HEAD")
	if _, err := runOnto(t, append(args, "--head", head)...); err == nil {
		t.Fatal("unverified PR head accepted")
	}
	if out, err := runOnto(t, append(args, "--head", r.Repositories[0].SourceCommit)...); err != nil || !strings.Contains(out, "remote publication is not verified") {
		t.Fatalf("pinned PR claim: %s %v", out, err)
	}
	after, _, err := integrationrecord.Load(archive, "review")
	if err != nil || after.Repositories[0].PublicationHead != r.Repositories[0].SourceCommit {
		t.Fatalf("publication claim not pinned: %+v %v", after, err)
	}
}

func TestAuditExplicitCombinedInterruptedArchive(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, moved := range []bool{false, true} {
			t.Run(fmt.Sprintf("nested-config=%t/moved=%t", nested, moved), func(t *testing.T) {
				top := prepWorkspace(t)
				root, sourcePath := top, "."
				if nested {
					root, sourcePath = filepath.Join(top, "control"), ".."
				}
				writeFile(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("schema_version=2\n[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[workflow]\nroot='records/deep/tree'\ngit='existing'\n[repos]\napp=%q\n", sourcePath))
				if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", "onto"), 0755); err != nil {
					t.Fatal(err)
				}
				run := func(args ...string) string {
					t.Helper()
					out, err := runOnto(t, append(args, "--dir", root)...)
					if err != nil {
						t.Fatalf("onto %v: %v\n%s", args, err, out)
					}
					return out
				}
				run("init")
				commitAll(t, top, "explicit combined config")
				run("new", "combined", "--workflow", "tweak", "--repo", "app")
				runGit(t, top, "checkout", "-b", "work/combined")
				writeFile(t, filepath.Join(top, "source with spaces.txt"), "small source edit\n")
				commitAll(t, top, "source")
				changeDir := filepath.Join(workflowRoot(root), "changes", "combined")
				st, err := ontostate.LoadChange(changeDir)
				if err != nil {
					t.Fatal(err)
				}
				st.Phase, st.Integration, st.CloseConfirmed = "close", "merge", "reviewed"
				if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(changeDir, "proposal.md"), strings.Repeat("workflow bookkeeping\n", 250))
				writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] 1.1 Small edit [trace #1]\n\nScenario-ID: SC-small\n")
				writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\nSmall source edit verified.\n")
				run("set", "verify-result", "combined", "pass")
				run("merge-deltas", "combined")
				commitAll(t, top, "final workflow artifacts")
				if files, lines, level, err := stateDiffScale(root, st); err != nil || files != 1 || lines != 1 || level != "light" {
					t.Fatalf("workflow bookkeeping inflated source scale: %d %d %s %v", files, lines, level, err)
				}
				st, _ = ontostate.LoadChange(changeDir)
				entries, err := captureIntegrationEntries(root, st)
				if err != nil {
					t.Fatal(err)
				}
				if err := integrationrecord.Save(changeDir, integrationrecord.NewExplicitPending("combined", "merge", entries)); err != nil {
					t.Fatal(err)
				}
				if moved {
					archive, err := archiveDestination(root, "combined", "2026-09-08")
					if err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(changeDir, archive); err != nil {
						t.Fatal(err)
					}
				}
				// Recovery may excuse only its own pending sidecar / active removals.
				writeFile(t, filepath.Join(top, "source with spaces.txt"), "uncommitted source must block\n")
				if _, err := runOnto(t, "close", "combined", "--dir", root); err == nil || !strings.Contains(err.Error(), "dirty worktree") {
					t.Fatalf("source dirt hidden by recovery: %v", err)
				}
				writeFile(t, filepath.Join(top, "source with spaces.txt"), "small source edit\n")
				run("close", "combined")
				archive, archived, err := locateArchive(root, "combined")
				if err != nil || !archived.Archived || !archived.IntegrationRequired {
					t.Fatalf("recovery failed: %+v %v", archived, err)
				}
				r, _, err := integrationrecord.Load(archive, "combined")
				if err != nil || r.Repositories[0].SourceCommit != entries[0].SourceCommit {
					t.Fatalf("recovery rebound candidate: %+v %v", r, err)
				}
				// Archive records are committed by the history wrapper. Their
				// bookkeeping-only descendant remains a valid publication candidate.
				sha := mergeChangeBranch(t, top, entries[0].BaseBranch, entries[0].SourceBranch)
				run("complete-integration", "combined", "--receipt", "merge:"+sha)
			})
		}
	}
}

func TestAuditUnchangedReceiptRequiresProof(t *testing.T) {
	root := prepWorkspace(t)
	baseBranch := seedBaseBranch(t, root)
	base, _ := resolveCommit(root, "HEAD")
	runGit(t, root, "checkout", "-b", "zero-diff")
	runGit(t, root, "commit", "--allow-empty", "-m", "no source changes")
	source, _ := resolveCommit(root, "HEAD")
	entry := integrationrecord.Entry{BaseBranch: baseBranch, BaseCommit: base, SourceBranch: "zero-diff", SourceCommit: source}
	if _, err := validateUnchangedReceipt(root, "unchanged:"+base, entry); err != nil {
		t.Fatalf("tree-identical descendant refused: %v", err)
	}
	writeFile(t, filepath.Join(root, "feature"), "not integrated\n")
	commitAll(t, root, "source edit")
	entry.SourceCommit, _ = resolveCommit(root, "HEAD")
	if _, err := validateUnchangedReceipt(root, "unchanged:"+base, entry); err == nil {
		t.Fatal("unintegrated source edit accepted as unchanged")
	}
	if _, err := validateUnchangedReceipt(root, "unchanged:"+entry.SourceCommit, entry); err == nil {
		t.Fatal("receipt outside receiving branch accepted")
	}
	merge := mergeChangeBranch(t, root, baseBranch, entry.SourceBranch)
	if _, err := validateUnchangedReceipt(root, "unchanged:"+merge, entry); err == nil {
		t.Fatal("post-capture merge accepted as previously integrated")
	}
	entry.BaseCommit = merge
	if _, err := validateUnchangedReceipt(root, "unchanged:"+merge, entry); err != nil {
		t.Fatalf("already integrated source refused: %v", err)
	}
	entry.BaseCommit = entry.SourceCommit
	entry.SourceCommit = base
	if _, err := validateUnchangedReceipt(root, "unchanged:"+base, entry); err == nil {
		t.Fatal("receipt preceding recorded base accepted")
	}
}

func TestAuditGateExposesVerificationRecovery(t *testing.T) {
	l := explicitWorkspace(t)
	changeDir := reviewCloseReady(t, l, false)
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass|fail\n")
	for _, command := range []string{"state", "gate", "handoff"} {
		out, err := runOnto(t, command, "review", "--json", "--dir", l.ConfigRoot)
		if err != nil || !strings.Contains(out, "Verification recovery") || !strings.Contains(out, `"pending"`) {
			t.Fatalf("%s hid malformed report: %s %v", command, out, err)
		}
	}
	writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\n")
	writeFile(t, filepath.Join(l.Repos["a"], "feature"), "unverified edit\n")
	commitAll(t, l.Repos["a"], "unverified source")
	if out, err := runOnto(t, "gate", "review", "--json", "--dir", l.ConfigRoot); err != nil || !strings.Contains(out, "Verification recovery") {
		t.Fatalf("gate hid stale verified HEAD: %s %v", out, err)
	}
}
