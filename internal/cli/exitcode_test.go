package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanExitCode_Helper(t *testing.T) {
	if got := planExitCode(true, 0, false); got != 2 {
		t.Errorf("planExitCode(pending) = %d, want 2", got)
	}
	if got := planExitCode(false, 0, false); got != 0 {
		t.Errorf("planExitCode(clean) = %d, want 0", got)
	}
	if got := planExitCode(false, 1, false); got != 2 {
		t.Errorf("planExitCode(repins) = %d, want 2", got)
	}
	if got := planExitCode(false, 0, true); got != 2 {
		t.Errorf("planExitCode(stale catalog) = %d, want 2", got)
	}
}

func TestStatusExitCode_Helper(t *testing.T) {
	if got := statusExitCode(true, 0); got != 3 {
		t.Errorf("statusExitCode(drift) = %d, want 3", got)
	}
	if got := statusExitCode(false, 2); got != 2 {
		t.Errorf("statusExitCode(pending) = %d, want 2", got)
	}
	if got := statusExitCode(false, 0); got != 0 {
		t.Errorf("statusExitCode(clean) = %d, want 0", got)
	}
}

func TestExecute_PlanExitCodeFlag(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := filepath.Join(t.TempDir(), "homonto.toml")
	if err := os.WriteFile(cfg, []byte("[settings.opencode]\nshare = \"manual\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Execute([]string{"plan", "--exit-code", "--config", cfg}); code != 2 {
		t.Errorf("plan --exit-code (pending) = %d, want 2", code)
	}
	if code := Execute([]string{"plan", "--config", cfg}); code != 0 {
		t.Errorf("plan without --exit-code = %d, want 0", code)
	}
}
