package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/store"
)

// material is the ValidateResult of a one-commit assignment.
func material(t *testing.T, e *env, wt AssignmentWorktree, scope []string) CommitMaterial {
	t.Helper()
	m, err := e.svc.ValidateResult(context.Background(), wt, scope)
	if err != nil {
		t.Fatalf("gitx: validate %s: %v", wt.Path, err)
	}
	return m
}

func TestCreateIntegrationRejectsMismatchedBases(t *testing.T) {
	e := newEnv(t)
	wtA := e.assign(t, newAction(t), []string{"src"})
	implement(t, wtA, "src/a.go", "package a\n", "a")
	mA := material(t, e, wtA, []string{"src"})

	commitFile(t, e.member, "extra.txt", "extra\n", "extra")
	wtB := e.assign(t, newAction(t), []string{"src"})
	implement(t, wtB, "src/b.go", "package b\n", "b")
	mB := material(t, e, wtB, []string{"src"})

	_, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID:        e.workID,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		Commits:       []CommitMaterial{mA, mB},
	})
	if !errors.Is(err, ErrBaseMismatch) {
		t.Fatalf("CreateIntegration error = %v, want ErrBaseMismatch", err)
	}
}

func TestCreateIntegrationNamesBranchAndPath(t *testing.T) {
	e := newEnv(t)
	wt := e.assign(t, newAction(t), []string{"src"})
	implement(t, wt, "src/a.go", "package a\n", "a")
	m := material(t, e, wt, []string{"src"})

	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID:        e.workID,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		Commits:       []CommitMaterial{m},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	wantPath := IntegrationPath(e.root, e.workID, e.repoID)
	wantBranch := IntegrationBranch(e.workID, e.repoID)
	if iwt.Path != wantPath {
		t.Errorf("Path = %q, want %q", iwt.Path, wantPath)
	}
	if iwt.Branch != wantBranch {
		t.Errorf("Branch = %q, want %q", iwt.Branch, wantBranch)
	}
	if iwt.BaseCommit != m.BaseCommit {
		t.Errorf("BaseCommit = %q, want %q", iwt.BaseCommit, m.BaseCommit)
	}

	// The integration worktree exists at the shared base with its branch
	// checked out; no commit is applied by creation itself.
	out, err := ExecRunner{}.Run(context.Background(), iwt.Path, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	if got := strings.TrimSpace(out); got != wantBranch {
		t.Errorf("integration HEAD = %q, want %q", got, wantBranch)
	}
	if head(t, iwt.Path) != m.BaseCommit {
		t.Errorf("integration HEAD = %s, want base %s", head(t, iwt.Path), m.BaseCommit)
	}
	if got := opCount(t, e.db, OpKindCreateWorktree); got != 2 {
		t.Errorf("create ops = %d, want 2 (assignment + integration)", got)
	}
}

// TestIntegrationAppliesMaterialsInOrder drives the engine contract: the
// engine passes materials in dependency order (then action-id order) and
// applies each with ApplyCommit; every cherry-pick is journaled.
func TestIntegrationAppliesMaterialsInOrder(t *testing.T) {
	e := newEnv(t)
	a1, a2 := newAction(t), newAction(t)
	w1 := e.assign(t, a1, []string{"src"})
	implement(t, w1, "src/a.go", "package a\n", "a")
	m1 := material(t, e, w1, []string{"src"})
	w2 := e.assign(t, a2, []string{"src"})
	implement(t, w2, "src/b.go", "package b\n", "b")
	m2 := material(t, e, w2, []string{"src"})

	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID:        e.workID,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		Commits:       []CommitMaterial{m1, m2},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}

	res, err := e.svc.ApplyCommit(context.Background(), iwt, m1)
	if err != nil {
		t.Fatalf("ApplyCommit m1: %v", err)
	}
	if !res.Applied || len(res.Conflicts) != 0 {
		t.Errorf("ApplyCommit m1 result = %+v, want applied with no conflicts", res)
	}
	res, err = e.svc.ApplyCommit(context.Background(), iwt, m2)
	if err != nil {
		t.Fatalf("ApplyCommit m2: %v", err)
	}
	if !res.Applied {
		t.Errorf("ApplyCommit m2 result = %+v, want applied", res)
	}

	// Both files are present in the integration worktree, two picks ahead
	// of the base.
	for path, want := range map[string]string{"src/a.go": "package a\n", "src/b.go": "package b\n"} {
		data, err := os.ReadFile(filepath.Join(iwt.Path, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s in integration: %v", path, err)
		}
		if string(data) != want {
			t.Errorf("%s = %q, want %q", path, data, want)
		}
	}
	out, err := ExecRunner{}.Run(context.Background(), iwt.Path, "rev-list", "--count", m1.BaseCommit+"..HEAD")
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if n := strings.TrimSpace(out); n != "2" {
		t.Errorf("picks ahead of base = %s, want 2", n)
	}
	if porcelain(t, iwt.Path) != "" {
		t.Errorf("integration worktree dirty after picks: %q", porcelain(t, iwt.Path))
	}
	if got := opCount(t, e.db, OpKindCherryPick); got != 2 {
		t.Errorf("cherry-pick ops = %d, want 2", got)
	}

	// The integration worktree cleans up like any other.
	if err := e.svc.Cleanup(context.Background(), iwt.Worktree, false); err != nil {
		t.Fatalf("Cleanup of integration worktree: %v", err)
	}
	if registered(t, e.svc, e.member, iwt.Path) {
		t.Error("integration worktree still registered after cleanup")
	}
}

func TestIntegrationDetectsConflict(t *testing.T) {
	e := newEnv(t)
	w1 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w1, "src/shared.txt", "one\n", "one")
	m1 := material(t, e, w1, []string{"src"})
	w2 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w2, "src/shared.txt", "two\n", "two")
	m2 := material(t, e, w2, []string{"src"})

	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID:        e.workID,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		Commits:       []CommitMaterial{m1, m2},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	if res, err := e.svc.ApplyCommit(context.Background(), iwt, m1); err != nil || !res.Applied {
		t.Fatalf("ApplyCommit m1 = %+v, %v; want applied", res, err)
	}

	res, err := e.svc.ApplyCommit(context.Background(), iwt, m2)
	if res.Applied {
		t.Error("ApplyCommit m2 applied, want conflict")
	}
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("ApplyCommit m2 error = %v, want *ConflictError", err)
	}
	if !errors.Is(err, ErrConflict) {
		t.Error("errors.Is(err, ErrConflict) = false, want true")
	}
	if len(ce.Files) != 1 || ce.Files[0] != "src/shared.txt" {
		t.Errorf("conflicted files = %v, want [src/shared.txt]", ce.Files)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0] != "src/shared.txt" {
		t.Errorf("result conflicts = %v, want [src/shared.txt]", res.Conflicts)
	}

	// The cherry-pick is left in progress for the engine to orchestrate:
	// CHERRY_PICK_HEAD exists and the conflicted pick's operation is
	// rolled back in the journal so recovery never re-applies it.
	if _, err := (ExecRunner{}).Run(context.Background(), iwt.Path, "rev-parse", "-q", "--verify", "CHERRY_PICK_HEAD"); err != nil {
		t.Errorf("cherry-pick not in progress after conflict: %v", err)
	}
	if got := opCount(t, e.db, OpKindCherryPick); got != 2 {
		t.Errorf("cherry-pick ops = %d, want 2", got)
	}
	id, state := latestOp(t, e.db, OpKindCherryPick)
	if state != store.OpRolledBack {
		t.Errorf("conflicted pick op %s state = %q, want %q", id, state, store.OpRolledBack)
	}
}

