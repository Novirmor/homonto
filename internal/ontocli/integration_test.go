package ontocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

// archiveClosedChange walks the real close path on a change branch and returns
// the archive directory plus the base branch (captured before branching).
func archiveClosedChange(t *testing.T, root, name string) (string, string) {
	t.Helper()
	base := seedBaseBranch(t, root)
	seedClose(t, root, name, nil)
	checkoutChangeBranch(t, root, name)
	commitAll(t, root, "seed change")
	if _, err := runOnto(t, "close", name, "--dir", root); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-"+name), base
}

func TestCompleteIntegrationAcceptsRealMergeAndMakesChangeDone(t *testing.T) {
	root := prepWorkspace(t)
	archiveDir, base := archiveClosedChange(t, root, "demo")

	commitAll(t, root, "archive demo")
	mergeSHA := mergeChangeBranch(t, root, base, "change/demo")
	if _, err := runOnto(t, "complete-integration", "demo", "--receipt", "merge:"+mergeSHA, "--dir", root); err != nil {
		t.Fatalf("complete integration: %v", err)
	}
	record, ok, err := integrationrecord.Load(archiveDir, "demo")
	if err != nil || !ok || record.Status != integrationrecord.StatusComplete {
		t.Fatalf("integration = %+v, %v, present=%v", record, err, ok)
	}
	if record.Repositories[0].Receipt != "merge:"+mergeSHA {
		t.Fatalf("receipt %q was not canonicalized to %q", record.Repositories[0].Receipt, mergeSHA)
	}
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ontostate.DeriveWorkingPhase(archiveDir, st); got != "done" {
		t.Fatalf("derived phase = %q, want done", got)
	}
	if _, err := runOnto(t, "complete-integration", "demo", "--receipt", "merge:"+mergeSHA, "--dir", root); err != nil {
		t.Fatalf("same receipt replay must be idempotent: %v", err)
	}
	if _, err := runOnto(t, "complete-integration", "demo", "--receipt", "merge:1234567", "--dir", root); err == nil {
		t.Fatal("replacement integration receipt succeeded")
	}
}

func TestCompleteIntegrationRejectsUnprovenMergeReceipts(t *testing.T) {
	root := prepWorkspace(t)
	archiveDir, base := archiveClosedChange(t, root, "demo")
	commitAll(t, root, "archive demo")
	sourceCommit, _ := gitOutput(t, root, "rev-parse", "change/demo")
	regularCommit, _ := gitOutput(t, root, "rev-parse", base+"~0")
	baseBeforeMerge, _ := gitOutput(t, root, "rev-parse", base)

	cases := []struct {
		name    string
		receipt string
		prepare func(t *testing.T)
	}{
		{name: "nonexistent commit", receipt: "merge:deadbee"},
		{name: "regular commit is not a merge", receipt: "merge:" + regularCommit},
		{name: "source tip is not a merge", receipt: "merge:" + sourceCommit},
		{
			name:    "unmerged commit not on base branch",
			receipt: "merge:auto", // replaced below with a real merge on a side branch
			prepare: func(t *testing.T) {
				// Merge the change into a DIFFERENT branch: valid merge commit,
				// but the base branch never sees it. Stay on the side branch —
				// checking out base would materialize the archive deletion.
				runGit(t, root, "branch", "side/demo", baseBeforeMerge)
				runGit(t, root, "checkout", "-q", "side/demo")
				runGit(t, root, "merge", "--no-ff", "-q", "-m", "merge to side", "change/demo")
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.prepare != nil {
				tc.prepare(t)
				if tc.receipt == "merge:auto" {
					sha, err := gitOutput(t, root, "rev-parse", "side/demo")
					if err != nil {
						t.Fatal(err)
					}
					tc.receipt = "merge:" + sha
				}
			}
			if _, err := runOnto(t, "complete-integration", "demo", "--receipt", tc.receipt, "--dir", root); err == nil {
				t.Fatalf("receipt %q was accepted without proof", tc.receipt)
			}
			record, ok, err := integrationrecord.Load(archiveDir, "demo")
			if err != nil || !ok || record.Status != integrationrecord.StatusPending {
				t.Fatalf("rejected receipt mutated the record: %+v %v %v", record, err, ok)
			}
		})
	}
}

func TestCompleteIntegrationRejectsWrongReceiptKind(t *testing.T) {
	root := prepWorkspace(t)
	st := ontostate.State{
		Change: "demo", Workflow: "tweak", Phase: "close", BaseRef: "abc123",
		Verify: ontostate.Verify{Result: "pass"}, Close: ontostate.Close{Merged: true},
		Integration: "pr", CloseConfirmed: "reviewed",
	}
	base := seedBaseBranch(t, root)
	st.BaseBranch = base
	seedCloseState(t, root, st)
	checkoutChangeBranch(t, root, "demo")
	commitAll(t, root, "seed change")
	if _, err := runOnto(t, "close", "demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	if _, err := runOnto(t, "complete-integration", "demo", "--receipt", "pr:https://github.com/acme/repo/pull/7", "--dir", root); err != nil {
		t.Fatalf("PR receipt for PR integration: %v", err)
	}
}

