package ontocli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tocli"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

// toRun drives the `to` binary's command surface from these tests (tocli
// does not import ontocli, so there is no cycle). The promote→demote
// round-trip is inherently cross-binary.
func toRun(t *testing.T, wantErr bool, args ...string) string {
	t.Helper()
	cmd := tocli.NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	if wantErr && err == nil {
		t.Fatalf("to execute %v = nil, want error; output: %s", args, out.String())
	}
	if !wantErr && err != nil {
		t.Fatalf("to execute %v: %v\noutput: %s", args, err, out.String())
	}
	return out.String()
}

// seedDemotable creates an onto change at the requested phase with proposal
// and (optionally) a task list in the canonical dotted+trace form.
func seedDemotable(t *testing.T, dir, name, phase, tasks string) {
	t.Helper()
	if _, err := run(t, "new", name, "--dir", dir); err != nil {
		t.Fatalf("new %s: %v", name, err)
	}
	stPath := filepath.Join(dir, "docs", "changes", name, "onto-state.yaml")
	if phase != "open" {
		st, err := ontostate.Load(stPath)
		if err != nil {
			t.Fatal(err)
		}
		st.Phase = phase
		if err := ontostate.Save(stPath, st); err != nil {
			t.Fatal(err)
		}
	}
	if tasks != "" {
		writeFile(t, filepath.Join(dir, "docs", "changes", name, "tasks.md"), tasks)
	}
}

const canonicalTasks = "# Tasks\n\n- [ ] 1.1 ship the parser [trace #1]\n- [x] 1.2 pin the fixtures [trace #2]\n"

func TestPromoteRetainsPreImplementationAnchorsAndScale(t *testing.T) {
	for _, detached := range []bool{false, true} {
		t.Run(fmt.Sprintf("detached=%v", detached), func(t *testing.T) {
			l := explicitWorkspace(t)
			configPath := filepath.Join(l.ConfigRoot, "homonto.toml")
			writeFile(t, configPath, readFile(t, configPath)+"\n[frameworks.to]\nsource='builtin:to'\nscope='project'\n")
			if err := os.MkdirAll(filepath.Join(l.ConfigRoot, ".homonto", "catalog", "skills", "to"), 0755); err != nil {
				t.Fatal(err)
			}
			want := map[string]ontostate.RepoBase{}
			for alias, branch := range map[string]string{"a": "main", "b": "develop"} {
				dir := l.Repos[alias]
				head, err := resolveCommit(dir, "HEAD")
				if err != nil {
					t.Fatal(err)
				}
				identity, err := sourceIdentity(dir)
				if err != nil {
					t.Fatal(err)
				}
				want[alias] = ontostate.RepoBase{BaseRef: head, BaseBranch: branch, GitCommonDir: identity}
				runGit(t, dir, "switch", "-c", "feature/cross")
			}
			toRun(t, false, "new", "cross", "--repo", "a,b", "--base", "a=main", "--base", "b=develop", "--dir", l.ConfigRoot)
			toPath := filepath.Join(l.WorkflowRoot, "tasks", "cross", tostate.FileName)
			before, err := tostate.Load(toPath)
			if err != nil {
				t.Fatal(err)
			}
			for alias, base := range want {
				if before.RepoBases[alias] != tostate.RepoBase(base) {
					t.Fatalf("creation did not record selected base %s: %+v", alias, before.RepoBases)
				}
			}
			toRun(t, false, "phase", "cross", "--dir", l.ConfigRoot)
			for alias, dir := range l.Repos {
				writeFile(t, filepath.Join(dir, "feature.go"), "package feature\n")
				commitAll(t, dir, "implement "+alias)
				head, err := resolveCommit(dir, "HEAD")
				if err != nil || head == want[alias].BaseRef {
					t.Fatalf("source %s did not advance: %s %v", alias, head, err)
				}
				if detached {
					runGit(t, dir, "checkout", "--detach")
				}
			}
			after, err := tostate.Load(toPath)
			if err != nil || after.Phase != "do" || !reflect.DeepEqual(after.RepoBases, before.RepoBases) {
				t.Fatalf("to implementation changed anchors: %+v %v", after, err)
			}
			toRun(t, false, "promote", "cross", "--yes", "--dir", l.ConfigRoot)
			st, err := ontostate.LoadChange(filepath.Join(l.WorkflowRoot, "changes", "cross"))
			if err != nil || st.Validate() != nil || st.ID != before.ID || st.RepoMode != "explicit" || !reflect.DeepEqual(st.RepoBases, want) {
				t.Fatalf("promotion replaced creation-time provenance: %+v %v", st, err)
			}
			out, err := run(t, "scale", "cross", "--json", "--dir", l.ConfigRoot)
			if err != nil {
				t.Fatal(err)
			}
			var scale struct {
				Files int `json:"files"`
				Lines int `json:"lines"`
			}
			if err := json.Unmarshal([]byte(out), &scale); err != nil || scale.Files != 2 || scale.Lines != 2 {
				t.Fatalf("scale excluded implementation work: %s %v", out, err)
			}
		})
	}
}

