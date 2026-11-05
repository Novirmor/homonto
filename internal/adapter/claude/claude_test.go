package claude

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeFindingsReportsPATHWhenMCPFileCannotBeRead(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New(t.TempDir(), "").WithProjectRoot(root).WithWorkflow(&Workflow{ConfigPath: filepath.Join(root, "homonto.toml")})
	findings := strings.Join(a.RuntimeFindings(func(string) (string, error) {
		return "", errors.New("not found")
	}), "\n")
	if !strings.Contains(findings, "cannot read") {
		t.Errorf("unreadable MCP file is not reported:\n%s", findings)
	}
	if !strings.Contains(findings, "not on PATH") {
		t.Errorf("missing homonto binary is not reported:\n%s", findings)
	}
}
