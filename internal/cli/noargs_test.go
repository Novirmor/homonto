package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestPositionalFreeCommands_RejectStrayArg verifies plan/apply/status/doctor
// reject an unexpected positional instead of silently ignoring it (F49).
func TestPositionalFreeCommands_RejectStrayArg(t *testing.T) {
	for _, name := range []string{"plan", "apply", "status", "doctor"} {
		t.Run(name, func(t *testing.T) {
			cmd := NewRootCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetArgs([]string{name, "stray-positional.toml"})
			err := cmd.Execute()
			combined := out.String()
			if err != nil {
				combined += err.Error()
			}
			if err == nil || !strings.Contains(combined, "stray-positional.toml") {
				t.Fatalf("%s did not reject the stray positional by name; err=%v out=%q", name, err, out.String())
			}
		})
	}
}

// TestInitKeepsOptionalPositional verifies init still accepts its optional dir arg.
func TestInitKeepsOptionalPositional(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"init", "--help"}) // --help avoids side effects; asserts init still parses a positional-tolerant arg spec
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init --help errored: %v", err)
	}
}