func TestLegacyScopedPromotionPreservesAuthorityAndInverse(t *testing.T) {
	for _, version := range []int{-1, 0, 1} {
		for _, anchors := range []bool{false, true} {
			t.Run(fmt.Sprintf("config%d/anchors=%v", version, anchors), func(t *testing.T) {
				t.Setenv("GIT_AUTHOR_NAME", "Conversion Test")
				t.Setenv("GIT_AUTHOR_EMAIL", "convert@example.test")
				t.Setenv("GIT_COMMITTER_NAME", "Conversion Test")
				t.Setenv("GIT_COMMITTER_EMAIL", "convert@example.test")
				base := t.TempDir()
				root := filepath.Join(base, "config")
				api := filepath.Join(base, "api")
				replacement := filepath.Join(base, "replacement")
				for _, dir := range []string{root, api, replacement} {
					writeFile(t, filepath.Join(dir, "tracked"), "base\n")
					runGit(t, dir, "init", "-b", "main")
					commitAll(t, dir, "base")
				}
				config := "[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[frameworks.to]\nsource='builtin:to'\nscope='project'\n[repos]\napi='../api'\n"
				if version >= 0 {
					config = fmt.Sprintf("schema_version=%d\n", version) + config
				}
				writeFile(t, filepath.Join(root, "homonto.toml"), config)
				writeFile(t, filepath.Join(root, ".gitignore"), ".homonto/\n")
				for _, framework := range []string{"onto", "to"} {
					if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", framework), 0755); err != nil {
						t.Fatal(err)
					}
				}
				commitAll(t, root, "config")
				toRun(t, false, "new", "cross", "--repo", "api", "--dir", root)
				toPath := filepath.Join(root, "docs", "tasks", "cross", tostate.FileName)
				planPath := filepath.Join(filepath.Dir(toPath), "plan.md")
				originalPlan := "# Plan\n\nLegacy scoped work.\n"
				writeFile(t, planPath, originalPlan)
				before, err := tostate.Load(toPath)
				if err != nil || before.RepoMode != "legacy" || len(before.RepoBases) != 2 || before.ID == "" {
					t.Fatalf("new legacy scoped state: %+v %v", before, err)
				}
				if anchors {
					for alias, dir := range map[string]string{"": root, "api": api} {
						b := before.RepoBases[alias]
						b.BaseRef, err = resolveCommit(dir, "HEAD")
						if err != nil {
							t.Fatal(err)
						}
						b.BaseBranch = "main"
						before.RepoBases[alias] = b
					}
					if err := tostate.Save(toPath, before); err != nil {
						t.Fatal(err)
					}
				}
				original := readFile(t, toPath)
				toRun(t, false, "promote", "cross", "--yes", "--dir", root)
				ontoDir := filepath.Join(root, "docs", "changes", "cross")
				ontoPath := filepath.Join(ontoDir, "onto-state.yaml")
				promoted := readFile(t, ontoPath)
				st, err := ontostate.Load(ontoPath)
				if err != nil || st.Validate() != nil || st.ID != before.ID || st.RepoMode != "legacy" || len(st.RepoBases) != 2 {
					t.Fatalf("promoted legacy scope: %+v %v", st, err)
				}
				for alias, b := range before.RepoBases {
					if st.RepoBases[alias] != ontostate.RepoBase(b) {
						t.Fatalf("promotion changed provenance for %q: %+v", alias, st.RepoBases)
					}
				}
				dirs, err := stateSourceDirs(root, st)
				if err != nil || !reflect.DeepEqual(dirs, map[string]string{"": root, "api": api}) {
					t.Fatalf("legacy source dirs: %+v %v", dirs, err)
				}
				dirt, err := stateWorktreeDirt(root, st)
				legacy := st
				legacy.RepoMode, legacy.RepoBases = "", nil
				wantDirt, wantErr := stateWorktreeDirt(root, legacy)
				if err != nil || wantErr != nil || !reflect.DeepEqual(dirt, wantDirt) {
					t.Fatalf("legacy workflow dirt classification changed: %+v %v (want %+v %v)", dirt, err, wantDirt, wantErr)
				}
				// A valid declaration pointing at a different object store is still
				// not the source authorized when the to change was created.
				writeFile(t, filepath.Join(root, "homonto.toml"), strings.Replace(config, "../api", "../replacement", 1))
				if message := runFail(t, "dirt", "cross", "--dir", root); !strings.Contains(message, "identity") {
					t.Fatalf("substituted alias not refused: %s", message)
				}
				if _, err := captureVerifyHeads(root, st); err == nil || !strings.Contains(err.Error(), "identity") {
					t.Fatalf("verification accepted substituted authority: %v", err)
				}
				out, _ := run(t, "doctor", "--dir", root)
				if !strings.Contains(out, "source scope") || !strings.Contains(out, "identity") {
					t.Fatalf("doctor missed substituted authority: %s", out)
				}
				writeFile(t, filepath.Join(root, "homonto.toml"), config)
				b := st.RepoBases[""]
				b.GitCommonDir = filepath.Join(replacement, ".git")
				st.RepoBases[""] = b
				if err := ontostate.Save(ontoPath, st); err != nil {
					t.Fatal(err)
				}
				if message := runFail(t, "dirt", "cross", "--dir", root); !strings.Contains(message, "identity") {
					t.Fatalf("config identity not enforced: %s", message)
				}
				writeFile(t, ontoPath, promoted)
				if _, err := run(t, "demote", "cross", "--yes", "--dir", root); err != nil {
					t.Fatal(err)
				}
				if got := readFile(t, toPath); got != original {
					t.Fatalf("inverse changed original state:\n%s", got)
				}
				if got := readFile(t, planPath); got != originalPlan {
					t.Fatalf("inverse changed original plan:\n%s", got)
				}
				// Force a fresh demotion, rather than restoring the snapshot.
				toRun(t, false, "promote", "cross", "--yes", "--dir", root)
				writeFile(t, filepath.Join(ontoDir, "proposal.md"), "edited\n")
				if _, err := run(t, "demote", "cross", "--yes", "--dir", root); err != nil {
					t.Fatal(err)
				}
				after, err := tostate.Load(toPath)
				if err != nil || after.ID != before.ID || after.RepoMode != "legacy" || !reflect.DeepEqual(after.RepoBases, before.RepoBases) {
					t.Fatalf("edited demotion lost provenance: %+v %v", after, err)
				}
				toRun(t, false, "promote", "cross", "--yes", "--dir", root)
				st, err = ontostate.Load(ontoPath)
				if err != nil {
					t.Fatal(err)
				}
				st.Phase, st.Archived, st.Integration, st.BaseBranch = "close", true, "pr", "main"
				if err := ontostate.Save(ontoPath, st); err != nil {
					t.Fatal(err)
				}
				var entries []integrationrecord.Entry
				for alias, dir := range map[string]string{"": root, "api": api} {
					head, err := resolveCommit(dir, "HEAD")
					if err != nil {
						t.Fatal(err)
					}
					entries = append(entries, integrationrecord.Entry{Alias: alias, BaseBranch: "main", BaseCommit: head, SourceBranch: "cross", SourceCommit: head, Receipt: "pr:https://example.test/pull/1"})
				}
				record := integrationrecord.NewPending(st.Change, "pr", "main", entries)
				record.Status = integrationrecord.StatusComplete
				if err := integrationrecord.Save(ontoDir, record); err != nil {
					t.Fatal(err)
				}
				if !ontostate.ArchiveIntegrationComplete(ontoDir, st) {
					t.Fatal("legacy provenance no longer accepts completed combined-layout integration")
				}
				archive := filepath.Join(root, "docs", "changes", "archive", "2026-09-07-cross")
				if err := os.MkdirAll(filepath.Dir(archive), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(ontoDir, archive); err != nil {
					t.Fatal(err)
				}
				if _, err := run(t, "complete-integration", "cross", "--repo", "api", "--receipt", "pr:https://example.test/pull/1", "--dir", root); err != nil {
					t.Fatalf("combined-layout integration retry: %v", err)
				}
				writeFile(t, filepath.Join(root, "homonto.toml"), strings.Replace(config, "../api", "../replacement", 1))
				if message := runFail(t, "complete-integration", "cross", "--repo", "api", "--receipt", "pr:https://example.test/pull/1", "--dir", root); !strings.Contains(message, "identity") {
					t.Fatalf("integration retry ignored authority substitution: %s", message)
				}
			})
		}
	}
}

