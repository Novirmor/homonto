package tocli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func explicitScopeWorkspace(t *testing.T, mode string) workspace.Layout {
	t.Helper()
	base := t.TempDir()
	root := filepath.Join(base, "control")
	for _, alias := range []string{"api", "web"} {
		initRepo(t, filepath.Join(base, alias))
	}
	writeFile(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf(`schema_version = 2
[frameworks.to]
source = "builtin:to"
scope = "project"
[workflow]
root = "records"
git = %q
[worktrees]
dir = "../execution"
[repos]
api = "../api"
web = "../web"
`, mode))
	if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", "skills", "to"), 0o755); err != nil {
		t.Fatal(err)
	}
	l, err := workspace.LoadRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "managed" {
		t.Setenv("GIT_AUTHOR_NAME", "Scope Test")
		t.Setenv("GIT_AUTHOR_EMAIL", "scope@example.test")
		t.Setenv("GIT_COMMITTER_NAME", "Scope Test")
		t.Setenv("GIT_COMMITTER_EMAIL", "scope@example.test")
		if err := workspace.InitManaged(l); err != nil {
			t.Fatal(err)
		}
	} else {
		initRepo(t, l.WorkflowRoot)
	}
	return l
}

func TestExplicitScopeBoundCompletionPreservesDirtyOriginals(t *testing.T) {
	for _, mode := range []string{"existing", "managed"} {
		t.Run(mode, func(t *testing.T) {
			l := explicitScopeWorkspace(t, mode)
			originals := map[string]string{}
			for alias, dir := range l.Repos {
				writeFile(t, filepath.Join(dir, "tracked"), "original dirty work\n")
				git(t, dir, "add", "tracked")
				writeFile(t, filepath.Join(dir, "untracked"), "keep this too\n")
				for _, arg := range []string{"HEAD", ":tracked"} {
					out, err := repoGitOutput(dir, "rev-parse", arg)
					if err != nil {
						t.Fatal(err)
					}
					originals[alias+arg] = string(out)
				}
			}
			run(t, false, "new", "scoped", "--repo", "web,api", "--dir", l.ConfigRoot)
			st, err := tostate.Load(statePath(l.ConfigRoot, "scoped"))
			if err != nil || st.RepoMode != tostate.SourceExplicit || st.ID == "" || !reflect.DeepEqual(st.Repos, []string{"api", "web"}) || len(st.RepoBases) != 2 {
				t.Fatalf("new state = %+v, %v", st, err)
			}
			execution := map[string]string{}
			for _, alias := range st.Repos {
				w, err := workspace.CreateWorktree(l, "to", "scoped", alias, "HEAD", "to-scoped")
				if err != nil {
					t.Fatal(err)
				}
				execution[alias] = w.Path
			}
			out := run(t, false, "handoff", "scoped", "--json", "--dir", l.ConfigRoot)
			var pack struct {
				SourceDirs map[string]string `json:"sourceDirs"`
				NextArgv   []string          `json:"nextArgv"`
			}
			if err := json.Unmarshal([]byte(out), &pack); err != nil || !reflect.DeepEqual(pack.SourceDirs, execution) {
				t.Fatalf("handoff source context = %s, %v", out, err)
			}
			if pack.NextArgv[len(pack.NextArgv)-1] != l.ConfigRoot {
				t.Fatalf("handoff lost control context: %v", pack.NextArgv)
			}
			run(t, false, "doctor", "--dir", l.ConfigRoot)
			run(t, false, "handoff", "scoped", "--write", "--dir", l.ConfigRoot)
			packs, err := filepath.Glob(filepath.Join(changeDir(l.ConfigRoot, "scoped"), ".to", "handoff", "*.json"))
			if err != nil || len(packs) != 1 {
				t.Fatalf("written handoff = %v, %v", packs, err)
			}
			data, err := os.ReadFile(packs[0])
			if err != nil || json.Unmarshal(data, &pack) != nil || !reflect.DeepEqual(pack.SourceDirs, execution) {
				t.Fatalf("written source context = %s, %v", data, err)
			}
			run(t, false, "phase", "scoped", "--dir", l.ConfigRoot)
			for _, dir := range execution {
				writeFile(t, filepath.Join(dir, "bound dirty file"), "preserve and commit\n")
			}
			out = runErr(t, "done", "scoped", "--verified", "--dir", l.ConfigRoot)
			for _, want := range []string{"api", "web", "bound dirty file", "preserve or isolate", "explicitly choose cleanup"} {
				if !strings.Contains(out, want) {
					t.Fatalf("dirt report lacks %q: %s", want, out)
				}
			}
			for _, dir := range execution {
				git(t, dir, "add", "bound dirty file")
				git(t, dir, "-c", "user.name=Scope Test", "-c", "user.email=scope@example.test", "commit", "-m", "preserve execution work")
			}
			run(t, false, "done", "scoped", "--verified", "--dir", l.ConfigRoot)
			st, err = tostate.Load(filepath.Join(findArchived(l.ConfigRoot, "scoped"), tostate.FileName))
			if err != nil || st.Phase != tostate.PhaseDone || st.RepoMode != tostate.SourceExplicit {
				t.Fatalf("archived state = %+v, %v", st, err)
			}
			for alias, dir := range l.Repos {
				for file, want := range map[string]string{"tracked": "original dirty work\n", "untracked": "keep this too\n"} {
					data, err := os.ReadFile(filepath.Join(dir, file))
					if err != nil || string(data) != want {
						t.Fatalf("original %s/%s changed: %s, %v", alias, file, data, err)
					}
				}
				for _, arg := range []string{"HEAD", ":tracked"} {
					out, err := repoGitOutput(dir, "rev-parse", arg)
					if err != nil || string(out) != originals[alias+arg] {
						t.Fatalf("original %s %s changed: %s, %v", alias, arg, out, err)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(l.ConfigRoot, ".git")); !os.IsNotExist(err) {
				t.Fatalf("control directory acquired Git: %v", err)
			}
		})
	}
}

func TestExplicitScopeRequiresSelectionBeforeWrites(t *testing.T) {
	l := explicitScopeWorkspace(t, "existing")
	for _, repos := range [][]string{nil, {"--repo", "missing"}, {"--repo", "api,api"}} {
		args := append([]string{"new", "invalid", "--dir", l.ConfigRoot}, repos...)
		runErr(t, args...)
		if _, err := os.Stat(changeDir(l.ConfigRoot, "invalid")); !os.IsNotExist(err) {
			t.Fatalf("invalid selection created state: %v", err)
		}
	}
}

func TestExplicitScopeRefusesChangedAuthorityAndCorruption(t *testing.T) {
	for _, failure := range []string{"missing-alias", "missing-source", "alias-substitution", "config-corrupt", "config-future", "config-downgrade", "state-future", "state-identity", "registry-corrupt", "registry-identity"} {
		t.Run(failure, func(t *testing.T) {
			l := explicitScopeWorkspace(t, "existing")
			run(t, false, "new", "guarded", "--repo", "api", "--dir", l.ConfigRoot)
			run(t, false, "phase", "guarded", "--dir", l.ConfigRoot)
			stateFile := statePath(l.ConfigRoot, "guarded")
			config, err := os.ReadFile(l.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "missing-alias":
				writeFile(t, l.ConfigPath, strings.ReplaceAll(string(config), "api = \"../api\"\n", ""))
			case "missing-source":
				if err := os.Rename(l.Repos["api"], l.Repos["api"]+"-moved"); err != nil {
					t.Fatal(err)
				}
			case "alias-substitution":
				replacement := filepath.Join(filepath.Dir(l.ConfigRoot), "replacement")
				initRepo(t, replacement)
				writeFile(t, l.ConfigPath, strings.ReplaceAll(string(config), "../api", "../replacement"))
			case "config-corrupt":
				writeFile(t, l.ConfigPath, "not valid TOML [")
			case "config-future":
				writeFile(t, l.ConfigPath, strings.ReplaceAll(string(config), "schema_version = 2", "schema_version = 999"))
			case "config-downgrade":
				writeFile(t, l.ConfigPath, "schema_version = 1\n[frameworks.to]\nsource = 'builtin:to'\nscope = 'project'\n[workflow]\nroot = 'records'\n[repos]\napi = '../api'\n")
			case "state-future":
				writeFile(t, statePath(l.ConfigRoot, "guarded"), "schema_version: 999\nchange: guarded\nphase: do\n")
			case "state-identity":
				st, err := tostate.Load(stateFile)
				if err != nil {
					t.Fatal(err)
				}
				st.Change = "substituted"
				if err := tostate.Save(stateFile, st); err != nil {
					t.Fatal(err)
				}
			case "registry-corrupt":
				writeFile(t, filepath.Join(l.ConfigRoot, ".homonto", "worktrees.json"), "{bad registry")
			case "registry-identity":
				w, err := workspace.CreateWorktree(l, "to", "guarded", "api", "HEAD", "guarded")
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(l.ConfigRoot, ".homonto", "worktrees.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, strings.ReplaceAll(string(data), w.StateID, "id:different-change"))
			}
			before, err := os.ReadFile(stateFile)
			if err != nil {
				t.Fatal(err)
			}
			runErr(t, "done", "guarded", "--verified", "--dir", l.ConfigRoot)
			runErr(t, "handoff", "guarded", "--json", "--dir", l.ConfigRoot)
			after, err := os.ReadFile(stateFile)
			if err != nil || string(before) != string(after) {
				t.Fatalf("failure changed state: %s, %v", after, err)
			}
		})
	}
}

func TestExplicitExistingRecordsExcludedOnlyWithinSource(t *testing.T) {
	l := explicitScopeWorkspace(t, "existing")
	data, err := os.ReadFile(l.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, l.ConfigPath, strings.ReplaceAll(string(data), "root = \"records\"", "root = \"../api/records\""))
	for _, alias := range []string{"api", "web"} {
		writeFile(t, filepath.Join(l.Repos[alias], "records", "old-note.md"), "old record\n")
		git(t, l.Repos[alias], "add", "-A")
		git(t, l.Repos[alias], "commit", "-m", "existing records")
	}
	l, err = workspace.LoadRoot(l.ConfigRoot)
	if err != nil {
		t.Fatal(err)
	}
	run(t, false, "new", "records", "--repo", "api,web", "--dir", l.ConfigRoot)
	run(t, false, "phase", "records", "--dir", l.ConfigRoot)
	writeFile(t, filepath.Join(l.WorkflowRoot, "old-note.md"), "record changed\n")
	st, err := tostate.Load(statePath(l.ConfigRoot, "records"))
	if err != nil {
		t.Fatal(err)
	}
	if err := requireCleanScope(l.ConfigRoot, st); err != nil {
		t.Fatalf("existing records blocked source gate: %v", err)
	}
	w, err := workspace.CreateWorktree(l, "to", "records", "api", "HEAD", "records")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(w.Path, "records", "old-note.md"), "bound record changed\n")
	if err := requireCleanScope(l.ConfigRoot, st); err != nil {
		t.Fatalf("bound records blocked source gate: %v", err)
	}
	writeFile(t, filepath.Join(l.Repos["web"], "records", "old-note.md"), "this is source, not workflow records\n")
	if err := requireCleanScope(l.ConfigRoot, st); err == nil || !strings.Contains(err.Error(), "web") {
		t.Fatalf("same-named source directory ignored: %v", err)
	}
	git(t, w.Path, "mv", "tracked", "records/moved-source")
	if err := requireCleanScope(l.ConfigRoot, st); err == nil || !strings.Contains(err.Error(), "tracked") {
		t.Fatalf("rename into records hid source deletion: %v", err)
	}
}

func TestLegacyScopeCannotBeReinterpretedByConfigUpgrade(t *testing.T) {
	l := explicitScopeWorkspace(t, "existing")
	st := tostate.State{Change: "legacy", Phase: tostate.PhaseDo, Repos: []string{"api"}}
	if err := requireCleanScope(l.ConfigRoot, st); err == nil || !strings.Contains(err.Error(), "scope changed") {
		t.Fatalf("legacy scoped state reinterpreted under v2: %v", err)
	}
	st.Repos = nil
	if err := requireCleanScope(l.ConfigRoot, st); err != nil {
		t.Fatalf("legacy unscoped change no longer git-blind: %v", err)
	}
	st.SchemaVersion, st.RepoMode = tostate.CurrentSchemaVersion, tostate.SourceLegacy
	writeFile(t, l.ConfigPath, "corrupt TOML [")
	if err := requireCleanScope(l.ConfigRoot, st); err != nil {
		t.Fatalf("unscoped gate inspected config: %v", err)
	}
}

func TestNewLegacyScopeCapturesImplicitConfig(t *testing.T) {
	root := setUpGatedWorkspace(t)
	initRepo(t, root)
	api := filepath.Join(t.TempDir(), "api")
	initRepo(t, api)
	writeFile(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("[frameworks.to]\nsource = 'builtin:to'\nscope = 'project'\n[repos]\napi = %q\n", api))
	run(t, false, "new", "legacy", "--repo", "api", "--dir", root)
	st, err := tostate.Load(statePath(root, "legacy"))
	if err != nil || st.RepoMode != tostate.SourceLegacy || len(st.RepoBases) != 2 || st.RepoBases[""].GitCommonDir != filepath.Join(root, ".git") {
		t.Fatalf("legacy provenance = %+v, %v", st, err)
	}
	if err := requireCleanScope(root, st); err == nil || !strings.Contains(err.Error(), "config repo") {
		t.Fatalf("legacy lost implicit config gate: %v", err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "preserve legacy config and records")
	run(t, false, "phase", "legacy", "--dir", root)
	run(t, false, "done", "legacy", "--verified", "--dir", root)
}

func TestExplicitScopeIgnoresUnselectedDirt(t *testing.T) {
	l := explicitScopeWorkspace(t, "existing")
	writeFile(t, filepath.Join(l.Repos["web"], "unrelated"), "leave alone\n")
	run(t, false, "new", "selected", "--repo", "api", "--dir", l.ConfigRoot)
	run(t, false, "phase", "selected", "--dir", l.ConfigRoot)
	run(t, false, "done", "selected", "--verified", "--dir", l.ConfigRoot)
}

func TestExplicitNewRejectsInvalidSourcesAndConfig(t *testing.T) {
	for _, failure := range []string{"non-git-source", "corrupt", "future"} {
		t.Run(failure, func(t *testing.T) {
			l := explicitScopeWorkspace(t, "existing")
			switch failure {
			case "non-git-source":
				if err := os.Rename(filepath.Join(l.Repos["api"], ".git"), filepath.Join(l.Repos["api"], "git-backup")); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				writeFile(t, l.ConfigPath, "not TOML [")
			case "future":
				writeFile(t, l.ConfigPath, "schema_version = 999\n[frameworks.to]\nsource = 'builtin:to'\n")
			}
			runErr(t, "new", "invalid", "--repo", "api", "--dir", l.ConfigRoot)
			if _, err := os.Stat(filepath.Join(l.WorkflowRoot, "tasks", "invalid")); !os.IsNotExist(err) {
				t.Fatalf("invalid new created state: %v", err)
			}
		})
	}
}
