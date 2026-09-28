package ontocli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/evidence"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/workspace"
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

func TestAuditScenarioContractSources(t *testing.T) {
	for _, workflow := range []string{"full", "", "fix", "tweak"} {
		for _, specs := range []string{"absent", "empty", "README-only", "empty-delta", "delta-without-IDs", "delta-with-ID"} {
			t.Run(fmt.Sprintf("workflow=%s/specs=%s", workflow, specs), func(t *testing.T) {
				root := prepWorkspace(t)
				changeDir := filepath.Join(changesDir(root), "contract")
				st := ontostate.State{Change: "contract", Phase: "verify", Workflow: workflow}
				if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] 1.1 Verify [trace #1]\nScenario-ID: SC-task\n")
				writeFile(t, filepath.Join(changeDir, "verification.md"), "Result: pass\nScenario-ID: SC-report\n")
				want := scenarioIndex{
					"SC-task":   {{Path: "tasks.md", Line: 2}},
					"SC-report": {{Path: "verification.md", Line: 2}},
				}
				unknown := []string{"SC-unknown", "SC-readme"}
				switch specs {
				case "empty":
					if err := os.MkdirAll(filepath.Join(changeDir, "specs"), 0755); err != nil {
						t.Fatal(err)
					}
				case "README-only":
					writeFile(t, filepath.Join(changeDir, "specs", "README.md"), "No delta specs are needed.\nScenario-ID: SC-readme\n")
				case "empty-delta", "delta-without-IDs", "delta-with-ID":
					delta := ""
					want = scenarioIndex{}
					unknown = append(unknown, "SC-task", "SC-report")
					if specs == "delta-without-IDs" {
						delta = reviewDelta
					}
					if specs == "delta-with-ID" {
						delta = "## ADDED Requirements\n### Requirement: documented behavior\nRequirement-ID: REQ-contract\n#### Scenario: delta contract\nScenario-ID: SC-delta\n"
						want["SC-delta"] = []scenarioDeclaration{{Path: "specs/contract.md", Line: 5, Name: "delta contract", Requirement: "REQ-contract"}}
					}
					writeFile(t, filepath.Join(changeDir, "specs", "contract.md"), delta)
				}
				if got, err := loadScenarioIndex(changeDir); err != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("scenario contract: got %v, want %v: %v", got, want, err)
				}
				out, err := runOnto(t, "trace", "contract", "--json", "--dir", root)
				var graph traceGraph
				if err != nil || json.Unmarshal([]byte(out), &graph) != nil || len(graph.Findings) != 0 {
					t.Fatalf("trace before recording: %s %v", out, err)
				}
				traced := map[string]bool{}
				for _, node := range graph.Nodes {
					if node.Kind == "scenario" && strings.HasPrefix(node.ID, "SC-") {
						traced[node.ID] = true
					}
				}
				if len(traced) != len(want) {
					t.Fatalf("trace contract IDs: got %v, want %v", traced, want)
				}
				record := func(scenario string) error {
					t.Helper()
					_, err := runOnto(t, "evidence", "record", "contract", "--dir", root, "--task", "1", "--scenario", scenario, "--exec", "go", "--cmd-hash", cmdHash)
					return err
				}
				for _, scenario := range unknown {
					if err := record(scenario); err == nil || !strings.Contains(err.Error(), scenario) {
						t.Fatalf("unknown scenario %s accepted: %v", scenario, err)
					}
					if _, err := os.Stat(evidence.Path(changeDir)); !os.IsNotExist(err) {
						t.Fatalf("refused first claim created sidecar: %v", err)
					}
				}
				for scenario := range want {
					if !traced[scenario] {
						t.Errorf("trace missing %s before recording", scenario)
					}
					if err := record(scenario); err != nil {
						t.Fatal(err)
					}
				}
				if findings, _ := evidenceFindings(NewRootCmd(), root, changeDir, "contract"); len(findings) != 0 {
					t.Fatalf("declared evidence rejected: %v", findings)
				}
				if len(want) == 0 {
					return
				}
				before, err := os.ReadFile(evidence.Path(changeDir))
				if err != nil {
					t.Fatal(err)
				}
				for _, scenario := range unknown {
					if err := record(scenario); err == nil || !strings.Contains(err.Error(), scenario) {
						t.Fatalf("unknown scenario %s appended to history: %v", scenario, err)
					}
					after, err := os.ReadFile(evidence.Path(changeDir))
					if err != nil || !bytes.Equal(before, after) {
						t.Fatal("rejected unknown scenario changed audit history")
					}
				}
			})
		}
	}
}

