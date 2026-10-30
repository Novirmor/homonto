package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatus_JSONOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(cfg, []byte("[settings.opencode]\nshare = \"manual\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--output", "json", "--config", cfg})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("status --output json: %v (out=%q)", err, out.String())
	}
	var got struct {
		Drift    []string `json:"drift"`
		Pending  int      `json:"pending"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("output is not a single JSON object: %v\n%s", err, out.String())
	}
}

func TestStatus_InvalidOutput(t *testing.T) {
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"status", "--output", "xml"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("status --output xml accepted, want rejection")
	}
}