func TestLegacyProvenanceUsesCombinedIntegrationRecord(t *testing.T) {
	st := ontostate.State{Change: "cross", Phase: "close", Integration: "pr", BaseBranch: "main", RepoMode: "legacy", Repos: []string{"api"}, RepoBases: map[string]ontostate.RepoBase{
		"": {GitCommonDir: "/config/.git"}, "api": {GitCommonDir: "/api/.git"},
	}}
	entries := []integrationrecord.Entry{}
	for _, alias := range []string{"", "api"} {
		entries = append(entries, integrationrecord.Entry{Alias: alias, BaseBranch: "main", BaseCommit: strings.Repeat("a", 40), SourceBranch: "cross", SourceCommit: strings.Repeat("b", 40)})
	}
	record := integrationrecord.NewPending(st.Change, "pr", "main", entries)
	if err := validateIntegrationRecord(st, record); err != nil {
		t.Fatal(err)
	}
	record.Repositories = record.Repositories[1:]
	if err := validateIntegrationRecord(st, record); err == nil {
		t.Fatal("legacy provenance dropped implicit config integration")
	}
}

func TestDemoteExplicitSourcesRoundTripAndGates(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "Conversion Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "convert@example.test")
	t.Setenv("GIT_COMMITTER_NAME", "Conversion Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "convert@example.test")
	root := t.TempDir()
	for alias, branch := range map[string]string{"a": "main", "b": "develop"} {
		dir := filepath.Join(root, "source-"+alias)
		writeFile(t, filepath.Join(dir, "tracked"), alias+" base\n")
		runGit(t, dir, "init", "-b", branch)
		commitAll(t, dir, "base "+alias)
	}
	writeFile(t, filepath.Join(root, "homonto.toml"), "schema_version=2\n[frameworks.onto]\nsource='builtin:onto'\nscope='project'\n[workflow]\nroot='records/deep/tree'\ngit='existing'\n[repos]\na='source-a'\nb='source-b'\n")
	if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", "onto"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "records", "deep", "tree", "README.md"), "records\n")
	runGit(t, filepath.Join(root, "records", "deep", "tree"), "init", "-b", "records")
	l, err := workspace.LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "new", "cross", "--repo", "a,b", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	ontoDir := filepath.Join(l.WorkflowRoot, "changes", "cross")
	toDir := filepath.Join(l.WorkflowRoot, "tasks", "cross")
	statePath := filepath.Join(ontoDir, "onto-state.yaml")
	original := readFile(t, statePath)
	before, err := ontostate.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "demote", "cross", "--yes", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	st, err := tostate.Load(filepath.Join(toDir, tostate.FileName))
	if err != nil || st.Validate() != nil || st.SchemaVersion != 1 || st.ID != before.ID || st.RepoMode != "explicit" || !reflect.DeepEqual(st.Repos, before.Repos) || len(st.RepoBases) != 2 {
		t.Fatalf("demoted: %+v %v", st, err)
	}
	for alias, base := range before.RepoBases {
		if st.RepoBases[alias] != tostate.RepoBase(base) {
			t.Fatalf("lost source %s: %+v", alias, st.RepoBases)
		}
	}
	toRun(t, false, "promote", "cross", "--yes", "--dir", l.ConfigRoot)
	if got := readFile(t, statePath); got != original {
		t.Fatalf("inverse state differs:\n%s", got)
	}
	// Commit the outer wrapper after it acquires unrelated Git history. Neither
	// it nor the records repository may replace either selected source anchor.
	runGit(t, l.ConfigRoot, "init", "-b", "wrapper")
	writeFile(t, filepath.Join(l.ConfigRoot, "outer.txt"), "wrapper only\n")
	runGit(t, l.ConfigRoot, "add", "outer.txt")
	runGit(t, l.ConfigRoot, "commit", "-m", "outer wrapper")
	writeFile(t, filepath.Join(ontoDir, "proposal.md"), "edited proposal\n")
	if _, err := run(t, "demote", "cross", "--yes", "--dir", l.ConfigRoot); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(toDir, "plan.md"), "edited plan\n")
	toRun(t, false, "promote", "cross", "--yes", "--dir", l.ConfigRoot)
	after, err := ontostate.Load(statePath)
	if err != nil || after.ID != before.ID || after.RepoMode != "explicit" || !reflect.DeepEqual(after.RepoBases, before.RepoBases) {
		t.Fatalf("edited conversion changed provenance: %+v %v", after, err)
	}
	if entries := listDirEntries(t, filepath.Join(ontoDir, ".workflow", "events")); len(entries) != 4 {
		t.Fatalf("history: %v", entries)
	}
	for _, alias := range []string{"a", "b"} {
		writeFile(t, filepath.Join(l.Repos[alias], "tracked"), "uncommitted source\n")
	}
	dirt, err := stateWorktreeDirt(l.ConfigRoot, after)
	if err != nil || len(dirt) != 2 {
		t.Fatalf("source audit: %+v %v", dirt, err)
	}
	if message := scopedDirtGateError(dirt, "cross"); !strings.Contains(message, "a:") || !strings.Contains(message, "b:") {
		t.Fatalf("conversion dropped a source gate: %s", message)
	}
	if _, err := os.Stat(filepath.Join(l.ConfigRoot, "docs", "changes", "cross")); !os.IsNotExist(err) {
		t.Fatalf("conversion used default root: %v", err)
	}
}

