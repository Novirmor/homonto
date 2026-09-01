package tocli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noviopenworks/homonto/internal/tostate"
)

// overrideToday pins todayFn to a fixed date for the test and restores it on
// cleanup. No test in this package calls t.Parallel, so the override is safe.
func overrideToday(t *testing.T, date string) {
	t.Helper()
	prev := todayFn
	todayFn = func() string { return date }
	t.Cleanup(func() { todayFn = prev })
}

// TestTodayFn_NewStampsCreatedWithPinnedDate verifies the todayFn seam drives
// the Created timestamp on `to new`: an untampered clock would make this date
// flaky; pinning it makes the assertion exact.
func TestTodayFn_NewStampsCreatedWithPinnedDate(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	overrideToday(t, "2030-05-17")

	run(t, false, "new", "pinned", "--dir", dir)

	st, err := tostate.Load(statePath(dir, "pinned"))
	if err != nil {
		t.Fatalf("loading state: %v", err)
	}
	if st.Created != "2030-05-17" {
		t.Errorf("pinned new Created = %q, want 2030-05-17", st.Created)
	}
}

// TestTodayFn_DoneStampsFinishedAndArchivePrefix verifies the same seam drives
// both the Finished state field AND the date-prefixed archive directory name —
// the two must agree so a recurring chore can be reused across runs.
func TestTodayFn_DoneStampsFinishedAndArchivePrefix(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	overrideToday(t, "2030-05-17")

	run(t, false, "new", "chore", "--dir", dir)
	run(t, false, "phase", "chore", "--dir", dir)
	run(t, false, "done", "chore", "--verified", "--dir", dir)

	st, err := tostate.Load(filepath.Join(archiveDir(dir), "2030-05-17-chore", tostate.FileName))
	if err != nil {
		t.Fatalf("loading archived state: %v", err)
	}
	if st.Finished != "2030-05-17" {
		t.Errorf("pinned done Finished = %q, want 2030-05-17", st.Finished)
	}
}

// TestTodayFn_AbandonStampsFinished verifies abandon also stamps Finished via
// todayFn (the symmetric path to done).
func TestTodayFn_AbandonStampsFinished(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	overrideToday(t, "2030-11-09")

	run(t, false, "new", "dropped", "--dir", dir)
	run(t, false, "abandon", "dropped", "--dir", dir)

	st, err := tostate.Load(filepath.Join(archiveDir(dir), "2030-11-09-dropped", tostate.FileName))
	if err != nil {
		t.Fatalf("loading archived state: %v", err)
	}
	if st.Finished != "2030-11-09" {
		t.Errorf("pinned abandon Finished = %q, want 2030-11-09", st.Finished)
	}
}

// TestTodayFn_CompleteArchiveFallbackDatesByCompletion exercises the
// pre-Finished wedge path in completeArchive: a terminal state with no
// Finished field must fall back to todayFn for the archive date rather than
// producing "<empty>-<name>".
func TestTodayFn_CompleteArchiveFallbackDatesByCompletion(t *testing.T) {
	dir := setUpGatedWorkspace(t)
	overrideToday(t, "2030-02-03")

	run(t, false, "new", "wedged", "--dir", dir)
	// Simulate a wedged terminal-but-active state with Finished unset.
	if err := tostate.Save(statePath(dir, "wedged"), tostate.State{
		Change: "wedged", Phase: tostate.PhaseAbandoned,
	}); err != nil {
		t.Fatal(err)
	}

	run(t, false, "abandon", "wedged", "--dir", dir)
	if _, err := os.Stat(filepath.Join(archiveDir(dir), "2030-02-03-wedged", tostate.FileName)); err != nil {
		t.Errorf("completeArchive did not use todayFn as fallback: %v", err)
	}
}

// TestValidChangeName_TocliRejectsArchive is the tocli-specific reservation
// audit called out: "archive" matches the lowercase-hyphen shape rule but is
// refused because it is the archive directory itself.
func TestValidChangeName_TocliRejectsArchive(t *testing.T) {
	if err := toFramework.ValidChangeName("archive"); err == nil ||
		!strings.Contains(err.Error(), "reserved") {
		t.Errorf("tocli ValidChangeName(archive) = %v, want a reserved error", err)
	}
}

