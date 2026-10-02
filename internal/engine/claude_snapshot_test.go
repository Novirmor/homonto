package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/snapshot"
	"github.com/tidwall/gjson"
)

// multiSnapSetup builds a config whose MCP targets both tools, plus a
// resolver whose Pass backend fails only for the magic reference — so a test
// can make just one tool's apply explode.
func multiSnapSetup(t *testing.T, doc string) (home, cfgPath string, build func() *Engine) {
	t.Helper()
	home = t.TempDir()
	repo := t.TempDir()
	cfgPath = filepath.Join(repo, "homonto.toml")
	if err := os.WriteFile(cfgPath, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	build = func() *Engine {
		e, err := Build(context.Background(), cfgPath, home, "homonto")
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "unused", nil }}
		return e
	}
	return home, cfgPath, build
}

// A multi-tool snapshot apply journals BOTH tools, and undo restores each
// tool's structured files and state namespaces — including a deleted claude
// entry returning after undo (the reverse-apply path through the claude
// adapter). The journal's tool label names every tool it covers.
func TestSnapshotMultiToolUndoRestoresBothTools(t *testing.T) {
	home, cfgPath, build := multiSnapSetup(t, `
[mcps.alpha]
command = ["alpha", "v1"]
targets = ["opencode", "claude"]

[mcps.gone]
command = ["gone-srv"]
targets = ["claude"]
`)
	e := build()
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	ocFile := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	clFile := filepath.Join(home, ".claude.json")
	if got := gjson.GetBytes(mustRead(t, clFile), "mcpServers.gone.command").String(); got != "gone-srv" {
		t.Fatalf("baseline claude file lacks the server to delete")
	}
	if !gjson.GetBytes(mustRead(t, ocFile), "mcp.alpha").Exists() {
		t.Fatal("baseline opencode file lacks the dual-target server")
	}
	stateFile := filepath.Join(filepath.Dir(cfgPath), ".homonto", "state.json")
	beforeEntries := partitionOf(t, stateFile)

	// Change opencode-side state (alpha v2), delete the claude-only server,
	// and add a fresh claude one — one journal spanning create+delete across
	// both tools.
	if err := os.WriteFile(cfgPath, []byte(`
[mcps.alpha]
command = ["alpha", "v2"]
targets = ["opencode", "claude"]

[mcps.fresh]
command = ["fresh-srv"]
targets = ["claude"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	e2 := build()
	sets := mustPlan(t, e2)
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
	if j.Tool != "claude,opencode" {
		t.Fatalf("journal tool label = %q, want \"claude,opencode\"", j.Tool)
	}

	// The committed apply did its work on both tools.
	ocMid, _ := os.ReadFile(ocFile)
	clMid, _ := os.ReadFile(clFile)
	if gjson.GetBytes(ocMid, "mcp.alpha.command.1").String() != "v2" {
		t.Fatalf("opencode change missing mid-flight:\n%s", ocMid)
	}
	if !gjson.GetBytes(clMid, "mcpServers.fresh").Exists() || gjson.GetBytes(clMid, "mcpServers.gone").Exists() {
		t.Fatalf("claude create+delete missing mid-flight:\n%s", clMid)
	}

	// Undo restores BOTH tools' managed state namespaces AND both tools'
	// structured files to their before values — the reverse comes from the
	// journal's per-key records and partition before-images, not from a
	// replan against this (still-changed) config. Comparison is by managed
	// entries — tombstones/history the apply appended stay in place.
	if err := e2.UndoSnapshot(id); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !equalPartitions(beforeEntries, partitionOf(t, stateFile)) {
		t.Fatal("undo did not restore the main state partition entries (both tools' namespaces)")
	}
	if _, ok := e2.State.Get("claude", "mcp.gone"); !ok {
		t.Fatal("undo must bring back the deleted claude record")
	}
	ocAfter, clAfter := mustRead(t, ocFile), mustRead(t, clFile)
	if got := gjson.GetBytes(ocAfter, "mcp.alpha.command.1").String(); got != "v1" {
		t.Fatalf("undo did not restore opencode.jsonc to the before value (alpha v1), got %q:\n%s", got, ocAfter)
	}
	if !gjson.GetBytes(clAfter, "mcpServers.gone").Exists() || gjson.GetBytes(clAfter, "mcpServers.fresh").Exists() {
		t.Fatalf("undo did not restore .claude.json (gone must be back, fresh gone):\n%s", clAfter)
	}
	if j, _, _ := snapshot.Load(e2.StateDir, id); j.Status != snapshot.StatusRolledBack {
		t.Fatalf("journal not rolled back: %s", j.Status)
	}
	if err := e2.UndoSnapshot(id); err == nil {
		t.Fatal("undo of a rolled-back journal must refuse")
	}
}

// A failure in the multi-tool snapshot apply (a claude-side secret that cannot
// resolve) fails during the up-front resolution pass — BEFORE any adapter
// writes — so both tools' files and state must be byte-identical to baseline
// and the journal must end rolled back. (Reversal of writes that DID land is
// proven by TestSnapshotMultiToolLaterFailureReversesEarlierWrite.)
func TestSnapshotMultiToolFailureRollsBackAcrossTools(t *testing.T) {
	home, cfgPath, build := multiSnapSetup(t, `
[mcps.alpha]
command = ["alpha"]
targets = ["opencode", "claude"]
`)
	e := build()
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	ocFile := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	clFile := filepath.Join(home, ".claude.json")
	ocBefore, _ := os.ReadFile(ocFile)
	clBefore, _ := os.ReadFile(clFile)
	stateFile := filepath.Join(filepath.Dir(cfgPath), ".homonto", "state.json")
	stateBefore, _ := os.ReadFile(stateFile)

	// The new config changes opencode's server AND adds a claude server whose
	// env rides a secret the resolver cannot resolve: the apply must fail
	// after the opencode changeset was already journaled.
	if err := os.WriteFile(cfgPath, []byte(`
[mcps.alpha]
command = ["alpha", "changed"]
targets = ["opencode", "claude"]

[mcps.boom]
command = ["boom-srv"]
env = { K = "${SNAP_FAIL}" }
targets = ["claude"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	e2 := build()
	e2.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", os.ErrPermission }}
	if _, err := e2.ApplySnapshot(context.Background(), mustPlan(t, e2)); err == nil {
		t.Fatal("the broken secret must fail the snapshot apply")
	}
	ocAfter, _ := os.ReadFile(ocFile)
	clAfter, _ := os.ReadFile(clFile)
	stateAfter, _ := os.ReadFile(stateFile)
	if string(ocAfter) != string(ocBefore) {
		t.Fatalf("opencode.jsonc not rolled back\nbefore:\n%s\nafter:\n%s", ocBefore, ocAfter)
	}
	if string(clAfter) != string(clBefore) {
		t.Fatalf(".claude.json not rolled back\nbefore:\n%s\nafter:\n%s", clBefore, clAfter)
	}
	if string(stateAfter) != string(stateBefore) {
		t.Fatal("main state partition not rolled back")
	}
	ids, err := snapshot.List(e2.StateDir)
	if err != nil || len(ids) == 0 {
		t.Fatalf("no journal after failure: %v %v", ids, err)
	}
	j, ok, err := snapshot.Load(e2.StateDir, ids[0])
	if err != nil || !ok {
		t.Fatalf("journal missing: %v %v", ok, err)
	}
	if j.Status != snapshot.StatusRolledBack {
		t.Fatalf("journal not rolled back: %s", j.Status)
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}

// A failure that strikes AFTER an earlier tool committed — claude's config
// file becomes unparseable between plan and apply — must reverse that tool's
// structured write: rollback restores managed values across tools, not state
// alone. The journal ends rolled back and the foreign (broken) claude file is
// left exactly as found.
func TestSnapshotMultiToolLaterFailureReversesEarlierWrite(t *testing.T) {
	home, cfgPath, build := multiSnapSetup(t, `
[mcps.alpha]
command = ["alpha"]
targets = ["opencode", "claude"]
`)
	e := build()
	if err := e.Apply(context.Background(), mustPlan(t, e)); err != nil {
		t.Fatalf("baseline apply: %v", err)
	}
	ocFile := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	clFile := filepath.Join(home, ".claude.json")
	ocBefore := mustRead(t, ocFile)
	stateFile := filepath.Join(filepath.Dir(cfgPath), ".homonto", "state.json")
	stateBefore := mustRead(t, stateFile)

	// opencode's server changes; claude gets a new one whose apply will fail
	// because its registry file turns into unparseable bytes after planning.
	if err := os.WriteFile(cfgPath, []byte(`
[mcps.alpha]
command = ["alpha", "changed"]
targets = ["opencode", "claude"]

[mcps.beta]
command = ["beta-srv"]
targets = ["claude"]
`), 0o644); err != nil {
		t.Fatal(err)
	}
	e2 := build()
	sets := mustPlan(t, e2)
	if err := os.WriteFile(clFile, []byte(`{"mcpServers": `), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.ApplySnapshot(context.Background(), sets); err == nil {
		t.Fatal("claude apply over a broken registry must fail")
	}
	// The managed VALUE must be back, specifically the SHORTER command —
	// asserting command.0 alone would also pass with the un-rolled-back
	// ["alpha","changed"]. Byte formatting may differ: the reverse
	// re-serializes the document around the restored value (ADR 0030 restores
	// managed values, not file bytes).
	ocAfter := mustRead(t, ocFile)
	if n := gjson.GetBytes(ocAfter, "mcp.alpha.command.#").Int(); n != 1 {
		t.Fatalf("opencode's committed write was not reversed by rollback (command length %d)\nbefore:\n%s\nafter:\n%s", n, ocBefore, ocAfter)
	}
	if got := mustRead(t, clFile); string(got) != `{"mcpServers": ` {
		t.Fatalf("the foreign broken claude file must be left untouched, got %q", got)
	}
	if got := mustRead(t, stateFile); string(got) != string(stateBefore) {
		t.Fatal("main state partition not rolled back")
	}
	ids, err := snapshot.List(e2.StateDir)
	if err != nil || len(ids) == 0 {
		t.Fatalf("no journal after failure: %v %v", ids, err)
	}
	j, ok, err := snapshot.Load(e2.StateDir, ids[0])
	if err != nil || !ok {
		t.Fatalf("journal missing: %v %v", ok, err)
	}
	if j.Status != snapshot.StatusRolledBack {
		t.Fatalf("journal not rolled back: %s", j.Status)
	}
}
