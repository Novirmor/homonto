package cli

import (
	"encoding/json"
	"fmt"

	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

func worktreeCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "worktree", Short: "Manage registered per-change repository worktrees"}
	var workflow, repo, base, branch string
	var createJSON bool
	create := &cobra.Command{
		Use: "create <change>", Short: "Create an isolated checkout from an explicit base commit", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := worktreeLayout(cmd)
			if err != nil {
				return err
			}
			w, err := workspace.CreateWorktree(l, workflow, args[0], repo, base, branch)
			if err != nil {
				return err
			}
			return printWorktree(cmd, w, createJSON)
		},
	}
	create.Flags().StringVar(&workflow, "workflow", "", "workflow: onto or to")
	create.Flags().StringVar(&repo, "repo", "", "selected declared repository alias")
	create.Flags().StringVar(&base, "base", "", "explicit base ref (resolved to an immutable commit)")
	create.Flags().StringVar(&branch, "branch", "", "new branch name (never overwritten)")
	create.Flags().BoolVar(&createJSON, "json", false, "print the registered binding as JSON")
	for _, flag := range []string{"workflow", "repo", "base", "branch"} {
		_ = create.MarkFlagRequired(flag)
	}
	var receiverWorkflow, receiverRepo, receiverStateID string
	var receiverJSON bool
	receiver := &cobra.Command{
		Use: "receiver <change>", Short: "Allocate a clean worktree at the recorded integration target", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := worktreeLayout(cmd)
			if err != nil {
				return err
			}
			w, err := workspace.ReceiverWorktree(l, receiverWorkflow, args[0], receiverRepo, receiverStateID)
			if err != nil {
				return err
			}
			return printWorktree(cmd, w, receiverJSON)
		},
	}
	receiver.Flags().StringVar(&receiverWorkflow, "workflow", "", "workflow: onto or to")
	receiver.Flags().StringVar(&receiverRepo, "repo", "", "selected declared repository alias")
	receiver.Flags().StringVar(&receiverStateID, "state-id", "", "select an ambiguous archived generation by native ID or registered stateID; never rebinds an existing worktree")
	receiver.Flags().BoolVar(&receiverJSON, "json", false, "print the registered receiver binding as JSON")
	_ = receiver.MarkFlagRequired("workflow")
	_ = receiver.MarkFlagRequired("repo")
	var listJSON bool
	list := &cobra.Command{
		Use: "list", Short: "List and validate registered execution worktrees", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := worktreeLayout(cmd)
			if err != nil {
				return err
			}
			entries, err := workspace.ListWorktrees(l)
			if err != nil {
				return err
			}
			if listJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(entries)
			}
			for _, w := range entries {
				role := w.Role
				if role == "" {
					role = "execution"
				}
				cmd.Printf("%s/%s\t%s\t%s\t%s\t%s\n", w.Workflow, w.Change, w.Repo, role, w.Branch, w.Path)
			}
			return nil
		},
	}
	list.Flags().BoolVar(&listJSON, "json", false, "print registered bindings as JSON")
	var removeWorkflow, removeRepo, removeRole string
	var yes, removeJSON bool
	remove := &cobra.Command{
		Use: "remove <change>", Short: "Remove a clean, integrated worktree for a terminal change", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			l, err := worktreeLayout(cmd)
			if err != nil {
				return err
			}
			w, err := workspace.RemoveWorktreeRole(l, removeWorkflow, args[0], removeRepo, removeRole, yes)
			if err != nil {
				return err
			}
			return printWorktree(cmd, w, removeJSON)
		},
	}
	remove.Flags().StringVar(&removeWorkflow, "workflow", "", "workflow: onto or to")
	remove.Flags().StringVar(&removeRepo, "repo", "", "registered repository alias")
	remove.Flags().StringVar(&removeRole, "role", "execution", "binding role: execution or receiver")
	remove.Flags().BoolVar(&yes, "yes", false, "confirm removal without bypassing safety checks")
	remove.Flags().BoolVar(&removeJSON, "json", false, "print the removed binding as JSON")
	_ = remove.MarkFlagRequired("workflow")
	_ = remove.MarkFlagRequired("repo")
	cmd.AddCommand(create, receiver, list, remove)
	return cmd
}

func worktreeLayout(cmd *cobra.Command) (workspace.Layout, error) {
	path, err := cmd.Flags().GetString("config")
	if err != nil {
		return workspace.Layout{}, fmt.Errorf("worktree: config flag: %w", err)
	}
	return workspace.Load(path)
}

func printWorktree(cmd *cobra.Command, w workspace.Worktree, jsonMode bool) error {
	if jsonMode {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(w)
	}
	cmd.Println(w.Path)
	return nil
}
