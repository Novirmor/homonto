package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlan_JSONOutput(t *testing.T) {
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
	cmd.SetArgs([]string{"plan", "--output", "json", "--config", cfg})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("plan --output json: %v (out=%q)", err, out.String())
	}
	var got struct {
		Changes []struct {
			Tool    string `json:"tool"`
			Changes []struct {
				Action string `json:"action"`
				Key    string `json:"key"`
			} `json:"changes"`
		} `json:"changes"`
		Repins []struct {
			Name string `json:"name"`
		} `json:"repins"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("output is not a single JSON object: %v\n%s", err, out.String())
	}
	// the demo setting on a fresh config yields at least one create change
	if len(got.Changes) == 0 {
		t.Errorf("expected pending changes for a fresh config, got none: %s", out.String())
	}
	// secret safety: no old/new value keys in the JSON
	if strings.Contains(out.String(), "\"old\"") || strings.Contains(out.String(), "\"new\"") {
		t.Errorf("plan json must not include old/new change values: %s", out.String())
	}
}

func TestPlan_InvalidOutput(t *testing.T) {
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"plan", "--output", "toml"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("plan --output toml accepted, want rejection")
	}
}
