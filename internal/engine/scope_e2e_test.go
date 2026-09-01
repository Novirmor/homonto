package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noviopenworks/homonto/internal/plan"
	"github.com/noviopenworks/homonto/internal/secret"
	"github.com/noviopenworks/homonto/internal/state"
)

// TestProjectScopeEndToEnd exercises the compiled apply path at project scope:
// links land under the project root, a second apply is idempotent, status is
// clean, and switching back to user scope relocates the links (no orphan).
func TestProjectScopeEndToEnd(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	content := filepath.Join(repo, "content")
	os.MkdirAll(filepath.Join(content, "skills", "graphify"), 0o755)

	writeTOML := func(scope string) {
		os.WriteFile(filepath.Join(repo, "homonto.toml"),
			[]byte("[skills.graphify]\nsource=\"local:graphify\"\nscope=\""+scope+"\"\n"), 0o644)
	}
	build := func() *Engine {
		e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, content)
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
		return e
	}

	projOpen := filepath.Join(repo, ".opencode", "skills", "graphify")
	homeOpen := filepath.Join(home, ".config", "opencode", "skills", "graphify")

	// 1. Apply at project scope.
	writeTOML("project")
	e := build()
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(projOpen); err != nil {
		t.Fatalf("project link missing: %s (%v)", projOpen, err)
	}
	if _, err := os.Lstat(homeOpen); err == nil {
		t.Fatal("project scope must not link under home")
	}

	// 2. Idempotent + status clean.
	e2 := build()
	sets2, _ := e2.Plan()
	if plan.HasChanges(sets2) {
		t.Fatalf("project-scope apply not idempotent: %s", plan.Render(sets2))
	}
	drift, pending, err := build().Status()
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 0 || pending != 0 {
		t.Fatalf("status not clean at project scope: drift=%v pending=%d", drift, pending)
	}

	// 3. Switch back to user scope: links relocate, project links pruned.
	writeTOML("user")
	e3 := build()
	sets3, _ := e3.Plan()
	if err := e3.Apply(context.Background(), sets3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Readlink(homeOpen); err != nil {
		t.Fatalf("user link not created after switch: %v", err)
	}
	if _, err := os.Lstat(projOpen); err == nil {
		t.Fatal("project opencode link must be pruned after switch — orphan left behind")
	}
}

// TestScopeSwitchStatusReportsPendingNotDrift is the release blocker guard: a
// [skills] scope change that has NOT been applied yet must show as pending
// relocation, not false drift. Skill Applied hashes encode the link's
// destination path, so a naive ObserveHashes keyed on the new scope's dir would
// find the (not-yet-created) link absent and cry "missing/drift" while the old
// managed link is still perfectly intact. It covers both switch directions.
func TestScopeSwitchStatusReportsPendingNotDrift(t *testing.T) {
	for _, tc := range []struct{ from, to string }{
		{"user", "project"},
		{"project", "user"},
	} {
		t.Run(tc.from+"_to_"+tc.to, func(t *testing.T) {
			home := t.TempDir()
			repo := t.TempDir()
			content := filepath.Join(repo, "content")
			os.MkdirAll(filepath.Join(content, "skills", "graphify"), 0o755)
			writeTOML := func(scope string) {
				os.WriteFile(filepath.Join(repo, "homonto.toml"),
					[]byte("[skills.graphify]\nsource=\"local:graphify\"\nscope=\""+scope+"\"\n"), 0o644)
			}
			build := func() *Engine {
				e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, content)
				if err != nil {
					t.Fatal(err)
				}
				e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
				return e
			}

			// Apply at the origin scope, then confirm a clean baseline.
			writeTOML(tc.from)
			e := build()
			sets, _ := e.Plan()
			if err := e.Apply(context.Background(), sets); err != nil {
				t.Fatal(err)
			}
			if drift, pending, err := build().Status(); err != nil || len(drift) != 0 || pending != 0 {
				t.Fatalf("baseline not clean: drift=%v pending=%d err=%v", drift, pending, err)
			}

			// Switch scope in config but DO NOT apply. The old links are still intact.
			writeTOML(tc.to)
			drift, pending, err := build().Status()
			if err != nil {
				t.Fatal(err)
			}
			if len(drift) != 0 {
				t.Fatalf("pending scope switch reported as drift (old links are intact): %v", drift)
			}
			if pending == 0 {
				t.Fatal("pending scope switch not reported as pending work")
			}

			// Applying the switch converges: relocation happens and status goes clean.
			e2 := build()
			sets2, _ := e2.Plan()
			if err := e2.Apply(context.Background(), sets2); err != nil {
				t.Fatal(err)
			}
			if drift, pending, err := build().Status(); err != nil || len(drift) != 0 || pending != 0 {
				t.Fatalf("status not clean after applying switch: drift=%v pending=%d err=%v", drift, pending, err)
			}
		})
	}
}

// TestSkillsOnlyRebuildsLostState (verify round 1, FINDING 2): a skills-only
// project-scope config whose .homonto/state.json is deleted rebuilds state via
// adoption on the next apply, and the skill remains prunable afterward.
func TestSkillsOnlyRebuildsLostState(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	content := filepath.Join(repo, "content")
	os.MkdirAll(filepath.Join(content, "skills", "foo"), 0o755)
	os.WriteFile(filepath.Join(repo, "homonto.toml"),
		[]byte("[skills.foo]\nsource=\"local:foo\"\nscope=\"project\"\n"), 0o644)
	build := func() *Engine {
		e, err := Build(context.Background(), filepath.Join(repo, "homonto.toml"), home, content)
		if err != nil {
			t.Fatal(err)
		}
		e.Resolver = &secret.Resolver{Getenv: func(string) string { return "" }, Pass: func(string) (string, error) { return "", nil }}
		return e
	}

	// Apply, then wipe state to simulate a lost .homonto/state.json.
	e := build()
	sets, _ := e.Plan()
	if err := e.Apply(context.Background(), sets); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(repo, ".homonto", "state.json")
	os.Remove(stateFile)

	// Re-plan: the correct-but-unrecorded links must show as adoptions, and apply
	// must rebuild state.
	e2 := build()
	sets2, _ := e2.Plan()
	if !plan.HasAdoptions(sets2) {
		t.Fatalf("expected adoptions to rebuild lost state, got: %s", plan.Render(sets2))
	}
	if err := e2.Apply(context.Background(), sets2); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(filepath.Join(repo, ".homonto"))
	if _, ok := st.Get("opencode", "skill.foo"); !ok {
		t.Fatal("opencode skill.foo not rebuilt into state")
	}

	// With state rebuilt, removing the skill now prunes the links.
	os.WriteFile(filepath.Join(repo, "homonto.toml"), []byte(""), 0o644)
	e3 := build()
	sets3, _ := e3.Plan()
	if err := e3.Apply(context.Background(), sets3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".opencode", "skills", "foo")); err == nil {
		t.Fatal("skill link not pruned after removal (state rebuild failed to make it prunable)")
	}
}
