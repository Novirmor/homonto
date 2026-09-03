package ontocli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/buildinfo"
	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/ontostate"
)

// seedDocsLayout creates the full docs/{changes,specs,adr,guides} layout under
// root so a workspace passes the doctor's docs-layout check.
func seedDocsLayout(t *testing.T, root string) {
	t.Helper()
	for _, d := range docsLayout {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatalf("seedDocsLayout: creating %s: %v", d, err)
		}
	}
}

// seedActive writes docs/changes/<name>/onto-state.yaml (given phase, deps,
// archived:false) plus each named artifact file, so the change is picked up by
// the active-change glob.
func seedActive(t *testing.T, root, name, phase string, artifacts, deps []string) {
	t.Helper()
	changeDir := filepath.Join(root, "docs", "changes", name)
	st := ontostate.State{Change: name, Workflow: "full", Phase: phase, Created: "2026-07-11", Deps: deps}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatalf("seedActive: saving state: %v", err)
	}
	for _, a := range artifacts {
		writeFile(t, filepath.Join(changeDir, a), "")
	}
}

// seedArchived writes docs/changes/archive/<date>-<name>/onto-state.yaml with
// the given archived flag, so the archive-layout check sees it.
func seedArchived(t *testing.T, root, name string, archived bool) {
	t.Helper()
	entry := filepath.Join(root, "docs", "changes", "archive", "2026-07-11-"+name)
	st := ontostate.State{Change: name, Workflow: "full", Phase: "close", Created: "2026-07-11", Archived: archived}
	if err := ontostate.Save(filepath.Join(entry, "onto-state.yaml"), st); err != nil {
		t.Fatalf("seedArchived: saving state: %v", err)
	}
}

// execDoctor drives "onto doctor --dir tmp" through the public root command,
// capturing combined stdout+stderr, and returns the output and Execute error.
func execDoctor(t *testing.T, tmp string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"doctor", "--dir", tmp})
	err := cmd.Execute()
	return buf.String(), err
}

// writeHomontoState writes a minimal .homonto/state.json carrying only the
// homontoVersion field, so the doctor's skew check has a value to compare.
func writeHomontoState(t *testing.T, root, homontoVersion string) {
	t.Helper()
	dir := filepath.Join(root, ".homonto")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "state.json"), `{"homontoVersion":"`+homontoVersion+`"}`)
}

func TestDoctorReportsVersionSkew(t *testing.T) {
	// Matching version (this binary's own resolved version) → no skew.
	match := t.TempDir()
	seedDocsLayout(t, match)
	writeHomontoState(t, match, buildinfo.Resolve(Version, buildinfo.DevVersion))
	if out, err := execDoctor(t, match); err != nil || !strings.Contains(out, "healthy") {
		t.Fatalf("matching versions must be healthy; out=%q err=%v", out, err)
	}

	// Divergent version → a skew finding and a non-zero exit.
	skew := t.TempDir()
	seedDocsLayout(t, skew)
	writeHomontoState(t, skew, "v99.0.0")
	out, err := execDoctor(t, skew)
	if err == nil {
		t.Fatalf("version skew must be a finding (non-nil error); out=%q", out)
	}
	if !strings.Contains(out, "version skew") || !strings.Contains(out, "v99.0.0") {
		t.Fatalf("skew finding missing; out=%q", out)
	}
}

func TestDoctorNoSkewWhenNoHomontoState(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	// No .homonto/state.json → skew check is skipped, workspace is healthy.
	if out, err := execDoctor(t, tmp); err != nil || !strings.Contains(out, "healthy") {
		t.Fatalf("absent homonto state must skip skew; out=%q err=%v", out, err)
	}
}

// 1. healthy workspace → exit 0, "healthy\n".
func TestDoctorCommand_Healthy(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	seedActive(t, tmp, "alpha", "open", []string{"proposal.md", "tasks.md"}, nil)
	seedArchived(t, tmp, "beta", true)

	out, err := execDoctor(t, tmp)
	if err != nil {
		t.Fatalf("execute() = %v, want nil; out=%q", err, out)
	}
	if out != "healthy\n" {
		t.Errorf("out = %q, want %q", out, "healthy\n")
	}
}

// 2. missing docs/adr → non-nil, names the missing directory.
func TestDoctorCommand_MissingDocsDir(t *testing.T) {
	tmp := t.TempDir()
	for _, d := range []string{
		filepath.Join("docs", "changes"),
		filepath.Join("docs", "specs"),
		filepath.Join("docs", "guides"),
	} {
		if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}
	if want := filepath.Join("docs", "adr"); !strings.Contains(out, want) {
		t.Errorf("out = %q, want it to mention %q", out, want)
	}
}

