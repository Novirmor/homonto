package cli

import (
	"encoding/json"
	"fmt"

	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

func workspaceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workspace", Short: "Inspect and checkpoint owned workflow records"}
	load := func(cmd *cobra.Command) (workspace.Layout, error) {
		path, err := cmd.Flags().GetString("config")
		if err != nil {
			return workspace.Layout{}, err
		}
		return workspace.Load(path)
	}
	var jsonMode, yes bool
	inspect := &cobra.Command{
		Use: "inspect", Short: "Inspect workspace layout and history without writing files", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := load(cmd)
			if err != nil {
				return err
			}
			history, err := workspace.InspectHistory(l)
			if err != nil {
				return err
			}
			if jsonMode {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
					SchemaVersion int                         `json:"schema_version"`
					ConfigPath    string                      `json:"config_path"`
					ConfigRoot    string                      `json:"config_root"`
					WorkflowRoot  string                      `json:"workflow_root"`
					WorktreesDir  string                      `json:"worktrees_dir"`
					Repos         map[string]string           `json:"repos"`
					History       workspace.HistoryInspection `json:"history"`
				}{l.SchemaVersion, l.ConfigPath, l.ConfigRoot, l.WorkflowRoot, l.WorktreesDir, l.Repos, history})
			}
			cmd.Printf("Config: %s\nWorkflow: %s\nGit mode: %s\nInitialized: %t\nPending checkpoint: %t\n", l.ConfigPath, l.WorkflowRoot, l.GitMode, history.Initialized, history.Pending)
			return nil
		},
	}
	inspect.Flags().BoolVar(&jsonMode, "json", false, "emit a read-only JSON inspection")
	init := &cobra.Command{
		Use: "init", Short: "Initialize an empty managed records repository", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !yes {
				return fmt.Errorf("workspace init: pass --yes to explicitly initialize workflow history")
			}
			l, err := load(cmd)
			if err != nil {
				return err
			}
			return workspace.InitManaged(l)
		},
	}
	init.Flags().BoolVar(&yes, "yes", false, "confirm managed repository initialization")
	var paths []string
	var message string
	checkpoint := &cobra.Command{
		Use: "checkpoint", Short: "Commit edits only in changes, tasks, specs, adr, guides, and .workflow", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := load(cmd)
			if err != nil {
				return err
			}
			return workspace.Checkpoint(l, paths, message)
		},
	}
	checkpoint.Flags().StringVar(&message, "message", "", "checkpoint commit message (required)")
	checkpoint.Flags().StringArrayVar(&paths, "path", nil, "workflow-relative owned file or subtree (repeatable; defaults to all owned records)")
	_ = checkpoint.MarkFlagRequired("message")
	recover := &cobra.Command{
		Use: "recover", Short: "Retry an exact pending workflow checkpoint", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			l, err := load(cmd)
			if err != nil {
				return err
			}
			return workspace.RecoverHistory(l)
		},
	}
	cmd.AddCommand(inspect, init, checkpoint, recover, workspaceMigrateCmd())
	return cmd
}
