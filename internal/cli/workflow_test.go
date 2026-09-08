package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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
