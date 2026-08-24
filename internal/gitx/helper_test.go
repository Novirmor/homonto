package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
)

// env wires a workspace control root, a member repository, a runtime
// database, and a Service together for one test. The member is a real git
// repository seeded with one commit.
type env struct {
	root   string
	member string
	repoID identity.RepositoryID
	workID identity.WorkID
	dbPath string
	db     *store.DB
	ops    *operation.Manager
	svc    *Service
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		root:   t.TempDir(),
		member: t.TempDir(),
		dbPath: filepath.Join(t.TempDir(), "runtime.sqlite"),
	}
	repoID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("gitx: repository id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("gitx: work id: %v", err)
	}
	e.repoID, e.workID = repoID, workID
	initMember(t, e.member)
	commitFile(t, e.member, "seed.txt", "seed\n", "seed")
	e.open(t)
	return e
}

// open (re)opens the runtime database and rebuilds the managers — the
// reopen simulates a fresh process against the same journal after a crash.
func (e *env) open(t *testing.T) {
	t.Helper()
	db, err := store.Open(context.Background(), e.dbPath, store.OpenOptions{})
	if err != nil {
		t.Fatalf("gitx: open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e.db = db
	e.ops = operation.NewManager(db)
	e.svc, err = NewService(ExecRunner{}, db, e.ops, e.root)
	if err != nil {
		t.Fatalf("gitx: new service: %v", err)
	}
}

// close releases the database handle so a reopen can access the file.
func (e *env) close(t *testing.T) {
	t.Helper()
	if e.db == nil {
		return
	}
	if err := e.db.Close(); err != nil {
		t.Fatalf("gitx: close store: %v", err)
	}
	e.db = nil
}

// base returns the member's current HEAD as the assignment base.
func (e *env) base(t *testing.T) string { return head(t, e.member) }

// assign creates an assignment worktree through the service.
func (e *env) assign(t *testing.T, action identity.ActionID, scope []string) AssignmentWorktree {
	t.Helper()
	wt, err := e.svc.CreateAssignment(context.Background(), CreateRequest{
		WorkID:        e.workID,
		ActionID:      action,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		BaseCommit:    e.base(t),
		Scope:         scope,
	})
	if err != nil {
		t.Fatalf("gitx: create assignment: %v", err)
	}
	return wt
}

// implement commits a file into an assignment worktree and returns the sha.
func implement(t *testing.T, wt AssignmentWorktree, path, content, msg string) string {
	t.Helper()
	return commitFile(t, wt.Path, path, content, msg)
}

// implementAll writes several files and commits them in one commit, so the
// worktree ends exactly one commit ahead of its base.
func implementAll(t *testing.T, wt AssignmentWorktree, files map[string]string, msg string) string {
	t.Helper()
	r := ExecRunner{}
	ctx := context.Background()
	for path, content := range files {
		full := filepath.Join(wt.Path, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("gitx: mkdir %s: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("gitx: write %s: %v", full, err)
		}
		if _, err := r.Run(ctx, wt.Path, "add", filepath.FromSlash(path)); err != nil {
			t.Fatalf("gitx: add %s: %v", path, err)
		}
	}
	if _, err := r.Run(ctx, wt.Path, "commit", "-m", msg); err != nil {
		t.Fatalf("gitx: commit %s: %v", msg, err)
	}
	return head(t, wt.Path)
}

func newAction(t *testing.T) identity.ActionID {
	t.Helper()
	id, err := identity.NewActionID()
	if err != nil {
		t.Fatalf("gitx: action id: %v", err)
	}
	return id
}

// initMember initializes a git repository with a deterministic branch and a
// local identity, so commits and cherry-picks never depend on ambient
// configuration.
func initMember(t *testing.T, dir string) {
	t.Helper()
	initRepo(t, dir)
	r := ExecRunner{}
	for _, kv := range [][2]string{
		{"user.name", "test"},
		{"user.email", "test@example.invalid"},
		{"commit.gpgsign", "false"},
	} {
		if _, err := r.Run(context.Background(), dir, "config", kv[0], kv[1]); err != nil {
			t.Fatalf("gitx: config %s: %v", kv[0], err)
		}
	}
}

// commitFile writes path (relative to dir), stages it, commits it, and
// returns the new HEAD sha.
func commitFile(t *testing.T, dir, path, content, msg string) string {
	t.Helper()
	r := ExecRunner{}
	ctx := context.Background()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("gitx: mkdir %s: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("gitx: write %s: %v", full, err)
	}
	if _, err := r.Run(ctx, dir, "add", filepath.FromSlash(path)); err != nil {
		t.Fatalf("gitx: add %s: %v", path, err)
	}
	if _, err := r.Run(ctx, dir, "commit", "-m", msg); err != nil {
		t.Fatalf("gitx: commit %s: %v", path, err)
	}
	return head(t, dir)
}

// commitAll stages everything and commits, returning the new HEAD sha.
func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	r := ExecRunner{}
	if _, err := r.Run(context.Background(), dir, "add", "-A"); err != nil {
		t.Fatalf("gitx: add -A in %s: %v", dir, err)
	}
	if _, err := r.Run(context.Background(), dir, "commit", "-m", msg); err != nil {
		t.Fatalf("gitx: commit in %s: %v", dir, err)
	}
	return head(t, dir)
}

func head(t *testing.T, dir string) string {
	t.Helper()
	out, err := ExecRunner{}.Run(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("gitx: rev-parse HEAD in %s: %v", dir, err)
	}
	return strings.TrimSpace(out)
}

// porcelain returns the git status --porcelain output of dir.
func porcelain(t *testing.T, dir string) string {
	t.Helper()
	out, err := ExecRunner{}.Run(context.Background(), dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("gitx: status of %s: %v", dir, err)
	}
	return out
}

// opState reads the journal state of one operation; a missing row is "".
func opState(t *testing.T, db *store.DB, id identity.OperationID) string {
	t.Helper()
	var state string
	ctx := context.Background()
	err := db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT state FROM operations WHERE id=?`, string(id)).Scan(&state)
	})
	if err != nil {
		return ""
	}
	return state
}

// latestOp returns the most recent operation of kind as (id, state).
func latestOp(t *testing.T, db *store.DB, kind string) (identity.OperationID, string) {
	t.Helper()
	var id, state string
	ctx := context.Background()
	err := db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT id, state FROM operations WHERE kind=? ORDER BY created_at DESC, id DESC LIMIT 1`, kind).Scan(&id, &state)
	})
	if err != nil {
		t.Fatalf("gitx: latest op of kind %s: %v", kind, err)
	}
	return identity.OperationID(id), state
}

