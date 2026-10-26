//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package worktask

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type checkStartedWriter struct {
	once  sync.Once
	start func()
}

func (w *checkStartedWriter) Write(p []byte) (int, error) {
	w.once.Do(w.start)
	return len(p), nil
}

func TestCheckCancellationStopsDescendants(t *testing.T) {
	for _, reason := range []string{"cancel", "deadline"} {
		t.Run(reason, func(t *testing.T) {
			s := fixture(t)
			task := createNo(t, s, "existing")
			release := filepath.Join(t.TempDir(), "release")
			check := s.Config.Integration["pair"]
			// Release the child only after RunCheck returns: a surviving child
			// would then corrupt the candidate while the parent resumes work.
			check.Command = []string{"sh", "-c", `(printf ready; while [ ! -f "$1" ]; do sleep 0.01; done; printf orphan > tracked) & wait`, "check", release}
			s.Config.Integration["pair"] = check
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := false
			output := &checkStartedWriter{start: func() {
				started = true
				if reason == "cancel" {
					cancel()
				}
			}}
			r, err := s.RunCheck(ctx, task, "pair", output)
			if !started || err == nil || r.Status != "failed" {
				t.Fatalf("check = %+v, %v; started = %t", r, err, started)
			}
			put(t, release, "resume")
			// Allow an orphan to observe the release and write, including after
			// the old runner's WaitDelay closed its inherited output pipe.
			time.Sleep(500 * time.Millisecond)
			data, err := os.ReadFile(filepath.Join(task.Checkouts["api"].Path, "tracked"))
			if err != nil || strings.TrimSpace(string(data)) != "original" {
				t.Fatalf("child modified checkout after cancellation: %q, %v", data, err)
			}
		})
	}
}

func TestCheckExitStopsBackgroundDescendants(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "existing")
	check := s.Config.Integration["pair"]
	// The child detaches from the output pipe, so the check exits at once and
	// passes; the child must still not write after RunCheck returns.
	check.Command = []string{"sh", "-c", `(sleep 0.3; printf orphan > tracked) >/dev/null 2>&1 &`}
	s.Config.Integration["pair"] = check
	if r, err := s.RunCheck(context.Background(), task, "pair", nil); err != nil || r.Status != "passed" {
		t.Fatalf("check = %+v, %v", r, err)
	}
	time.Sleep(600 * time.Millisecond)
	data, err := os.ReadFile(filepath.Join(task.Checkouts["api"].Path, "tracked"))
	if err != nil || strings.TrimSpace(string(data)) != "original" {
		t.Fatalf("background child modified checkout after the check: %q, %v", data, err)
	}
}

func TestCheckPassesWhenBackgroundDescendantKeepsOutput(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "existing")
	check := s.Config.Integration["pair"]
	// A test server started in the background inherits the check's output and
	// outlives the check's last command, which passes.
	check.Command = []string{"sh", "-c", `(sleep 3; printf orphan > tracked) & echo tests passed`}
	s.Config.Integration["pair"] = check
	var output strings.Builder
	started := time.Now()
	r, err := s.RunCheck(context.Background(), task, "pair", &output)
	if err != nil || r.Status != "passed" || r.ExitCode != 0 {
		t.Fatalf("check = %+v, %v", r, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("check waited for its background descendant: %s", elapsed)
	}
	if !strings.Contains(output.String(), "tests passed") {
		t.Fatalf("check output lost: %q", output.String())
	}
	time.Sleep(3500 * time.Millisecond)
	data, err := os.ReadFile(filepath.Join(task.Checkouts["api"].Path, "tracked"))
	if err != nil || strings.TrimSpace(string(data)) != "original" {
		t.Fatalf("background child modified checkout after the check: %q, %v", data, err)
	}
}

func TestTimedOutCheckStillDetectsMutation(t *testing.T) {
	s := fixture(t)
	task := createNo(t, s, "existing")
	check := s.Config.Integration["pair"]
	check.Command = []string{"sh", "-c", `printf mutated > tracked; printf ready; sleep 5`}
	s.Config.Integration["pair"] = check
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := s.RunCheck(ctx, task, "pair", &checkStartedWriter{start: cancel})
	if err == nil || !r.Stale || !strings.Contains(r.StaleReason, "uncommitted input") {
		t.Fatalf("timed-out mutation = %+v, %v", r, err)
	}
}
