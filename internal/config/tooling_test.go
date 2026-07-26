package config

import (
	"strings"
	"testing"
)

// A config with no [tooling] table resolves both providers to "none", so an
// existing config keeps loading and names no third-party tool.
func TestTooling_AbsentTableDefaultsToNone(t *testing.T) {
	c, err := Load(writeConfig(t, "[mcps.demo]\ncommand = [\"true\"]\n"))
	if err != nil {
		t.Fatalf("absent [tooling] should load: %v", err)
	}
	got := c.ResolvedTooling()
	if got.ShellProxy != ToolingNone || got.CodeIntel != ToolingNone {
		t.Errorf("ResolvedTooling() = %+v, want both %q", got, ToolingNone)
	}
}

// An omitted key defaults independently of the one that is present.
func TestTooling_PartialTableDefaultsOmittedKey(t *testing.T) {
	c, err := Load(writeConfig(t, "[tooling]\nshell_proxy = \"rtk\"\n"))
	if err != nil {
		t.Fatalf("partial [tooling] should load: %v", err)
	}
	got := c.ResolvedTooling()
	if got.ShellProxy != "rtk" {
		t.Errorf("ShellProxy = %q, want %q", got.ShellProxy, "rtk")
	}
	if got.CodeIntel != ToolingNone {
		t.Errorf("CodeIntel = %q, want %q", got.CodeIntel, ToolingNone)
	}
}

// Every accepted value in both closed sets loads.
func TestTooling_AcceptsEveryDeclaredProvider(t *testing.T) {
	for _, tc := range []struct{ shell, code string }{
		{"rtk", "graphify"},
		{"rtk", "okf"},
		{"rtk", "none"},
		{"none", "graphify"},
		{"none", "okf"},
		{"none", "none"},
	} {
		body := "[tooling]\nshell_proxy = \"" + tc.shell + "\"\ncode_intel = \"" + tc.code + "\"\n"
		c, err := Load(writeConfig(t, body))
		if err != nil {
			t.Fatalf("shell_proxy=%q code_intel=%q should load: %v", tc.shell, tc.code, err)
		}
		got := c.ResolvedTooling()
		if got.ShellProxy != tc.shell || got.CodeIntel != tc.code {
			t.Errorf("ResolvedTooling() = %+v, want {%q %q}", got, tc.shell, tc.code)
		}
	}
}

// An explicit "none" is indistinguishable from an absent key downstream.
func TestTooling_ExplicitNoneMatchesAbsent(t *testing.T) {
	explicit, err := Load(writeConfig(t, "[tooling]\nshell_proxy = \"none\"\ncode_intel = \"none\"\n"))
	if err != nil {
		t.Fatalf("explicit none should load: %v", err)
	}
	absent, err := Load(writeConfig(t, "[mcps.demo]\ncommand = [\"true\"]\n"))
	if err != nil {
		t.Fatalf("absent should load: %v", err)
	}
	if explicit.ResolvedTooling() != absent.ResolvedTooling() {
		t.Errorf("explicit none %+v != absent %+v", explicit.ResolvedTooling(), absent.ResolvedTooling())
	}
}

// An unknown provider name fails at load, naming the key and the accepted set.
func TestTooling_RejectsUnknownProviderValue(t *testing.T) {
	_, err := Load(writeConfig(t, "[tooling]\ncode_intel = \"ctags\"\n"))
	if err == nil {
		t.Fatal("unknown code_intel provider should fail to load")
	}
	for _, want := range []string{"tooling.code_intel", "ctags", "graphify", "okf", "none"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func TestTooling_RejectsUnknownShellProxyValue(t *testing.T) {
	_, err := Load(writeConfig(t, "[tooling]\nshell_proxy = \"zoxide\"\n"))
	if err == nil {
		t.Fatal("unknown shell_proxy provider should fail to load")
	}
	if !strings.Contains(err.Error(), "tooling.shell_proxy") {
		t.Errorf("error = %q, want it to name tooling.shell_proxy", err)
	}
}

// go-toml drops keys a struct does not declare, so [tooling] is captured as a
// raw table to make an unknown key survive to validation and be named.
func TestTooling_RejectsUnknownKey(t *testing.T) {
	_, err := Load(writeConfig(t, "[tooling]\nshell_proxy = \"rtk\"\nindexer = \"ctags\"\n"))
	if err == nil {
		t.Fatal("unknown key inside [tooling] should fail to load")
	}
	if !strings.Contains(err.Error(), "indexer") {
		t.Errorf("error = %q, want it to name the unknown key %q", err, "indexer")
	}
}
