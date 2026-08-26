package portable

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

func TestRefreshCheckpointLeavesAbsentCheckpointUntouched(t *testing.T) {
	root := t.TempDir()
	manager := NewManager(root, workspacecfg.Config{}, nil, filepath.Join(root, "snapshots"), nil)

	if err := manager.RefreshCheckpoint(context.Background(), identity.WorkID("work-1"), "plan"); err != nil {
		t.Fatalf("RefreshCheckpoint: %v", err)
	}
	_, err := os.Stat(filepath.Join(root, ".homonto", "checkpoint.json"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("checkpoint stat error = %v, want not exist", err)
	}
}
