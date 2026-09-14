package workflowstatus

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGithubDraftRuntime(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node unavailable; GitHub draft runtime contract not run")
	}
	source, err := filepath.Abs("../../catalog/plugins/homonto-workflow/github.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/github-runtime.mjs", source)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("GitHub draft runtime: %v\n%s", err, out)
	}
}
