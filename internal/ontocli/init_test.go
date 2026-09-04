package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGate_MissingHomontoToml covers gate order step 1: no homonto.toml at
// all means the workspace was never initialized by homonto, so the error
// must point the user at `homonto init`.
func TestGate_MissingHomontoToml(t *testing.T) {
	dir := t.TempDir()

	err := ontoFramework.Gate(dir)
	if err == nil {
		t.Fatal("gate() = nil, want error")
	}
	if !strings.Contains(err.Error(), "homonto init") {
		t.Errorf("gate() error = %q, want it to mention %q", err.Error(), "homonto init")
	}

	assertNoDocsFiles(t, dir)
}

// TestGate_MissingFrameworksOnto covers gate order step 2: homonto.toml
// exists but declares no [frameworks.onto] table, so the error must tell
// the user to declare it and run `homonto apply`.
func TestGate_MissingFrameworksOnto(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[models.claude.architectural]\nmodel=\"opus\"\n")

	err := ontoFramework.Gate(dir)
	if err == nil {
		t.Fatal("gate() = nil, want error")
	}
	if !strings.Contains(err.Error(), "[frameworks.onto]") {
		t.Errorf("gate() error = %q, want it to mention %q", err.Error(), "[frameworks.onto]")
	}
	if !strings.Contains(err.Error(), "homonto apply") {
		t.Errorf("gate() error = %q, want it to mention %q", err.Error(), "homonto apply")
	}

	assertNoDocsFiles(t, dir)
}

// TestGate_MissingCatalog covers gate order step 3: [frameworks.onto] is
// declared but has not been applied yet, so the onto skill catalog
// directory doesn't exist. The error must tell the user to run
// `homonto apply`.
func TestGate_MissingCatalog(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n")

	err := ontoFramework.Gate(dir)
	if err == nil {
		t.Fatal("gate() = nil, want error")
	}
	if !strings.Contains(err.Error(), "homonto apply") {
		t.Errorf("gate() error = %q, want it to mention %q", err.Error(), "homonto apply")
	}

	assertNoDocsFiles(t, dir)
}

// TestGate_AllPresent covers gate order step 4: homonto.toml declares
// [frameworks.onto] and the catalog has been applied, so the gate must
// pass.
func TestGate_AllPresent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatalf("failed to create catalog dir: %v", err)
	}

	if err := ontoFramework.Gate(dir); err != nil {
		t.Errorf("gate() = %v, want nil", err)
	}
}

// assertNoDocsFiles verifies the gate performed no writes: it is a
// read-only check and must never scaffold docs/ files itself.
func assertNoDocsFiles(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "docs", "*"))
	if err != nil {
		t.Fatalf("glob docs: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("gate() created docs/ entries: %v", matches)
	}
}

// docsDirs is the fixed set of doc-layout directories "onto init" scaffolds.
var docsDirs = []string{
	filepath.Join("docs", "changes"),
	filepath.Join("docs", "specs"),
	filepath.Join("docs", "adr"),
	filepath.Join("docs", "guides"),
}

// setUpGatedWorkspace prepares a temp workspace that passes gate(): a
// homonto.toml declaring [frameworks.onto] plus an applied onto catalog
// directory.
func setUpGatedWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatalf("failed to create catalog dir: %v", err)
	}
	return dir
}

// TestInitCommand_ScaffoldsDocsLayout verifies that, once the framework
// gate passes, "onto init" creates all four docs/ directories and reports
// each as created.
func TestInitCommand_ScaffoldsDocsLayout(t *testing.T) {
	dir := setUpGatedWorkspace(t)

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	for _, d := range docsDirs {
		full := filepath.Join(dir, d)
		info, err := os.Stat(full)
		if err != nil || !info.IsDir() {
			t.Errorf("expected directory %q to exist, stat err = %v", full, err)
		}
		if !strings.Contains(got, "created "+full) {
			t.Errorf("output = %q, want it to contain %q", got, "created "+full)
		}
	}
}

// TestInitCommand_IsIdempotentAndNeverOverwrites verifies that a second run
// of "onto init" on an already-scaffolded workspace leaves pre-existing
// user content untouched and reports the directories as already existing.
func TestInitCommand_IsIdempotentAndNeverOverwrites(t *testing.T) {
	dir := setUpGatedWorkspace(t)

	cmd1 := NewRootCmd()
	cmd1.SetOut(&bytes.Buffer{})
	cmd1.SetErr(&bytes.Buffer{})
	cmd1.SetArgs([]string{"init", "--dir", dir})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("first execute: %v", err)
	}

	keepPath := filepath.Join(dir, "docs", "changes", "keep.txt")
	keepContent := "user data, do not touch\n"
	writeFile(t, keepPath, keepContent)

	cmd2 := NewRootCmd()
	var out bytes.Buffer
	cmd2.SetOut(&out)
	cmd2.SetErr(&out)
	cmd2.SetArgs([]string{"init", "--dir", dir})
	if err := cmd2.Execute(); err != nil {
		t.Fatalf("second execute: %v", err)
	}

	for _, d := range docsDirs {
		full := filepath.Join(dir, d)
		info, err := os.Stat(full)
		if err != nil || !info.IsDir() {
			t.Errorf("expected directory %q to still exist, stat err = %v", full, err)
		}
	}

	gotBytes, err := os.ReadFile(keepPath)
	if err != nil {
		t.Fatalf("reading keep.txt: %v", err)
	}
	if string(gotBytes) != keepContent {
		t.Errorf("keep.txt content = %q, want unchanged %q", string(gotBytes), keepContent)
	}

	got := out.String()
	for _, d := range docsDirs {
		full := filepath.Join(dir, d)
		if !strings.Contains(got, "exists "+full) {
			t.Errorf("output = %q, want it to contain %q", got, "exists "+full)
		}
		if strings.Contains(got, "created "+full) {
			t.Errorf("output = %q, want it NOT to report %q on second run", got, "created "+full)
		}
	}
}

// TestInitCommand_GateFailureCreatesNothing verifies that when the
// framework gate fails (no homonto.toml here), "onto init" returns a
// non-zero exit and creates no docs/ directory at all.
func TestInitCommand_GateFailureCreatesNothing(t *testing.T) {
	dir := t.TempDir()

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	if _, err := os.Stat(filepath.Join(dir, "docs")); !os.IsNotExist(err) {
		t.Errorf("expected docs/ to not exist, stat err = %v", err)
	}
}

func TestInitAndNewRejectEscapingWorkflowRoot(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[workflow]\nroot=\"workflow\"\n[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "workflow")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	for _, args := range [][]string{{"init", "--dir", dir}, {"new", "escape", "--dir", dir}} {
		cmd := NewRootCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "workflow.root") {
			t.Errorf("%v = %v, want workflow-root rejection", args, err)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "changes")); !os.IsNotExist(err) {
		t.Fatalf("workflow write escaped through symlink, stat err = %v", err)
	}
}
