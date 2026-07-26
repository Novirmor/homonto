package config

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// ToolingNone is the resolved value for a provider slot the user did not
// declare. An absent [tooling] table, an omitted key, and an explicit "none"
// all resolve to it, so the default path and the opt-out path run identical
// code downstream.
const ToolingNone = "none"

// Tooling is the resolved provider pair behind a change's tooling preflight.
// Both fields are always a concrete provider name (never empty) once resolved
// through Config.ResolvedTooling.
type Tooling struct {
	// ShellProxy names the shell/token proxy every workflow shell operation
	// goes through, or ToolingNone.
	ShellProxy string
	// CodeIntel names the code-intelligence provider the open and design
	// phases ground their codebase claims in, or ToolingNone.
	CodeIntel string
}

// toolingTable is the raw [tooling] table. It is decoded as a map rather than
// a struct because go-toml silently drops keys a struct does not declare (see
// decode's note in load.go): capturing the table raw is what lets validation
// reject an unknown key by name instead of ignoring a user's typo.
type toolingTable map[string]string

const (
	toolingKeyShellProxy = "shell_proxy"
	toolingKeyCodeIntel  = "code_intel"
)

// The two closed provider sets. Adding a provider is a deliberate change here
// plus a matching catalog/tooling/<provider>.md fragment — there is no plugin
// API, by design.
var (
	toolingShellProxyValues = []string{"rtk", ToolingNone}
	toolingCodeIntelValues  = []string{"graphify", "okf", ToolingNone}
)

// ResolvedTooling returns the declared provider pair with every omitted or
// blank slot defaulted to ToolingNone.
func (c *Config) ResolvedTooling() Tooling {
	t := Tooling{ShellProxy: ToolingNone, CodeIntel: ToolingNone}
	if v := strings.TrimSpace(c.Tooling[toolingKeyShellProxy]); v != "" {
		t.ShellProxy = v
	}
	if v := strings.TrimSpace(c.Tooling[toolingKeyCodeIntel]); v != "" {
		t.CodeIntel = v
	}
	return t
}

// validateTooling rejects an unknown key or an unknown provider name, naming
// the offender and the accepted set — the v0.8.0 precedent of failing loudly
// at the offending table rather than anonymously later.
func validateTooling(tbl toolingTable) error {
	keys := make([]string, 0, len(tbl))
	for k := range tbl {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic error for a config with several mistakes
	for _, k := range keys {
		if k != toolingKeyShellProxy && k != toolingKeyCodeIntel {
			return fmt.Errorf("tooling: unknown key %q (accepted: %s, %s)", k, toolingKeyCodeIntel, toolingKeyShellProxy)
		}
	}
	if err := validateToolingValue(toolingKeyShellProxy, tbl[toolingKeyShellProxy], toolingShellProxyValues); err != nil {
		return err
	}
	return validateToolingValue(toolingKeyCodeIntel, tbl[toolingKeyCodeIntel], toolingCodeIntelValues)
}

func validateToolingValue(key, raw string, accepted []string) error {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil // omitted: defaults to none
	}
	if slices.Contains(accepted, v) {
		return nil
	}
	return fmt.Errorf("tooling.%s %q is not a known provider (accepted: %s)", key, v, strings.Join(accepted, ", "))
}
