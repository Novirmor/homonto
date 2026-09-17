package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/workflowstatus"
)

func TestWorkflowSnapshotWritesJSONToStdout(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "homonto.toml")
	if err := os.WriteFile(cfg, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("HOMONTO_WORKFLOW_SNAPSHOT_HELPER") == "1" {
		os.Exit(Execute([]string{"workflow", "snapshot", "--json", "--config", cfg}))
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorkflowSnapshotWritesJSONToStdout$")
	cmd.Env = append(os.Environ(), "HOMONTO_WORKFLOW_SNAPSHOT_HELPER=1")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("workflow snapshot: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	var snapshot workflowstatus.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("stdout is not snapshot JSON: %v\n%s", err, stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestWorkflowSnapshotJSONIsReadOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	change := filepath.Join(root, "docs", "tasks", "demo")
	if err := os.MkdirAll(change, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(change, "to-state.yaml"), []byte("change: demo\nphase: do\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(change, "plan.md"), []byte("- [ ] work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(change, "to-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"workflow", "snapshot", "--json", "--config", filepath.Join(root, "homonto.toml")})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Changes []struct {
			Name string `json:"name"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(out.Bytes(), &snapshot); err != nil {
		t.Fatalf("snapshot JSON: %v\n%s", err, out.String())
	}
	if len(snapshot.Changes) != 1 || snapshot.Changes[0].Name != "demo" {
		t.Fatalf("snapshot = %s", out.String())
	}
	after, err := os.ReadFile(filepath.Join(change, "to-state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("workflow snapshot changed state")
	}
}

func TestWorkflowHandoffJSON(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "selected.toml")
	if err := os.WriteFile(cfg, []byte("[workflow]\nroot = 'records'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte("invalid = ["), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "records/tasks/demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "to-state.yaml"), []byte("id: exact\nchange: demo\nphase: do\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := []string{"workflow", "handoff", "--workflow", "to", "--change", "demo", "--identity", "exact", "--json", "--config", cfg}
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(base)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got workflowstatus.Handoff
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ConfigPath != cfg || got.Change.Identity != "exact" || got.NextSkill != "to-do" {
		t.Fatalf("handoff = %s", out.String())
	}
	for _, extra := range [][]string{{"--write"}, {"--file", "/etc/passwd"}, {"--argv", "status"}, {"unexpected"}, {"--identity", "missing"}, {"--json=false"}} {
		cmd := NewRootCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(append(append([]string{}, base...), extra...))
		if err := cmd.Execute(); err == nil {
			t.Errorf("accepted %v", extra)
		}
	}
}

func TestWorkflowSnapshotUsesExactConfigFilename(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "custom.toml")
	if err := os.WriteFile(path, []byte("[workflow]\nroot = 'selected'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "homonto.toml"), []byte("invalid = ["), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"workflow", "snapshot", "--json", "--config", path})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got struct {
		ConfigPath, WorkflowRoot string
		Findings                 []any
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ConfigPath != path || got.WorkflowRoot != filepath.Join(root, "selected") || len(got.Findings) != 0 {
		t.Fatalf("lost selected config: %s", out.String())
	}
}
