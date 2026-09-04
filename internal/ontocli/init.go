package ontocli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/spf13/cobra"
)

// ontoFramework parameterizes the shared workcli helpers for the onto binary.
// The gate prefix is "onto init" (the command the gate was first written for)
// and the change-name prefix is "onto new"; both are preserved verbatim so the
// refactor changes no observable diagnostic.
var ontoFramework = workcli.Framework{
	Name:          "onto",
	SkillsDir:     "skills/onto",
	GatePrefix:    "onto init",
	NamePrefix:    "onto new",
	ReservedNames: nil,
}

// docsLayout preserves the historic default labels used by diagnostics and
// tests. workflowLayout carries the same layout relative to the configured
// root.
var docsLayout = []string{
	filepath.Join("docs", "changes"),
	filepath.Join("docs", "specs"),
	filepath.Join("docs", "adr"),
	filepath.Join("docs", "guides"),
}

var workflowLayout = []string{"changes", "specs", "adr", "guides"}

// initCmd builds the "onto init" subcommand: it enforces ontoFramework.Gate(dir)
// (the framework-install precondition) before scaffolding the docs/ layout, and
// performs no writes at all if the gate fails.
func initCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold the onto docs layout, if the onto framework is installed",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInit(cmd, dir)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root to initialize")
	return cmd
}

// runInit enforces ontoFramework.Gate(root) and, only on success, idempotently
// scaffolds the docs/ layout: each directory is created with os.MkdirAll if
// missing, and left untouched (never overwritten) if it already exists. It
// reports one line per directory describing the outcome.
func runInit(cmd *cobra.Command, root string) error {
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}

	paths := make([]string, 0, len(workflowLayout))
	for _, d := range workflowLayout {
		path := filepath.Join(workflowRoot(root), d)
		if err := workcli.ValidateWorkflowPath(root, path); err != nil {
			return fmt.Errorf("onto init: unsafe workflow path: %w", err)
		}
		paths = append(paths, path)
	}
	// Record the root before the first scaffold write so a crash cannot leave
	// custom-root state that a later config edit cannot attribute.
	if err := workcli.MarkWorkflowState(root); err != nil {
		return fmt.Errorf("onto init: recording workflow root: %w", err)
	}

	for _, path := range paths {

		_, statErr := os.Stat(path)
		preExisted := statErr == nil

		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("onto init: creating %s: %w", path, err)
		}

		if preExisted {
			cmd.Printf("exists %s\n", path)
		} else {
			cmd.Printf("created %s\n", path)
		}
	}
	// Multi-repo context (ADR 0024 stage 1): the designated workflow tree —
	// these docs/ directories — lives in the config repo; declared repos are
	// reported so a multi-repo setup reads as one, with the scope stated
	// plainly until cross-repo changes ship.
	for _, line := range workcli.RepoContextLines(root) {
		cmd.Println(line)
	}

	return nil
}
