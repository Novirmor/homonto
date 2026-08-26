package initws

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

func TestInitRefusesExistingWorkspace(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, ".homonto", "config.toml")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatalf("create control directory: %v", err)
	}
	if err := os.WriteFile(manifest, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	_, err := Init(context.Background(), InitInput{
		Root: root, Workflow: workspacecfg.WorkflowTask,
	})
	if !errors.Is(err, ErrAlreadyInitialized) {
		t.Fatalf("Init error = %v, want ErrAlreadyInitialized", err)
	}
}
