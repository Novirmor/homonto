package lease

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/securefs"
	"github.com/noviopenworks/homonto/internal/store"
)

// env wires one control root, runtime database, operation manager, and lease
// manager together. member-a/b/c are the target directories; leases land in
// a lease.json inside each, exactly as they do beside a registration.
type env struct {
	dir    string
	dbPath string
	db     *store.DB
	ops    *operation.Manager
	mgr    *Manager
	wsID   identity.WorkspaceID
	workID identity.WorkID
	ids    []identity.RepositoryID
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"member-a", "member-b", "member-c"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatalf("lease: mkdir %s: %v", name, err)
		}
	}
	e := &env{dir: dir, dbPath: filepath.Join(dir, "runtime.sqlite")}
	wsID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("lease: workspace id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("lease: work id: %v", err)
	}
	e.wsID, e.workID = wsID, workID
	for i := 0; i < 3; i++ {
		id, err := identity.NewRepositoryID()
		if err != nil {
			t.Fatalf("lease: repository id: %v", err)
		}
		e.ids = append(e.ids, id)
	}
	e.open(t)
	return e
}

// open (re)opens the database and builds fresh managers — the reopen simulates
// a new process against the same journal after a crash.
func (e *env) open(t *testing.T) {
	t.Helper()
	db, err := store.Open(context.Background(), e.dbPath, store.OpenOptions{})
	if err != nil {
		t.Fatalf("lease: open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	e.db = db
	e.ops = operation.NewManager(db)
	e.mgr = NewManager(db, e.ops)
}

// close releases the database handle so a reopen (or a subprocess) can
// access the file.
func (e *env) close(t *testing.T) {
	t.Helper()
	if e.db == nil {
		return
	}
	if err := e.db.Close(); err != nil {
		t.Fatalf("lease: close store: %v", err)
	}
	e.db = nil
}

// target returns the i-th (1-based) lease target.
func (e *env) target(i int) Target {
	return Target{
		RepositoryID: e.ids[i-1],
		Path:         filepath.Join(e.dir, fmt.Sprintf("member-%c", 'a'+i-1), "lease.json"),
	}
}

// allTargets returns the three targets in shuffled order.
func (e *env) allTargets() []Target {
	return []Target{e.target(3), e.target(1), e.target(2)}
}

// req builds an AcquireRequest for the env's workspace/work at generation 1.
func (e *env) req(t *testing.T, targets []Target) AcquireRequest {
	t.Helper()
	prov, err := CurrentProcess()
	if err != nil {
		t.Fatalf("lease: current process: %v", err)
	}
	return AcquireRequest{
		WorkspaceID: e.wsID,
		WorkID:      e.workID,
		Generation:  1,
		Provenance:  prov,
		ControlRoot: e.dir,
		Targets:     targets,
	}
}

// acquire runs a full acquisition and fails the test on error.
func (e *env) acquire(t *testing.T, targets []Target) []Lease {
	t.Helper()
	leases, err := e.mgr.AcquireAll(context.Background(), e.req(t, targets))
	if err != nil {
		t.Fatalf("lease: acquire: %v", err)
	}
	return leases
}

// writeForeignLease plants a lease file at path owned by a different
// workspace and work, with a valid token — the shape a live foreign
// acquisition leaves behind.
func writeForeignLease(t *testing.T, path string) {
	t.Helper()
	wsID, err := identity.NewWorkspaceID()
	if err != nil {
		t.Fatalf("lease: foreign workspace id: %v", err)
	}
	workID, err := identity.NewWorkID()
	if err != nil {
		t.Fatalf("lease: foreign work id: %v", err)
	}
	repoID, err := identity.NewRepositoryID()
	if err != nil {
		t.Fatalf("lease: foreign repository id: %v", err)
	}
	token, err := identity.NewToken()
	if err != nil {
		t.Fatalf("lease: foreign token: %v", err)
	}
	prov, err := CurrentProcess()
	if err != nil {
		t.Fatalf("lease: foreign process: %v", err)
	}
	writeLeaseContent(t, path, LeaseContent{
		SchemaVersion: 1,
		WorkspaceID:   wsID,
		RepositoryID:  repoID,
		WorkID:        workID,
		Generation:    1,
		Process:       prov,
		RecoveryToken: token,
	})
}

// writeLeaseContent writes content to path atomically with the production
// mode.
func writeLeaseContent(t *testing.T, path string, content LeaseContent) {
	t.Helper()
	data, err := content.Marshal()
	if err != nil {
		t.Fatalf("lease: marshal fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("lease: mkdir %s: %v", filepath.Dir(path), err)
	}
	root, err := securefs.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatalf("lease: open root: %v", err)
	}
	defer root.Close()
	if err := root.WriteAtomic(filepath.Base(path), data, 0o600); err != nil {
		t.Fatalf("lease: write fixture %s: %v", path, err)
	}
}

// acquireOpRows returns every lease.acquire operation, oldest first.
func acquireOpRows(t *testing.T, db *store.DB) []struct {
	ID    identity.OperationID
	State string
} {
	t.Helper()
	var out []struct {
		ID    identity.OperationID
		State string
	}
	ctx := context.Background()
	err := db.View(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT id, state FROM operations WHERE kind=? ORDER BY created_at, id`, "lease.acquire")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, state string
			if err := rows.Scan(&id, &state); err != nil {
				return err
			}
			out = append(out, struct {
				ID    identity.OperationID
				State string
			}{identity.OperationID(id), state})
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("lease: query acquire ops: %v", err)
	}
	return out
}

// opCountByKind counts operations of kind in each state.
func opCountByKind(t *testing.T, db *store.DB, kind string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	ctx := context.Background()
	err := db.View(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT state, COUNT(*) FROM operations WHERE kind=? GROUP BY state`, kind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var state string
			var n int
			if err := rows.Scan(&state, &n); err != nil {
				return err
			}
			counts[state] = n
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("lease: count ops of kind %s: %v", kind, err)
	}
	return counts
}

// workState reads the works row state for workID; missing is "".
func workState(t *testing.T, db *store.DB, workID identity.WorkID) string {
	t.Helper()
	var state string
	ctx := context.Background()
	err := db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT state FROM works WHERE id=?`, string(workID)).Scan(&state)
	})
	if err != nil {
		return ""
	}
	return state
}

// metaValue reads a meta key; missing is "".
func metaValue(t *testing.T, db *store.DB, key string) string {
	t.Helper()
	var value string
	ctx := context.Background()
	err := db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx,
			`SELECT value FROM meta WHERE key=?`, key).Scan(&value)
	})
	if err != nil {
		return ""
	}
	return value
}

// setFailpoint installs a hook that panics (simulating process death) the
// nth time point is reached. The returned restore clears the hook.
func setFailpoint(t *testing.T, point string, nth int) (restore func()) {
	t.Helper()
	counts := map[string]int{}
	return operation.SetFailpointHook(func(p string) {
		if p != point {
			return
		}
		counts[p]++
		if counts[p] == nth {
			panic(fmt.Sprintf("simulated crash at %s", p))
		}
	})
}

// mustCrash runs run and fails the test unless the failpoint panicked.
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
			panic(fmt.Sprintf("returned error before crash point: %v", err))
		}
	}()
	if !panicked {
		t.Fatal("lease: expected simulated crash at failpoint")
	}
}

// mustRecoverCrash runs recoverFn and fails the test unless the failpoint
// panicked mid-recovery.
func mustRecoverCrash(t *testing.T, recoverFn func() error) {
	t.Helper()
	panicked := false
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		if err := recoverFn(); err != nil {
			panic(fmt.Sprintf("recovery returned error before crash point: %v", err))
		}
	}()
	if !panicked {
		t.Fatal("lease: expected simulated crash at recovery failpoint")
	}
}

// assertLeasesPresent verifies every lease file exists, decodes strictly,
// and carries the stable identity fields plus a valid recovery token.
func assertLeasesPresent(t *testing.T, e *env, leases []Lease) {
	t.Helper()
	for _, l := range leases {
		content, err := ReadLease(l.Path)
		if err != nil {
			t.Fatalf("lease: read %s: %v", l.Path, err)
		}
		if content.WorkspaceID != e.wsID || content.WorkID != e.workID || content.Generation != 1 {
			t.Errorf("lease: %s content = %+v, want workspace %s work %s generation 1",
				l.Path, content, e.wsID, e.workID)
		}
		if err := identity.ValidateToken(string(content.RecoveryToken)); err != nil {
			t.Errorf("lease: %s token %q: %v", l.Path, content.RecoveryToken, err)
		}
	}
}
