package tocli

import (
	"fmt"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/bypasslog"
	"github.com/noviopenworks/homonto/internal/tostate"
	"github.com/spf13/cobra"
)

var toBypassNow = func() time.Time { return time.Now() }

func bypassCmd() *cobra.Command {
	var (
		dir    string
		target string
		reason string
	)
	cmd := &cobra.Command{
		Use:   "bypass <change-name> --to <phase|archive> --reason <reason>",
		Short: "Explicitly bypass workflow gates and move a change to a target",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBypass(cmd, dir, args[0], target, reason)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().StringVar(&target, "to", "", "forward phase target or archive")
	cmd.Flags().StringVar(&reason, "reason", "", "user-provided reason recorded in the bypass audit log")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

func runBypass(cmd *cobra.Command, root, name, target, reason string) error {
	if err := toFramework.Gate(root); err != nil {
		return err
	}
	target = strings.TrimSpace(target)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("to bypass: --reason must be non-empty")
	}
	if err := bypasslog.RequireRealParents(root, tasksDir(root)); err != nil {
		return err
	}
	unlock, err := lock(root)
	if err != nil {
		return err
	}
	defer unlock()

	st, err := loadChange(root, name)
	if err != nil {
		return err
	}
	if err := st.Validate(); err != nil {
		return fmt.Errorf("to bypass: %w", err)
	}
	if st.Change != name {
		return fmt.Errorf("to bypass: state change %q does not match requested change %q", st.Change, name)
	}
	if err := bypasslog.RequireRealParents(changeDir(root, name), changeDir(root, name)); err != nil {
		return err
	}
	record := bypasslog.Record{
		At:      toBypassNow().UTC().Format(time.RFC3339),
		Command: fmt.Sprintf("to bypass %s --to %s --reason %q", name, target, reason),
		From:    st.Phase,
		To:      target,
		Reason:  reason,
		Skipped: toBypassSkipped(target),
	}

	switch target {
	case tostate.PhasePlan, tostate.PhaseDo:
		old := st
		if err := bypasslog.Append(changeDir(root, name), name, "to", record); err != nil {
			return fmt.Errorf("to bypass: recording audit: %w", err)
		}
		st.Phase = target
		st.Finished = ""
		st.Verified = false
		st.Evidence = ""
		if err := tostate.Save(statePath(root, name), st); err != nil {
			return fmt.Errorf("to bypass: saving state: %w", err)
		}
		cmd.Printf("change %q bypassed %s -> %s\n", name, old.Phase, st.Phase)
		return nil
	case tostate.PhaseDone, "archive":
		return bypassTerminal(cmd, root, st, record)
	default:
		return fmt.Errorf("to bypass: --to must name plan, do, done, or archive (got %q)", target)
	}
}

func bypassTerminal(cmd *cobra.Command, root string, st tostate.State, record bypasslog.Record) error {
	change := st.Change
	changePath := changeDir(root, change)
	if err := bypasslog.RequireRealParents(root, archiveDir(root)); err != nil {
		return err
	}
	if err := bypasslog.Append(changePath, change, "to", record); err != nil {
		return err
	}

	if st.Phase == tostate.PhaseDone {
		st.Verified = false
		st.Evidence = ""
		if err := tostate.Save(statePath(root, change), st); err != nil {
			return fmt.Errorf("to bypass: saving terminal state: %w", err)
		}
		dest, err := completeArchive(root, st)
		if err != nil {
			return fmt.Errorf("to bypass: %w", err)
		}
		cmd.Printf("change %q bypassed workflow and completed archive at %s\n", change, dest)
		return nil
	}
	st.Phase = tostate.PhaseDone
	st.Verified = false
	st.Evidence = ""
	st.Finished = todayFn()
	dest, err := finishAndArchive(root, st)
	if err != nil {
		return fmt.Errorf("to bypass: %w", err)
	}
	cmd.Printf("change %q bypassed workflow and archived at %s\n", change, dest)
	return nil
}

func toBypassSkipped(target string) []string {
	if target == tostate.PhasePlan || target == tostate.PhaseDo {
		return []string{"phase-boundary"}
	}
	return []string{"phase-boundary", "verification", "worktree-cleanliness"}
}
