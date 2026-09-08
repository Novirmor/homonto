package tocli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/bypasslog"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
)

func auditSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += target
		} else if !d.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += string(data)
		}
		out[rel] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func auditChangedPaths(before, after map[string]string) []string {
	var paths []string
	for path, value := range before {
		if other, ok := after[path]; !ok || value != other {
			paths = append(paths, path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func TestAuditChangedPaths(t *testing.T) {
	before := map[string]string{"same": "secret", "removed": "", ".git/config": "old secret", "link": "old target", "mode": "-rw-------"}
	after := map[string]string{"same": "secret", "added": "", ".git/config": "new secret", "link": "new target", "mode": "-rw-r--r--"}
	want := []string{".git/config", "added", "link", "mode", "removed"}
	if got := auditChangedPaths(before, after); !reflect.DeepEqual(got, want) {
		t.Fatalf("changed paths = %q, want %q", got, want)
	}
	if got := auditChangedPaths(before, before); len(got) != 0 {
		t.Fatalf("unchanged snapshot reported paths: %q", got)
	}
}

func TestAuditFixtureDisablesAutomaticMaintenance(t *testing.T) {
	base := t.TempDir()
	config := filepath.Join(base, "gitconfig")
	writeFile(t, config, "[gc]\nauto = 1\nautoDetach = true\n[maintenance]\nauto = true\nautoDetach = true\n[maintenance \"commit-graph\"]\nenabled = true\nauto = -1\n[maintenance \"loose-objects\"]\nenabled = true\nauto = 1\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	trace := filepath.Join(base, "git-trace")
	t.Setenv("GIT_TRACE", trace)
	repo := filepath.Join(base, "repo")
	initRepo(t, repo)
	git(t, repo, "config", "--local", "--get", "gc.auto", "^0$")
	git(t, repo, "config", "--local", "--get", "maintenance.auto", "^false$")
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "commit -m init") {
		t.Fatal("Git trace did not capture the fixture commit")
	}
	if strings.Contains(string(data), "maintenance run") || strings.Contains(string(data), "gc --auto") {
		t.Fatal("fixture commit launched automatic maintenance")
	}
}

func TestAuditIdentityMismatchPreservesBothChanges(t *testing.T) {
	for _, tc := range []struct {
		phase string
		args  []string
	}{
		{tostate.PhasePlan, []string{"phase", "a"}},
		{tostate.PhasePlan, []string{"abandon", "a"}},
		{tostate.PhaseAbandoned, []string{"abandon", "a"}},
		{tostate.PhaseDo, []string{"done", "a", "--verified"}},
		{tostate.PhaseDone, []string{"done", "a", "--verified"}},
		{tostate.PhasePlan, []string{"bypass", "a", "--to", "do", "--reason", "test"}},
		{tostate.PhasePlan, []string{"bypass", "a", "--to", "archive", "--reason", "test"}},
		{tostate.PhasePlan, []string{"handoff", "a", "--write"}},
	} {
		t.Run(tc.args[0]+"-"+tc.phase+"-"+strings.Join(tc.args[2:], "-"), func(t *testing.T) {
			root := setUpGatedWorkspace(t)
			for _, name := range []string{"a", "b"} {
				writeFile(t, statePath(root, name), fmt.Sprintf("# preserve this formatting\nchange: b\nphase: %s\n", tc.phase))
				writeFile(t, planPath(root, name), "plan for "+name)
			}
			before := auditSnapshot(t, root)
			out := runErr(t, append(tc.args, "--dir", root)...)
			if !strings.Contains(out, "identity mismatch") {
				t.Fatalf("wrong refusal: %s", out)
			}
			if changed := auditChangedPaths(before, auditSnapshot(t, root)); len(changed) != 0 {
				t.Fatalf("identity failure changed paths: %q", changed)
			}
		})
	}
}

func TestAuditTerminalDestinationFailuresPreserveState(t *testing.T) {
	for _, layout := range []string{"legacy", "custom", "external-v2"} {
		for _, kind := range []string{"outside-symlink", "inside-config-symlink", "dangling", "non-directory", "name-too-long"} {
			for _, op := range []struct {
				phase string
				args  []string
			}{
				{tostate.PhaseDo, []string{"done", "--verified"}},
				{tostate.PhaseDone, []string{"done", "--verified"}},
				{tostate.PhasePlan, []string{"abandon"}},
				{tostate.PhaseAbandoned, []string{"abandon"}},
				{tostate.PhasePlan, []string{"bypass", "--to", "archive", "--reason", "test"}},
				{tostate.PhaseDone, []string{"bypass", "--to", "done", "--reason", "test"}},
			} {
				t.Run(layout+"/"+kind+"/"+op.args[0]+"-"+op.phase, func(t *testing.T) {
					root := setUpGatedWorkspace(t)
					if layout == "custom" {
						writeFile(t, filepath.Join(root, "homonto.toml"), "[frameworks.to]\n[workflow]\nroot = 'workflow/records'\n")
					} else if layout == "external-v2" {
						writeFile(t, filepath.Join(root, "homonto.toml"), fmt.Sprintf("schema_version = 2\n[frameworks.to]\n[workflow]\nroot = %q\ngit = 'existing'\n", filepath.Join(t.TempDir(), "records")))
					}
					if err := workcli.MarkWorkflowState(root); err != nil {
						t.Fatal(err)
					}
					name := "x"
					if kind == "name-too-long" {
						name = strings.Repeat("x", 245)
					}
					state := statePath(root, name)
					writeFile(t, state, fmt.Sprintf("# original bytes\nchange: %s\nphase: %s\nverified: true\nevidence: original evidence\nfinished: 2030-01-01\n", name, op.phase))
					outside := t.TempDir()
					if kind == "inside-config-symlink" {
						outside = filepath.Join(root, "not-records")
					}
					writeFile(t, filepath.Join(outside, "sentinel"), "keep outside unchanged\n")
					target := outside
					if kind == "dangling" {
						target = filepath.Join(outside, "missing")
					}
					if kind == "non-directory" {
						writeFile(t, archiveDir(root), "not a directory")
					} else if kind == "name-too-long" {
						if err := os.MkdirAll(archiveDir(root), 0o755); err != nil {
							t.Fatal(err)
						}
					} else {
						if err := os.Symlink(target, archiveDir(root)); err != nil {
							t.Fatal(err)
						}
					}
					before := auditSnapshot(t, tasksDir(root))
					external := auditSnapshot(t, outside)
					args := append([]string{op.args[0], name}, op.args[1:]...)
					runErr(t, append(args, "--dir", root)...)
					if !reflect.DeepEqual(before, auditSnapshot(t, tasksDir(root))) {
						t.Fatal("destination failure changed active state, audit log, or archive")
					}
					if !reflect.DeepEqual(external, auditSnapshot(t, outside)) {
						t.Fatal("destination failure changed outside files")
					}
				})
			}
		}
	}
}

func TestAuditPlantedParentsRefusedBeforeLocksAndWrites(t *testing.T) {
	for _, component := range []string{"docs", "tasks", "change", "state"} {
		for _, dangling := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dangling=%t", component, dangling), func(t *testing.T) {
				root := setUpGatedWorkspace(t)
				outside := t.TempDir()
				writeFile(t, filepath.Join(outside, "to-state.yaml"), "change: x\nphase: plan\n")
				writeFile(t, filepath.Join(outside, ".to.lock"), "pid=999999999\n")
				path := map[string]string{"docs": filepath.Join(root, "docs"), "tasks": tasksDir(root), "change": changeDir(root, "x"), "state": statePath(root, "x")}[component]
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				target := outside
				if component == "state" {
					target = filepath.Join(outside, "to-state.yaml")
				}
				if dangling {
					target = filepath.Join(outside, "missing")
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				before, external := auditSnapshot(t, root), auditSnapshot(t, outside)
				runErr(t, "phase", "x", "--dir", root)
				if !reflect.DeepEqual(before, auditSnapshot(t, root)) || !reflect.DeepEqual(external, auditSnapshot(t, outside)) {
					t.Fatal("planted parent caused writes or lock reclamation")
				}
			})
		}
	}
}

func TestAuditArchiveLookupExactIdentityAndNumericOrder(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct{ dir, change string }{
		{"2030-01-01-bar-foo", "bar-foo"},
		{"not-a-date-foo", "foo"},
		{"2030-01-01-foo-1", "foo"},
		{"2030-01-01-foo-02", "foo"},
		{"2030-01-01-foo-2", "foo-2"},
		{"2031-01-01-foo", "different"},
	} {
		writeFile(t, filepath.Join(archiveDir(root), tc.dir, tostate.FileName), fmt.Sprintf("change: %s\nphase: done\n", tc.change))
	}
	if got := findArchived(root, "foo"); got != "" {
		t.Fatalf("unrelated archive matched: %s", got)
	}
	for _, dir := range []string{"foo", "2030-01-01-foo", "2030-01-01-foo-9", "2030-01-01-foo-10"} {
		writeFile(t, filepath.Join(archiveDir(root), dir, tostate.FileName), "change: foo\nphase: done\n")
	}
	want := filepath.Join(archiveDir(root), "2030-01-01-foo-10")
	if got := findArchived(root, "foo"); got != want {
		t.Fatalf("newest = %q, want %q", got, want)
	}
	if _, err := loadChange(root, "foo"); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("missing archive diagnostic: %v", err)
	}
	writeFile(t, statePath(root, "foo"), "bad YAML: [")
	if _, err := loadChange(root, "foo"); err == nil || strings.Contains(err.Error(), "is archived") {
		t.Fatalf("corrupt active state was hidden by archive: %v", err)
	}
}

func TestAuditArchiveDestDanglingCollisionAndInvalidDate(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(archiveDir(root), "2030-01-01-x")
	if err := os.MkdirAll(archiveDir(root), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), first); err != nil {
		t.Fatal(err)
	}
	if got, err := archiveDest(root, "x", "2030-01-01"); err != nil || got != first+"-2" {
		t.Fatalf("dangling collision = %q, %v", got, err)
	}
	if _, err := archive(root, "x", first); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("dangling destination not protected: %v", err)
	}
	for _, date := range []string{"../escape", "2030-99-99", ""} {
		if _, err := archiveDest(root, "x", date); err == nil {
			t.Fatalf("accepted invalid date %q", date)
		}
	}
}

