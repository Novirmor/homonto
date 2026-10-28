package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorktreeCleanupGenerationBindingAndInterruptedRemoval(t *testing.T) {
	for _, cut := range []string{"before-git", "after-git", "replacement", "no-intent"} {
		t.Run(cut, func(t *testing.T) {
			l := wtFixture(t, "onto")
			w := wtCreate(t, l, "onto", "a", "main")
			wtState(t, l, "onto", "feature", "generation-one", true, "a", "b")
			if _, err := RemoveWorktreeRole(l, "onto", "feature", "a", "execution", true, "id:another-generation"); err == nil {
				t.Fatal("generation mismatch was accepted")
			}
			registry, err := readWorktreeRegistry(l)
			if err != nil {
				t.Fatal(err)
			}
			if cut != "no-intent" {
				registry.Entries[0].Status = "removing"
				if err := saveWorktreeRegistry(l, registry); err != nil {
					t.Fatal(err)
				}
			}
			if cut != "before-git" {
				wtGit(t, l.Repos["a"], "worktree", "remove", w.Path)
			}
			if cut == "replacement" {
				if err := os.MkdirAll(w.Path, 0o755); err != nil {
					t.Fatal(err)
				}
				wtWrite(t, filepath.Join(w.Path, "user-work"), "do not remove\n")
			}
			removals, err := InspectWorkflowRemovals(l, "onto", "feature", filepath.Join(l.WorkflowRoot, "changes", "feature"))
			if err != nil || len(removals) != 1 {
				t.Fatalf("removal not discoverable: %+v, %v", removals, err)
			}
			_, err = RemoveWorktreeRole(l, "onto", "feature", "a", "execution", true, w.StateID)
			if cut == "replacement" || cut == "no-intent" {
				if err == nil || removals[0].Blocker == "" {
					t.Fatalf("unsafe recovery accepted: %+v, %v", removals, err)
				}
				if cut == "replacement" && string(wtRead(t, filepath.Join(w.Path, "user-work"))) != "do not remove\n" {
					t.Fatal("replacement work was changed")
				}
				return
			}
			if err != nil || removals[0].Blocker != "" || !removals[0].Recovering {
				t.Fatalf("authorized recovery failed: %+v, %v", removals, err)
			}
			if err := EnsureNameAvailable(l, "onto", "feature"); err != nil {
				t.Fatal(err)
			}
			wtGit(t, l.Repos["a"], "rev-parse", "refs/heads/"+w.Branch)
		})
	}
}