// TestContinueConflictAfterManualResolution proves the engine flow: the
// implementer edits the conflicted files and stages them, and
// ContinueConflict completes the cherry-pick under the pinned editor.
func TestContinueConflictAfterManualResolution(t *testing.T) {
	e := newEnv(t)
	w1 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w1, "src/shared.txt", "one\n", "one")
	m1 := material(t, e, w1, []string{"src"})
	w2 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w2, "src/shared.txt", "two\n", "two")
	m2 := material(t, e, w2, []string{"src"})

	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID:        e.workID,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		Commits:       []CommitMaterial{m1, m2},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, m1); err != nil {
		t.Fatalf("ApplyCommit m1: %v", err)
	}
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, m2); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyCommit m2 = %v, want conflict", err)
	}

	// The engine resolves the conflict by editing and staging.
	shared := filepath.Join(iwt.Path, "src", "shared.txt")
	if err := os.WriteFile(shared, []byte("resolved\n"), 0o644); err != nil {
		t.Fatalf("gitx: write resolution: %v", err)
	}
	if _, err := (ExecRunner{}).Run(context.Background(), iwt.Path, "add", "src/shared.txt"); err != nil {
		t.Fatalf("gitx: stage resolution: %v", err)
	}

	if err := e.svc.ContinueConflict(context.Background(), iwt); err != nil {
		t.Fatalf("ContinueConflict: %v", err)
	}
	if porcelain(t, iwt.Path) != "" {
		t.Errorf("integration worktree dirty after continue: %q", porcelain(t, iwt.Path))
	}
	if data, err := os.ReadFile(shared); err != nil || string(data) != "resolved\n" {
		t.Errorf("resolved file = %q (err %v), want resolved\n", data, err)
	}
	out, err := ExecRunner{}.Run(context.Background(), iwt.Path, "rev-list", "--count", m1.BaseCommit+"..HEAD")
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	if n := strings.TrimSpace(out); n != "2" {
		t.Errorf("picks ahead of base = %s, want 2", n)
	}
	if id, state := latestOp(t, e.db, OpKindCherryPickContinue); state != store.OpFinalized {
		t.Errorf("continue op %s state = %q, want %q", id, state, store.OpFinalized)
	}
}

