package tocli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/noviopenworks/homonto/internal/workspace"
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
	// [frameworks.h] transitively installs to (its catalog dependency),
	// so an applied h satisfies this gate too.
	GateAliases: []string{"h"},
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

// validateWorkflowDir checks the configured boundary and refuses planted parents,
// even when the workflow root or its trailing directories do not exist yet.
func validateWorkflowDir(root, dir string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	configPath := filepath.Join(root, "homonto.toml")
	if _, err := os.Lstat(configPath); err == nil {
		if _, err := os.Stat(configPath); err != nil {
			return err // A dangling configured entry is not config-free recovery.
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	wf, err := workcli.WorkflowRoot(root)
	if err != nil {
		return err
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err := workcli.ValidateWorkflowPath(root, dir); err != nil {
		return err
	}
	rel, err := filepath.Rel(wf, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("to: %s is outside workflow root %s", dir, wf)
	}
	if err := fsutil.RequireRealParents(filepath.VolumeName(wf)+string(filepath.Separator), wf); err != nil {
		return err
	}
	if err := fsutil.RequireRealParents(wf, dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// loadChange loads an active (non-archived) change's state, with an error
// that distinguishes "never existed" from "already archived".
func loadChange(root, name string) (tostate.State, error) {
	if err := toFramework.ValidChangeName(name); err != nil {
		return tostate.State{}, err
	}
	if err := validateWorkflowDir(root, changeDir(root, name)); err != nil {
		return tostate.State{}, err
	}
	if fi, err := os.Lstat(statePath(root, name)); err == nil && !fi.Mode().IsRegular() {
		return tostate.State{}, fmt.Errorf("to: state %s is not a regular file (symlinks are refused)", statePath(root, name))
	} else if err != nil && !os.IsNotExist(err) {
		return tostate.State{}, err
	}
	st, err := tostate.Load(statePath(root, name))
	if err == nil {
		if st.Change != name {
			return tostate.State{}, fmt.Errorf("to: state identity mismatch: requested %q, recorded %q", name, st.Change)
		}
		if err := st.Validate(); err != nil {
			return tostate.State{}, err
		}
		return st, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return tostate.State{}, err
	}
	if pathErr := validateWorkflowDir(root, archiveDir(root)); pathErr != nil {
		return tostate.State{}, pathErr
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
	if err := validateWorkflowDir(root, archiveDir(root)); err != nil {
		return ""
	}
	entries, err := os.ReadDir(archiveDir(root))
	if err != nil {
		return ""
	}
	newest, newestDate, newestSuffix := "", "", 0
	for _, entry := range entries {
		date, suffix, ok := archiveOrder(entry.Name(), name)
		if !entry.IsDir() || !ok {
			continue
		}
		path := filepath.Join(archiveDir(root), entry.Name())
		stateFile := filepath.Join(path, tostate.FileName)
		fi, err := os.Lstat(stateFile)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		st, err := tostate.Load(stateFile)
		if err != nil || st.Validate() != nil || st.Change != name || !st.Terminal() {
			continue
		}
		if newest == "" || date > newestDate || (date == newestDate && suffix > newestSuffix) {
			newest, newestDate, newestSuffix = path, date, suffix
		}
	}
	return newest
}

func archiveOrder(entry, name string) (string, int, bool) {
	if entry == name {
		return "", 1, true // pre-v0.5.0 archive
	}
	if len(entry) < 12 || entry[10] != '-' {
		return "", 0, false
	}
	date := entry[:10]
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return "", 0, false
	}
	tail := entry[11:]
	if tail == name {
		return date, 1, true
	}
	suffix, ok := strings.CutPrefix(tail, name+"-")
	n, err := strconv.Atoi(suffix)
	return date, n, ok && err == nil && n >= 2 && strconv.Itoa(n) == suffix
}

// archiveDest is where a change finishing on the given date archives to. The
// date prefix frees the change name for reuse (a recurring chore can run
// again next time); a same-day reuse gets a numeric suffix so finishing can
// never collide into a wedge.
func archiveDest(root, name, finished string) (string, error) {
	if err := toFramework.ValidChangeName(name); err != nil {
		return "", err
	}
	if _, err := time.Parse("2006-01-02", finished); err != nil {
		return "", fmt.Errorf("to: invalid archive date %q: %w", finished, err)
	}
	if err := validateWorkflowDir(root, archiveDir(root)); err != nil {
		return "", err
	}
	// Inspect against the real destination parent. A missing parent can mask
	// errors such as ENAMETOOLONG until after a terminal state has been written.
	if err := os.MkdirAll(archiveDir(root), 0o755); err != nil {
		return "", fmt.Errorf("to: creating archive directory: %w", err)
	}
	base := filepath.Join(archiveDir(root), finished+"-"+name)
	dest := base
	for n := 2; ; n++ {
		if _, err := os.Lstat(dest); os.IsNotExist(err) {
			return dest, nil
		} else if err != nil {
			return "", fmt.Errorf("to: inspecting archive destination %s: %w", dest, err)
		}
		dest = fmt.Sprintf("%s-%d", base, n)
	}
}

// archive moves an active change directory to dest. It refuses to clobber an
// existing archive; callers pre-check the destination before writing terminal
// state so a collision cannot strand a terminal change in the active tree.
func archive(root, name, dest string) (string, error) {
	if err := validateWorkflowDir(root, filepath.Dir(dest)); err != nil {
		return "", err
	}
	if _, err := os.Lstat(dest); err == nil {
		return "", fmt.Errorf("to: archive destination %s already exists", dest)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("to: inspecting archive destination %s: %w", dest, err)
	}
	if err := validateWorkflowDir(root, changeDir(root, name)); err != nil {
		return "", err
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
func finishAndArchive(ctx context.Context, root string, st tostate.State) (string, error) {
	dest, date, err := commandArchiveDest(ctx, root, st.Change, st.Finished)
	if err != nil {
		return "", err
	}
	st.Finished = date
	if err := tostate.Save(statePath(root, st.Change), st); err != nil {
		return "", err
	}
	if err := workspace.CheckArchiveTarget(ctx, changeDir(root, st.Change), dest); err != nil {
		return "", err
	}
	return archive(root, st.Change, dest)
}

// completeArchive finishes the interrupted half of a terminal-but-active
// change: the state already says done/abandoned, only the move into the
// archive is missing.
func completeArchive(ctx context.Context, root string, st tostate.State) (string, error) {
	finished := st.Finished
	if finished == "" {
		// A wedged pre-Finished state; date the archive by completion instead.
		finished = todayFn()
	}
	dest, _, err := commandArchiveDest(ctx, root, st.Change, finished)
	if err != nil {
		return "", err
	}
	if err := workspace.CheckArchiveTarget(ctx, changeDir(root, st.Change), dest); err != nil {
		return "", err
	}
	return archive(root, st.Change, dest)
}

func commandArchiveDest(ctx context.Context, root, name, finished string) (string, string, error) {
	dest, date, planned, err := workspace.ArchiveTarget(ctx, changeDir(root, name))
	if err != nil {
		return "", "", err
	}
	if !planned {
		dest, err = archiveDest(root, name, finished)
		return dest, finished, err
	}
	if err := validateWorkflowDir(root, archiveDir(root)); err != nil {
		return "", "", err
	}
	// As with archiveDest, expose path errors before writing terminal state.
	if err := os.MkdirAll(archiveDir(root), 0o755); err != nil {
		return "", "", err
	}
	if err := workspace.CheckArchiveTarget(ctx, changeDir(root, name), dest); err != nil {
		return "", "", err
	}
	return dest, date, nil
}

// lock takes an exclusive per-workspace lock for a mutating command, so two
// concurrent sessions cannot interleave writes on the same change
// (last-writer-wins with no diagnostic). The lock lives at
// docs/tasks/.to.lock via the shared workcli helper — the same file `onto
// demote` holds as its destination lock — and a lock whose recorded pid is
// provably no longer running is reclaimed automatically by the next attempt.
func lock(root string) (func(), error) {
	if err := validateWorkflowDir(root, tasksDir(root)); err != nil {
		return nil, err
	}
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
