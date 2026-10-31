package tocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Novirmor/homonto/internal/buildinfo"
	"github.com/Novirmor/homonto/internal/tostate"
	"github.com/Novirmor/homonto/internal/workcli"
)

// writeHomontoState writes a minimal .homonto/state.json carrying only the
// homontoVersion field, mirroring ontocli's doctor test helper.
func writeHomontoState(t *testing.T, root, homontoVersion string) {
	t.Helper()
	dir := filepath.Join(root, ".homonto")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "state.json"), `{"homontoVersion":"`+homontoVersion+`"}`)
}

// TestDoctor_NoSkewWhenMatching verifies that a homonto state whose version
// resolves to this binary's own version does NOT trip the skew check.
func TestDoctor_NoSkewWhenMatching(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	writeHomontoState(t, dir, buildinfo.Resolve(Version, buildinfo.DevVersion))
	if out := run(t, false, "doctor", "--dir", dir); !strings.Contains(out, "healthy") {
		t.Errorf("matching-version doctor = %q, want healthy", out)
	}
}

// TestDoctor_VersionSkew exercises the divergent-version path: a recorded
// homonto version that does not match the binary's resolved version is a
// finding naming the recorded value and the remediation.
func TestDoctor_VersionSkew(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	writeHomontoState(t, dir, "v99.0.0")
	out := run(t, true, "doctor", "--dir", dir)
	for _, want := range []string{"version skew", "v99.0.0", "homonto update"} {
		if !strings.Contains(out, want) {
			t.Errorf("version-skew doctor = %q missing %q", out, want)
		}
	}
}

// TestDoctor_NoSkewWhenNoHomontoState verifies the boundary: with no
// .homonto/state.json at all, the skew check is silently skipped (best-effort,
// never a false finding for a repo that simply lacks the file).
func TestDoctor_NoSkewWhenNoHomontoState(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	if out := run(t, false, "doctor", "--dir", dir); !strings.Contains(out, "healthy") {
		t.Errorf("no-state doctor = %q, want healthy (skew check skipped)", out)
	}
}

// TestDoctor_ArchiveEntryNotTerminal verifies the archive walk flags an entry
// whose state is somehow non-terminal (the archive is for terminal changes
// only).
func TestDoctor_ArchiveEntryNotTerminal(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	archived := filepath.Join(archiveDir(dir), "2030-01-01-misarchived")
	if err := tostate.Save(filepath.Join(archived, tostate.FileName), tostate.State{
		Change: "misarchived", Phase: tostate.PhaseDo, Created: "2030-01-01",
	}); err != nil {
		t.Fatal(err)
	}
	out := run(t, true, "doctor", "--dir", dir)
	for _, want := range []string{"archive/", "not terminal", "do"} {
		if !strings.Contains(out, want) {
			t.Errorf("non-terminal-archive doctor = %q missing %q", out, want)
		}
	}
}

// TestDoctor_ArchiveEntryCorruptState verifies the archive walk reports an
// entry with a malformed or missing state file.
func TestDoctor_ArchiveEntryCorruptState(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	archived := filepath.Join(archiveDir(dir), "2030-01-01-corrupt")
	writeFile(t, filepath.Join(archived, tostate.FileName), "phase: not-a-phase\n")
	out := run(t, true, "doctor", "--dir", dir)
	for _, want := range []string{"archive/", "invalid"} {
		if !strings.Contains(out, want) {
			t.Errorf("corrupt-archive doctor = %q missing %q", out, want)
		}
	}
}

// TestDoctor_ActiveMissingPlan verifies an active change directory without a
// plan.md is reported.
func TestDoctor_ActiveMissingPlan(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "planless", "--dir", dir)
	if err := os.Remove(planPath(dir, "planless")); err != nil {
		t.Fatal(err)
	}
	out := run(t, true, "doctor", "--dir", dir)
	if !strings.Contains(out, "plan.md is missing") {
		t.Errorf("missing-plan doctor = %q, want plan.md-missing finding", out)
	}
}

// TestDoctor_AbortedWedgeSuggestsAbandonCommand verifies the wedge finding
// names the right command for an abandoned-but-active change (not the done
// command).
func TestDoctor_AbortedWedgeSuggestsAbandonCommand(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "new", "stuck", "--dir", dir)
	if err := tostate.Save(statePath(dir, "stuck"), tostate.State{
		Change: "stuck", Phase: tostate.PhaseAbandoned, Finished: "2030-01-01",
	}); err != nil {
		t.Fatal(err)
	}
	out := run(t, true, "doctor", "--dir", dir)
	for _, want := range []string{"interrupted archive", "to abandon stuck"} {
		if !strings.Contains(out, want) {
			t.Errorf("abandoned-wedge doctor = %q missing %q", out, want)
		}
	}
}

// TestErrQuietFindingsAliasesWorkcliSentinel verifies the package-level alias
// is the SAME value as workcli.ErrQuietFindings (errors.Is contract the main
// relies on).
func TestErrQuietFindingsAliasesWorkcliSentinel(t *testing.T) {
	if ErrQuietFindings != workcli.ErrQuietFindings {
		t.Errorf("ErrQuietFindings is a distinct value from workcli.ErrQuietFindings; the main's errors.Is check would break")
	}
}

// TestDoctor_NoTasksDirIsHealthy verifies the empty-workspace contract: a
// missing docs/tasks/ is healthy (the repo may not use to yet), not a finding.
func TestDoctor_NoTasksDirIsHealthy(t *testing.T) {
	dir := t.TempDir()
	if out := run(t, false, "doctor", "--dir", dir); !strings.Contains(out, "healthy") {
		t.Errorf("no-tasks-dir doctor = %q, want healthy", out)
	}
}

// TestDoctor_FindingsShapeAsJSONNotSupported is a negative contract: doctor
// has no --json flag, and trying to pass it surfaces cobra's unknown-flag
// error rather than silently dropping the request.
func TestDoctor_FindingsShapeAsJSONNotSupported(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	if err := runJSON(t, "doctor", "--json", "--dir", dir); err == nil {
		t.Errorf("doctor --json returned nil; want an unknown-flag error")
	}
}

// runJSON is a small helper used only in negative tests: it returns the
// Execute error so the caller can assert unknown-flag behavior.
func runJSON(t *testing.T, args ...string) error {
	t.Helper()
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	return cmd.Execute()
}
