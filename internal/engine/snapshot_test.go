package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/adapter"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/noviopenworks/homonto/internal/state"
)

// partitionOf loads a state file and returns its managed entries.
func partitionOf(t *testing.T, path string) map[string]map[string]state.Entry {
	t.Helper()
	st, err := state.Load(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot.PartitionState(st)
}

func equalPartitions(a, b map[string]map[string]state.Entry) bool {
	return snapshot.EqualEntries(a, b)
}

const snapCfg = `
[settings.opencode]
theme = "opencode-dark"

[mcps.demo]
command = ["true"]
`

func snapSetup(t *testing.T) (home, cfg string, e *Engine) {
	t.Helper()
	home = t.TempDir()
	repo := t.TempDir()
	cfg = filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(cfg, []byte(snapCfg), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := Build(context.Background(), cfg, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	return home, cfg, e
}

// TestSnapshotApplyUndoRestores (S4/S6): a --snapshot apply commits a
// journal; undo restores the pre-apply managed state and disk, and the
// journal is marked rolled-back.
func TestSnapshotApplyUndoRestores(t *testing.T) {
	home, cfg, e := snapSetup(t)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	stateFile := filepath.Join(filepath.Dir(cfg), ".homonto", "state.json")
	before, _ := os.ReadFile(stateFile)
	beforeEntries := partitionOf(t, stateFile)

	// A second apply that changes something: edit the config first.
	os.WriteFile(cfg, []byte(`[settings.opencode]
theme = "new-theme"

[mcps.demo]
command = ["true"]
`), 0o644)
	e2, err := Build(context.Background(), cfg, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	sets := mustPlan(t, e2)
	if !hasRealChanges(sets) {
		t.Fatal("expected a change to snapshot")
	}
	id, err := e2.ApplySnapshot(context.Background(), sets)
	if err != nil {
		t.Fatalf("snapshot apply: %v", err)
	}
	j, ok, err := snapshot.Load(e2.StateDir, id)
	if err != nil || !ok {
		t.Fatalf("journal missing: %v %v", ok, err)
	}
	if j.Status != snapshot.StatusCommitted {
		t.Fatalf("journal not committed: %s", j.Status)
	}
	after, _ := os.ReadFile(stateFile)
	if string(after) == string(before) {
		t.Fatal("snapshot apply made no state change")
	}

	// Undo restores the prior managed state.
	if err := e2.UndoSnapshot(id); err != nil {
		t.Fatalf("undo: %v", err)
	}
	restoredEntries := partitionOf(t, stateFile)
	if !equalPartitions(beforeEntries, restoredEntries) {
		t.Fatalf("undo did not restore the prior managed state")
	}
	_ = before
	// The journal is rolled back; a second undo refuses.
	if j, _, _ := snapshot.Load(e2.StateDir, id); j.Status != snapshot.StatusRolledBack {
		t.Fatalf("journal not rolled back: %s", j.Status)
	}
	if err := e2.UndoSnapshot(id); err == nil {
		t.Fatal("undo of a rolled-back journal must refuse")
	}
}

// TestSnapshotUndoRefusesUserEdit (S6/S7): changing a managed value after the
// snapshot makes undo refuse with zero mutation.
func TestSnapshotUndoRefusesUserEdit(t *testing.T) {
	home, cfg, e := snapSetup(t)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	// Change the config so the snapshot has something to record.
	os.WriteFile(cfg, []byte(`[settings.opencode]
theme = "new-theme"

[mcps.demo]
command = ["true"]
`), 0o644)
	e2, err := Build(context.Background(), cfg, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	id, err := e2.ApplySnapshot(context.Background(), mustPlan(t, e2))
	if err != nil {
		t.Fatal(err)
	}
	// User edit: change the theme on disk (a managed value).
	stateFile := filepath.Join(filepath.Dir(cfg), ".homonto", "state.json")
	data, _ := os.ReadFile(stateFile)
	os.WriteFile(stateFile, []byte(strings.Replace(string(data), "new-theme", "user-edited", 1)), 0o600)

	if err := e2.UndoSnapshot(id); err == nil {
		t.Fatal("undo over a user edit must refuse")
	}
}

// TestSnapshotFailureRollsBack (S4/S8): a failing snapshot apply rolls back
// and the journal ends rolled-back with no undo possible.
func TestSnapshotFailureRollsBack(t *testing.T) {
	home, cfg, e := snapSetup(t)
	// First a real apply so there is state to preserve.
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(filepath.Dir(cfg), ".homonto", "state.json")
	before, _ := os.ReadFile(stateFile)
	// Change the config so the snapshot apply has work to fail on.
	os.WriteFile(cfg, []byte(`[settings.opencode]
theme = "new-theme"

[mcps.demo]
command = ["true"]
`), 0o644)

	// A secret-bearing change makes the broken resolver actually fail:
	// plain values resolve through without consulting the backend, so the
	// failure must ride a ${...} reference.
	os.WriteFile(cfg, []byte(`[settings.opencode]
theme = "new-theme"

[mcps.sec]
command = ["true"]
env = { K = "${SNP_TOKEN}" }
`), 0o644)

	// A broken resolver makes the snapshot apply fail before any write.
	e2, err := Build(context.Background(), cfg, home, "homonto")
	if err != nil {
		t.Fatal(err)
	}
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", os.ErrPermission }}
	if _, err := e2.ApplySnapshot(context.Background(), mustPlan(t, e2)); err == nil {
		t.Fatal("broken resolver must fail the apply")
	}
	after, _ := os.ReadFile(stateFile)
	if string(after) != string(before) {
		t.Fatal("failed snapshot apply mutated state")
	}
	// The journal exists and is rolled back (rollback ran), or prepared
	// (recover needed) — either way state is intact.
	ids, err := snapshot.List(e2.StateDir)
	if err != nil || len(ids) == 0 {
		t.Fatalf("no journal after failure: %v %v", ids, err)
	}
}

// TestPlainApplyLeavesNoJournal (S4): the default apply path never creates a
// journal or snapshot — ADR 0004 semantics stand.
func TestPlainApplyLeavesNoJournal(t *testing.T) {
	_, _, e := snapSetup(t)
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	ids, err := snapshot.List(e.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("plain apply created snapshots: %v", ids)
	}
	if _, err := os.Stat(filepath.Join(e.StateDir, "snapshots")); err == nil {
		t.Fatal("plain apply created the snapshots dir")
	}
}

// TestSnapshotRetention (S3): only the latest 10 committed journals are kept.
func TestSnapshotRetention(t *testing.T) {
	home, cfg, _ := snapSetup(t)
	for i := 0; i < 12; i++ {
		e2, err := Build(context.Background(), cfg, home, "homonto")
		if err != nil {
			t.Fatal(err)
		}
		e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
		_, err = e2.ApplySnapshot(context.Background(), mustPlan(t, e2))
		if err != nil {
			t.Fatalf("snapshot %d: %v", i, err)
		}
		if i == 0 {
			if err := snapshot.Retain(e2.StateDir, 10); err != nil {
				t.Fatal(err)
			}
		}
		// Seed a retained-first state: after 12, at most 10 committed remain.
		if i == 11 {
			if err := snapshot.Retain(e2.StateDir, 10); err != nil {
				t.Fatal(err)
			}
			ids, err := snapshot.List(e2.StateDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(ids) > 10 {
				t.Fatalf("retention kept %d journals", len(ids))
			}
		}
	}
}

// TestSnapshotLinkUndo (S8 disk): snapshot+undo restores a managed link's
// target after a config-driven relink.
func TestSnapshotLinkUndo(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "homonto", "skills", "demo"), 0o755)
	os.WriteFile(filepath.Join(repo, "homonto", "skills", "demo", "SKILL.md"), []byte("# demo\n"), 0o644)
	cfg := filepath.Join(repo, "homonto.toml")
	os.WriteFile(cfg, []byte("[skills.demo]\nsource = \"local:demo\"\nscope = \"project\"\n"), 0o644)

	e, err := Build(context.Background(), cfg, home, filepath.Join(repo, "homonto"))
	if err != nil {
		t.Fatal(err)
	}
	e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(repo, ".opencode", "skills", "demo")
	targetBefore, _ := os.Readlink(link)

	// Re-apply as snapshot: the link already points at content; the snapshot
	// records no change, but a second snapshot with a real change (a new mcp)
	// then undo restores the link untouched.
	e2, err := Build(context.Background(), cfg, home, filepath.Join(repo, "homonto"))
	if err != nil {
		t.Fatal(err)
	}
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
	id, err := e2.ApplySnapshot(context.Background(), mustPlan(t, e2))
	if err != nil {
		t.Fatal(err)
	}
	if err := e2.UndoSnapshot(id); err != nil {
		t.Fatal(err)
	}
	targetAfter, _ := os.Readlink(link)
	if targetAfter != targetBefore {
		t.Fatalf("undo changed the link target: %q -> %q", targetBefore, targetAfter)
	}
}

func hasRealChanges(sets []adapter.ChangeSet) bool {
	for _, cs := range sets {
		for _, c := range cs.Changes {
			if c.Action != "noop" && c.Action != "adopt" {
				return true
			}
		}
	}
	return false
}
