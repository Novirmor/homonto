package ontocli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/integrationrecord"
)

// treeSnapshot walks dir and records each file's relative path, size, and
// mod time, so callers can assert a run left the tree byte-for-byte
// untouched (no file created, modified, or removed).
func treeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	snap := make(map[string]string)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		snap[rel] = fmt.Sprintf("%s|%d", info.ModTime().String(), info.Size())
		return nil
	})
	if err != nil {
		t.Fatalf("failed to snapshot tree: %v", err)
	}
	return snap
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("failed to create dir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write %s: %v", path, err)
	}
}

func TestStatusCommand_ReportsValidAndInvalidChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"), "change: alpha\nphase: build\n")
	writeFile(t, filepath.Join(dir, "docs", "changes", "beta", "onto-state.yaml"), "change: [unterminated\n  phase: build")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "alpha: build") {
		t.Errorf("output = %q, want it to contain %q", got, "alpha: build")
	}
	if !strings.Contains(got, "beta:") || !strings.Contains(got, "malformed") {
		t.Errorf("output = %q, want a line for beta containing %q", got, "malformed")
	}
}

func TestStatusCommand_DeletedStateFile_IsMissingStateRow(t *testing.T) {
	dir := t.TempDir()
	// a change directory that exists but has no state file (deleted)
	if err := os.MkdirAll(filepath.Join(dir, "docs", "changes", "gamma"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out, err := runOnto(t, "status", "--dir", dir)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "gamma") || !strings.Contains(out, "missing-state") {
		t.Errorf("output = %q, want a gamma missing-state row (not silently dropped)", out)
	}
}

func TestStatusCommand_IsReadOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"), "change: alpha\nphase: build\n")
	writeFile(t, filepath.Join(dir, "docs", "changes", "beta", "onto-state.yaml"), "change: [unterminated\n  phase: build")

	before := treeSnapshot(t, dir)

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	after := treeSnapshot(t, dir)

	if len(before) != len(after) {
		t.Fatalf("file count changed: before=%d after=%d", len(before), len(after))
	}
	for path, sig := range before {
		afterSig, ok := after[path]
		if !ok {
			t.Errorf("file %q was removed by status", path)
			continue
		}
		if sig != afterSig {
			t.Errorf("file %q was modified by status: before=%q after=%q", path, sig, afterSig)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("file %q was created by status", path)
		}
	}
}

func TestStatusCommand_SucceedsWithoutHomontoToml(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"), "change: alpha\nphase: open\n")

	if _, err := os.Stat(filepath.Join(dir, "homonto.toml")); !os.IsNotExist(err) {
		t.Fatalf("expected no homonto.toml in temp workspace, stat err = %v", err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "alpha: open") {
		t.Errorf("output = %q, want it to contain %q", out.String(), "alpha: open")
	}
}

// TestStatusCommand_SkipsArchivedChanges verifies the real exclusion
// mechanism: the status glob "docs/changes/*/onto-state.yaml" uses a single
// wildcard segment, which does not cross path separators, so it only
// matches direct children of docs/changes/. An archived change nested one
// level deeper at docs/changes/archive/<name>/onto-state.yaml is therefore
// never matched and never appears in the output, while an active change at
// docs/changes/<name>/onto-state.yaml is matched and reported.
func TestStatusCommand_SkipsArchivedChanges(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"), "change: alpha\nphase: build\n")
	writeFile(t, filepath.Join(dir, "docs", "changes", "archive", "old-change", "onto-state.yaml"), "change: old-change\nphase: close\narchived: true\n")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "old-change") {
		t.Errorf("output = %q, want archived change to be skipped", got)
	}
	if !strings.Contains(got, "alpha: build") {
		t.Errorf("output = %q, want active change to be reported", got)
	}
}