func TestAbortIntegrationDiscardsConflictAndCleansUp(t *testing.T) {
	e := newEnv(t)
	w1 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w1, "src/shared.txt", "one\n", "one")
	m1 := material(t, e, w1, []string{"src"})
	w2 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w2, "src/shared.txt", "two\n", "two")
	m2 := material(t, e, w2, []string{"src"})

	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID:        e.workID,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		Commits:       []CommitMaterial{m1, m2},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, m1); err != nil {
		t.Fatalf("ApplyCommit m1: %v", err)
	}
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, m2); !errors.Is(err, ErrConflict) {
		t.Fatalf("ApplyCommit m2 = %v, want conflict", err)
	}

	if err := e.svc.AbortIntegration(context.Background(), iwt); err != nil {
		t.Fatalf("AbortIntegration: %v", err)
	}
	if _, err := os.Stat(iwt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("integration path %s still exists (err %v)", iwt.Path, err)
	}
	if registered(t, e.svc, e.member, iwt.Path) {
		t.Error("integration worktree still registered after abort")
	}
	// The integration branch ref survives (the design leaves branches in
	// place for external handling).
	if _, err := (ExecRunner{}).Run(context.Background(), e.member, "show-ref", "--verify", "--quiet", "refs/heads/"+iwt.Branch); err != nil {
		t.Error("abort deleted the integration branch ref")
	}
	if id, state := latestOp(t, e.db, OpKindCherryPickAbort); state != store.OpFinalized {
		t.Errorf("abort op %s state = %q, want %q", id, state, store.OpFinalized)
	}
}

// TestApplyCommitRejectsForeignMaterial proves ApplyCommit refuses a
// material whose base does not match the integration worktree's base.
func TestApplyCommitRejectsForeignMaterial(t *testing.T) {
	e := newEnv(t)
	w1 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w1, "src/a.go", "package a\n", "a")
	m1 := material(t, e, w1, []string{"src"})
	w2 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w2, "src/b.go", "package b\n", "b")
	m2 := material(t, e, w2, []string{"src"})

	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID:        e.workID,
		RepositoryID:  e.repoID,
		RepositoryDir: e.member,
		Commits:       []CommitMaterial{m1},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	// m2's base differs only if the member moved; force the mismatch by
	// rewriting the material's base.
	foreign := m2
	foreign.BaseCommit = strings.Repeat("0", 40)
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, foreign); err == nil {
		t.Fatal("ApplyCommit accepted a material with a foreign base")
	}
}

