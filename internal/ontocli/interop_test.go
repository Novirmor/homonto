package ontocli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewRefusesSiblingActiveName (ADR 0042): active names are globally
// unique — a name already active in the `to` workflow is refused with the
// promotion hint, never duplicated.
func TestNewRefusesSiblingActiveName(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	os.MkdirAll(filepath.Join(dir, "docs", "tasks", "taken"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "tasks", "taken", "to-state.yaml"), []byte("change: taken\nphase: do\n"), 0o644)

	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"new", "taken", "--dir", dir})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "the `to` workflow") || !strings.Contains(err.Error(), "to promote taken") {
		t.Fatalf("sibling-active name must be refused with the promotion hint: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "docs", "changes", "taken")); serr == nil {
		t.Fatal("refused new must not create the change")
	}
}

// TestDoctorReportsSiblingDuplicate: an active name colliding with the `to`
// tree is a finding both workflows surface.
func TestDoctorReportsSiblingDuplicate(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	if _, err := run(t, "new", "dupe", "--dir", dir); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "docs", "tasks", "dupe"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "tasks", "dupe", "to-state.yaml"), []byte("change: dupe\nphase: plan\n"), 0o644)

	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"doctor", "--dir", dir})
	// doctor prints findings; a duplicate-name finding must be among them.
	if err := cmd.Execute(); err != nil {
		_ = err // findings make doctor exit non-zero; the text is the assert
	}
	if !strings.Contains(out.String(), "also active in the `to` workflow") {
		t.Fatalf("doctor must report the duplicate active name, output: %s", out.String())
	}
}