func TestAuditFullDocumentationOnlyLifecycle(t *testing.T) {
	l := explicitWorkspace(t)
	root, name := l.ConfigRoot, "docs-only"
	run := func(args ...string) string {
		t.Helper()
		out, err := runOnto(t, append(args, "--dir", root)...)
		if err != nil {
			t.Fatalf("onto %v: %v\n%s", args, err, out)
		}
		return out
	}
	run("new", name, "--workflow", "full", "--repo", "a")
	changeDir := filepath.Join(l.WorkflowRoot, "changes", name)
	load := func() ontostate.State {
		t.Helper()
		st, err := ontostate.LoadChange(changeDir)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	advance := func(from, to string) {
		t.Helper()
		st := load()
		if st.Phase != from || len(pendingGates(name, st)) != 0 {
			t.Fatalf("unfulfilled %s phase: %+v, gates: %+v", from, st, pendingGates(name, st))
		}
		run("advance", name)
		if st := load(); st.Phase != to {
			t.Fatalf("advance %s: got %s, want %s", from, st.Phase, to)
		}
	}
	st := load()
	if st.Workflow != "full" || st.RepoMode != "explicit" || !reflect.DeepEqual(st.Repos, []string{"a"}) {
		t.Fatalf("full explicit source scope: %+v", st)
	}
	const justification = "Documentation-only clarification of existing evidence recording order; no behavior, requirements, or scenarios in living specs change, so no delta specs are needed."
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "# Proposal\n\nClarify that the report is finalized before evidence is recorded.\n\n"+justification+"\n")
	run("set", "proposal-approved", name, "Reviewed the documentation scope against the request; "+justification)
	advance("open", "design")
	writeFile(t, filepath.Join(changeDir, "design.md"), "# Design\n\nStatus: Confirmed\n\nUpdate docs/evidence.md in source a and check the committed instruction with git grep.\n\n"+justification+"\n")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [ ] 1.1 Clarify and check evidence recording order [trace #1]\n")
	writeFile(t, filepath.Join(changeDir, "plan.md"), "# Plan\n\n## Task 1.1 — Clarify and check evidence recording order\n\nWrite docs/evidence.md in source a, commit it, and check the committed instruction with git grep. Record SC-docs-order in the finalized verification report.\n")
	run("set", "approach-confirmed", name, "Reviewed the documentation-only approach and its exact-line check; "+justification)
	w, err := workspace.CreateWorktree(l, "onto", name, "a", st.RepoBases["a"].BaseBranch, "work/docs-only")
	if err != nil {
		t.Fatal(err)
	}
	run("set", "isolation", name, "worktree")
	advance("design", "build")
	run("set", "build-mode", name, "direct")
	run("set", "tdd-mode", name, "direct")
	const instruction = "Finalize verification.md before recording evidence."
	writeFile(t, filepath.Join(w.Path, "docs", "evidence.md"), "# Evidence recording\n\n"+instruction+"\n")
	commitAll(t, w.Path, "docs: clarify evidence recording order")
	candidate, err := resolveCommit(w.Path, "HEAD")
	if err != nil || candidate == st.RepoBases["a"].BaseRef {
		t.Fatalf("documentation candidate: %s %v", candidate, err)
	}
	if files, err := gitOutput(t, w.Path, "diff", "--name-only", st.RepoBases["a"].BaseRef, candidate); err != nil || files != "docs/evidence.md" {
		t.Fatalf("candidate is not documentation-only: %q %v", files, err)
	}
	check := exec.Command("git", "grep", "-n", "-F", instruction, "HEAD", "--", "docs/evidence.md")
	check.Dir = w.Path
	output, err := check.CombinedOutput()
	if err != nil || string(output) != "HEAD:docs/evidence.md:3:"+instruction+"\n" {
		t.Fatalf("committed documentation check: %s %v", output, err)
	}
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "- [x] 1.1 Clarify and check evidence recording order [trace #1]\n")
	advance("build", "verify")
	const command = "git grep -n -F 'Finalize verification.md before recording evidence.' HEAD -- docs/evidence.md"
	outputPath := filepath.Join(changeDir, "verification-output.txt")
	writeFile(t, outputPath, string(output))
	report := fmt.Sprintf("# Verification\n\nResult: pass\n\nScenario-ID: SC-docs-order\nGiven source a at %s, when `%s` runs, it exits 0 and finds the finalized-report instruction on line 3 of docs/evidence.md.\n\n%s\n", candidate, command, justification)
	reportPath := filepath.Join(changeDir, "verification.md")
	writeFile(t, reportPath, report)
	run("set", "verify-scale", name, "full")
	commandHash := fmt.Sprintf("%x", sha256.Sum256([]byte(command)))
	run("evidence", "record", name, "--repo", "a", "--task", "1", "--scenario", "SC-docs-order", "--exec", "git", "--cmd-hash", commandHash, "--exit", "0", "--output", outputPath, "--artifact", reportPath)
	sc, present, err := evidence.Load(name, evidence.Path(changeDir))
	if err != nil || !present || len(sc.Records) != 1 {
		t.Fatalf("documentation evidence: %+v %v", sc, err)
	}
	rec := sc.Records[0]
	if rec.Repo != "a" || rec.Task != 1 || rec.Scenario != "SC-docs-order" || rec.Commit != candidate || rec.Executable != "git" || rec.ExitStatus != 0 || rec.CommandHash != commandHash || rec.OutputHash != fmt.Sprintf("%x", sha256.Sum256(output)) || rec.ArtifactHash != fmt.Sprintf("%x", sha256.Sum256([]byte(report))) {
		t.Fatalf("evidence lost candidate or artifact binding: %+v", rec)
	}
	run("set", "verify-result", name, "pass")
	advance("verify", "close")
	if st := load(); !reflect.DeepEqual(st.Verify.Heads, map[string]string{"a": candidate}) {
		t.Fatalf("pass not bound to source worktree: %+v", st.Verify.Heads)
	}
	run("set", "guides", name, "updated")
	run("set", "integration", name, "merge")
	run("set", "close-confirmed", name, "Reviewed the verified documentation candidate, updated guide, and empty delta set; "+justification)
	if out, err := runOnto(t, "doctor", "--dir", root); err != nil || strings.TrimSpace(out) != "healthy" {
		t.Errorf("full documentation-only doctor: %s %v", out, err)
	}
	out := run("trace", name, "--json")
	var graph traceGraph
	if err := json.Unmarshal([]byte(out), &graph); err != nil || len(graph.Findings) != 0 {
		t.Fatalf("documentation trace: %s %v", out, err)
	}
	for _, want := range []traceEdge{
		{From: "scenario:SC-docs-order", To: "evidence:docs-only/e1", Kind: "verified-by"},
		{From: "task:docs-only#1", To: "evidence:docs-only/e1", Kind: "verified-by"},
		{From: "evidence:docs-only/e1", To: "commit:" + candidate, Kind: "recorded-at"},
	} {
		found := false
		for _, edge := range graph.Edges {
			found = found || edge == want
		}
		if !found {
			t.Errorf("trace missing %+v: %s", want, out)
		}
	}
	run("merge-deltas", name)
	if st := load(); !st.Close.Merged || len(pendingGates(name, st)) != 0 {
		t.Fatalf("unfulfilled close gates: %+v", st)
	}
	run("close", name)
	archive, archived, err := locateArchive(root, name)
	if err != nil || !archived.Archived || !archived.Close.Merged || archived.Verify.Heads["a"] != candidate {
		t.Fatalf("documentation archive: %+v %v", archived, err)
	}
	for _, dir := range []string{archive, l.WorkflowRoot} {
		if paths, err := deltaSpecPaths(filepath.Join(dir, "specs")); err != nil || len(paths) != 0 {
			t.Fatalf("documentation-only close invented specs: %v %v", paths, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(archive, "verification.md")); err != nil || string(data) != report {
		t.Fatalf("close changed finalized report: %q %v", data, err)
	}
	if after, _, err := evidence.Load(name, evidence.Path(archive)); err != nil || !reflect.DeepEqual(after, sc) {
		t.Fatalf("close changed evidence history: %+v %v", after, err)
	}
	integration, _, err := integrationrecord.Load(archive, name)
	if err != nil || len(integration.Repositories) != 1 || integration.Repositories[0].Alias != "a" || integration.Repositories[0].SourceCommit != candidate || integration.Repositories[0].SourceBranch != "work/docs-only" {
		t.Fatalf("close lost documentation candidate: %+v %v", integration, err)
	}
	if head, err := resolveCommit(l.Repos["a"], "HEAD"); err != nil || head != st.RepoBases["a"].BaseRef {
		t.Fatalf("original source checkout moved: %s %v", head, err)
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
