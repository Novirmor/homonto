package cli

import (
	"strings"
	"testing"
)

// TestExecuteReportsTheTaxonomy pins the exit codes a script branches on.
func TestExecuteReportsTheTaxonomy(t *testing.T) {
	// A successful command exits zero.
	if got := Execute([]string{"version"}); got != ExitOK {
		t.Errorf("version exited %d, want %d", got, ExitOK)
	}
	// An unknown command is an error, not a refusal.
	if got := Execute([]string{"nonsense-command"}); got != ExitError {
		t.Errorf("an unknown command exited %d, want %d", got, ExitError)
	}
	// The exit code is reset per run: a previous unhealthy result must not
	// leak into the next command.
	setExitCode(ExitUnhealthy)
	if got := Execute([]string{"version"}); got != ExitOK {
		t.Errorf("a stale exit code leaked into the next run: %d", got)
	}
}

// TestRefusalIsDistinctFromFailure proves a host hook can tell the guard
// working from the guard breaking.
func TestRefusalIsDistinctFromFailure(t *testing.T) {
	if ExitRefused == ExitError {
		t.Fatal("a refusal and a failure share an exit code")
	}
	err := errRefused{}
	if !strings.Contains(err.Error(), ":") {
		t.Fatalf("a refusal does not explain itself: %q", err.Error())
	}
}
