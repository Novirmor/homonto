package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/noviopenworks/homonto/internal/gitx"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/task"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// newGitWorkspace builds the smallest workspace Open accepts: one Git
// control repository that is also the only member, with a seeded commit.
func newGitWorkspace(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("the recovery fixture needs a POSIX shell and git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	ctx := context.Background()
	if err := gitx.Init(ctx, gitx.ExecRunner{}, root); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# ws\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.email=t@example.com", "-c", "user.name=T", "commit", "-m", "seed"},
	} {
		if _, err := (gitx.ExecRunner{}).Run(ctx, root, args...); err != nil {
			t.Fatalf("git %s: %v", args[0], err)
		}
	}
	controlID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("NewRepositoryID: %v", err)
	}
	workspaceID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("NewWorkspaceID: %v", err)
	}
	manifest, err := workspacecfg.Marshal(workspacecfg.Config{
		SchemaVersion: workspacecfg.CurrentSchemaVersion,
		Workspace:     workspacecfg.Workspace{ID: workspaceID, Workflow: workspacecfg.WorkflowTask},
		Control:       workspacecfg.Control{ID: controlID, Path: "."},
	})
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	dir := filepath.Join(root, ControlDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), manifest, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return root
}

// TestOpenRecoversPendingLeaseOperations proves the recovery pass runs
// with every shipped effect kind registered. A crash between journaling
// and applying a lease acquisition used to wedge the workspace: the next
// Open recovered pending operations before the lease manager existed, so
// its effect kinds were unknown to the manager and every later command
// refused to open the workspace at all.
func TestOpenRecoversPendingLeaseOperations(t *testing.T) {
	root := newGitWorkspace(t)
	ctx := context.Background()

	a, err := Open(ctx, Options{Root: root})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	restore := operation.SetFailpointHook(func(point string) {
		if point == "prepared" {
			panic("simulated crash after prepare")
		}
	})
	func() {
		defer restore()
		defer func() { _ = recover() }()
		_, _ = a.StartTask(ctx, task.StartInput{Name: "crash-mid-lease", Goal: "prove recovery"})
	}()
	if err := a.Close(); err != nil {
		t.Fatalf("close after the crash: %v", err)
	}

	// The lease acquisition's effects were prepared and journaled, so the
	// next open must roll them forward and succeed, not refuse the
	// workspace.
	reopened, err := Open(ctx, Options{Root: root})
	if err != nil {
		t.Fatalf("reopen after an interrupted lease acquisition: %v", err)
	}
	defer reopened.Close()
}

// TestOpenRecoversPendingHandoffOperations proves a prepared handoff
// operation — journaled by a crashed portable handoff or attach — is
// recoverable by an ordinary Open. The rows are written directly, exactly
// as a crash between prepare and apply would have left them; the effect
// targets a sentinel that does not exist, whose removal is an idempotent
// no-op, so recovery's verdict is about dispatch, not filesystem state.
func TestOpenRecoversPendingHandoffOperations(t *testing.T) {
	root := newGitWorkspace(t)
	ctx := context.Background()

	db, err := store.Open(ctx, filepath.Join(root, ControlDir, "runtime.db"), store.OpenOptions{})
	if err != nil {
		t.Fatalf("open the runtime database: %v", err)
	}
	opID := identity.OperationID("op-handoff-recovery-test")
	now := time.Now().UTC()
	err = db.Update(ctx, func(tx *store.Tx) error {
		if err := tx.InsertOperation(ctx, store.OperationRecord{
			ID: opID, Kind: "handoff.attach", State: store.OpPrepared,
			Policy: string(operation.RollForward), Payload: json.RawMessage(`{}`),
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			return err
		}
		return tx.InsertEffect(ctx, store.EffectRow{
			OpID: opID, Seq: 1, Kind: "handoff.sentinel_remove", State: store.EffectPending,
			Payload: json.RawMessage(`{"path":"/nonexistent/sentinel.json","content":{}}`),
		})
	})
	if err != nil {
		t.Fatalf("journal the interrupted handoff operation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close the runtime database: %v", err)
	}

	reopened, err := Open(ctx, Options{Root: root})
	if err != nil {
		t.Fatalf("reopen with a pending handoff operation: %v", err)
	}
	defer reopened.Close()
}
