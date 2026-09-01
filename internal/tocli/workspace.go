package tocli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/spf13/cobra"
)

// toFramework parameterizes the shared workcli helpers for the to binary.
// "archive" is reserved because it is the archive directory itself; no other
// name is reserved for to.
var toFramework = workcli.Framework{
	Name:          "to",
	SkillsDir:     "skills/to",
	GatePrefix:    "to",
	NamePrefix:    "to",
	ReservedNames: []string{"archive"},
}

// todayFn returns today's date for created/finished stamps and archive
// prefixes; a variable so tests can pin it.
var todayFn = func() string { return time.Now().Format("2006-01-02") }

// tasksDir/archiveDir are to's territory. Fully disjoint from onto's
// docs/changes/ so a mixed repo never confuses either tool's commands —
// though homonto refuses to declare both frameworks anyway.
func tasksDir(root string) string   { return filepath.Join(root, "docs", "tasks") }
func archiveDir(root string) string { return filepath.Join(root, "docs", "tasks", "archive") }

func changeDir(root, name string) string { return filepath.Join(tasksDir(root), name) }
func statePath(root, name string) string {
	return filepath.Join(changeDir(root, name), tostate.FileName)
}
func planPath(root, name string) string { return filepath.Join(changeDir(root, name), "plan.md") }

// loadChange loads an active (non-archived) change's state, with an error
// that distinguishes "never existed" from "already archived".
func loadChange(root, name string) (tostate.State, error) {
	if err := toFramework.ValidChangeName(name); err != nil {
		return tostate.State{}, err
	}
	st, err := tostate.Load(statePath(root, name))
	if err == nil {
		return st, nil
	}
	if archived := findArchived(root, name); archived != "" {
		return tostate.State{}, fmt.Errorf("to: change %q is archived at %s", name, archived)
	}
	return tostate.State{}, err
}

// findArchived returns the newest archive directory holding the named change
// ("" if none). Archive dirs are date-prefixed (<YYYY-MM-DD>-<name>) so a
// name can be reused across changes; pre-v0.5.0 unprefixed dirs are matched
// too.
func findArchived(root, name string) string {
	newest := ""
	if _, err := os.Stat(filepath.Join(archiveDir(root), name)); err == nil {
		newest = filepath.Join(archiveDir(root), name)
	}
	matches, _ := filepath.Glob(filepath.Join(archiveDir(root), "*-"+name))
	for _, m := range matches {
		// The date prefix sorts lexically, so the last match is the newest.
		if newest == "" || filepath.Base(m) > filepath.Base(newest) {
			newest = m
		}
	}
	return newest
}

// archiveDest is where a change finishing on the given date archives to. The
// date prefix frees the change name for reuse (a recurring chore can run
// again next time); a same-day reuse gets a numeric suffix so finishing can
// never collide into a wedge.
func archiveDest(root, name, finished string) string {
	base := filepath.Join(archiveDir(root), finished+"-"+name)
	dest := base
	for n := 2; ; n++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			return dest
		}
		dest = fmt.Sprintf("%s-%d", base, n)
	}
}

// archive moves an active change directory to dest. It refuses to clobber an
// existing archive; callers pre-check the destination before writing terminal
// state so a collision cannot strand a terminal change in the active tree.
func archive(root, name, dest string) (string, error) {
	if _, err := os.Stat(dest); err == nil {
		return "", fmt.Errorf("to: archive destination %s already exists", dest)
	}
	if err := os.MkdirAll(archiveDir(root), 0o755); err != nil {
		return "", fmt.Errorf("to: creating %s: %w", archiveDir(root), err)
	}
	if err := os.Rename(changeDir(root, name), dest); err != nil {
		return "", fmt.Errorf("to: archiving %s: %w", name, err)
	}
	return dest, nil
}

// finishAndArchive is the shared terminal move for done and abandon: pick a
// free archive destination, write the terminal state, then rename. A crash
// (or kill) between the write and the rename leaves a terminal change in the
// active tree; re-running the same command converges it (completeArchive), and
// `to doctor` reports it.
func finishAndArchive(root string, st tostate.State) (string, error) {
	dest := archiveDest(root, st.Change, st.Finished)
	if err := tostate.Save(statePath(root, st.Change), st); err != nil {
		return "", err
	}
	return archive(root, st.Change, dest)
}

// completeArchive finishes the interrupted half of a terminal-but-active
// change: the state already says done/abandoned, only the move into the
// archive is missing.
func completeArchive(root string, st tostate.State) (string, error) {
	finished := st.Finished
	if finished == "" {
		// A wedged pre-Finished state; date the archive by completion instead.
		finished = todayFn()
	}
	return archive(root, st.Change, archiveDest(root, st.Change, finished))
}

// lock takes an exclusive per-workspace lock for a mutating command, so two
// concurrent sessions cannot interleave writes on the same change
// (last-writer-wins with no diagnostic). Same O_EXCL pattern as homonto's
// applylock: portable, fail-fast, and a SIGKILLed holder leaves a lockfile
// whose content names the holder pid. Unlike applylock (where reclamation is
// deliberately manual), a lock whose recorded pid is provably no longer
// running is reclaimed automatically by the next attempt: the holder is gone,
// so nothing live can be stolen. A lock with no readable pid — a crash in the
// create-to-write window — and a lock held by a live pid are never touched.
func lock(root string) (func(), error) {
	if err := os.MkdirAll(tasksDir(root), 0o755); err != nil {
		return nil, fmt.Errorf("to: lock: %w", err)
	}
	path := filepath.Join(tasksDir(root), ".to.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, fs.ErrExist) {
		if pid, ok := lockPid(path); ok && !pidAlive(pid) {
			if rmErr := os.Remove(path); rmErr == nil {
				f, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			}
		}
	}
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("to: another to command is in progress (lock held at %s); wait for it, or remove the file if none is running", path)
		}
		return nil, fmt.Errorf("to: lock: %w", err)
	}
	fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}

// lockPid reads the holder pid recorded in a lockfile. ok=false when the file
// is unreadable or carries no parseable pid= line: such a lock is never
// auto-reclaimed, because the create-to-write window means it may still have
// a live owner that simply has not written its pid yet.
func lockPid(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(ln, "pid="); ok {
			if pid, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && pid > 0 {
				return pid, true
			}
		}
	}
	return 0, false
}

// pidAlive reports whether pid names a running process. Only a confirmed
// finished/dead process counts as dead — os.Signal(0) yields
// os.ErrProcessDone (or ESRCH) for one that no longer runs — while a
// permission error means the process exists but belongs to another user. On
// Windows os.FindProcess itself fails for a finished pid, so its success is
// the answer there. A recycled pid (dead holder's number taken by an
// unrelated process) therefore reads as alive — the safe direction; the lock
// waits for hand cleanup, as before.
func pidAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return !(errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH))
	}
	return true
}

// printJSON marshals v with indentation to the command's stdout.
func printJSON(cmd *cobra.Command, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("to: encoding json: %w", err)
	}
	cmd.Println(string(b))
	return nil
}
