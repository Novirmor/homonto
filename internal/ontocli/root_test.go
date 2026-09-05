package ontocli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewRootCmdUse(t *testing.T) {
	cmd := NewRootCmd()
	if cmd.Use != "onto" {
		t.Fatalf("Use = %q, want %q", cmd.Use, "onto")
	}
}

// TestNewRootCmd_RegistersAllSubcommands verifies every shipped subcommand is
// wired (a regression here would silently drop a command from the binary).
func TestNewRootCmd_RegistersAllSubcommands(t *testing.T) {
	root := NewRootCmd()
	want := map[string]bool{
		"version": true,
		"init":    true,
		"new":     true,
		"status":  true,
		"advance": true,
		"bypass":  true,
		"close":   true,
		"abandon": true,
		"demote":  true,
		"handoff": true,
		"doctor":  true,
		"set":     true,
		"state":   true,
		"gate":    true,
		"dirt":    true,
		"scale":   true,
		"graph":   true,
	}
	got := map[string]bool{}
	for _, c := range root.Commands() {
		got[c.Name()] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("root command missing %q; registered: %v", name, got)
		}
	}
}

func TestVersionCommand(t *testing.T) {
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "onto "+Version) {
		t.Fatalf("got %q, want it to contain %q", got, "onto "+Version)
	}
}
