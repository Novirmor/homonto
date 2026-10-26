package worktask

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckRejectsHiddenIndexContents(t *testing.T) {
	for _, flag := range []string{"--skip-worktree", "--assume-unchanged"} {
		t.Run(flag, func(t *testing.T) {
			s := fixture(t)
			task := createNo(t, s, "existing")
			ctx := context.Background()
			if _, err := s.RunCheck(ctx, task, "pair", nil); err != nil {
				t.Fatal(err)
			}
			api := task.Checkouts["api"].Path
			runGit(t, api, "update-index", flag, "tracked")
			put(t, filepath.Join(api, "tracked"), "hidden contents\n")
			flags := runGit(t, api, "ls-files", "-v")
			if status := runGit(t, api, "status", "--porcelain"); status != "" {
				t.Fatalf("fixture must hide the edit from status: %s", status)
			}
			s.RefreshResults(ctx, task)
			if !task.Results["pair"].Stale {
				t.Fatal("hidden contents retained fresh passing evidence")
			}
			// This command would pass only on contents absent from the recorded HEAD.
			check := s.Config.Integration["pair"]
			check.Command = []string{"sh", "-c", "grep -q hidden tracked"}
			s.Config.Integration["pair"] = check
			r, err := s.RunCheck(ctx, task, "pair", nil)
			if err == nil || !strings.Contains(err.Error(), "index flags") || r.Status != "failed" || r.ExitCode != -1 {
				t.Fatalf("hidden input ran: %+v, %v", r, err)
			}
			loaded, err := s.Load(ctx, task.Name)
			if err != nil || loaded.Results["pair"].Status != "failed" {
				t.Fatalf("preflight failure did not replace prior pass: %+v, %v", loaded, err)
			}
			if got := runGit(t, api, "ls-files", "-v"); got != flags {
				t.Fatalf("check changed index flags: %s", got)
			}
			if data, err := os.ReadFile(filepath.Join(api, "tracked")); err != nil || string(data) != "hidden contents\n" {
				t.Fatalf("check changed hidden contents: %q, %v", data, err)
			}
		})
	}
}

func TestCheckDetectsHiddenIndexMutation(t *testing.T) {
	for _, flag := range []string{"--skip-worktree", "--assume-unchanged"} {
		t.Run(flag, func(t *testing.T) {
			s := fixture(t)
			task := createNo(t, s, "existing")
			check := s.Config.Integration["pair"]
			check.Command = []string{"sh", "-c", "git update-index " + flag + " tracked && printf hidden > tracked"}
			s.Config.Integration["pair"] = check
			r, err := s.RunCheck(context.Background(), task, "pair", nil)
			if err == nil || !strings.Contains(err.Error(), "index flags") || r.Status != "failed" || !r.Stale || r.ExitCode != 0 {
				t.Fatalf("hidden mutation not detected after successful command: %+v, %v", r, err)
			}
		})
	}
}

func TestCreateRejectsHiddenVerificationInput(t *testing.T) {
	s := fixture(t)
	web := s.Layout.Repos["web"]
	runGit(t, web, "update-index", "--skip-worktree", "tracked")
	put(t, filepath.Join(web, "tracked"), "hidden contents\n")
	if _, err := s.Create(context.Background(), "feature", "no", "", "existing", []string{"api"}, nil); err == nil || !strings.Contains(err.Error(), "index flags") {
		t.Fatalf("hidden verification input accepted during allocation: %v", err)
	}
}

func TestCleanupRecoversCompletedRemoval(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "worktree")
	ctx := context.Background()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	// Lose the successful removal acknowledgement, leaving precisely the state
	// of a process interrupted after Git removed the checkout but before save.
	bin := t.TempDir()
	wrapper := filepath.Join(bin, "git")
	put(t, wrapper, "#!/bin/sh\n\"$REAL_GIT\" \"$@\"\nstatus=$?\nif [ \"$3\" = worktree ] && [ \"$4\" = remove ] && [ \"$status\" = 0 ]; then exit 1; fi\nexit \"$status\"\n")
	if err := os.Chmod(wrapper, 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("REAL_GIT", realGit)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+oldPath)
	if _, err := s.Cleanup(ctx, task.Name, true); err == nil {
		t.Fatal("expected interrupted cleanup")
	}
	t.Setenv("PATH", oldPath)
	api := task.Checkouts["api"]
	if _, err := os.Stat(api.Path); !os.IsNotExist(err) {
		t.Fatalf("Git did not remove the checkout: %v", err)
	}
	loaded, err := s.Load(ctx, task.Name)
	if err != nil {
		t.Fatalf("interrupted removal is not inspectable: %v", err)
	}
	if !loaded.Checkouts["api"].Removing || loaded.Checkouts["api"].Removed {
		t.Fatalf("missing durable removal intent: %+v", loaded.Checkouts["api"])
	}
	if _, err := s.RunCheck(ctx, loaded, "pair", nil); err == nil {
		t.Fatal("check accepted a checkout pending removal")
	}
	if _, err := s.Create(ctx, task.Name, "no", "", "worktree", []string{"api"}, nil); err == nil {
		t.Fatal("create resumed a checkout pending removal")
	}
	for range 2 {
		if _, err := s.Cleanup(ctx, task.Name, true); err != nil {
			t.Fatalf("cleanup retry: %v", err)
		}
	}
	loaded, err = s.Load(ctx, task.Name)
	if err != nil {
		t.Fatal(err)
	}
	for alias, c := range loaded.Checkouts {
		if !c.Removed || c.Removing {
			t.Fatalf("%s removal not persisted: %+v", alias, c)
		}
		if _, err := os.Stat(c.Path); !os.IsNotExist(err) {
			t.Fatalf("%s checkout remains after recovery: %v", alias, err)
		}
	}
	runGit(t, api.Source, "rev-parse", "--verify", "refs/heads/"+api.Branch)
}

func TestCleanupRevalidatesPendingRemoval(t *testing.T) {
	for _, scenario := range []string{"unchanged", "dirty", "moved", "missing-with-registration", "missing-without-intent", "replacement"} {
		t.Run(scenario, func(t *testing.T) {
			s := fixture(t)
			task := createNo(t, s, "worktree")
			api := task.Checkouts["api"]
			api.Removing = scenario != "missing-without-intent"
			task.Checkouts["api"] = api
			if err := s.save(task); err != nil {
				t.Fatal(err)
			}
			preserve := api.Path
			switch scenario {
			case "dirty":
				put(t, filepath.Join(api.Path, "tracked"), "work to preserve\n")
			case "moved":
				preserve = filepath.Join(t.TempDir(), "moved")
				runGit(t, api.Source, "worktree", "move", api.Path, preserve)
			case "missing-with-registration":
				if err := os.RemoveAll(api.Path); err != nil {
					t.Fatal(err)
				}
			case "missing-without-intent":
				runGit(t, api.Source, "worktree", "remove", api.Path)
			case "replacement":
				runGit(t, api.Source, "worktree", "remove", api.Path)
				put(t, filepath.Join(api.Path, "unrelated"), "work to preserve\n")
			}
			_, err := s.Cleanup(context.Background(), task.Name, true)
			if scenario == "unchanged" {
				if err != nil {
					t.Fatalf("could not resume before Git removal: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("cleanup accepted ambiguous or dirty removal")
			}
			if !strings.HasPrefix(scenario, "missing-") {
				if _, err := os.Stat(preserve); err != nil {
					t.Fatalf("cleanup discarded existing work: %v", err)
				}
			}
		})
	}
}
