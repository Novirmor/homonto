//go:build windows

package applylock

// AcquireProcess on Windows falls back to the portable O_EXCL lockfile with
// pid reclamation: flock does not exist there, and LockFileEx is deferred.
// A killed holder leaves a lockfile whose recorded pid is provably dead and
// is reclaimed by the next attempt (the same protocol as the workflow
// workspace locks).
func AcquireProcess(dir string) (*Lock, error) {
	return Acquire(dir)
}
