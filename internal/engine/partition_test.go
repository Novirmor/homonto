package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Novirmor/homonto/internal/adapter/claude"
	"github.com/Novirmor/homonto/internal/state"
)

// repoHarnessFixture applies one repo-scoped skill to both harnesses and
// returns a builder for the configuration plus the engine's state directory.
func repoHarnessFixture(t *testing.T) (build func() *Engine, stateDir string) {
	t.Helper()
	home := t.TempDir()
	base := t.TempDir()
	cfgRepo := filepath.Join(base, "cfg")
	newGitWorktree(t, filepath.Join(base, "svc"))
	content := filepath.Join(cfgRepo, "homonto")
	if err := os.MkdirAll(content, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(cfgRepo, "homonto.toml")
	cfg := `
harnesses = ["opencode", "claude"]

[repos]
svc = "../svc"

[skills.svc-skill]
source = "builtin:handoff"
scope = "project"
repo = "svc"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	build = func() *Engine {
		t.Helper()
		e, err := Build(context.Background(), cfgPath, home, content)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := build()
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}
	return build, e.StateDir
}

// A partition written before ADR 0116 kept Claude's repository records under
// the adapter label. Loading it moves them to the harness id: the next plan
// recognizes the projection instead of re-creating it, and the next apply
// persists the corrected namespace.
func TestRepoPartitionMigratesLabelledClaudeKeys(t *testing.T) {
	build, stateDir := repoHarnessFixture(t)
	st, err := state.LoadNamed(stateDir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Keys("claude")) == 0 {
		t.Fatal("fixture: no Claude records in the partition")
	}
	// Rewrite the partition the way an older release recorded it.
	st.Managed["claude@svc"] = st.Managed["claude"]
	delete(st.Managed, "claude")
	if err := st.SaveNamed(stateDir, "svc"); err != nil {
		t.Fatal(err)
	}

	e := build()
	sets, err := e.Plan()
	if err != nil {
		t.Fatal(err)
	}
	for _, cs := range sets {
		for _, c := range cs.Changes {
			if c.Action != "noop" {
				t.Errorf("%s %s: %s after migration; the projection is already recorded", cs.Tool, c.Key, c.Action)
			}
		}
	}
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}
	st, err = state.LoadNamed(stateDir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := st.Managed["claude@svc"]; ok {
		t.Error("the labelled namespace survived the apply")
	}
	if _, ok := st.Get("claude", "skill.svc-skill"); !ok {
		t.Error("the Claude record was not persisted under the harness id")
	}
}

// Apply-time provenance reaches repository partitions: every repo record
// carries the operation that last touched it, for both harnesses.
func TestRepoPartitionRecordsProvenance(t *testing.T) {
	_, stateDir := repoHarnessFixture(t)
	st, err := state.LoadNamed(stateDir, "svc")
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"opencode", "claude"} {
		e, ok := st.Get(tool, "skill.svc-skill")
		if !ok {
			t.Fatalf("%s: no record", tool)
		}
		if e.LastEvent == nil || e.LastEvent.Op == "" {
			t.Errorf("%s: repo record has no provenance: %+v", tool, e)
		}
	}
}

func TestPartitionResolvesLabelsToTheHarnessID(t *testing.T) {
	main, repo := &state.State{}, &state.State{}
	e := &Engine{State: main, RepoTargets: []RepoTarget{
		{Name: "svc", Adapter: claude.New(t.TempDir(), t.TempDir()).WithRepo("svc"), State: repo},
		{Name: "svc", Adapter: snapshotRepoAdapter{name: "opencode@svc"}, State: repo},
	}}
	for _, tc := range []struct {
		label, key, repo string
		st               *state.State
	}{
		{"claude@svc", "claude", "svc", repo},
		{"opencode@svc", "opencode", "svc", repo},
		{"claude", "claude", "", main},
		{"opencode", "opencode", "", main},
	} {
		st, key, r := e.Partition(tc.label)
		if st != tc.st || key != tc.key || r != tc.repo {
			t.Errorf("Partition(%q) = (%p, %q, %q), want (%p, %q, %q)", tc.label, st, key, r, tc.st, tc.key, tc.repo)
		}
	}
}
