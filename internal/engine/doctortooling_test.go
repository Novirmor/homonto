package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/config"
)

func absentPath(string) (string, error) { return "", errors.New("not found") }
func presentPath(n string) (string, error) {
	return filepath.Join("/usr/bin", n), nil
}

func hasWarnMentioning(findings []string, want string) bool {
	for _, f := range findings {
		if strings.HasPrefix(f, "warn:") && strings.Contains(f, want) {
			return true
		}
	}
	return false
}

func hasAnyWarn(findings []string) bool {
	for _, f := range findings {
		if strings.HasPrefix(f, "warn:") {
			return true
		}
	}
	return false
}

// A provider the user declared but that is nowhere to be found is a degraded
// workflow the user should hear about — as a warning, never a failure.
func TestToolingFindings_DeclaredButAbsentWarns(t *testing.T) {
	got := toolingFindings(config.Tooling{ShellProxy: "rtk", CodeIntel: "graphify"}, t.TempDir(), absentPath)
	if !hasWarnMentioning(got, "rtk") {
		t.Errorf("absent rtk should warn, got %v", got)
	}
	if !hasWarnMentioning(got, "graphify") {
		t.Errorf("absent graphify should warn, got %v", got)
	}
}

// Nothing declared means nothing to probe and nothing to say.
func TestToolingFindings_NoneIsSilent(t *testing.T) {
	got := toolingFindings(config.Tooling{ShellProxy: "none", CodeIntel: "none"}, t.TempDir(), absentPath)
	if len(got) != 0 {
		t.Errorf("undeclared providers should produce no findings, got %v", got)
	}
}

// A provider found on PATH reports ok, not a warning.
func TestToolingFindings_PresentOnPathIsOK(t *testing.T) {
	got := toolingFindings(config.Tooling{ShellProxy: "rtk", CodeIntel: "none"}, t.TempDir(), presentPath)
	if hasAnyWarn(got) {
		t.Errorf("rtk present on PATH should not warn, got %v", got)
	}
}

// An index directory counts as present even when the binary is not on PATH:
// grounding works off the index, which is the thing that matters.
func TestToolingFindings_IndexDirectoryCountsAsPresent(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "graphify-out"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := toolingFindings(config.Tooling{ShellProxy: "none", CodeIntel: "graphify"}, repo, absentPath)
	if hasAnyWarn(got) {
		t.Errorf("graphify index present should not warn, got %v", got)
	}
}

func TestToolingFindings_OKFBundleCountsAsPresent(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".okf"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := toolingFindings(config.Tooling{ShellProxy: "none", CodeIntel: "okf"}, repo, absentPath)
	if hasAnyWarn(got) {
		t.Errorf("okf bundle present should not warn, got %v", got)
	}
}

// The finding is advisory: a missing provider must never fail a projection.
func TestToolingFindings_NeverBlocksApply(t *testing.T) {
	for _, f := range toolingFindings(config.Tooling{ShellProxy: "rtk", CodeIntel: "okf"}, t.TempDir(), absentPath) {
		if !strings.HasPrefix(f, "warn:") && !strings.HasPrefix(f, "ok:") {
			t.Errorf("finding %q is neither warn: nor ok: — doctor findings are advisory only", f)
		}
	}
}