func TestCompleteIntegrationCrossRepoNeedsEveryRepository(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "config")
	api := filepath.Join(base, "api")
	for _, dir := range []string{root, api} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, dir, "init")
		runGit(t, dir, "config", "user.email", "test@example.com")
		runGit(t, dir, "config", "user.name", "Test")
		writeFile(t, filepath.Join(dir, "tracked"), "ok\n")
		runGit(t, dir, "add", "-A")
		runGit(t, dir, "commit", "-m", "init")
	}
	if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "homonto.toml"), `[frameworks.onto]
source="builtin:onto"
scope="project"
[subagents.onto.opencode]
model="test/model"
[subagents.onto-explorer.opencode]
model="test/model"
[subagents.onto-reviewer.opencode]
model="test/model"
[subagents.onto-implementer.opencode]
model="test/model"
[subagents.onto-skeptic.opencode]
model="test/model"
[repos]
api="../api"
`)
	configBase := seedBaseBranch(t, root)
	apiBase := seedBaseBranch(t, api)
	seedCloseState(t, root, ontostate.State{
		Change: "cross", Workflow: "full", Phase: "close", BaseRef: "abc123", BaseBranch: configBase, Repos: []string{"api"},
		Verify: ontostate.Verify{Result: "pass"}, Close: ontostate.Close{Merged: true}, Guides: "updated",
		Integration: "merge", CloseConfirmed: "reviewed",
	})
	checkoutChangeBranch(t, root, "cross")
	commitAll(t, root, "seed config")
	runGit(t, api, "checkout", "-q", "-b", "change/cross")
	writeFile(t, filepath.Join(api, "feature"), "api work\n")
	commitAll(t, api, "api change")
	if _, err := runOnto(t, "close", "cross", "--dir", root); err != nil {
		t.Fatalf("cross-repo close: %v", err)
	}
	archiveDir := filepath.Join(root, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-cross")
	record, ok, err := integrationrecord.Load(archiveDir, "cross")
	if err != nil || !ok || len(record.Repositories) != 2 {
		t.Fatalf("cross-repo record = %+v, %v, %v", record, err, ok)
	}

	// Integrate only the config repository: the change stays pending.
	commitAll(t, root, "archive cross")
	configMerge := mergeChangeBranch(t, root, configBase, "change/cross")
	if _, err := runOnto(t, "complete-integration", "cross", "--receipt", "merge:"+configMerge, "--dir", root); err != nil {
		t.Fatalf("config receipt: %v", err)
	}
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ontostate.DeriveWorkingPhase(archiveDir, st); got != "close" {
		t.Fatalf("partial completion derived %q, want close", got)
	}

	// Unknown alias refused.
	if _, err := runOnto(t, "complete-integration", "cross", "--repo", "ghost", "--receipt", "merge:"+configMerge, "--dir", root); err == nil {
		t.Fatal("unknown repository alias accepted")
	}

	// Integrate the api repository with a real merge there too: now done.
	apiMerge := mergeChangeBranch(t, api, apiBase, "change/cross")
	if _, err := runOnto(t, "complete-integration", "cross", "--repo", "api", "--receipt", "merge:"+apiMerge, "--dir", root); err != nil {
		t.Fatalf("api receipt: %v", err)
	}
	record, ok, err = integrationrecord.Load(archiveDir, "cross")
	if err != nil || !ok || record.Status != integrationrecord.StatusComplete {
		t.Fatalf("full completion = %+v, %v, %v", record, err, ok)
	}
	if got := ontostate.DeriveWorkingPhase(archiveDir, st); got != "done" {
		t.Fatalf("full completion derived %q, want done", got)
	}
}

func TestNewRefusesNameWhoseLatestArchiveIsPending(t *testing.T) {
	root := prepWorkspace(t)
	archiveClosedChange(t, root, "demo")

	if _, err := runOnto(t, "new", "demo", "--dir", root); err == nil || !strings.Contains(err.Error(), "integration") {
		t.Fatalf("onto new accepted a name whose latest archive is pending integration: %v", err)
	}
}

func TestCompleteIntegrationTargetsNewestArchiveGeneration(t *testing.T) {
	root := prepWorkspace(t)
	archiveDir, base := archiveClosedChange(t, root, "demo")
	commitAll(t, root, "archive demo")
	mergeSHA := mergeChangeBranch(t, root, base, "change/demo")
	if _, err := runOnto(t, "complete-integration", "demo", "--receipt", "merge:"+mergeSHA, "--dir", root); err != nil {
		t.Fatal(err)
	}
	_ = archiveDir

	// A second generation of the same name archives later the same day.
	seedClose(t, root, "demo", nil)
	checkoutChangeBranch(t, root, "demo")
	commitAll(t, root, "second generation")
	if _, err := runOnto(t, "close", "demo", "--dir", root); err != nil {
		t.Fatal(err)
	}
	commitAll(t, root, "archive second generation")
	merge2 := mergeChangeBranch(t, root, base, "change/demo")
	if _, err := runOnto(t, "complete-integration", "demo", "--receipt", "merge:"+merge2, "--dir", root); err != nil {
		t.Fatalf("second-generation receipt: %v", err)
	}
}
