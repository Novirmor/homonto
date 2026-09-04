package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/bypasslog"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

var ontoBypassNow = func() time.Time { return time.Now() }

func bypassCmd() *cobra.Command {
	var (
		dir    string
		target string
		reason string
	)
	cmd := &cobra.Command{
		Use:   "bypass <change> --to <phase|archive> --reason <reason>",
		Short: "Explicitly bypass workflow gates and move a change to a target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBypass(cmd, dir, args[0], target, reason)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().StringVar(&target, "to", "", "forward phase target or archive")
	cmd.Flags().StringVar(&reason, "reason", "", "user-provided reason recorded in the bypass audit log")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

func runBypass(cmd *cobra.Command, root, name, target, reason string) error {
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}
	if err := ontoFramework.ValidChangeName(name); err != nil {
		return err
	}
	if err := bypasslog.RequireRealParents(root, changesDir(root)); err != nil {
		return err
	}
	unlock, err := bypassLock(root)
	if err != nil {
		return err
	}
	defer unlock()
	target = strings.TrimSpace(target)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("onto bypass: --reason must be non-empty")
	}
	changeDir := filepath.Join(changesDir(root), name)
	statePath := filepath.Join(changeDir, "onto-state.yaml")
	st, err := ontostate.Load(statePath)
	if err != nil {
		return fmt.Errorf("onto bypass: loading %s: %w", statePath, err)
	}
	if err := st.Validate(); err != nil {
		return fmt.Errorf("onto bypass: %w", err)
	}
	if st.Change != name {
		return fmt.Errorf("onto bypass: state change %q does not match requested change %q", st.Change, name)
	}
	if err := bypasslog.RequireRealParents(changeDir, changeDir); err != nil {
		return err
	}
	record := bypasslog.Record{
		At:      ontoBypassNow().UTC().Format(time.RFC3339),
		Command: fmt.Sprintf("onto bypass %s --to %s --reason %q", name, target, reason),
		From:    st.Phase,
		To:      target,
		Reason:  reason,
		Skipped: ontoBypassSkipped(target),
	}
	if target == "archive" {
		if err := bypasslog.Append(changeDir, name, "onto", record); err != nil {
			return err
		}
		return bypassArchive(cmd, root, changeDir, st)
	}
	if !ontostate.ValidPhase(target) {
		return fmt.Errorf("onto bypass: --to must name open, design, build, verify, close, or archive (got %q)", target)
	}
	if err := bypasslog.Append(changeDir, name, "onto", record); err != nil {
		return fmt.Errorf("onto bypass: recording audit: %w", err)
	}
	old := st
	st.Phase = target
	st.Abandoned = false
	st.Archived = false
	if err := ontostate.Save(statePath, st); err != nil {
		return fmt.Errorf("onto bypass: saving state: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: bypassed %s -> %s\n", name, old.Phase, target)
	return nil
}

func bypassArchive(cmd *cobra.Command, root, changeDir string, st ontostate.State) error {
	archiveDir := filepath.Join(ontoArchiveDir(root), time.Now().Format("2006-01-02")+"-"+st.Change)
	if err := bypasslog.RequireRealParents(root, filepath.Dir(archiveDir)); err != nil {
		return err
	}
	if _, err := os.Stat(archiveDir); err == nil {
		return fmt.Errorf("onto bypass: archive target already exists: %s", archiveDir)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("onto bypass: checking archive target: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(archiveDir), 0o755); err != nil {
		return fmt.Errorf("onto bypass: creating archive directory: %w", err)
	}
	if err := os.Rename(changeDir, archiveDir); err != nil {
		return fmt.Errorf("onto bypass: moving %s to %s: %w", changeDir, archiveDir, err)
	}
	st.Archived = true
	if err := ontostate.Save(filepath.Join(archiveDir, "onto-state.yaml"), st); err != nil {
		if rollbackErr := os.Rename(archiveDir, changeDir); rollbackErr != nil {
			return fmt.Errorf("onto bypass: recording archived flag failed (%v) and move rollback failed (%v)", err, rollbackErr)
		}
		return fmt.Errorf("onto bypass: recording archived flag: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: bypassed workflow and archived to %s\n", st.Change, archiveDir)
	return nil
}

func bypassLock(root string) (func(), error) {
	path := filepath.Join(changesDir(root), ".onto-bypass.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("onto bypass: another bypass is in progress (lock held at %s)", path)
		}
		return nil, fmt.Errorf("onto bypass: creating lock: %w", err)
	}
	_, _ = fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}

func ontoBypassSkipped(target string) []string {
	if target == "archive" {
		return []string{"phase", "artifacts", "tasks", "workflow-evidence", "verification", "merge", "dependencies", "worktree-cleanliness"}
	}
	return []string{"phase-boundary", "artifacts", "tasks", "workflow-evidence", "verification", "isolation", "dependency-cycle", "worktree-cleanliness"}
}