// opCount counts operations of kind in any state.
func opCount(t *testing.T, db *store.DB, kind string) int {
	t.Helper()
	var n int
	ctx := context.Background()
	err := db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM operations WHERE kind=?`, kind).Scan(&n)
	})
	if err != nil {
		t.Fatalf("gitx: count ops of kind %s: %v", kind, err)
	}
	return n
}

// registered reports whether the worktree path is in git's worktree list.
func registered(t *testing.T, svc *Service, repoDir, path string) bool {
	t.Helper()
	entries, err := svc.WorktreeInventory(context.Background(), repoDir)
	if err != nil {
		t.Fatalf("gitx: worktree inventory of %s: %v", repoDir, err)
	}
	for _, e := range entries {
		if filepath.Clean(e.Path) == filepath.Clean(path) {
			return true
		}
	}
	return false
}

// mustCrash runs run and fails the test unless a failpoint panicked.
func mustCrash(t *testing.T, run func() error) {
	t.Helper()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		if err := run(); err != nil {
			panic(fmt.Sprintf("gitx: returned error before crash point: %v", err))
		}
	}()
	if !panicked {
		t.Fatal("gitx: expected simulated crash at failpoint")
	}
}

// setFailpoint installs a hook that panics the nth time point fires.
func setFailpoint(t *testing.T, point string, nth int) (restore func()) {
	t.Helper()
	counts := map[string]int{}
	restore = operation.SetFailpointHook(func(p string) {
		if p != point {
			return
		}
		counts[p]++
		if counts[p] == nth {
			panic(fmt.Sprintf("gitx: simulated crash at %s", p))
		}
	})
	t.Cleanup(restore)
	return restore
}

// captureUnrecordedCrash panics the first time an unrecorded-apply boundary
// with the given prefix and seq suffix fires and captures the operation id
// embedded in the boundary name, so the test can identify the crashed
// journal row after the panic unwinds.
func captureUnrecordedCrash(t *testing.T, prefix string, seq int64) *capturedCrash {
	t.Helper()
	c := &capturedCrash{}
	restore := operation.SetFailpointHook(func(p string) {
		if strings.HasPrefix(p, prefix) && strings.HasSuffix(p, ":"+strconv.FormatInt(seq, 10)) {
			c.id = identity.OperationID(strings.TrimSuffix(strings.TrimPrefix(p, prefix), ":"+strconv.FormatInt(seq, 10)))
			panic(fmt.Sprintf("gitx: simulated crash at %s", p))
		}
	})
	t.Cleanup(restore)
	c.restore = restore
	return c
}

type capturedCrash struct {
	id      identity.OperationID
	restore func()
}
