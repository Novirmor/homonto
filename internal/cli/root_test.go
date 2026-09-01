package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); got != "homonto "+Version+"\n" {
		t.Fatalf("got %q", got)
	}
}

// TestApplyFailsWhenAdapterSkipped: when the (only) adapter's plan fails —
// here a corrupt opencode.jsonc — apply must not hard-fail but must exit
// non-zero with a skipped-adapters summary. "No changes" with exit 0 would
// hide that nothing was written at all. Plan/status keep exit 0 with warnings.
func TestApplyFailsWhenAdapterSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfgFile := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	if err := os.MkdirAll(filepath.Dir(cfgFile), 0o755); err != nil {
		t.Fatal(err)
	}
	// Corrupt opencode.jsonc: the OpenCode adapter's Plan fails and is skipped.
	if err := os.WriteFile(cfgFile, []byte(`[not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	cfg := filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(cfg, []byte("[settings.opencode]\nmodel=\"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"apply", "--yes", "--config", cfg})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("apply with a skipped adapter must exit non-zero; output:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "completed with skipped adapters") {
		t.Fatalf("error = %v; want a skipped-adapters summary", err)
	}
	// A skipped adapter's file is never written: the corrupt bytes must still
	// be on disk, not replaced by a homonto-authored document.
	if b, rerr := os.ReadFile(cfgFile); rerr != nil || string(b) != `[not json` {
		t.Fatalf("skipped adapter's file must be left untouched, got %q (%v)", string(b), rerr)
	}
}