// 3. malformed active state (malformed YAML) → non-nil, names change + "malformed".
func TestDoctorCommand_InvalidActiveState(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	writeFile(t, filepath.Join(tmp, "docs", "changes", "broken", "onto-state.yaml"), "a: b: c\n")

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}
	if !strings.Contains(out, "broken") {
		t.Errorf("out = %q, want it to name %q", out, "broken")
	}
	if !strings.Contains(out, "malformed") {
		t.Errorf("out = %q, want it to contain %q", out, "malformed")
	}
}

// 4. phase build missing plan.md → non-nil, names the missing artifact.
func TestDoctorCommand_PhaseMissingArtifact(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	seedActive(t, tmp, "gamma", "build", []string{"proposal.md", "tasks.md", "design.md"}, nil)

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}
	if !strings.Contains(out, "plan.md") {
		t.Errorf("out = %q, want it to mention %q", out, "plan.md")
	}
}

// 5. unresolved dependency → non-nil, contains the dep name.
func TestDoctorCommand_UnresolvedDep(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	seedActive(t, tmp, "delta", "open", []string{"proposal.md", "tasks.md"}, []string{"missing"})

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}
	if !strings.Contains(out, "missing") {
		t.Errorf("out = %q, want it to contain %q", out, "missing")
	}
}

// 6. active change marked archived:true → non-nil, contains "archived".
func TestDoctorCommand_ActiveMarkedArchived(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	changeDir := filepath.Join(tmp, "docs", "changes", "eps")
	st := ontostate.State{Change: "eps", Workflow: "full", Phase: "open", Created: "2026-07-11", Archived: true}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		t.Fatalf("saving state: %v", err)
	}
	writeFile(t, filepath.Join(changeDir, "proposal.md"), "")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "")

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}
	if want := "eps: active change marked archived"; !strings.Contains(out, want) {
		t.Errorf("out = %q, want it to contain %q", out, want)
	}
}

// 7. archive entry with archived:false → non-nil, names the archive entry.
func TestDoctorCommand_ArchiveEntryNotArchived(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	seedArchived(t, tmp, "zeta", false)

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}
	if want := "not marked archived"; !strings.Contains(out, want) {
		t.Errorf("out = %q, want it to contain %q (naming archive entry zeta)", out, want)
	}
	if !strings.Contains(out, "zeta") {
		t.Errorf("out = %q, want it to name the archive entry %q", out, "zeta")
	}
}

