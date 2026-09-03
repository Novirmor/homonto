package ontocli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// --- worktreeDirt classification ---

// TestWorktreeDirt_Classification pins the three structural classes: the
// change's own docs are "own", another change's docs (and the archive) are
// "change", everything else is "source" — and only "change" is exempt from
// blocking close.
func TestWorktreeDirt_Classification(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "demo", "close")
	seedChange(t, dir, "other", "build")
	writeFile(t, filepath.Join(dir, "main.go"), "package main\n")
	commitAll(t, dir, "seed")

	writeFile(t, filepath.Join(dir, "docs", "changes", "demo", "scratch.txt"), "own dirt\n")
	writeFile(t, filepath.Join(dir, "docs", "changes", "other", "notes.md"), "foreign dirt\n")
	writeFile(t, filepath.Join(dir, "docs", "changes", "archive", "2026-01-01-old", "onto-state.yaml"), "archived dirt\n")
	writeFile(t, filepath.Join(dir, "main.go"), "package main // edited\n")

	entries, determinable := worktreeDirt(dir, "demo")
	if !determinable {
		t.Fatal("worktreeDirt() determinable = false, want true")
	}
	got := map[string]dirtEntry{}
	for _, e := range entries {
		got[e.Path] = e
	}
	for path, want := range map[string]struct {
		class  string
		blocks bool
	}{
		"docs/changes/demo/scratch.txt":                       {"own", true},
		"docs/changes/other/notes.md":                         {"change", false},
		"docs/changes/archive/2026-01-01-old/onto-state.yaml": {"change", false},
		"main.go": {"source", true},
	} {
		e, ok := got[path]
		if !ok {
			t.Errorf("no entry for %s in %v", path, entries)
			continue
		}
		if e.Class != want.class || e.BlocksClose != want.blocks {
			t.Errorf("%s: class=%q blocks=%v, want class=%q blocks=%v", path, e.Class, e.BlocksClose, want.class, want.blocks)
		}
	}
}

// TestWorktreeDirt_UntrackedArtifactIsOwn verifies that untracked-files=all
// reports the concrete change artifact and classifies it as blocking own dirt.
func TestWorktreeDirt_UntrackedAncestorIsOwn(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	writeFile(t, filepath.Join(dir, "a.txt"), "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "init")
	writeFile(t, filepath.Join(dir, "docs", "changes", "demo", "onto-state.yaml"), "never committed\n")

	entries, determinable := worktreeDirt(dir, "demo")
	if !determinable {
		t.Fatal("determinable = false, want true")
	}
	if len(entries) != 1 || entries[0].Path != "docs/changes/demo/onto-state.yaml" || entries[0].Class != "own" || !entries[0].BlocksClose {
		t.Errorf("entries = %+v, want one blocking 'own' artifact", entries)
	}
}

// TestWorktreeDirt_SubdirWorkspace: when the onto workspace root is a
// subdirectory of the git repository, git reports paths relative to the REPO
// root — classification must account for the prefix or every docs/changes
// path would misclassify as "source".
func TestWorktreeDirt_SubdirWorkspace(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	ws := filepath.Join(repo, "ws")
	writeFile(t, filepath.Join(ws, "docs", "changes", "other", "notes.md"), "seed\n")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-m", "init")

	writeFile(t, filepath.Join(ws, "docs", "changes", "other", "notes.md"), "foreign dirt\n")
	writeFile(t, filepath.Join(repo, "toplevel.txt"), "source dirt outside ws\n")

	entries, determinable := worktreeDirt(ws, "demo")
	if !determinable {
		t.Fatal("determinable = false, want true")
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.Path] = e.Class
	}
	if got["ws/docs/changes/other/notes.md"] != "change" {
		t.Errorf("prefixed foreign-change path classified %q, want %q (entries %v)", got["ws/docs/changes/other/notes.md"], "change", entries)
	}
	if got["toplevel.txt"] != "source" {
		t.Errorf("repo-level path classified %q, want %q", got["toplevel.txt"], "source")
	}
}

func TestWorktreeDirt_RenameRetainsSourceDeletion(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	writeFile(t, filepath.Join(dir, "old.txt"), "content\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "init")
	runGit(t, dir, "mv", "old.txt", "new.txt")

	entries, determinable := worktreeDirt(dir, "")
	if !determinable {
		t.Fatal("determinable = false, want true")
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want deletion and addition", entries)
	}
	got := map[string]string{}
	for _, entry := range entries {
		got[entry.Path] = entry.Status
	}
	if got["old.txt"] != "D " || got["new.txt"] != "A " {
		t.Fatalf("rename records = %v, want old deletion and new addition", got)
	}
}

// --- the carve-out in the close gates ---

// TestAdvanceCommand_VerifyToCloseAllowsForeignChangeDirt: uncommitted docs of
// ANOTHER change must not block this change's verify→close — parallel changes
// used to deadlock on each other's in-flight artifacts (any dirt blocked
// close, repo-wide).
func TestAdvanceCommand_VerifyToCloseAllowsForeignChangeDirt(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "verify")
	writeFile(t, filepath.Join(dir, "docs", "changes", "feature-x", "verification.md"), "Result: pass\n")
	setVerifyResult(t, dir, "feature-x", "pass")
	commitAll(t, dir, "seed change")
	writeFile(t, filepath.Join(dir, "docs", "changes", "in-flight", "proposal.md"), "another change's WIP\n")

	cmd := NewRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (foreign-change dirt must not block close)", err)
	}
	st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if st.Phase != "close" {
		t.Errorf("st.Phase = %q, want %q", st.Phase, "close")
	}
}

