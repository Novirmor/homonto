package tocli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewRefusesSiblingActiveName (ADR 0042): active names are globally
// unique — a name already active in the onto workflow is refused with the
// demotion hint, never duplicated.
func TestNewRefusesSiblingActiveName(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	os.MkdirAll(filepath.Join(dir, "docs", "changes", "taken"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "changes", "taken", "onto-state.yaml"), []byte("schema_version: 3\nchange: taken\nphase: open\n"), 0o644)

	if msg := runErrString(t, "new", "taken", "--dir", dir); !strings.Contains(msg, "onto workflow") || !strings.Contains(msg, "onto demote taken") {
		t.Fatalf("sibling-active name must be refused with the demotion hint: %q", msg)
	}
	if _, err := os.Stat(changeDir(dir, "taken")); err == nil {
		t.Fatal("refused new must not create the change")
	}
}

// TestDoctorReportsSiblingDuplicate: an active name colliding with the onto
// tree is a finding both workflows surface.
func TestDoctorReportsSiblingDuplicate(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "init", "--dir", dir)
	run(t, false, "new", "dupe", "--dir", dir)
	os.MkdirAll(filepath.Join(dir, "docs", "changes", "dupe"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "changes", "dupe", "onto-state.yaml"), []byte("schema_version: 3\nchange: dupe\nphase: open\n"), 0o644)

	findings, err := collectFindings(dir)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(findings, "\n")
	if !strings.Contains(joined, "also active in the onto workflow") {
		t.Fatalf("doctor must report the duplicate active name, findings: %v", findings)
	}
}

// TestStatusAllIncludesSiblingInventory: --all reports both workflows' active
// changes in one inventory (JSON object with tasks + onto arrays).
func TestStatusAllIncludesSiblingInventory(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	run(t, false, "init", "--dir", dir)
	run(t, false, "new", "mine", "--dir", dir)
	os.MkdirAll(filepath.Join(dir, "docs", "changes", "theirs"), 0o755)
	os.WriteFile(filepath.Join(dir, "docs", "changes", "theirs", "onto-state.yaml"), []byte("schema_version: 3\nchange: theirs\nphase: build\n"), 0o644)

	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--dir", dir, "--json", "--all"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var inv struct {
		Tasks []statusEntry `json:"tasks"`
		Onto  []statusEntry `json:"onto"`
	}
	if err := json.Unmarshal([]byte(out.String()), &inv); err != nil {
		t.Fatalf("status --all --json malformed: %v\n%s", err, out.String())
	}
	if len(inv.Tasks) != 1 || inv.Tasks[0].Change != "mine" {
		t.Fatalf("tasks inventory = %+v", inv.Tasks)
	}
	if len(inv.Onto) != 1 || inv.Onto[0].Change != "theirs" || inv.Onto[0].Phase != "build" {
		t.Fatalf("onto inventory = %+v", inv.Onto)
	}
}
