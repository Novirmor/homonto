package gitx

import (
	"context"
	"path/filepath"
	"sync"
)

// Git's worktree bookkeeping is not concurrency-safe.
//
// `git worktree add` builds `.git/worktrees/<name>/` a file at a time and,
// while doing so, reads the sibling entries to prune stale ones. Two adds
// running at once against the same repository will therefore sometimes
// read a half-written sibling:
//
//	fatal: failed to read .git/worktrees/<other>/commondir: Success
//
// That is not a rare theoretical interleaving. Homonto issues several
// implementer assignments in a round and cuts each a worktree from the
// same member, which is exactly the shape that triggers it — and the
// failure surfaces as a refused assignment for no reason the user did
// anything to cause.
//
// Serializing per repository costs nothing that matters: `worktree add`
// is a few milliseconds of file creation, and the alternative is a
// workflow that fails intermittently under precisely the load it was
// designed for.
//
// One Homonto process holds the workspace lease, so a process-local lock
// covers every writer that can exist. Two Homonto processes cannot reach
// the same workspace to begin with.
var (
	repoLocksMu sync.Mutex
	repoLocks   = map[string]*sync.Mutex{}
)

// lockWorktree takes the repository's worktree bookkeeping exclusively and
// returns the release. Callers `defer` it, so the whole check-then-mutate
// sequence is held, not just the git invocation.
//
// The key is the shared repository directory rather than the directory git
// was invoked in: a repository and its own worktrees are different
// directories writing the same `.git/worktrees`, and locking the invocation
// directory would let them race. Resolving it costs one `rev-parse`; when
// that fails — an unreadable or not-yet-a-repository path — the invocation
// directory is used, which still serializes the common case and leaves the
// real error to be reported by the operation itself.
func lockWorktree(ctx context.Context, runner Runner, repoDir string) func() {
	lock := lockFor(worktreeLockKey(ctx, runner, repoDir))
	lock.Lock()
	return lock.Unlock
}

// worktreeLockKey identifies the bookkeeping two invocations would share.
func worktreeLockKey(ctx context.Context, runner Runner, repoDir string) string {
	repo, ok, err := Inspect(ctx, runner, repoDir)
	if err == nil && ok && repo.CommonDir != "" {
		return repo.CommonDir
	}
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return repoDir
	}
	return abs
}

// lockFor returns the mutex for a key, creating it once.
//
// The map is never pruned. Keys are repository paths, of which a run sees
// a handful; a map that grows with the number of distinct repositories
// touched in one process is not a leak worth the code to bound it.
func lockFor(key string) *sync.Mutex {
	repoLocksMu.Lock()
	defer repoLocksMu.Unlock()
	lock, ok := repoLocks[key]
	if !ok {
		lock = &sync.Mutex{}
		repoLocks[key] = lock
	}
	return lock
}
