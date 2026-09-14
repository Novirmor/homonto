package convert

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func provenanceGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func provenanceWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}

func provenanceWorkspace(t *testing.T) (workspace.Layout, tostate.State) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "Conversion Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "convert@example.test")
	t.Setenv("GIT_COMMITTER_NAME", "Conversion Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "convert@example.test")
	root := t.TempDir()
	st := tostate.State{SchemaVersion: 1, ID: "stable-id", Change: "cross", Phase: "do", RepoMode: "explicit", Repos: []string{"a", "b"}, RepoBases: map[string]tostate.RepoBase{}}
	for alias, branch := range map[string]string{"a": "main", "b": "develop"} {
		dir := filepath.Join(root, alias)
		provenanceWrite(t, filepath.Join(dir, "tracked"), alias)
		provenanceGit(t, dir, "init", "-b", branch)
		provenanceGit(t, dir, "add", ".")
		provenanceGit(t, dir, "commit", "-m", "base")
		st.RepoBases[alias] = tostate.RepoBase{
			GitCommonDir: filepath.Join(dir, ".git"),
			BaseRef:      provenanceGit(t, dir, "rev-parse", "HEAD"),
			BaseBranch:   branch,
		}
	}
	provenanceWrite(t, filepath.Join(root, "homonto.toml"), "schema_version=2\n[workflow]\nroot='records/deep/tree'\ngit='managed'\n[worktrees]\ndir='execution'\n[repos]\na='a'\nb='b'\n")
	l, err := workspace.LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := workspace.InitManaged(l); err != nil {
		t.Fatal(err)
	}
	if err := tostate.Save(filepath.Join(l.WorkflowRoot, "tasks", st.Change, tostate.FileName), st); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(l.WorkflowRoot, "changes"), 0755); err != nil {
		t.Fatal(err)
	}
	return l, st
}

func TestExplicitProvenanceConversionAndInverse(t *testing.T) {
	for _, direction := range []string{Promote, Demote} {
		t.Run(direction, func(t *testing.T) {
			l, st := provenanceWorkspace(t)
			ops := testOps()
			ontoDir := filepath.Join(l.WorkflowRoot, "changes", "cross")
			toDir := filepath.Join(l.WorkflowRoot, "tasks", "cross")
			bases, err := promotionBases(l.ConfigRoot, toDir)
			if err != nil {
				t.Fatal(err)
			}
			for alias, base := range bases {
				if base.BaseRef != provenanceGit(t, l.Repos[alias], "rev-parse", "HEAD") || base.GitCommonDir != st.RepoBases[alias].GitCommonDir {
					t.Fatalf("base %s: %+v", alias, base)
				}
			}
			if bases["a"].BaseBranch != "main" || bases["b"].BaseBranch != "develop" {
				t.Fatalf("targets: %+v", bases)
			}
			src, inverse := toDir, Demote
			if direction == Demote {
				if err := os.RemoveAll(toDir); err != nil {
					t.Fatal(err)
				}
				ost := ontostate.State{Change: "cross", ID: st.ID, Workflow: "full", Phase: "open", Repos: st.Repos, RepoMode: "explicit", RepoBases: bases}
				if err := ontostate.Save(filepath.Join(ontoDir, "onto-state.yaml"), ost); err != nil {
					t.Fatal(err)
				}
				src, inverse = ontoDir, Promote
			}
			before, err := digestActive(src)
			if err != nil {
				t.Fatal(err)
			}
			created, err := Run(direction, l.ConfigRoot, "cross", "cross", ops)
			if err != nil {
				t.Fatal(err)
			}
			if direction == Promote {
				got, err := ontostate.LoadChange(created)
				if err != nil || got.Validate() != nil || got.SchemaVersion != 3 || got.ID != st.ID || got.RepoMode != "explicit" || !reflect.DeepEqual(got.Repos, st.Repos) || !reflect.DeepEqual(got.RepoBases, bases) {
					t.Fatalf("promoted: %+v %v", got, err)
				}
			} else {
				got, err := tostate.Load(filepath.Join(created, tostate.FileName))
				if err != nil || got.Validate() != nil || got.SchemaVersion != 1 || got.ID != st.ID || got.RepoMode != "explicit" || !reflect.DeepEqual(got.Repos, st.Repos) {
					t.Fatalf("demoted: %+v %v", got, err)
				}
				for alias, base := range bases {
					if got.RepoBases[alias] != tostate.RepoBase(base) {
						t.Fatalf("lost base %s: %+v", alias, got)
					}
				}
			}
			if _, err := Run(inverse, l.ConfigRoot, "cross", "cross", ops); err != nil {
				t.Fatal(err)
			}
			after, err := digestActive(src)
			if err != nil || before != after {
				t.Fatalf("inverse changed active bytes: %s != %s: %v", before, after, err)
			}
			lin, _, err := loadLineage(src)
			if err != nil || len(lin.Events) != 2 {
				t.Fatalf("inverse history: %+v %v", lin, err)
			}
		})
	}
}