func TestAuditMissingArchiveParentDoesNotMaskDestinationError(t *testing.T) {
	root := setUpGatedWorkspace(t)
	name := strings.Repeat("x", 245)
	writeFile(t, statePath(root, name), "change: "+name+"\nphase: do\n")
	before := auditSnapshot(t, changeDir(root, name))
	runErr(t, "done", name, "--verified", "--dir", root)
	if !reflect.DeepEqual(before, auditSnapshot(t, changeDir(root, name))) {
		t.Fatal("missing archive parent masked a destination error until after the terminal write")
	}
}

func TestAuditDoneRecoveryReportsRecordedVerification(t *testing.T) {
	root := setUpGatedWorkspace(t)
	writeFile(t, statePath(root, "x"), "change: x\nphase: done\nverified: false\nevidence: kept\n")
	before := auditSnapshot(t, changeDir(root, "x"))
	out := run(t, false, "done", "x", "--verified", "--json", "--dir", root)
	var result struct {
		Verified *bool  `json:"verified"`
		Archived string `json:"archived"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil || result.Verified == nil || *result.Verified {
		t.Fatalf("recovery invented verification: %s, %v", out, err)
	}
	if !reflect.DeepEqual(before, auditSnapshot(t, result.Archived)) {
		t.Fatal("recovery rewrote recorded state")
	}
}

func TestAuditBypassExternalWorkflowRoot(t *testing.T) {
	for _, target := range []string{"do", "done", "archive"} {
		t.Run(target, func(t *testing.T) {
			l := explicitScopeWorkspace(t, "existing")
			data, err := os.ReadFile(l.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, l.ConfigPath, strings.ReplaceAll(string(data), `root = "records"`, `root = "../records"`))
			run(t, false, "new", "x", "--repo", "api", "--dir", l.ConfigRoot)
			sourceBefore := auditSnapshot(t, l.Repos["api"])
			run(t, false, "bypass", "x", "--to", target, "--reason", "external recovery", "--dir", l.ConfigRoot)
			path := changeDir(l.ConfigRoot, "x")
			if target != "do" {
				path = findArchived(l.ConfigRoot, "x")
			}
			if !strings.HasPrefix(path, filepath.Join(filepath.Dir(l.ConfigRoot), "records")) {
				t.Fatalf("wrong records root: %s", path)
			}
			sc, exists, err := bypasslog.Load(bypasslog.Path(path, "to"), "x", "to")
			if err != nil || !exists || len(sc.Records) != 1 || sc.Records[0].To != target {
				t.Fatalf("missing external audit: %+v, %v", sc, err)
			}
			st, err := tostate.Load(filepath.Join(path, tostate.FileName))
			if err != nil || st.Verified || (target == "do" && st.Phase != "do") || (target != "do" && st.Phase != "done") {
				t.Fatalf("wrong bypass state: %+v, %v", st, err)
			}
			if !reflect.DeepEqual(sourceBefore, auditSnapshot(t, l.Repos["api"])) {
				t.Fatal("bypass changed source repository")
			}
		})
	}
}

func TestAuditDiagnosticsRejectConfiguredFailures(t *testing.T) {
	for _, failure := range []string{"corrupt", "future", "invalid-root", "missing-source", "non-git-source", "dangling-root", "non-directory-root", "dangling-config"} {
		t.Run(failure, func(t *testing.T) {
			l := explicitScopeWorkspace(t, "existing")
			if err := toFramework.Gate(l.ConfigRoot); err != nil {
				t.Fatalf("valid fixture failed gate before corruption: %v", err)
			}
			data, err := os.ReadFile(l.ConfigPath)
			if err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "corrupt":
				writeFile(t, l.ConfigPath, "invalid TOML [")
			case "future":
				writeFile(t, l.ConfigPath, "schema_version = 999\n")
			case "invalid-root":
				writeFile(t, l.ConfigPath, strings.ReplaceAll(string(data), `root = "records"`, `root = "."`))
			case "missing-source":
				if err := os.Rename(l.Repos["api"], l.Repos["api"]+"-moved"); err != nil {
					t.Fatal(err)
				}
			case "non-git-source":
				if err := os.Rename(filepath.Join(l.Repos["api"], ".git"), filepath.Join(l.Repos["api"], "git-backup")); err != nil {
					t.Fatal(err)
				}
			case "dangling-root", "non-directory-root":
				if err := os.Rename(l.WorkflowRoot, l.WorkflowRoot+"-saved"); err != nil {
					t.Fatal(err)
				}
				if failure == "dangling-root" {
					if err := os.Symlink(l.WorkflowRoot+"-missing", l.WorkflowRoot); err != nil {
						t.Fatal(err)
					}
				} else {
					writeFile(t, l.WorkflowRoot, "not a directory")
				}
			case "dangling-config":
				if err := os.Rename(l.ConfigPath, l.ConfigPath+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(l.ConfigPath+"-missing", l.ConfigPath); err != nil {
					t.Fatal(err)
				}
			}
			before := auditSnapshot(t, filepath.Dir(l.ConfigRoot))
			for _, args := range [][]string{{"doctor"}, {"status", "--json"}, {"status", "--all"}} {
				runErr(t, append(args, "--dir", l.ConfigRoot)...)
			}
			if err := runJSON(t, "doctor", "--quiet", "--dir", l.ConfigRoot); !errors.Is(err, ErrQuietFindings) {
				t.Fatalf("quiet doctor error = %v", err)
			}
			if changed := auditChangedPaths(before, auditSnapshot(t, filepath.Dir(l.ConfigRoot))); len(changed) != 0 {
				t.Fatalf("diagnostics changed workspace paths: %q", changed)
			}
		})
	}
}

func TestAuditStatusSiblingErrorsAreVisible(t *testing.T) {
	root := t.TempDir()
	writeFile(t, statePath(root, "broken-task"), "bad YAML: [")
	writeFile(t, filepath.Join(root, "docs", "changes", "broken-onto", "state.yaml"), "bad YAML: [")
	out := run(t, false, "status", "--all", "--dir", root)
	for _, want := range []string{"broken-task\tinvalid\t", "broken-onto\tinvalid\t"} {
		if !strings.Contains(out, want) {
			t.Fatalf("hidden error %q: %s", want, out)
		}
	}
	jsonOut := run(t, false, "status", "--all", "--json", "--dir", root)
	var entries map[string][]statusEntry
	if err := json.Unmarshal([]byte(jsonOut), &entries); err != nil || len(entries["onto"]) != 1 || entries["onto"][0].Error == "" {
		t.Fatalf("JSON hid sibling error: %s, %v", jsonOut, err)
	}
	if err := os.Rename(filepath.Join(root, "docs", "changes"), filepath.Join(root, "docs", "saved")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "docs", "changes"), "not a directory")
	runErr(t, "status", "--all", "--dir", root)
	runErr(t, "doctor", "--dir", root)
}

func TestAuditHandoffSafeArgvAndControlDir(t *testing.T) {
	for _, plan := range []string{"", "# still planning\n", "- [ ] incomplete\n", "- [ ] implement\n  - Files: x.go\n  - Change: fix x\n  - Verify: go test\nFinal Verify: go test ./...\n"} {
		for _, mode := range []string{"text", "json", "write"} {
			t.Run(fmt.Sprintf("%d/%s", len(plan), mode), func(t *testing.T) {
				root := t.TempDir()
				writeFile(t, statePath(root, "x"), "change: x\nphase: plan\n")
				writeFile(t, planPath(root, "x"), plan)
				args := []string{"handoff", "x", "--dir", root}
				if mode != "text" {
					args = append(args, "--"+mode)
				}
				out := run(t, false, args...)
				if mode == "text" {
					if !strings.Contains(out, `"--dir" "`+root+`"`) {
						t.Fatalf("text lost control context: %s", out)
					}
					return
				}
				if mode == "write" {
					files, err := filepath.Glob(filepath.Join(changeDir(root, "x"), ".to", "handoff", "*.json"))
					if err != nil || len(files) != 1 {
						t.Fatalf("handoff files: %v, %v", files, err)
					}
					data, err := os.ReadFile(files[0])
					if err != nil {
						t.Fatal(err)
					}
					out = string(data)
				}
				var pack struct {
					NextArgv []string `json:"nextArgv"`
				}
				if err := json.Unmarshal([]byte(out), &pack); err != nil {
					t.Fatal(err)
				}
				want := []string{"to", "status", "--json", "--dir", root}
				if len(planContractFindings(plan)) == 0 {
					want = []string{"to", "phase", "x", "--dir", root}
				}
				if !reflect.DeepEqual(pack.NextArgv, want) {
					t.Fatalf("argv = %v, want %v", pack.NextArgv, want)
				}
			})
		}
	}
}
