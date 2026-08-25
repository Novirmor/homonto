// Package cli is Homonto's command surface. It parses flags, renders
// output, and calls into internal/app; every decision behind a command
// lives in an engine, so a command can be read as "what does the user
// type" without also being "what does the workflow do".
package cli

import (
	"errors"
	"fmt"
	"os"
)

// Exit codes. They are a small, stable taxonomy so a script can branch
// without parsing prose.
const (
	// ExitOK: the command did what it was asked.
	ExitOK = 0
	// ExitError: the command failed.
	ExitError = 1
	// ExitRefused: a guard refused a presented operation. It is distinct
	// from a failure because a refusal is the guard working, and a host
	// hook needs to tell the two apart.
	ExitRefused = 2
	// ExitUnhealthy: doctor found something that needs attention.
	ExitUnhealthy = 3
)

// exitCode is the taxonomy code a command set. Homonto runs one command
// per process, so a package-level sink is safe.
var exitCode int

func setExitCode(c int) { exitCode = c }

// Execute runs the root command and returns the process exit code.
func Execute(args []string) int {
	exitCode = ExitOK
	root := NewRootCmd()
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		var refused errRefused
		if errors.As(err, &refused) {
			// The decision itself has already been written to stdout for
			// the hook to read; stderr carries the human-readable half.
			fmt.Fprintln(os.Stderr, "refused:", err)
			return ExitRefused
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return ExitError
	}
	return exitCode
}
