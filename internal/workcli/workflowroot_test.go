package workcli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/schema"
	"github.com/noviopenworks/homonto/internal/workflowroot"
)

func TestSchemaTwoMarkerRejectsSymlinks(t *testing.T) {
	for _, kind := range []string{"dangling leaf", "existing leaf", "parent"} {
		t.Run(kind, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte("schema_version=2\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, ".homonto")
			link := filepath.Join(dir, workflowroot.LayoutMarkerFile)
			target := filepath.Join(outside, workflowroot.LayoutMarkerFile)
			if kind == "parent" {
				link = dir
				if err := os.Symlink(outside, link); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if kind == "existing leaf" {
					if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			}
			if err := MarkWorkflowState(root); err == nil {
				t.Fatal("accepted symlinked marker path")
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("symlink replaced: %v, %v", info, err)
			}
			if kind == "existing leaf" {
				if data, err := os.ReadFile(target); err != nil || string(data) != "untouched" {
					t.Fatalf("target changed: %q, %v", data, err)
				}
			} else if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("external target created: %v", err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if kind == "existing leaf" {
				want = 1
			}
			if len(entries) != want {
				t.Fatalf("external directory changed: %v", entries)
			}
		})
	}
}

func TestSchemaTwoMarkerUnchangedIsNotRewritten(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte("schema_version=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MarkWorkflowState(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".homonto", workflowroot.LayoutMarkerFile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	then := time.Unix(1000000000, 0)
	if err := os.Chtimes(path, then, then); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := MarkWorkflowState(root); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, after) || !info.ModTime().Equal(after.ModTime()) {
		t.Fatal("unchanged marker was rewritten")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != string(before) {
		t.Fatalf("marker changed: %q, %v", data, err)
	}
}

func TestSchemaTwoExternalWorkflowBoundary(t *testing.T) {
	base := t.TempDir()
	root, records := filepath.Join(base, "control"), filepath.Join(base, "records home")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	config := "schema_version=2\n[workflow]\nroot='../records home'\n"
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	// A missing records directory must remain a valid, read-only target.
	if err := ValidateWorkflowPath(root, filepath.Join(records, "tasks", "new")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(records); !os.IsNotExist(err) {
		t.Fatalf("validation wrote records: %v", err)
	}
	if err := MarkWorkflowState(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(records, "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := WorkflowRoot(root); err != nil || got != records {
		t.Fatalf("WorkflowRoot = %q, %v", got, err)
	}
	for _, path := range []string{filepath.Join(root, "docs", "tasks"), filepath.Join(base, "other", "task"), filepath.Join(records, "..", "escape"), filepath.Join(base, "records home suffix", "task")} {
		if err := ValidateWorkflowPath(root, path); err == nil {
			t.Fatalf("accepted outside path %q", path)
		}
	}
	if err := os.Symlink(root, filepath.Join(records, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkflowPath(root, filepath.Join(records, "escape", "state")); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink escape: %v", err)
	}
	if err := os.Symlink(filepath.Join(records, "tasks"), filepath.Join(records, "inside")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkflowPath(root, filepath.Join(records, "inside", "new")); err != nil {
		t.Fatalf("internal symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join(base, "missing"), filepath.Join(records, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkflowPath(root, filepath.Join(records, "dangling", "new")); err == nil {
		t.Fatal("dangling symlink accepted")
	}
}

func TestSchemaTwoMarkerPreservesLegacyMarker(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "homonto.toml")
	if err := os.WriteFile(path, []byte("schema_version=2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".homonto"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, ".homonto", "workflow-root")
	if err := os.WriteFile(legacy, []byte("docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := MarkWorkflowState(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := MarkWorkflowState(root); err != nil {
		t.Fatalf("matching ownership: %v", err)
	}
	if data, err := os.ReadFile(legacy); err != nil || string(data) != "docs\n" {
		t.Fatalf("legacy marker changed: %q, %v", data, err)
	}
	for _, body := range []string{"schema_version=1\n", "schema_version=2\n[workflow]\ngit='managed'\n", "schema_version=2\n[workflow]\nroot='new'\n"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := ValidateWorkflowRootChange(root); err == nil {
			t.Fatalf("layout reinterpretation accepted: %s", body)
		}
	}
}

func TestGateSchemaForwardSafetyAndMinimalParsing(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "homonto.toml")
	if err := os.WriteFile(path, []byte("schema_version=3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := onto.Gate(root); !errors.Is(err, schema.ErrTooNew) {
		t.Fatalf("future gate = %v", err)
	}
	if _, err := WorkflowRoot(root); !errors.Is(err, schema.ErrTooNew) {
		t.Fatalf("future root = %v", err)
	}
	if err := ValidateWorkflowPath(root, filepath.Join(root, "docs")); !errors.Is(err, schema.ErrTooNew) {
		t.Fatalf("future path = %v", err)
	}
	// Workflow helpers must not decode unrelated projection configuration.
	if err := os.WriteFile(path, []byte("schema_version=2\n[frameworks.onto]\nsource=42\n[subagents.broken]\nsource=42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".homonto", "catalog", onto.SkillsDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := onto.Gate(root); err != nil {
		t.Fatalf("minimal schema-2 gate: %v", err)
	}
	for _, version := range []int{0, 1} {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("schema_version=%d\n[workflow]\ngit=''\n", version)), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := WorkflowRoot(root); err == nil || !strings.Contains(err.Error(), "schema_version=2") {
			t.Fatalf("legacy fields: %v", err)
		}
	}
}

func TestValidateWorkflowRootChangeUsesSharedDiagnostics(t *testing.T) {
	for _, kind := range []string{"both roots", "marker directory", "legacy root file", "absent config", "omitted root"} {
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
				if err := os.MkdirAll(filepath.Join(dir, "old", "records", "changes"), 0o755); err != nil {
					t.Fatal(err)
				}
				if kind == "legacy root file" {
					if err := os.WriteFile(filepath.Join(dir, "docs"), nil, 0o644); err != nil {
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
			wantRel := "new/records"
			// Deliberately invalid projection settings must not engage config.Load.
			config := "[subagents.example]\nsource = 42\n"
			if kind == "absent config" || kind == "omitted root" {
				wantRel = "docs"
			} else {
				config += "[workflow]\nroot = \"new/./records\"\n"
			}
			if kind != "absent config" {
				if err := os.WriteFile(filepath.Join(dir, "homonto.toml"), []byte(config), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want := workflowroot.ValidateChange(dir, wantRel)
			cwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			relRepo, err := filepath.Rel(cwd, dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, repo := range []string{dir, relRepo} {
				err := ValidateWorkflowRootChange(repo)
				if want == nil || err == nil || err.Error() != want.Error() {
					t.Fatalf("ValidateWorkflowRootChange(%q) = %v, want shared diagnostic: %v", repo, err, want)
				}
				if kind == "marker directory" || kind == "legacy root file" {
					var got, cause *os.PathError
					if !errors.As(err, &got) || !errors.As(want, &cause) || got.Path != cause.Path || !errors.Is(err, cause.Err) {
						t.Fatalf("validator did not preserve underlying filesystem error: %v", err)
					}
				}
			}
			if _, err := os.Lstat(filepath.Join(dir, "new")); !os.IsNotExist(err) {
				t.Fatalf("validation created selected root: %v", err)
			}
		})
	}
}
