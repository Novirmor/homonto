package tocli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
// workflow-root/changes so a mixed repo never confuses either tool's commands —
// though homonto refuses to declare both frameworks anyway.
func tasksDir(root string) string   { return filepath.Join(workcli.WorkflowRootOrDefault(root), "tasks") }
func archiveDir(root string) string { return filepath.Join(tasksDir(root), "archive") }

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
// (last-writer-wins with no diagnostic). The lock lives at
// docs/tasks/.to.lock via the shared workcli helper — the same file `onto
// demote` holds as its destination lock — and a lock whose recorded pid is
// provably no longer running is reclaimed automatically by the next attempt.
func lock(root string) (func(), error) {
	if err := os.MkdirAll(tasksDir(root), 0o755); err != nil {
		return nil, fmt.Errorf("to: lock: %w", err)
	}
	return workcli.LockWorkspace("to", filepath.Join(tasksDir(root), ".to.lock"))
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