func TestPromotionPinsAnchorsAcrossResumeAndEditedConversions(t *testing.T) {
	l, st := provenanceWorkspace(t)
	src := filepath.Join(l.WorkflowRoot, "tasks", st.Change)
	ops := testOps()
	stg, m, _, err := findOrStage(specs[Promote], l.ConfigRoot, l.WorkflowRoot, src, st.Change, st.Change, ops)
	if err != nil {
		t.Fatal(err)
	}
	if err := buildWork(specs[Promote], filepath.Join(stg, "work"), src, m); err != nil {
		t.Fatal(err)
	}
	for _, dir := range l.Repos {
		provenanceGit(t, dir, "commit", "--allow-empty", "-m", "later")
		provenanceGit(t, dir, "switch", "-c", "later")
	}
	created, err := Run(Promote, l.ConfigRoot, st.Change, st.Change, ops)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ontostate.LoadChange(created)
	if err != nil || !reflect.DeepEqual(got.RepoBases, m.TargetIdent.RepoBases) {
		t.Fatalf("resume recaptured bases: %+v %v", got, err)
	}
	// Edits force generation instead of the immediate inverse snapshot path.
	for i, direction := range []string{Demote, Promote, Demote, Promote} {
		provenanceWrite(t, filepath.Join(created, "notes.md"), "edited")
		created, err = Run(direction, l.ConfigRoot, st.Change, st.Change, ops)
		if err != nil {
			t.Fatalf("conversion %d: %v", i, err)
		}
	}
	got, err = ontostate.LoadChange(created)
	if err != nil || got.ID != st.ID || !reflect.DeepEqual(got.RepoBases, m.TargetIdent.RepoBases) {
		t.Fatalf("edited history lost provenance: %+v %v", got, err)
	}
	lin, _, err := loadLineage(created)
	if err != nil || len(lin.Events) != 5 {
		t.Fatalf("history: %+v %v", lin, err)
	}
	seen := map[string]bool{}
	for _, id := range lin.Events {
		if seen[id] {
			t.Fatalf("duplicate event %s", id)
		}
		seen[id] = true
	}
	for path, value := range provenanceTree(t, filepath.Join(created, controlDir, snapshotsDir)) {
		if value != "directory" && strings.Count(path, controlDir) != 1 {
			t.Fatalf("nested history: %s", path)
		}
	}
}

func TestPromotionRefusesProvenanceLossBeforeWrites(t *testing.T) {
	for _, kind := range []string{"scope-model-changed", "missing-alias", "identity", "base", "branch", "missing-base-ref", "missing-base-branch", "future", "unknown-mode", "unknown-field"} {
		t.Run(kind, func(t *testing.T) {
			l, st := provenanceWorkspace(t)
			src := filepath.Join(l.WorkflowRoot, "tasks", st.Change)
			path := filepath.Join(src, tostate.FileName)
			switch kind {
			case "scope-model-changed":
				st.RepoMode = "legacy"
				st.RepoBases[""] = tostate.RepoBase{GitCommonDir: filepath.Join(l.ConfigRoot, ".git")}
			case "missing-alias":
				st.Repos[1] = "missing"
				st.RepoBases["missing"] = st.RepoBases["b"]
				delete(st.RepoBases, "b")
			case "identity":
				st.RepoBases["b"] = st.RepoBases["a"]
			case "base":
				base := st.RepoBases["b"]
				base.BaseRef = strings.Repeat("0", 40)
				st.RepoBases["b"] = base
			case "branch":
				base := st.RepoBases["b"]
				base.BaseBranch = "missing"
				st.RepoBases["b"] = base
			}
			if err := tostate.Save(path, st); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing-base-ref":
				data = []byte(strings.Replace(string(data), "base_ref: "+st.RepoBases["b"].BaseRef, "base_ref: \"\"", 1))
			case "missing-base-branch":
				data = []byte(strings.Replace(string(data), "base_branch: "+st.RepoBases["b"].BaseBranch, "base_branch: \"\"", 1))
			case "future":
				data = []byte(strings.Replace(string(data), "schema_version: 1", "schema_version: 99", 1))
			case "unknown-mode":
				data = []byte(strings.Replace(string(data), "repo_mode: explicit", "repo_mode: future", 1))
			case "unknown-field":
				data = append(data, []byte("new_authority: unknown\n")...)
			}
			provenanceWrite(t, path, string(data))
			before := provenanceTree(t, l.ConfigRoot)
			_, err = Run(Promote, l.ConfigRoot, st.Change, st.Change, testOps())
			if err == nil {
				t.Fatal("unsafe conversion succeeded")
			}
			if strings.HasPrefix(kind, "missing-base-") && !strings.Contains(err.Error(), "captured at creation") {
				t.Fatalf("missing anchor error is unclear: %v", err)
			}
			if !reflect.DeepEqual(before, provenanceTree(t, l.ConfigRoot)) {
				t.Fatal("refusal changed files")
			}
			if _, err := os.Stat(filepath.Join(l.WorkflowRoot, ".to-promote")); !os.IsNotExist(err) {
				t.Fatalf("refusal left staging: %v", err)
			}
		})
	}
}

