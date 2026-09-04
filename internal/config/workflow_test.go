package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