// TestAdvanceCommand_CloseBlockErrorListsPaths: the refusal must name the
// offending paths and point at `onto dirt` — "dirty worktree blocks close"
// with no what/where sent agents on a git-status hunt.
func TestAdvanceCommand_CloseBlockErrorListsPaths(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "feature-x", "verify")
	writeFile(t, filepath.Join(dir, "docs", "changes", "feature-x", "verification.md"), "Result: pass\n")
	setVerifyResult(t, dir, "feature-x", "pass")
	commitAll(t, dir, "seed change")
	writeFile(t, filepath.Join(dir, "uncommitted.txt"), "source dirt\n")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"advance", "feature-x", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want error")
	}
	for _, want := range []string{"uncommitted.txt", "onto dirt feature-x"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestCloseCommand_AllowsForeignChangeDirt mirrors the advance carve-out on
// `onto close` itself.
func TestCloseCommand_AllowsForeignChangeDirt(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	checkoutChangeBranch(t, dir, "demo")
	commitAll(t, dir, "seed change")
	writeFile(t, filepath.Join(dir, "docs", "changes", "in-flight", "proposal.md"), "another change's WIP\n")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (foreign-change dirt must not block close)", err)
	}
	archiveDir := filepath.Join(dir, "docs", "changes", "archive", time.Now().Format("2006-01-02")+"-demo")
	if _, err := os.Stat(archiveDir); err != nil {
		t.Errorf("archive dir stat: %v, want archived", err)
	}
}

// TestCloseCommand_SourceDirtErrorListsPaths: close's refusal is as actionable
// as advance's.
func TestCloseCommand_SourceDirtErrorListsPaths(t *testing.T) {
	dir := prepWorkspace(t)
	seedClose(t, dir, "demo", nil)
	commitAll(t, dir, "seed change")
	writeFile(t, filepath.Join(dir, "uncommitted.txt"), "source dirt\n")

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"close", "demo", "--dir", dir})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("execute() = nil, want error")
	}
	for _, want := range []string{"uncommitted.txt", "onto dirt demo"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// --- onto dirt command ---

func TestDirtCommand_TextOutput(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "demo", "build")
	commitAll(t, dir, "seed")
	writeFile(t, filepath.Join(dir, "docs", "changes", "demo", "scratch.txt"), "own\n")
	writeFile(t, filepath.Join(dir, "src.go"), "source\n")

	out, err := runOnto(t, "dirt", "demo", "--dir", dir)
	if err != nil {
		t.Fatalf("onto dirt: %v", err)
	}
	for _, want := range []string{"docs/changes/demo/scratch.txt (own)", "src.go (source)", "2 uncommitted path(s), 2 blocking close"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
}

func TestDirtCommand_JSON(t *testing.T) {
	dir := prepWorkspace(t)
	seedChange(t, dir, "demo", "build")
	commitAll(t, dir, "seed")
	writeFile(t, filepath.Join(dir, "docs", "changes", "other", "notes.md"), "foreign\n")

	out, err := runOnto(t, "dirt", "demo", "--json", "--dir", dir)
	if err != nil {
		t.Fatalf("onto dirt --json: %v", err)
	}
	var report struct {
		Change        string      `json:"change"`
		Clean         bool        `json:"clean"`
		BlockingClose int         `json:"blocking_close"`
		Entries       []dirtEntry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if report.Change != "demo" || report.Clean || report.BlockingClose != 0 || len(report.Entries) != 1 {
		t.Errorf("report = %+v, want change=demo, dirty, 0 blocking, 1 entry", report)
	}
	if report.Entries[0].Class != "change" || report.Entries[0].BlocksClose {
		t.Errorf("entry = %+v, want non-blocking 'change' class", report.Entries[0])
	}
}

func TestDirtCommand_CleanAndJSONEmptyArray(t *testing.T) {
	dir := prepWorkspace(t) // already committed clean

	out, err := runOnto(t, "dirt", "--dir", dir)
	if err != nil {
		t.Fatalf("onto dirt: %v", err)
	}
	if !strings.Contains(out, "clean") {
		t.Errorf("output = %q, want it to report clean", out)
	}
	out, err = runOnto(t, "dirt", "--json", "--dir", dir)
	if err != nil {
		t.Fatalf("onto dirt --json: %v", err)
	}
	if !strings.Contains(out, `"entries": []`) {
		t.Errorf("json = %q, want an empty entries ARRAY (never null)", out)
	}
}

func TestDirtCommand_NonRepoErrors(t *testing.T) {
	if _, err := runOnto(t, "dirt", "--dir", t.TempDir()); err == nil {
		t.Fatal("expected error outside a git repository")
	}
}

func TestDirtCommand_InvalidChangeName(t *testing.T) {
	if _, err := runOnto(t, "dirt", "../evil", "--dir", t.TempDir()); err == nil {
		t.Fatal("expected error for an invalid change name")
	}
}
