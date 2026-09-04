package tocli

import (
	"fmt"
	"os"

	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/spf13/cobra"
)

// initCmd builds "to init": it enforces toFramework.Gate(dir) before scaffolding
// docs/tasks/ and docs/tasks/archive/, and performs no writes if the gate
// fails. Existing directories are left untouched.
func initCmd() *cobra.Command {
	var (
		dir      string
		jsonMode bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold the to tasks layout, if the to framework is installed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd, dir, jsonMode)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to initialize")
	cmd.Flags().BoolVar(&jsonMode, "json", false, "emit the result as JSON")
	return cmd
}

func runInit(cmd *cobra.Command, root string, jsonMode bool) error {
	if err := toFramework.Gate(root); err != nil {
		return err
	}

	for _, path := range []string{tasksDir(root), archiveDir(root)} {
		if err := workcli.ValidateWorkflowPath(root, path); err != nil {
			return fmt.Errorf("to init: unsafe workflow path: %w", err)
		}
	}
	// Record the root before the first scaffold write so a crash cannot leave
	// custom-root state that a later config edit cannot attribute.
	if err := workcli.MarkWorkflowState(root); err != nil {
		return fmt.Errorf("to init: recording workflow root: %w", err)
	}

	created, existed := []string{}, []string{}
	for _, path := range []string{tasksDir(root), archiveDir(root)} {
		_, statErr := os.Stat(path)
		preExisted := statErr == nil

		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("to init: creating %s: %w", path, err)
		}
		if preExisted {
			existed = append(existed, path)
		} else {
			created = append(created, path)
		}
	}

	if jsonMode {
		return printJSON(cmd, map[string][]string{"created": created, "exists": existed})
	}
	for _, p := range existed {
		cmd.Printf("exists %s\n", p)
	}
	for _, p := range created {
		cmd.Printf("created %s\n", p)
	}
	// Multi-repo context (ADR 0024 stage 1): the designated workflow tree —
	// docs/tasks/ — lives in the config repo; declared repos are reported so
	// a multi-repo setup reads as one, with the scope stated plainly until
	// cross-repo changes ship.
	for _, line := range workcli.RepoContextLines(root) {
		cmd.Println(line)
	}
	return nil
}
