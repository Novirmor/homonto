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
