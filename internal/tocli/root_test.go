package tocli

import (
	"strings"
	"testing"
)

// TestNewRootCmd_RegistersAllSubcommands verifies every shipped subcommand is
// wired (a regression here would silently drop a command from the binary).
func TestNewRootCmd_RegistersAllSubcommands(t *testing.T) {
	root := NewRootCmd()
	want := map[string]bool{
		"version": true,
		"init":    true,
		"new":     true,
		"status":  true,
		"phase":   true,
		"bypass":  true,
		"done":    true,
		"abandon": true,
		"handoff": true,
		"doctor":  true,
	}
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("root command missing %q; registered: %v", name, keys(got))
		}
	}
}

// TestNewRootCmd_VersionPrintsBinaryName verifies `to version` outputs the
// documented "to <version>" form.
func TestNewRootCmd_VersionPrintsBinaryName(t *testing.T) {
	out := run(t, false, "version")
	if !strings.HasPrefix(out, "to ") {
		t.Errorf("version output %q does not start with 'to '", out)
	}
}

// TestNewRootCmd_UnknownCommandErrors verifies an unknown command surfaces an
// error rather than silently exiting 0.
func TestNewRootCmd_UnknownCommandErrors(t *testing.T) {
	if err := runJSON(t, "totally-made-up"); err == nil {
		t.Errorf("unknown command returned nil, want an error")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
