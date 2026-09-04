package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/noviopenworks/homonto/internal/ontostate"
)

// TestValidChangeName_Valid covers the accepted shape: lowercase
// alphanumeric segments separated by single hyphens.
func TestValidChangeName_Valid(t *testing.T) {
	if err := ontoFramework.ValidChangeName("feature-x"); err != nil {
		t.Errorf("validChangeName(%q) = %v, want nil", "feature-x", err)
	}
}

// TestValidChangeName_Invalid covers every rejected shape: empty, path
// traversal, path separators, uppercase, and a leading hyphen.
func TestValidChangeName_Invalid(t *testing.T) {
	cases := []string{"", "../evil", "Foo", "a/b", "-x"}
	for _, name := range cases {
		if err := ontoFramework.ValidChangeName(name); err == nil {
			t.Errorf("validChangeName(%q) = nil, want error", name)
		}
	}
}

var createdDatePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// TestNewCommand_CreatesSkeleton verifies that, once the framework gate
// passes and the name is valid, "onto new" scaffolds onto-state.yaml
// (phase open, matching change name, created date) plus empty proposal.md
// and tasks.md, and exits 0.
func TestNewCommand_CreatesSkeleton(t *testing.T) {
	dir := setUpGatedWorkspace(t)

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"new", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	changeDir := filepath.Join(dir, "docs", "changes", "feature-x")

	state, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		t.Fatalf("loading onto-state.yaml: %v", err)
	}
	if state.Change != "feature-x" {
		t.Errorf("state.Change = %q, want %q", state.Change, "feature-x")
	}
	if state.Phase != "open" {
		t.Errorf("state.Phase = %q, want %q", state.Phase, "open")
	}
	if !createdDatePattern.MatchString(state.Created) {
		t.Errorf("state.Created = %q, want match of %q", state.Created, createdDatePattern.String())
	}

	// full: proposal.md is scaffolded; tasks.md is NOT (the task list is derived
	// from the confirmed design in the design phase).
	if _, err := os.Stat(filepath.Join(changeDir, "proposal.md")); err != nil {
		t.Errorf("expected proposal.md to exist, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(changeDir, "tasks.md")); err == nil {
		t.Error("full change must NOT scaffold tasks.md at new (derived in design)")
	}
}

func TestCustomWorkflowRootLifecycle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "homonto.toml"), "[workflow]\nroot=\"workflow/records\"\n[frameworks.onto]\nsource=\"builtin:onto\"\nscope=\"project\"\n")
	if err := os.MkdirAll(filepath.Join(dir, ".homonto", "catalog", "skills", "onto"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"init", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}
	cmd = NewRootCmd()
	cmd.SetArgs([]string{"new", "custom", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "workflow", "records", "changes", "custom", "onto-state.yaml")); err != nil {
		t.Fatalf("custom workspace missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes")); !os.IsNotExist(err) {
		t.Fatalf("default workspace was created, stat err = %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(dir, ".homonto", "workflow-root"))
	if err != nil {
		t.Fatalf("reading workflow marker: %v", err)
	}
	if got, want := string(marker), "workflow/records\n"; got != want {
		t.Fatalf("workflow marker = %q, want %q", got, want)
	}
}

// A fix/tweak preset skips design and decomposes at open-lite, so `new` DOES
// scaffold tasks.md for it.
func TestNewCommand_PresetScaffoldsTasks(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"new", "bug-y", "--workflow", "fix", "--dir", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	changeDir := filepath.Join(dir, "docs", "changes", "bug-y")
	for _, f := range []string{"proposal.md", "tasks.md"} {
		if _, err := os.Stat(filepath.Join(changeDir, f)); err != nil {
			t.Errorf("preset must scaffold %s, stat err = %v", f, err)
		}
	}
}

// TestNewCommand_RefusesToClobberExisting verifies that a second "onto new"
// with the same change name fails without touching any pre-existing
// artifact in that change directory.
func TestNewCommand_RefusesToClobberExisting(t *testing.T) {
	dir := setUpGatedWorkspace(t)

	cmd1 := NewRootCmd()
	cmd1.SetOut(&bytes.Buffer{})
	cmd1.SetErr(&bytes.Buffer{})
	cmd1.SetArgs([]string{"new", "feature-x", "--dir", dir})
	if err := cmd1.Execute(); err != nil {
		t.Fatalf("first execute: %v", err)
	}

	proposalPath := filepath.Join(dir, "docs", "changes", "feature-x", "proposal.md")
	knownContent := "known bytes, do not touch\n"
	writeFile(t, proposalPath, knownContent)

	cmd2 := NewRootCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs([]string{"new", "feature-x", "--dir", dir})
	if err := cmd2.Execute(); err == nil {
		t.Fatal("second execute() = nil, want error")
	}

	got, err := os.ReadFile(proposalPath)
	if err != nil {
		t.Fatalf("reading proposal.md: %v", err)
	}
	if string(got) != knownContent {
		t.Errorf("proposal.md content = %q, want unchanged %q", string(got), knownContent)
	}
}

// TestNewCommand_InvalidNameCreatesNothing verifies that an invalid change
// name fails before any write, leaving docs/changes/<name> absent.
func TestNewCommand_InvalidNameCreatesNothing(t *testing.T) {
	dir := setUpGatedWorkspace(t)

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"new", "Bad", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "Bad")); !os.IsNotExist(err) {
		t.Errorf("expected docs/changes/Bad to not exist, stat err = %v", err)
	}
}

// TestNewCommand_GateFailureCreatesNothing verifies that when the framework
// gate fails (no homonto.toml here), "onto new" returns a non-zero exit
// and creates no docs/changes directory at all.
func TestNewCommand_GateFailureCreatesNothing(t *testing.T) {
	dir := t.TempDir()

	cmd := NewRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"new", "feature-x", "--dir", dir})

	if err := cmd.Execute(); err == nil {
		t.Fatal("execute() = nil, want error")
	}

	if _, err := os.Stat(filepath.Join(dir, "docs", "changes")); !os.IsNotExist(err) {
		t.Errorf("expected docs/changes to not exist, stat err = %v", err)
	}
}

func TestNewCommand_WorkflowFlag_SetsWorkflow(t *testing.T) {
	for _, wf := range []string{"full", "fix", "tweak"} {
		t.Run(wf, func(t *testing.T) {
			dir := setUpGatedWorkspace(t)
			if _, err := runOnto(t, "new", "feature-x", "--workflow", wf, "--dir", dir); err != nil {
				t.Fatalf("new --workflow %s: %v", wf, err)
			}
			st, err := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-x", "onto-state.yaml"))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if st.Workflow != wf {
				t.Errorf("Workflow = %q, want %q", st.Workflow, wf)
			}
		})
	}
}

func TestNewCommand_WorkflowDefaultsFull(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	if _, err := runOnto(t, "new", "feature-y", "--dir", dir); err != nil {
		t.Fatalf("new: %v", err)
	}
	st, _ := ontostate.Load(filepath.Join(dir, "docs", "changes", "feature-y", "onto-state.yaml"))
	if st.Workflow != "full" {
		t.Errorf("Workflow = %q, want full", st.Workflow)
	}
}

func TestNewCommand_InvalidWorkflowCreatesNothing(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	if _, err := runOnto(t, "new", "feature-z", "--workflow", "epic", "--dir", dir); err == nil {
		t.Fatal("new --workflow epic succeeded, want rejection")
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "changes", "feature-z")); !os.IsNotExist(err) {
		t.Errorf("expected docs/changes/feature-z to not exist, stat err = %v", err)
	}
}