// TestValidChangeName_AcceptsAndRejects is a focused table covering the
// tocli Framework's accepted shape plus the explicit `..`-containing name
// the audit asked about.
func TestValidChangeName_AcceptsAndRejects(t *testing.T) {
	for _, ok := range []string{"a", "fix-42", "lowercase-hyphen", "abc"} {
		if err := toFramework.ValidChangeName(ok); err != nil {
			t.Errorf("ValidChangeName(%q) = %v, want nil", ok, err)
		}
	}
	for _, bad := range []string{
		"",          // empty
		"../evil",   // traversal
		"a/b",       // separator
		"a..b",      // embedded .. (audit ask)
		"..",        // bare ..
		"Foo",       // uppercase
		"a--b",      // double hyphen
		"-leading",  // leading hyphen
		"trailing-", // trailing hyphen
		"has space", // space
	} {
		if err := toFramework.ValidChangeName(bad); err == nil {
			t.Errorf("ValidChangeName(%q) = nil, want an error", bad)
		}
	}
}

// TestArchiveDest_NumericSuffixOnSameDay verifies a same-day same-name reuse
// (e.g. a recurring chore) gets a -2 suffix instead of colliding.
func TestArchiveDest_NumericSuffixOnSameDay(t *testing.T) {
	dir := t.TempDir()
	first := archiveDest(dir, "chore", "2030-01-01")
	if !strings.HasSuffix(first, filepath.Join("archive", "2030-01-01-chore")) {
		t.Fatalf("first dest = %q, want .../2030-01-01-chore", first)
	}
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	second := archiveDest(dir, "chore", "2030-01-01")
	if !strings.HasSuffix(second, filepath.Join("archive", "2030-01-01-chore-2")) {
		t.Errorf("second dest = %q, want .../2030-01-01-chore-2", second)
	}
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	third := archiveDest(dir, "chore", "2030-01-01")
	if !strings.HasSuffix(third, filepath.Join("archive", "2030-01-01-chore-3")) {
		t.Errorf("third dest = %q, want .../2030-01-01-chore-3", third)
	}
}

// TestArchive_DestinationExistsIsError covers archive's refusal to clobber an
// existing destination — the wedge guard callers pre-check before writing
// terminal state.
func TestArchive_DestinationExistsIsError(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(archiveDir(dir), "2030-01-01-x")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := archive(dir, "x", dest); err == nil ||
		!strings.Contains(err.Error(), "already exists") {
		t.Errorf("archive(clobber) = %v, want an already-exists error", err)
	}
}

// TestFindArchived_LegacyUnprefixedDir verifies pre-v0.5.0 unprefixed archive
// dirs (just "<name>") are still recognized.
func TestFindArchived_LegacyUnprefixedDir(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(archiveDir(dir), "legacy-change")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := findArchived(dir, "legacy-change"); got != legacy {
		t.Errorf("findArchived(legacy) = %q, want %q", got, legacy)
	}
}