// runFail executes the root command and returns the error text; the command
// must fail.
func runFail(t *testing.T, args ...string) string {
	t.Helper()
	_, err := run(t, args...)
	if err == nil {
		t.Fatalf("execute %v = nil, want error", args)
	}
	return err.Error()
}

// TestDemotePhaseAwareMapping: open/design demote to phase plan (nothing
// claimable); build with translatable tasks continues at phase do with a
// doctor-clean carried plan; build without parseable tasks restarts at plan.
func TestDemotePhaseAwareMapping(t *testing.T) {
	for _, tc := range []struct {
		phase string
		tasks string
		want  string
	}{
		{"open", canonicalTasks, "plan"},
		{"design", canonicalTasks, "plan"},
		{"build", canonicalTasks, "do"},
		{"verify", canonicalTasks, "do"},
		{"build", "# Tasks\n\nnothing parseable\n", "plan"},
	} {
		dir := setUpGatedWorkspace(t)
		seedDemotable(t, dir, "shrinker", tc.phase, tc.tasks)
		if tc.tasks == canonicalTasks {
			writeFile(t, filepath.Join(dir, "docs", "changes", "shrinker", "plan.md"), fmt.Sprintf("## Task 1.1 - Ship the parser\n- Owner: implementer\n- Repo: legacy config source\n- Cwd: `%s`\n- Files: parser.go\n- Do: Implement the parser\n- Verify: `go test ./parser`\n\n## Task 1.2 - Pin the fixtures\n- Owner: coordinator\n- Repo: legacy config source\n- Cwd: `%s`\n- Files: parser_test.go\n- Do: Validate parser fixtures\n- Verify: `go test ./...`\n", dir, dir))
		}
		if _, err := run(t, "demote", "shrinker", "--dir", dir, "--yes"); err != nil {
			t.Fatalf("demote(%s): %v", tc.phase, err)
		}
		st := readFile(t, filepath.Join(dir, "docs", "tasks", "shrinker", "to-state.yaml"))
		if !strings.Contains(st, "phase: "+tc.want) {
			t.Fatalf("demote(%s) phase = %q, want %q (state: %q)", tc.phase, st, tc.want, st)
		}
		if tc.want == "do" {
			plan := readFile(t, filepath.Join(dir, "docs", "tasks", "shrinker", "plan.md"))
			for _, needle := range []string{"- [ ] #1 ship the parser", "- [x] #2 pin the fixtures", "Final Verify:"} {
				if !strings.Contains(plan, needle) {
					t.Fatalf("translated plan missing %q:\n%s", needle, plan)
				}
			}
		}
	}
}