func TestDoctorCommand_ReportsPendingIntegration(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	seedArchived(t, tmp, "zeta", true)
	archiveDir := filepath.Join(tmp, "docs", "changes", "archive", "2026-07-11-zeta")
	st, err := ontostate.Load(filepath.Join(archiveDir, "onto-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	st.Integration = "merge"
	st.BaseBranch = "main"
	if err := ontostate.Save(filepath.Join(archiveDir, "onto-state.yaml"), st); err != nil {
		t.Fatal(err)
	}
	if err := integrationrecord.Save(archiveDir, integrationrecord.NewPending("zeta", "merge", "main", []integrationrecord.Entry{{BaseBranch: "main", BaseCommit: "1111111111111111111111111111111111111111", SourceBranch: "change/zeta", SourceCommit: "2222222222222222222222222222222222222222"}})); err != nil {
		t.Fatal(err)
	}

	out, err := execDoctor(t, tmp)
	if err == nil || !strings.Contains(out, "integration pending") || !strings.Contains(out, "complete-integration") {
		t.Fatalf("doctor did not report actionable pending integration; out=%q err=%v", out, err)
	}
}

// 8. ungated read-only: bare temp dir → still runs (non-nil for layout
// findings) AND writes nothing (no homonto.toml, no docs/, tree unchanged).
func TestDoctorCommand_UngatedReadOnly(t *testing.T) {
	tmp := t.TempDir()
	before := treeSnapshot(t, tmp)

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}

	if _, statErr := os.Stat(filepath.Join(tmp, "homonto.toml")); !os.IsNotExist(statErr) {
		t.Errorf("homonto.toml stat err = %v, want IsNotExist (nothing created)", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(tmp, "docs")); !os.IsNotExist(statErr) {
		t.Errorf("docs/ stat err = %v, want IsNotExist (nothing created)", statErr)
	}

	after := treeSnapshot(t, tmp)
	if len(before) != len(after) {
		t.Errorf("tree changed: before %d files, after %d files", len(before), len(after))
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Errorf("command created a new file: %s", path)
		}
	}
}

func TestDoctor_MissingStateDir_IsFinding(t *testing.T) {
	dir := t.TempDir()
	for _, d := range []string{"changes", "specs", "adr", "guides"} {
		if err := os.MkdirAll(filepath.Join(dir, "docs", d), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	// a change directory with no state file (deleted)
	if err := os.MkdirAll(filepath.Join(dir, "docs", "changes", "gamma"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	out, err := runOnto(t, "doctor", "--dir", dir)
	if err == nil {
		t.Fatal("doctor exited 0 with a missing-state change dir, want non-zero")
	}
	if !strings.Contains(out, "gamma") || !strings.Contains(out, "missing-state") {
		t.Errorf("output = %q, want a gamma missing-state finding", out)
	}
}

func TestDoctorQuiet_ExitCodeOnly(t *testing.T) {
	// Healthy → no output, nil error.
	healthy := t.TempDir()
	seedDocsLayout(t, healthy)
	out, err := execDoctorArgs(t, "--dir", healthy, "--quiet")
	if err != nil || out != "" {
		t.Fatalf("quiet healthy: out=%q err=%v, want empty+nil", out, err)
	}
	// A finding → non-nil error (non-zero exit) and no findings on stdout.
	broken := t.TempDir()
	seedDocsLayout(t, broken)
	writeFile(t, filepath.Join(broken, "docs", "changes", "bad", "onto-state.yaml"), "not: [valid")
	out, err = execDoctorArgs(t, "--dir", broken, "--quiet")
	if err == nil {
		t.Fatal("quiet with a finding must return a non-nil error (non-zero exit)")
	}
	if strings.Contains(out, "malformed") {
		t.Errorf("--quiet must not print findings to stdout: %q", out)
	}
}

func execDoctorArgs(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs(append([]string{"doctor"}, args...))
	return buf.String(), cmd.Execute()
}

// 12. tasks.md <-> plan.md drift is a doctor finding, not just close-time prose.
// This is the resumability guarantee: a fresh session resumes at the first
// unchecked tasks.md item and needs its detail under a matching plan.md heading.
func TestDoctorCommand_TaskPlanDrift(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	seedActive(t, tmp, "drifted", "build", []string{"proposal.md", "design.md"}, nil)
	changeDir := filepath.Join(tmp, "docs", "changes", "drifted")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "# Tasks\n\n- [ ] 1.1 a\n- [ ] 1.2 discovered\n")
	writeFile(t, filepath.Join(changeDir, "plan.md"), "# Plan\n\n## Task 1.1 — a\n")

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}
	if !strings.Contains(out, "1.2") {
		t.Errorf("out = %q, want it to name the task number missing from plan.md", out)
	}
}

// 13. A preset has no plan.md, and that must not read as drift — otherwise
// doctor is permanently unhappy on every fix/tweak change.
func TestDoctorCommand_PresetWithoutPlanIsHealthy(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	seedActive(t, tmp, "preset", "open", []string{"proposal.md"}, nil)
	writeFile(t, filepath.Join(tmp, "docs", "changes", "preset", "tasks.md"), "# Tasks\n\n- [ ] 1.1 a\n")

	out, err := execDoctor(t, tmp)
	if err != nil {
		t.Fatalf("execute() = %v, want nil; out=%q", err, out)
	}
	if !strings.Contains(out, "healthy") {
		t.Errorf("out = %q, want healthy", out)
	}
}

// 14. plan.md must carry no completion state (ADR 0018).
func TestDoctorCommand_CheckboxInPlanIsFinding(t *testing.T) {
	tmp := t.TempDir()
	seedDocsLayout(t, tmp)
	seedActive(t, tmp, "boxed", "build", []string{"proposal.md", "design.md"}, nil)
	changeDir := filepath.Join(tmp, "docs", "changes", "boxed")
	writeFile(t, filepath.Join(changeDir, "tasks.md"), "# Tasks\n\n- [ ] 1.1 a\n")
	writeFile(t, filepath.Join(changeDir, "plan.md"), "# Plan\n\n## Task 1.1 — a\n\n- [x] 1.1 done\n")

	out, err := execDoctor(t, tmp)
	if err == nil {
		t.Fatalf("execute() = nil, want error; out=%q", out)
	}
	if !strings.Contains(out, "single checkoff") {
		t.Errorf("out = %q, want it to name tasks.md as the single checkoff", out)
	}
}