// TestFindArchived_PrefixedPicksNewest verifies the lexical-sort newest
// selection across multiple dated archives of the same name (date prefixes
// sort lexically under the ISO 8601 shape).
func TestFindArchived_PrefixedPicksNewest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"2025-01-01-recur",
		"2025-06-06-recur",
		"2026-01-01-recur",
	} {
		if err := os.MkdirAll(filepath.Join(archiveDir(dir), name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got := findArchived(dir, "recur")
	want := filepath.Join(archiveDir(dir), "2026-01-01-recur")
	if got != want {
		t.Errorf("findArchived(recur) = %q, want %q (newest)", got, want)
	}
}

// TestLoadChange_ArchivedNamesTheArchive verifies loadChange distinguishes
// "already archived" from "never existed" by surfacing the archive path.
func TestLoadChange_ArchivedNamesTheArchive(t *testing.T) {
	dir := t.TempDir()
	archived := filepath.Join(archiveDir(dir), "2026-03-03-old")
	if err := os.MkdirAll(archived, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := loadChange(dir, "old")
	if err == nil ||
		!strings.Contains(err.Error(), "archived") ||
		!strings.Contains(err.Error(), archived) {
		t.Errorf("loadChange(archived) = %v, want an error naming %q", err, archived)
	}
}

// TestLoadChange_InvalidNameFailsFirst verifies name validation precedes the
// filesystem lookup — the audit's path-escape concern.
func TestLoadChange_InvalidNameFailsFirst(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadChange(dir, "../escape"); err == nil ||
		!strings.Contains(err.Error(), "path separators") {
		t.Errorf("loadChange(../escape) = %v, want a path-separator error", err)
	}
}

// TestPrintJSON_ErrorOnUnmarshallable verifies printJSON surfaces marshal
// failures (defensive: the cobra command never sees these in practice).
func TestPrintJSON_ErrorOnUnmarshallable(t *testing.T) {
	cmd := NewRootCmd()
	var out strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	if err := printJSON(cmd, make(chan int)); err == nil ||
		!strings.Contains(err.Error(), "encoding json") {
		t.Errorf("printJSON(chan) = %v, want an encoding-json error", err)
	}
}

// deadPid returns the pid of a child process that has already exited and been
// reaped, so the number provably names no running process (CI is linux-only;
// "true" exists everywhere we run).
func deadPid(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("spawning throwaway process: %v", err)
	}
	return cmd.Process.Pid
}

// TestPidAlive_SelfAndDead verifies both directions of the liveness probe the
// stale-lock reclaim trusts: the test's own pid reads alive, a reaped child's
// pid reads dead.
func TestPidAlive_SelfAndDead(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Errorf("pidAlive(self) = false, want true")
	}
	if pidAlive(deadPid(t)) {
		t.Errorf("pidAlive(reaped child) = true, want false")
	}
}

// TestLock_ExcludesConcurrentAndReleases verifies the mutual-exclusion
// contract: a second acquire while held fails naming the lock, and succeeds
// again after release.
func TestLock_ExcludesConcurrentAndReleases(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lock(dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	if _, err := lock(dir); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Errorf("second lock while held = %v, want an in-progress error", err)
	}
	unlock()
	if _, err := lock(dir); err != nil {
		t.Errorf("lock after release: %v", err)
	}
}

// TestLock_StaleDeadHolderIsReclaimed verifies the auto-reclaim: a lockfile
// naming a pid that provably no longer runs is taken over by the next
// mutating command instead of wedging the workspace until hand cleanup.
func TestLock_StaleDeadHolderIsReclaimed(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(tasksDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(tasksDir(dir), ".to.lock")
	if err := os.WriteFile(stale, []byte(fmt.Sprintf("pid=%d\n", deadPid(t))), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock, err := lock(dir)
	if err != nil {
		t.Fatalf("lock over stale lockfile: %v", err)
	}
	unlock()
}

// TestLock_LiveHolderIsNeverStolen verifies the safety side of the reclaim: a
// lockfile naming a running pid (the test process itself) fails the acquire
// and the file is left byte-for-byte intact.
func TestLock_LiveHolderIsNeverStolen(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(tasksDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(tasksDir(dir), ".to.lock")
	held := fmt.Sprintf("pid=%d\n", os.Getpid())
	if err := os.WriteFile(stale, []byte(held), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lock(dir); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Errorf("lock over live holder = %v, want an in-progress error", err)
	}
	got, err := os.ReadFile(stale)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != held {
		t.Errorf("live holder's lockfile was modified: %q, want %q", got, held)
	}
}

// TestLock_UnreadablePidLeftForHandCleanup verifies the conservative branch:
// a lockfile with no parseable pid (a crash in the create-to-write window may
// still have a live owner) is never auto-removed.
func TestLock_UnreadablePidLeftForHandCleanup(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(tasksDir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(tasksDir(dir), ".to.lock")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := lock(dir); err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Errorf("lock over unreadable lockfile = %v, want an in-progress error", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Errorf("unreadable lockfile was removed: %v", err)
	}
}