// TestDemoteCreatesToWorkspace: demotion moves the source whole into the
// neutral control plane, generates the fresh workspace, and prints the
// complementary next steps.
func TestDemoteCreatesToWorkspace(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", canonicalTasks)

	out, err := run(t, "demote", "shrinker", "--dir", dir, "--yes")
	if err != nil {
		t.Fatalf("demote: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "demoted shrinker") || !strings.Contains(out, "complementary") {
		t.Fatalf("demote output = %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "shrinker")); err == nil {
		t.Fatal("source workspace still under docs/changes")
	}
	toDir := filepath.Join(dir, "docs", "tasks", "shrinker")
	for _, f := range []string{"to-state.yaml", "plan.md"} {
		if _, err := os.Stat(filepath.Join(toDir, f)); err != nil {
			t.Fatalf("demoted change missing %s: %v", f, err)
		}
	}
	// The snapshotted onto state is byte-identical history.
	snapOnto := findFile(t, filepath.Join(toDir, ".workflow", "snapshots"), "onto-state.yaml")
	if !strings.Contains(readFile(t, snapOnto), "change: shrinker") {
		t.Fatal("snapshotted onto state must keep the original bytes")
	}
	if !strings.Contains(readFile(t, filepath.Join(toDir, ".workflow", "lineage.json")), "\"currentWorkflow\": \"to\"") {
		t.Fatal("lineage must name to as current")
	}
}

// TestDemoteRequiresYesRefusesCollisionsAndTerminals: --yes is required;
// an existing target is refused; closed, abandoned, and unknown sources are
// refused — and no staging survives any refusal.
func TestDemoteRequiresYesRefusesCollisionsAndTerminals(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", canonicalTasks)

	if msg := runFail(t, "demote", "shrinker", "--dir", dir); !strings.Contains(msg, "--yes") {
		t.Fatalf("demote without --yes must fail: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")

	// Target collision — including the to-reserved "archive".
	os.MkdirAll(filepath.Join(dir, "docs", "tasks", "shrinker"), 0o755)
	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing to overwrite") {
		t.Fatalf("existing target must be refused: %q", msg)
	}
	os.RemoveAll(filepath.Join(dir, "docs", "tasks", "shrinker"))
	if msg := runFail(t, "demote", "shrinker", "--as", "archive", "--dir", dir, "--yes"); !strings.Contains(msg, "reserved") {
		t.Fatalf("reserved target name must be refused: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")

	// Unknown source.
	if msg := runFail(t, "demote", "ghost", "--dir", dir, "--yes"); !strings.Contains(msg, "no such") {
		t.Fatalf("unknown source must fail: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")

	// Closed source is terminal.
	stPath := filepath.Join(dir, "docs", "changes", "shrinker", "onto-state.yaml")
	st, _ := ontostate.Load(stPath)
	st.Phase = "close"
	ontostate.Save(stPath, st)
	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "closed") {
		t.Fatalf("closed change must be refused: %q", msg)
	}

	// Abandoned source is terminal too.
	st, _ = ontostate.Load(stPath)
	st.Phase = "build"
	st.Abandoned = true
	ontostate.Save(stPath, st)
	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "terminal") {
		t.Fatalf("abandoned change must be refused: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")
}

// TestDemoteStateIdentityMustMatch: a copied workspace whose state names
// another change is refused before anything moves.
func TestDemoteStateIdentityMustMatch(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", "")
	writeFile(t, filepath.Join(dir, "docs", "changes", "shrinker", "onto-state.yaml"), "change: impostor\nphase: open\n")
	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "impostor") {
		t.Fatalf("state/directory mismatch must be refused: %q", msg)
	}
	assertNoStaging(t, dir, ".onto-demote")
}

// TestDemoteIdempotentRetryAndUnrelatedTarget: a completed demotion retries
// as a receipt-verified success; an unrelated existing target is a refusal,
// never a fake completion.
func TestDemoteIdempotentRetryAndUnrelatedTarget(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", canonicalTasks)
	if _, err := run(t, "demote", "shrinker", "--dir", dir, "--yes"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "demote", "shrinker", "--dir", dir, "--yes"); err != nil { // receipt-verified retry
		t.Fatal(err)
	}

	os.MkdirAll(filepath.Join(dir, "docs", "tasks", "bystander"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "tasks", "bystander", "to-state.yaml"), []byte("change: bystander\nphase: plan\n"), 0o644)
	if msg := runFail(t, "demote", "ghost", "--as", "bystander", "--dir", dir, "--yes"); !strings.Contains(msg, "no such") {
		t.Fatalf("unknown source must be refused, got: %q", msg)
	}
	// A live onto source aimed at the occupied target is a collision.
	seedDemotable(t, dir, "alive", "open", "")
	if msg := runFail(t, "demote", "alive", "--as", "bystander", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing to overwrite") {
		t.Fatalf("occupied target must be refused, got: %q", msg)
	}
}

// TestDemoteTamperedStagingRefused: staging whose snapshot does not hash to
// the manifest is refused.
func TestDemoteTamperedStagingRefused(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", canonicalTasks)

	base := filepath.Join(dir, "docs", ".onto-demote")
	os.MkdirAll(base, 0o755)
	stg := filepath.Join(base, "deadbeef")
	os.MkdirAll(filepath.Join(stg, "work", ".workflow", "snapshots", "deadbeef", "onto"), 0o755)
	os.WriteFile(filepath.Join(stg, "manifest.json"), []byte(`{"schemaVersion":1,"kind":"convert","direction":"demote","source":"shrinker","target":"shrinker","operationId":"deadbeef","lineage":{"schemaVersion":1,"lineageId":"lin","created":"2026-09-04","currentWorkflow":"to"},"sourcePhase":"open","targetIdentity":{"phase":"plan","created":"2026-09-04"},"sourceDigest":"sha256:x","sourceHashes":{"onto-state.yaml":"00"}}`), 0o644)

	if msg := runFail(t, "demote", "shrinker", "--dir", dir, "--yes"); !strings.Contains(msg, "refusing") && !strings.Contains(msg, "tampered") {
		t.Fatalf("tampered staging must be refused: %q", msg)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "tasks", "shrinker")); err == nil {
		t.Fatal("refused recovery must not install a target")
	}
}

// TestPromoteThenDemoteRestores (ADR 0042): converting back with nothing
// changed restores the original `to` workspace byte-for-byte and appends a
// restore event; converting back AFTER an edit is a fresh conversion, not a
// restore.
func TestPromoteThenDemoteRestores(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	// Both frameworks declared and applied: complementarity is the premise.
	writeFile(t, filepath.Join(dir, "homonto.toml"),
		"[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n[frameworks.to]\nsource=\"builtin:to\"\nscope=\"project\"\n")
	os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "to"), 0o755)

	toRun(t, false, "new", "grower", "--dir", dir)
	toRun(t, false, "phase", "grower", "--dir", dir)
	planPath := filepath.Join(dir, "docs", "tasks", "grower", "plan.md")
	os.WriteFile(planPath, []byte("# plan\n- [ ] #1 the work\n  - Files: `x.go`\n  - Change: the work\n  - Verify: `go test ./...`\nFinal Verify: `go test ./...`\n"), 0o644)
	originalPlan := readFile(t, planPath)

	toRun(t, false, "promote", "grower", "--dir", dir, "--yes")
	if _, err := run(t, "demote", "grower", "--dir", dir, "--yes"); err != nil {
		t.Fatalf("demote after promote: %v", err)
	}

	// The original workspace is back: phase do, identical plan bytes.
	if st := readFile(t, filepath.Join(dir, "docs", "tasks", "grower", "to-state.yaml")); !strings.Contains(st, "phase: do") {
		t.Fatalf("restored state must be the original do phase: %q", st)
	}
	if got := readFile(t, planPath); got != originalPlan {
		t.Fatalf("restored plan differs:\n got: %q\nwant: %q", got, originalPlan)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "grower")); err == nil {
		t.Fatal("demoted onto workspace still present")
	}
	lin := readFile(t, filepath.Join(dir, "docs", "tasks", "grower", ".workflow", "lineage.json"))
	if !strings.Contains(lin, "\"currentWorkflow\": \"to\"") {
		t.Fatalf("lineage must name to after restore: %s", lin)
	}
	if events := listDirEntries(t, filepath.Join(dir, "docs", "tasks", "grower", ".workflow", "events")); len(events) != 2 {
		t.Fatalf("promote + restore events expected, got %v", events)
	}

	// A second round trip after an edit is a fresh conversion: promote
	// (fresh, since the restore discarded the onto bytes), edit the onto
	// proposal, then demote — the edited onto bytes are snapshotted, not
	// discarded.
	toRun(t, false, "promote", "grower", "--dir", dir, "--yes")
	writeFile(t, filepath.Join(dir, "docs", "changes", "grower", "proposal.md"), "# Proposal: grower\n\nedited after promotion\n")
	if _, err := run(t, "demote", "grower", "--dir", dir, "--yes"); err != nil {
		t.Fatalf("demote after edit: %v", err)
	}
	snapProposal := findFile(t, filepath.Join(dir, "docs", "tasks", "grower", ".workflow", "snapshots"), "proposal.md")
	if got := readFile(t, snapProposal); !strings.Contains(got, "edited after promotion") {
		t.Fatalf("edited source must be snapshotted, got %q", got)
	}
	// The edited demote is a normal conversion: phase comes from mapping
	// (open -> plan), not a restore to do.
	if st := readFile(t, filepath.Join(dir, "docs", "tasks", "grower", "to-state.yaml")); !strings.Contains(st, "phase: plan") {
		t.Fatalf("edited round trip must map open -> plan, got %q", st)
	}
}

// TestDemoteAcceptsEitherFramework pins the bridge gate: demotion works with
// [frameworks.onto] applied (the change lives there) and with
// [frameworks.to] applied (the destination framework, ADR 0042).
func TestDemoteAcceptsEitherFramework(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	seedDemotable(t, dir, "shrinker", "open", "")
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.to]\nsource=\"builtin:to\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "to"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "demote", "shrinker", "--dir", dir, "--yes"); err != nil {
		t.Fatalf("demote under to-applied config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "tasks", "shrinker", "to-state.yaml")); err != nil {
		t.Fatalf("to state missing: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func findFile(t *testing.T, root, name string) string {
	t.Helper()
	var found string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == name {
			found = p
		}
		return nil
	})
	if found == "" {
		t.Fatalf("no %s under %s", name, root)
	}
	return found
}

func listDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func assertNoStaging(t *testing.T, dir, name string) {
	t.Helper()
	if entries := listDirEntries(t, filepath.Join(dir, "docs", name)); len(entries) != 0 {
		t.Fatalf("failed precondition must leave no %s staging, got %v", name, entries)
	}
}
