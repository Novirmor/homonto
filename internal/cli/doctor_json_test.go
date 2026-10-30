package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctor_JSONOutput(t *testing.T) {
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
	cmd.SetArgs([]string{"doctor", "--output", "json", "--config", cfg})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("doctor --output json: %v (out=%q)", err, out.String())
	}
	var got struct {
		Findings []string `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("output is not a single JSON object: %v\n%s", err, out.String())
	}
}

func TestDoctor_InvalidOutput(t *testing.T) {
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"doctor", "--output", "yaml"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("doctor --output yaml accepted, want rejection")
	}
}
