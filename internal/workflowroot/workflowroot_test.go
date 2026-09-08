package workflowroot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLayoutMarkerRejectsUnsafePaths(t *testing.T) {
	for _, kind := range []string{"dangling leaf", "existing leaf", "parent", "dangling parent", "config root", "directory", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			repo, outside := t.TempDir(), t.TempDir()
			marker := LayoutMarker{SchemaVersion: 2, ConfigPath: filepath.Join(repo, "homonto.toml"), WorkflowRoot: filepath.Join(repo, "docs"), GitMode: "existing"}
			path := filepath.Join(repo, ".homonto", LayoutMarkerFile)
			target := filepath.Join(outside, LayoutMarkerFile)
			link := ""
			switch kind {
			case "parent", "dangling parent":
				link = filepath.Dir(path)
				destination := outside
				if kind == "dangling parent" {
					destination = filepath.Join(outside, "missing")
				}
				if err := os.Symlink(destination, link); err != nil {
					t.Fatal(err)
				}
			case "config root":
				alias := filepath.Join(repo, "alias")
				if err := os.Symlink(outside, alias); err != nil {
					t.Fatal(err)
				}
				link = alias
				marker.ConfigPath = filepath.Join(alias, "homonto.toml")
				path = filepath.Join(alias, ".homonto", LayoutMarkerFile)
			default:
				if err := os.Mkdir(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "directory":
					if err := os.Mkdir(path, 0o755); err != nil {
						t.Fatal(err)
					}
				case "corrupt":
					if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
						t.Fatal(err)
					}
				default:
					if kind == "existing leaf" {
						data, err := json.Marshal(marker)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(target, data, 0o600); err != nil {
							t.Fatal(err)
						}
					}
					link = path
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
			}
			before, readErr := os.ReadFile(target)
			for _, operation := range []struct {
				name string
				run  func() error
			}{
				{"read", func() error {
					return ValidateLayout(marker.ConfigPath, marker.WorkflowRoot, marker.GitMode, marker.SchemaVersion)
				}},
				{"write", func() error { return WriteLayoutMarker(marker) }},
			} {
				err := operation.run()
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", path)) {
					t.Fatalf("%s = %v, want rejected path %q", operation.name, err, path)
				}
			}
			if link != "" {
				if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("link replaced: %v, %v", info, err)
				}
			}
			if kind == "corrupt" {
				if data, err := os.ReadFile(path); err != nil || string(data) != "{broken" {
					t.Fatalf("corrupt marker replaced: %q, %v", data, err)
				}
			}
			if readErr == nil {
				if data, err := os.ReadFile(target); err != nil || string(data) != string(before) {
					t.Fatalf("external target changed: %q, %v", data, err)
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

func TestWriteLayoutMarkerIdempotenceAndMigration(t *testing.T) {
	repo := t.TempDir()
	marker := LayoutMarker{SchemaVersion: 2, ConfigPath: filepath.Join(repo, "homonto.toml"), WorkflowRoot: filepath.Join(repo, "docs"), GitMode: "existing"}
	path := filepath.Join(repo, ".homonto", LayoutMarkerFile)
	if err := WriteLayoutMarker(marker); err != nil {
		t.Fatal(err)
	}
	if err := ValidateLayout(marker.ConfigPath, marker.WorkflowRoot, marker.GitMode, marker.SchemaVersion); err != nil {
		t.Fatal(err)
	}
	// Semantic identity, not formatting, determines whether a rewrite is needed.
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	then := time.Unix(1000000000, 0)
	if err := os.Chtimes(path, then, then); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := WriteLayoutMarker(marker); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) || after.Mode().Perm() != 0o600 {
		t.Fatal("identical ownership was rewritten or permissions changed")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("marker formatting changed: %q, %v", got, err)
	}
	// Layout changes remain permitted only after the old records are relocated.
	marker.WorkflowRoot = filepath.Join(repo, "new")
	if err := os.MkdirAll(filepath.Join(repo, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteLayoutMarker(marker); err == nil {
		t.Fatal("reinterpreted old records")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("marker changed on rejected migration: %q, %v", got, err)
	}
	if err := os.Rename(filepath.Join(repo, "docs"), marker.WorkflowRoot); err != nil {
		t.Fatal(err)
	}
	if err := WriteLayoutMarker(marker); err == nil {
		t.Fatal("adopted unowned destination records")
	}
	if err := os.Remove(filepath.Join(marker.WorkflowRoot, "tasks")); err != nil {
		t.Fatal(err)
	}
	if err := WriteLayoutMarker(marker); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("atomic replacement loosened mode: %v, %v", info, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 || entries[0].Name() != LayoutMarkerFile {
		t.Fatalf("temporary files left behind: %v, %v", entries, err)
	}
}

func TestValidateLayoutOwnership(t *testing.T) {
	for _, change := range []string{"unchanged", "mode", "root", "schema", "config", "malformed", "relative ownership"} {
		t.Run(change, func(t *testing.T) {
			repo := t.TempDir()
			config := filepath.Join(repo, "homonto.toml")
			root := filepath.Join(t.TempDir(), "records")
			if err := os.MkdirAll(filepath.Join(root, "tasks", "archive"), 0o755); err != nil {
				t.Fatal(err)
			}
			marker := LayoutMarker{SchemaVersion: 2, ConfigPath: config, WorkflowRoot: root, GitMode: "existing"}
			if change == "relative ownership" {
				marker.WorkflowRoot = "records"
			}
			data, err := json.Marshal(marker)
			if err != nil {
				t.Fatal(err)
			}
			if change == "malformed" {
				data = []byte(`{"schema_version":3}`)
			}
			if err := os.Mkdir(filepath.Join(repo, ".homonto"), 0o755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(repo, ".homonto", LayoutMarkerFile)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatal(err)
			}
			mode, version := "existing", 2
			switch change {
			case "mode":
				mode = "managed"
			case "root":
				root = filepath.Join(repo, "new")
			case "schema":
				version = 1
			case "config":
				config = filepath.Join(repo, "other.toml")
			}
			err = ValidateLayout(config, root, mode, version)
			if (err == nil) != (change == "unchanged") {
				t.Fatalf("ValidateLayout = %v", err)
			}
			if after, err := os.ReadFile(path); err != nil || string(after) != string(data) {
				t.Fatalf("guard modified ownership: %q, %v", after, err)
			}
		})
	}
}

func TestValidateLayoutLegacySameRootIsNotUpgrade(t *testing.T) {
	for _, name := range []string{"changes", "tasks", ".to-promote", ".onto-demote"} {
		repo := t.TempDir()
		writeMarker(t, repo, "docs")
		path := filepath.Join(repo, "docs", name)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ValidateLayout(filepath.Join(repo, "homonto.toml"), "docs", "existing", 2); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%q", path)) {
			t.Fatalf("legacy upgrade = %v", err)
		}
		if err := ValidateLayout(filepath.Join(repo, "homonto.toml"), "docs", "existing", 1); err != nil {
			t.Fatalf("legacy unchanged = %v", err)
		}
	}
}

func writeMarker(t *testing.T, repo, root string) string {
	t.Helper()
	marker := filepath.Join(repo, ".homonto", "workflow-root")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(root+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return marker
}

func TestValidateChangeReportsAllRootsAndEntries(t *testing.T) {
	repo := t.TempDir()
	old := "old/records"
	marker := writeMarker(t, repo, old)
	want := "new/records"
	wantAbs := filepath.Join(repo, filepath.FromSlash(want))
	expected := fmt.Sprintf("workflow.root changed to %q (absolute location %q) while workflow state exists:", want, wantAbs)
	for _, rel := range []string{old, "docs"} {
		abs := filepath.Join(repo, filepath.FromSlash(rel))
		source := "legacy default docs source"
		if rel == old {
			source = fmt.Sprintf("recorded by marker %q", marker)
		}
		expected += fmt.Sprintf("\n  old root %q (absolute location %q; %s):", rel, abs, source)
		for _, name := range []string{"changes", "tasks", ".to-promote", ".onto-demote"} {
			path := filepath.Join(abs, name)
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			expected += fmt.Sprintf("\n    %q", path)
		}
	}
	expected += fmt.Sprintf("\nrestore the prior workflow.root or explicitly relocate and preserve workflow records, including archives, to the new root %q; no state is moved or deleted automatically", wantAbs)
	for range 2 {
		err := ValidateChange(repo, want)
		if err == nil || err.Error() != expected {
			t.Fatalf("ValidateChange = %v, want:\n%s", err, expected)
		}
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != old+"\n" {
		t.Fatalf("marker modified: %q, %v", data, err)
	}
	if _, err := os.Lstat(wantAbs); !os.IsNotExist(err) {
		t.Fatalf("selected root was created: %v", err)
	}
}

func TestValidateChangeEntryExistence(t *testing.T) {
	for _, name := range []string{"changes", "tasks", ".to-promote", ".onto-demote"} {
		for _, kind := range []string{"empty directory", "file", "dangling symlink"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				repo := t.TempDir()
				root := filepath.Join(repo, "docs")
				if err := os.MkdirAll(root, 0o755); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, name)
				var err error
				switch kind {
				case "empty directory":
					err = os.Mkdir(path, 0o755)
				case "file":
					err = os.WriteFile(path, nil, 0o644)
				case "dangling symlink":
					err = os.Symlink("missing", path)
					if err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				err = ValidateChange(repo, "workflow")
				if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("\n    %q", path)) {
					t.Fatalf("ValidateChange = %v, want exact blocker %q", err, path)
				}
				if got := strings.Count(err.Error(), "\n    "); got != 1 {
					t.Fatalf("reported %d entries, want only the existing one: %v", got, err)
				}
				if _, err := os.Lstat(path); err != nil {
					t.Fatalf("blocker modified: %v", err)
				}
			})
		}
	}
}

func TestValidateChangeAccepted(t *testing.T) {
	for _, tc := range []struct {
		name, marker, want, entry string
	}{
		{name: "no state", want: "workflow"},
		{name: "default", want: "docs", entry: "docs/changes"},
		{name: "unchanged marker", marker: "workflow", want: "workflow", entry: "workflow/tasks"},
		{name: "unchanged default marker", marker: "docs", want: "docs", entry: "docs/.to-promote"},
		{name: "old root without blockers", marker: "old", want: "new", entry: "old/guides"},
		{name: "no recursion", marker: "old", want: "new", entry: "old/unrelated/changes"},
		{name: "legacy documentation only", want: "new", entry: "docs/guides"},
		{name: "new root state", marker: "old", want: "new", entry: "new/changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if tc.marker != "" {
				writeMarker(t, repo, tc.marker)
			}
			if tc.entry != "" {
				if err := os.MkdirAll(filepath.Join(repo, filepath.FromSlash(tc.entry)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := ValidateChange(repo, tc.want); err != nil {
				t.Fatalf("ValidateChange = %v, want accepted", err)
			}
			if tc.marker == "" {
				if _, err := os.Lstat(filepath.Join(repo, ".homonto")); !os.IsNotExist(err) {
					t.Fatalf("validation created marker directory: %v", err)
				}
			}
		})
	}
}

func TestValidateChangeDeduplicatesDefaultRoot(t *testing.T) {
	for _, old := range []string{"docs", "./docs"} {
		t.Run(old, func(t *testing.T) {
			repo := t.TempDir()
			marker := writeMarker(t, repo, old)
			path := filepath.Join(repo, "docs", "tasks")
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			err := ValidateChange(repo, "new")
			if err == nil {
				t.Fatal("ValidateChange accepted legacy state")
			}
			if strings.Count(err.Error(), fmt.Sprintf("\n    %q", path)) != 1 || !strings.Contains(err.Error(), fmt.Sprintf("recorded by marker %q", marker)) {
				t.Fatalf("want one docs entry attributed to marker: %v", err)
			}
		})
	}
}

func TestValidateChangeLegacyBlocksEvenWithUnchangedMarker(t *testing.T) {
	repo := t.TempDir()
	writeMarker(t, repo, "new")
	if err := os.MkdirAll(filepath.Join(repo, "docs", "tasks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateChange(repo, "new"); err == nil || !strings.Contains(err.Error(), "legacy default docs source") {
		t.Fatalf("ValidateChange = %v, want legacy state blocker", err)
	}
}

func TestValidateChangeAfterExplicitRelocationOrRemoval(t *testing.T) {
	for _, action := range []string{"relocate", "remove"} {
		t.Run(action, func(t *testing.T) {
			repo := t.TempDir()
			marker := writeMarker(t, repo, "old")
			old := filepath.Join(repo, "old", "changes")
			if err := os.MkdirAll(filepath.Join(old, "archive"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(old, "archive", "record"), []byte("preserved"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := ValidateChange(repo, "new"); err == nil {
				t.Fatal("ValidateChange accepted old archive")
			}
			if action == "relocate" {
				if err := os.Rename(filepath.Join(repo, "old"), filepath.Join(repo, "new")); err != nil {
					t.Fatal(err)
				}
			} else if err := os.RemoveAll(old); err != nil {
				t.Fatal(err)
			}
			if err := ValidateChange(repo, "new"); err != nil {
				t.Fatalf("ValidateChange after %s = %v", action, err)
			}
			if data, err := os.ReadFile(marker); err != nil || string(data) != "old\n" {
				t.Fatalf("marker modified: %q, %v", data, err)
			}
			if action == "relocate" {
				data, err := os.ReadFile(filepath.Join(repo, "new", "changes", "archive", "record"))
				if err != nil || string(data) != "preserved" {
					t.Fatalf("archive modified: %q, %v", data, err)
				}
			}
		})
	}
}

func TestValidateChangeQuotesPaths(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo\n\t\"name")
	old := "old\n\t\"records"
	want := "new\n\t\"records"
	marker := writeMarker(t, repo, old)
	entry := filepath.Join(repo, old, "changes")
	if err := os.MkdirAll(entry, 0o755); err != nil {
		t.Fatal(err)
	}
	err := ValidateChange(repo, want)
	if err == nil {
		t.Fatal("ValidateChange accepted old state")
	}
	for _, path := range []string{repo, old, want, marker, entry} {
		if strings.Contains(err.Error(), path) {
			t.Errorf("unquoted path %q in %v", path, err)
		}
	}
	for _, path := range []string{old, want, marker, entry, filepath.Join(repo, old), filepath.Join(repo, want)} {
		if !strings.Contains(err.Error(), fmt.Sprintf("%q", path)) {
			t.Errorf("missing quoted path %q in %v", path, err)
		}
	}
}

func TestValidateChangeInspectionErrors(t *testing.T) {
	for _, kind := range []string{"marker directory", "marker parent file", "old root file", "legacy root file"} {
		t.Run(kind, func(t *testing.T) {
			repo := filepath.Join(t.TempDir(), "repo\nname")
			marker := writeMarker(t, repo, "old")
			path := marker
			var underlying error
			switch kind {
			case "marker directory":
				if err := os.Remove(marker); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(marker, 0o755); err != nil {
					t.Fatal(err)
				}
				_, underlying = os.ReadFile(marker)
			case "marker parent file":
				if err := os.RemoveAll(filepath.Dir(marker)); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Dir(marker), nil, 0o644); err != nil {
					t.Fatal(err)
				}
				_, underlying = os.ReadFile(marker)
			default:
				root := "old"
				if kind == "legacy root file" {
					root = "docs"
				}
				if err := os.WriteFile(filepath.Join(repo, root), nil, 0o644); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(repo, root, "changes")
				_, underlying = os.Lstat(path)
			}
			var cause *os.PathError
			if !errors.As(underlying, &cause) || os.IsNotExist(underlying) {
				t.Fatalf("fixture did not produce a non-ENOENT path error: %v", underlying)
			}
			err := ValidateChange(repo, "new")
			var got *os.PathError
			if !errors.As(err, &got) || got.Path != path || !errors.Is(err, cause.Err) {
				t.Fatalf("ValidateChange = %v, want wrapped path error %v", err, underlying)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("%q", path)) || strings.Contains(err.Error(), repo) {
				t.Fatalf("error must quote exact path without raw controls: %v", err)
			}
		})
	}
}