// TestStatusCommand_ReportsSkeletonOk covers a change whose onto-state.yaml
// loads and derives a phase, and whose required artifacts (proposal.md,
// tasks.md alongside onto-state.yaml) are all present: the status line for
// that change must carry a "skeleton ok" note.
func TestStatusCommand_ReportsSkeletonOk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"), "change: alpha\nphase: open\n")
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "proposal.md"), "proposal")
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "tasks.md"), "tasks")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "alpha: open") || !strings.Contains(got, "skeleton ok") {
		t.Errorf("output = %q, want it to contain %q and %q", got, "alpha: open", "skeleton ok")
	}
}

// TestStatusCommand_ReportsSkeletonMissingArtifact covers a change whose
// onto-state.yaml loads fine but which is missing a required artifact
// (tasks.md): the status line must carry a note naming the missing file,
// and must not abort processing of other changes or change the exit code.
func TestStatusCommand_ReportsSkeletonMissingArtifact(t *testing.T) {
	dir := t.TempDir()
	// A fix preset requires tasks.md from the open exit, so an open preset with
	// only proposal.md is a reported skeleton gap.
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"), "change: alpha\nworkflow: fix\nphase: open\n")
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "proposal.md"), "proposal")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "skeleton") || !strings.Contains(got, "tasks.md") {
		t.Errorf("output = %q, want it to contain %q and %q", got, "skeleton", "tasks.md")
	}
}

// TestStatusCommand_ReportsInvalidPhase covers the DerivePhase error branch:
// a syntactically valid onto-state.yaml whose phase value is not one of the
// known onto workflow phases. ontostate.Load succeeds (the YAML parses
// fine), but state.DerivePhase() fails validation, so status must still
// print an "invalid" line for it (via the second error branch in
// runStatus) and must not abort processing of other changes.
func TestStatusCommand_ReportsInvalidPhase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "docs", "changes", "alpha", "onto-state.yaml"), "change: alpha\nphase: build\n")
	writeFile(t, filepath.Join(dir, "docs", "changes", "gamma", "onto-state.yaml"), "change: gamma\nphase: bogus\n")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	got := out.String()
	if !strings.Contains(got, "alpha: build") {
		t.Errorf("output = %q, want it to contain %q", got, "alpha: build")
	}
	if !strings.Contains(got, "gamma:") || !strings.Contains(got, "malformed") {
		t.Errorf("output = %q, want a line for gamma containing %q", got, "malformed")
	}
}

// TestStatusCommand_FlagsWorkingPhaseMismatch: a claim the artifacts cannot
// support is annotated with the derived working phase.
func TestStatusCommand_FlagsWorkingPhaseMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "onto-state.yaml"),
		"schema_version: 1\nchange: c\nworkflow: full\nphase: verify\n")
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "proposal.md"), "p")
	writeFile(t, filepath.Join(root, "docs", "changes", "c", "tasks.md"), "- [ ] a\n")

	out, err := runOnto(t, "status", "--dir", root)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out, "(working: design)") {
		t.Errorf("status must flag the mismatch, got:\n%s", out)
	}
}

func TestStatusCommand_ReportsArchivedPendingIntegration(t *testing.T) {
	dir := t.TempDir()
	archiveDir := filepath.Join(dir, "docs", "changes", "archive", "2026-09-03-c")
	writeFile(t, filepath.Join(archiveDir, "onto-state.yaml"), "change: c\nworkflow: full\nphase: close\narchived: true\nintegration: merge\nbase_branch: main\n")
	if err := integrationrecord.Save(archiveDir, integrationrecord.NewPending("c", "merge", "main", []integrationrecord.Entry{{BaseBranch: "main", BaseCommit: "1111111111111111111111111111111111111111", SourceBranch: "change/c", SourceCommit: "2222222222222222222222222222222222222222"}})); err != nil {
		t.Fatal(err)
	}
	out, err := runOnto(t, "status", "--dir", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "c: close") || !strings.Contains(out, "integration pending") {
		t.Fatalf("pending archive missing from status:\n%s", out)
	}
}
