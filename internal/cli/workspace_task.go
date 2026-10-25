package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/worktask"
	"github.com/spf13/cobra"
)

func workspaceTaskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Allocate and verify a task's selected repository checkouts"}
	open := func(cmd *cobra.Command) (*worktask.Store, error) {
		path, err := cmd.Flags().GetString("config")
		if err != nil {
			return nil, err
		}
		return worktask.Open(path)
	}
	var engine, change, isolation string
	var repos []string
	var overrides map[string]string
	create := &cobra.Command{Use: "create <name>", Short: "Record one engine and an explicit isolation choice (JSON output)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := open(cmd)
		if err != nil {
			return err
		}
		t, err := s.Create(cmd.Context(), args[0], engine, change, isolation, repos, overrides)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(t)
	}}
	create.Flags().StringVar(&engine, "engine", "", "selected engine: no, onto, or to (immutable for this task)")
	create.Flags().StringVar(&change, "change", "", "existing active lifecycle change (required for onto/to)")
	create.Flags().StringVar(&isolation, "isolation", "", "user-selected isolation: existing or worktree")
	create.Flags().StringArrayVar(&repos, "repo", nil, "changed repository alias (repeatable)")
	create.Flags().StringToStringVar(&overrides, "isolation-override", nil, "per-repository isolation, e.g. web=worktree")
	for _, flag := range []string{"engine", "isolation", "repo"} {
		_ = create.MarkFlagRequired(flag)
	}
	inspect := &cobra.Command{Use: "inspect <name>", Short: "Resume recorded choices and inspect current integration evidence (JSON)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := open(cmd)
		if err != nil {
			return err
		}
		t, err := s.Load(args[0])
		if err != nil {
			return err
		}
		s.RefreshResults(cmd.Context(), t)
		return json.NewEncoder(cmd.OutOrStdout()).Encode(t)
	}}
	var checkName string
	var paused bool
	var timeout time.Duration
	check := &cobra.Command{Use: "check <name>", Short: "Run matching configured integration checks; participating writers must be paused", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if !paused {
			return fmt.Errorf("pause participating writers, then pass --writers-paused")
		}
		if timeout <= 0 {
			return fmt.Errorf("timeout must be positive")
		}
		s, err := open(cmd)
		if err != nil {
			return err
		}
		t, err := s.Load(args[0])
		if err != nil {
			return err
		}
		names := s.Config.IntegrationChecksFor(t.Changed)
		if checkName != "" {
			names = []string{checkName}
		}
		if len(names) == 0 {
			cmd.Println("No configured check matches; choose and report task-specific integration verification.")
			return nil
		}
		var failures []error
		for _, name := range names {
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			result, err := s.RunCheck(ctx, t, name, cmd.ErrOrStderr())
			cancel()
			if encErr := json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				Name   string          `json:"name"`
				Result worktask.Result `json:"result"`
			}{name, result}); encErr != nil {
				return encErr
			}
			if err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", name, err))
			}
			if cmd.Context().Err() != nil {
				break
			}
		}
		return errors.Join(failures...)
	}}
	check.Flags().StringVar(&checkName, "check", "", "run one matching check (default: all matching checks)")
	check.Flags().BoolVar(&paused, "writers-paused", false, "confirm participating writers have been paused")
	check.Flags().DurationVar(&timeout, "timeout", 10*time.Minute, "time limit per integration check")
	var yes bool
	cleanup := &cobra.Command{Use: "cleanup <name>", Short: "Remove clean integrated task-owned worktrees; native onto/to bindings use worktree remove", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := open(cmd)
		if err != nil {
			return err
		}
		t, err := s.Load(args[0])
		if err != nil {
			return err
		}
		return s.Cleanup(cmd.Context(), t, yes)
	}}
	cleanup.Flags().BoolVar(&yes, "yes", false, "confirm removal of task-owned worktrees")
	cmd.AddCommand(create, inspect, check, cleanup)
	return cmd
}
