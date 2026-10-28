package workflowcleanup

import (
	"encoding/json"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

// Command serves the same contract in all three binaries. An empty workflow
// enables homonto's cross-engine inventory and requires --workflow for selection.
func Command(workflow string) *cobra.Command {
	root := &cobra.Command{Use: "cleanup", Short: "Inspect and safely clean completed or explicitly abandoned workflows"}
	load := func(cmd *cobra.Command) (workspace.Layout, error) {
		if workflow == "" {
			path, err := cmd.Flags().GetString("config")
			if err != nil {
				return workspace.Layout{}, err
			}
			return workspace.Load(path)
		}
		dir, err := cmd.Flags().GetString("dir")
		if err != nil {
			return workspace.Layout{}, err
		}
		return workspace.LoadRoot(dir)
	}
	for _, action := range []string{"list", "preview", "apply"} {
		var selected, identity string
		var jsonMode, yes bool
		cmd := &cobra.Command{Use: action, Args: cobra.NoArgs}
		if action != "list" {
			cmd.Use += " <change>"
			cmd.Args = cobra.ExactArgs(1)
		}
		switch action {
		case "list":
			cmd.Short = "Inventory cleanup state and blockers without writing files"
		case "preview":
			cmd.Short = "Preview cleanup for one exact generation without writing files"
		case "apply":
			cmd.Short = "Clean one exact terminal generation; --yes never bypasses safety checks"
		}
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			l, err := load(cmd)
			if err != nil {
				return err
			}
			if action == "list" {
				inventory, err := List(l, selected)
				if err != nil {
					return err
				}
				if jsonMode {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(inventory)
				}
				for _, p := range inventory.Items {
					printPlan(cmd, p)
				}
				for _, f := range inventory.Findings {
					cmd.Printf("finding: %s/%s: %s\n", f.Workflow, f.Change, f.Message)
				}
				if len(inventory.Items) == 0 && len(inventory.Findings) == 0 {
					cmd.Println("No workflow records.")
				}
				return nil
			}
			var p Plan
			if action == "apply" {
				p, err = Apply(cmd.Context(), l, selected, args[0], identity, yes)
			} else {
				p, err = Preview(l, selected, args[0], identity)
			}
			if p.Identity != "" {
				if jsonMode {
					if outputErr := json.NewEncoder(cmd.OutOrStdout()).Encode(p); outputErr != nil {
						return outputErr
					}
				} else {
					printPlan(cmd, p)
				}
			}
			return err
		}
		cmd.Flags().BoolVar(&jsonMode, "json", false, "emit schema-versioned JSON")
		if workflow == "" {
			cmd.Flags().StringVar(&selected, "workflow", "", "workflow: onto or to (required for preview/apply)")
		} else {
			selected = workflow
			cmd.Flags().String("dir", ".", "workspace root containing homonto.toml")
		}
		if action != "list" {
			cmd.Flags().StringVar(&identity, "identity", "", "exact generation identity from cleanup list (required for apply)")
		}
		if action == "apply" {
			cmd.Flags().BoolVar(&yes, "yes", false, "confirm generation-scoped cleanup without bypassing safety checks")
		}
		root.AddCommand(cmd)
	}
	return root
}

func printPlan(cmd *cobra.Command, p Plan) {
	cmd.Printf("%s/%s\t%s\tidentity=%s\t%s\n", p.Workflow, p.Change, p.Status, p.Identity, p.Record)
	if p.Archive != "" {
		cmd.Printf("  archive: %s\n", p.Archive)
	}
	for _, w := range p.Worktrees {
		cmd.Printf("  remove worktree: %s (%s/%s); retain branch %s\n", filepath.Clean(w.Path), w.Repo, w.Role, w.Branch)
	}
	for _, blocker := range p.Blockers {
		cmd.Printf("  blocked: %s\n", blocker)
	}
	if p.Status == "cleanup-pending" {
		cmd.Printf("  apply: %s cleanup apply %s --identity %s --yes\n", p.Workflow, p.Change, p.Identity)
	}
}
