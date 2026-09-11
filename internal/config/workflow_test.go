package config

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/noviopenworks/homonto/internal/workflowroot"
	"github.com/noviopenworks/homonto/internal/workspace"
)

func TestWorkflowLayoutParserParity(t *testing.T) {
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{"schema_version=2\n", true},
		{"schema_version=2\n[workflow]\nroot='work/./discard/../records'\n", true},
		{"schema_version=2\n[workflow]\nroot='../external records'\ngit='existing'\n[worktrees]\ndir='../trees'\n", true},
		{"schema_version=2\n[workflow]\nroot='.'\n", false},
		{"schema_version=2\n[workflow]\nroot='/'\n", false},
		{"schema_version=2\n[workflow]\nroot='.homonto/records'\n", false},
		{"schema_version=2\n[workflow]\ngit=''\n", false},
		{"schema_version=2\n[workflow]\ngit='auto'\n", false},
		{"schema_version=2\n[worktrees]\ndir='docs/trees'\n", false},
		{"schema_version=2\n[repos]\nfake='.'\n", false},
		{"schema_version=2\n[repos]\n'1'='.'\n", false},
		{"schema_version=1\n[workflow]\ngit='existing'\n", false},
		{"[worktrees]\n", false},
		{"schema_version=3\n", false},
		{"schema_version=-1\n", false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			path := writeConfig(t, tc.body)
			c, configErr := Load(path)
			l, layoutErr := workspace.Load(path)
			root, cliErr := workcli.WorkflowRoot(filepath.Dir(path))
			for name, err := range map[string]error{"config": configErr, "workspace": layoutErr, "workcli": cliErr} {
				if (err == nil) != tc.valid {
					t.Fatalf("%s = %v, want valid=%v", name, err, tc.valid)
				}
			}
			if tc.valid && (root != l.WorkflowRoot || c.Workflow.GitOrDefault() != l.GitMode) {
				t.Fatalf("parsers disagree: %q, %+v, %+v", root, c.Workflow, l)
			}
		})
	}
}

func TestSchemaTwoDoesNotReinterpretLegacyRecords(t *testing.T) {
	for _, old := range []string{"docs", "records"} {
		t.Run(old, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, old, "tasks", "archive"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, ".homonto"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, ".homonto", "workflow-root"), []byte(old+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "homonto.toml")
			if err := os.WriteFile(path, []byte("schema_version=2\n[workflow]\nroot="+strconv.Quote(old)+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "reinterpretation") {
				t.Fatalf("config accepted legacy records: %v", err)
			}
			if _, err := workcli.WorkflowRoot(root); err == nil || !strings.Contains(err.Error(), "reinterpretation") {
				t.Fatalf("workcli accepted legacy records: %v", err)
			}
		})
	}
}

func TestWorkflowRootDefaultsAndNormalizes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "homonto.toml")
	if err := os.WriteFile(path, []byte("[workflow]\nroot = \"work/./records\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Workflow.RootOrDefault(); got != filepath.Join("work", "records") {
		t.Fatalf("workflow root = %q, want %q", got, filepath.Join("work", "records"))
	}
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Workflow.RootOrDefault(); got != "docs" {
		t.Fatalf("default workflow root = %q, want docs", got)
	}
}

func TestWorkflowRootRejectsOutsideConfigRepo(t *testing.T) {
	for _, root := range []string{"../workflow", "/var/workflow", "."} {
		err := loadDoc(t, "[workflow]\nroot = \""+root+"\"\n")
		if err == nil || !strings.Contains(err.Error(), "workflow.root") {
			t.Errorf("root %q error = %v, want workflow.root rejection", root, err)
		}
	}
}

func TestWorkflowRootRejectsConfigRootAliases(t *testing.T) {
	for _, prefix := range []string{"", "schema_version=1\n", "schema_version=2\n"} {
		for _, root := range []string{".", "./", "docs/..", " ./ ", "work/records/../.."} {
			t.Run(prefix+root, func(t *testing.T) {
				err := loadDoc(t, prefix+"[workflow]\nroot = "+strconv.Quote(root)+"\n")
				if err == nil || !strings.Contains(err.Error(), "workflow.root") {
					t.Fatalf("root %q error = %v, want workflow.root rejection", root, err)
				}
			})
		}
	}
}

func TestWorkflowRootRejectsEscapingSymlinks(t *testing.T) {
	for _, tc := range []struct {
		name string
		root string
		link string
	}{
		{name: "root", root: "workflow", link: "workflow"},
		{name: "parent", root: "workflow/records", link: "workflow"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			outside := t.TempDir()
			if err := os.Symlink(outside, filepath.Join(dir, tc.link)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			path := filepath.Join(dir, "homonto.toml")
			if err := os.WriteFile(path, []byte("[workflow]\nroot = \""+tc.root+"\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "resolves outside") {
				t.Fatalf("Load = %v, want escaping symlink rejection", err)
			}
		})
	}
}

func TestWorkflowRootMarkerUsesSlashNormalizedPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workflow", "records", "changes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".homonto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".homonto", "workflow-root"), []byte("workflow/records\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "homonto.toml")
	if err := os.WriteFile(path, []byte("[workflow]\nroot = \"workflow/records\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load = %v, want matching marker accepted", err)
	}
}

func TestWorkflowRootChangeWithExistingDefaultStateFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs", "changes"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "homonto.toml")
	if err := os.WriteFile(path, []byte("[workflow]\nroot = \"workflow\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "while workflow state exists") {
		t.Fatalf("load = %v, want fail-closed root-change error", err)
	}
}

func TestWorkflowRootChangeUsesSharedDiagnostics(t *testing.T) {
	for _, kind := range []string{"both roots", "marker directory", "old root file"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, ".homonto", "workflow-root")
			if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
				t.Fatal(err)
			}
			if kind == "marker directory" {
				if err := os.Mkdir(marker, 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(marker, []byte("old/records\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(dir, "old"), 0o755); err != nil {
					t.Fatal(err)
				}
				if kind == "old root file" {
					if err := os.WriteFile(filepath.Join(dir, "old", "records"), nil, 0o644); err != nil {
						t.Fatal(err)
					}
				} else {
					for _, root := range []string{"old/records", "docs"} {
						for _, name := range []string{"changes", "tasks", ".to-promote", ".onto-demote"} {
							if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(root), name), 0o755); err != nil {
								t.Fatal(err)
							}
						}
					}
				}
			}
			path := filepath.Join(dir, "homonto.toml")
			if err := os.WriteFile(path, []byte("[workflow]\nroot = \"new/./records\"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			want := workflowroot.ValidateChange(dir, "new/records")
			_, err := Load(path)
			if want == nil || err == nil || !strings.HasSuffix(err.Error(), "parse config: "+want.Error()) || !strings.Contains(err.Error(), strconv.Quote(path)) {
				t.Fatalf("Load = %v, want config filename %q and shared diagnostic suffix: %v", err, path, want)
			}
			if kind != "both roots" {
				var got, cause *os.PathError
				if !errors.As(err, &got) || !errors.As(want, &cause) || got.Path != cause.Path || !errors.Is(err, cause.Err) {
					t.Fatalf("Load did not preserve underlying filesystem error: %v", err)
				}
			}
			if _, err := os.Lstat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
				t.Fatalf("Load created selected root: %v", err)
			}
		})
	}
}