// TestIntegrationRoundStartsFromTheBase is the regression test for a
// repair round integrating on top of the round it repairs.
//
// An integration area is named for its work and member, so the second
// round finds the first round's area still holding the first round's
// materials. Cherry-picking this round's materials on top of that
// integrates work that was already superseded: git either stops with "the
// previous cherry-pick is now empty" — which surfaced as a workflow that
// could not get past a failed check — or, worse, succeeds and leaves the
// failed attempt on the branch while the record says the checks passed.
func TestIntegrationRoundStartsFromTheBase(t *testing.T) {
	e := newEnv(t)
	base := e.base(t)

	w1 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w1, "src/login.go", "package src\n\nfunc Login() bool { return false }\n", "attempt")
	first := material(t, e, w1, []string{"src"})
	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID: e.workID, RepositoryID: e.repoID, RepositoryDir: e.member,
		Commits: []CommitMaterial{first},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, first); err != nil {
		t.Fatalf("ApplyCommit: %v", err)
	}
	head, err := e.svc.runner.Run(context.Background(), iwt.Path, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(head) == base {
		t.Fatal("the first round applied nothing, so the second proves nothing")
	}

	// The repair round: same work, same member, superseding material.
	w2 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w2, "src/login.go", "package src\n\nfunc Login() bool { return true }\n", "repair")
	second := material(t, e, w2, []string{"src"})
	iwt, err = e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID: e.workID, RepositoryID: e.repoID, RepositoryDir: e.member,
		Commits: []CommitMaterial{second},
	})
	if err != nil {
		t.Fatalf("CreateIntegration for the repair round: %v", err)
	}
	restarted, err := e.svc.runner.Run(context.Background(), iwt.Path, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(restarted) != base {
		t.Fatalf("the repair round started at %s, not at the base %s",
			strings.TrimSpace(restarted), base)
	}
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, second); err != nil {
		t.Fatalf("the repair could not be integrated: %v", err)
	}
	shown, err := e.svc.runner.Run(context.Background(), iwt.Path, "show", "HEAD:src/login.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(shown, "return true") {
		t.Errorf("the integration holds the superseded attempt:\n%s", shown)
	}
}

// TestIntegrationRoundRefusesToDiscardUncommittedWork: uncommitted changes
// in an integration area are someone's unfinished conflict resolution, and
// discarding that silently is not a decision this code gets to make.
func TestIntegrationRoundRefusesToDiscardUncommittedWork(t *testing.T) {
	e := newEnv(t)
	w1 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w1, "src/login.go", "package src\n\nfunc Login() bool { return false }\n", "attempt")
	first := material(t, e, w1, []string{"src"})
	iwt, err := e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID: e.workID, RepositoryID: e.repoID, RepositoryDir: e.member,
		Commits: []CommitMaterial{first},
	})
	if err != nil {
		t.Fatalf("CreateIntegration: %v", err)
	}
	if _, err := e.svc.ApplyCommit(context.Background(), iwt, first); err != nil {
		t.Fatalf("ApplyCommit: %v", err)
	}
	if err := os.WriteFile(filepath.Join(iwt.Path, "src", "login.go"),
		[]byte("package src\n\n// half-resolved\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w2 := e.assign(t, newAction(t), []string{"src"})
	implement(t, w2, "src/login.go", "package src\n\nfunc Login() bool { return true }\n", "repair")
	second := material(t, e, w2, []string{"src"})
	_, err = e.svc.CreateIntegration(context.Background(), IntegrationRequest{
		WorkID: e.workID, RepositoryID: e.repoID, RepositoryDir: e.member,
		Commits: []CommitMaterial{second},
	})
	if err == nil {
		t.Fatal("a round started over someone's uncommitted resolution")
	}
	if !errors.Is(err, ErrDirtyWorktree) {
		t.Errorf("refused with %v, want a dirty-worktree refusal", err)
	}
}