func TestConversionRefusesBoundWorktreesIncludingInverseAndResume(t *testing.T) {
	for _, kind := range []string{"promote", "demote", "inverse", "resume", "invalid-registry", "registry-lock"} {
		t.Run(kind, func(t *testing.T) {
			l, st := provenanceWorkspace(t)
			direction, workflow := Promote, "to"
			ops := testOps()
			if kind == "demote" || kind == "inverse" {
				created, err := Run(Promote, l.ConfigRoot, st.Change, st.Change, ops)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "demote" {
					provenanceWrite(t, filepath.Join(created, "notes.md"), "edited")
				}
				direction, workflow = Demote, "onto"
			}
			if kind == "registry-lock" {
				provenanceWrite(t, filepath.Join(l.ConfigRoot, ".homonto", "worktrees.lock"), "pid=123\n")
			} else {
				if _, err := workspace.CreateWorktree(l, workflow, st.Change, "a", "main", "change/cross"); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "resume" {
				src := filepath.Join(l.WorkflowRoot, "tasks", st.Change)
				stg, m, _, err := findOrStage(specs[Promote], l.ConfigRoot, l.WorkflowRoot, src, st.Change, st.Change, ops)
				if err != nil {
					t.Fatal(err)
				}
				if err := buildWork(specs[Promote], filepath.Join(stg, "work"), src, m); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "invalid-registry" {
				provenanceWrite(t, filepath.Join(l.ConfigRoot, ".homonto", "worktrees.json"), "{}")
			}
			// Include history and registry in the byte comparison.
			before := provenanceTree(t, l.ConfigRoot)
			_, err := Run(direction, l.ConfigRoot, st.Change, st.Change, ops)
			if err == nil || !strings.Contains(err.Error(), "worktree") || !strings.Contains(err.Error(), "hand off") {
				t.Fatalf("binding refusal: %v", err)
			}
			if !reflect.DeepEqual(before, provenanceTree(t, l.ConfigRoot)) {
				t.Fatal("binding refusal modified workspace")
			}
		})
	}
}

func provenanceTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && filepath.Base(path) == ".homonto" {
			entries, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			if len(entries) == 1 && entries[0].Name() == "lock-guardians" && entries[0].IsDir() {
				return filepath.SkipDir
			}
		}
		if d.IsDir() && filepath.Base(path) == "lock-guardians" && (filepath.Base(filepath.Dir(path)) == ".homonto" || filepath.Base(filepath.Dir(path)) == ".git") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			out[path] = "directory"
			return nil
		}
		data, err := os.ReadFile(path)
		out[path] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLegacyConversionPreservesAbsenceAndReceiptIdentity(t *testing.T) {
	root := t.TempDir()
	seedToChange(t, root, "old")
	ops := testOps()
	created, err := Run(Promote, root, "old", "old", ops)
	if err != nil {
		t.Fatal(err)
	}
	st, err := ontostate.LoadChange(created)
	if err != nil || st.ID == "" || st.RepoMode != "" || len(st.RepoBases) != 0 {
		t.Fatalf("legacy promotion: %+v %v", st, err)
	}
	provenanceWrite(t, filepath.Join(created, "notes.md"), "edited")
	created, err = Run(Demote, root, "old", "old", ops)
	if err != nil {
		t.Fatal(err)
	}
	tst, err := tostate.Load(filepath.Join(created, tostate.FileName))
	if err != nil || tst.RepoMode != "" || tst.SchemaVersion != 0 || len(tst.RepoBases) != 0 || tst.ID != st.ID {
		t.Fatalf("legacy demotion: %+v %v", tst, err)
	}
	// Simulate a shipped demotion, which stored identity only in its receipt.
	tst.ID = ""
	if err := tostate.Save(filepath.Join(created, tostate.FileName), tst); err != nil {
		t.Fatal(err)
	}
	created, err = Run(Promote, root, "old", "renamed", ops)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ontostate.LoadChange(created)
	if err != nil || got.ID != st.ID {
		t.Fatalf("receipt identity lost: %+v %v", got, err)
	}
}

func TestUnversionedScopedConversionKeepsLegacyScope(t *testing.T) {
	l, st := provenanceWorkspace(t)
	st.SchemaVersion, st.RepoMode, st.RepoBases = 0, "", nil
	path := filepath.Join(l.WorkflowRoot, "tasks", st.Change, tostate.FileName)
	if err := tostate.Save(path, st); err != nil {
		t.Fatal(err)
	}
	ops := testOps()
	created, err := Run(Promote, l.ConfigRoot, st.Change, st.Change, ops)
	if err != nil {
		t.Fatal(err)
	}
	ost, err := ontostate.LoadChange(created)
	if err != nil || ost.RepoMode != "" || len(ost.RepoBases) != 0 || !reflect.DeepEqual(ost.Repos, st.Repos) {
		t.Fatalf("config upgraded legacy scope: %+v %v", ost, err)
	}
	provenanceWrite(t, filepath.Join(created, "notes.md"), "edited")
	created, err = Run(Demote, l.ConfigRoot, st.Change, st.Change, ops)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tostate.Load(filepath.Join(created, tostate.FileName))
	if err != nil || got.SchemaVersion != 0 || got.RepoMode != "" || len(got.RepoBases) != 0 || !reflect.DeepEqual(got.Repos, st.Repos) {
		t.Fatalf("demotion upgraded legacy scope: %+v %v", got, err)
	}
}

func TestLegacyScopedPromotionDoesNotRequireGitAnchors(t *testing.T) {
	base := t.TempDir()
	root, api := filepath.Join(base, "config"), filepath.Join(base, "api")
	for _, dir := range []string{root, api} {
		provenanceWrite(t, filepath.Join(dir, "tracked"), "uncommitted\n")
		provenanceGit(t, dir, "init", "-b", "main")
	}
	provenanceWrite(t, filepath.Join(root, "homonto.toml"), "schema_version=1\n[repos]\napi='../api'\n")
	seedToChange(t, root, "cross")
	st := tostate.State{SchemaVersion: 1, Change: "cross", ID: "stable-id", Phase: "plan", RepoMode: "legacy", Repos: []string{"api"}, RepoBases: map[string]tostate.RepoBase{
		"": {GitCommonDir: filepath.Join(root, ".git")}, "api": {GitCommonDir: filepath.Join(api, ".git")},
	}}
	if err := tostate.Save(filepath.Join(root, "docs", "tasks", "cross", tostate.FileName), st); err != nil {
		t.Fatal(err)
	}
	created, err := Run(Promote, root, "cross", "cross", testOps())
	if err != nil {
		t.Fatalf("legacy promotion requires commits or clean sources: %v", err)
	}
	got, err := ontostate.LoadChange(created)
	if err != nil || got.Validate() != nil || got.ID != st.ID || got.RepoMode != "legacy" || len(got.RepoBases) != 2 {
		t.Fatalf("legacy promotion: %+v %v", got, err)
	}
	for alias, b := range st.RepoBases {
		if got.RepoBases[alias] != ontostate.RepoBase(b) {
			t.Fatalf("invented legacy anchors for %q: %+v", alias, got.RepoBases)
		}
	}
}

func TestDemotionRefusesUnsupportedSourceFormatsBeforeWrites(t *testing.T) {
	for _, fields := range []string{
		"schema_version: 99\n",
		"schema_version: 3\nrepo_mode: future\n",
		"schema_version: 2\nrepo_mode: explicit\nrepos: [a, b]\n",
		"schema_version: 3\nrepo_mode: explicit\nrepos: [a, b]\n",
	} {
		t.Run(strings.ReplaceAll(fields, "\n", "/"), func(t *testing.T) {
			root := t.TempDir()
			provenanceWrite(t, filepath.Join(root, "docs", "changes", "cross", "onto-state.yaml"), "change: cross\nphase: open\n"+fields)
			before := provenanceTree(t, root)
			if _, err := Run(Demote, root, "cross", "cross", testOps()); err == nil {
				t.Fatal("unsupported format accepted")
			}
			if !reflect.DeepEqual(before, provenanceTree(t, root)) {
				t.Fatal("refusal changed files")
			}
		})
	}
}
