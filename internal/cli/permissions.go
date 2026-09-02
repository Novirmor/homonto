package cli

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// permissionsCmd groups the permission-suggestion surface (ADR 0029). The
// OpenCode permission-observer plugin (shipped as catalog content) keeps
// approved commands in memory and sends a candidate once per session; this
// command validates and renders the suggestion. Nothing is ever written or
// recorded here — the user pastes the snippet into homonto.toml.
func permissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "permissions",
		Short: "Render reviewed Bash allowlist suggestions (writes nothing)",
	}
	cmd.AddCommand(permissionsSuggestCmd())
	return cmd
}

func permissionsSuggestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "suggest",
		Short: "Render a bash_allow_add snippet for approved commands (stdin)",
		Long: "Reads one exact command per line from stdin, validates each against " +
			"the additive-allow rules (no patterns, no shell composition, no " +
			"credentials, no destructive or privilege-escalating commands), and " +
			"prints the [subagents.<name>.opencode] bash_allow_add TOML snippet. " +
			"Nothing is written; the user reviews and pastes.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmds, err := readCommandLines(cmd.InOrStdin())
			if err != nil {
				return err
			}
			if len(cmds) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no commands given (stdin was empty)")
				return nil
			}
			seen := map[string]bool{}
			var valid []string
			for _, c := range cmds {
				if seen[c] {
					continue
				}
				seen[c] = true
				if err := suggestToken(c); err != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "# rejected: %s\n", err)
					continue
				}
				valid = append(valid, c)
			}
			if len(valid) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "# nothing to suggest")
				return nil
			}
			sort.Strings(valid)
			fmt.Fprintln(cmd.OutOrStdout(), "# reviewed suggestions — paste under the agent's [subagents.<name>.opencode] block")
			fmt.Fprintln(cmd.OutOrStdout(), "bash_allow_add = [")
			for _, c := range valid {
				fmt.Fprintf(cmd.OutOrStdout(), "  %q,\n", c)
			}
			fmt.Fprintln(cmd.OutOrStdout(), "]")
			return nil
		},
	}
	return cmd
}

// readCommandLines reads one command per line; blank lines are skipped.
func readCommandLines(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("permissions suggest: reading stdin: %w", err)
	}
	return out, nil
}

// suggestToken is the shared validation for a suggestion candidate — the same
// rules config load applies to bash_allow_add entries (ADR 0029): exact
// commands only, no shell composition, no credential-like or destructive
// content.
func suggestToken(cmdLine string) error {
	if cmdLine == "" {
		return fmt.Errorf("empty command")
	}
	if strings.ContainsAny(cmdLine, "*?[]{}") {
		return fmt.Errorf("%q must be exact; wildcards are a base-allowlist decision", cmdLine)
	}
	for _, bad := range []string{"|", "&&", "||", ";", ">", "<", "$(", "`", "\\\n"} {
		if strings.Contains(cmdLine, bad) {
			return fmt.Errorf("%q carries shell composition", cmdLine)
		}
	}
	if strings.ContainsAny(cmdLine, "=") || strings.Contains(cmdLine, "PASS") || strings.Contains(cmdLine, "TOKEN") || strings.Contains(cmdLine, "KEY") || strings.Contains(cmdLine, "SECRET") {
		return fmt.Errorf("%q looks like an environment assignment or credential name", cmdLine)
	}
	for _, bad := range []string{"rm ", "sudo ", "su ", "mkfs", "dd ", "chmod 777", "curl "} {
		if strings.HasPrefix(cmdLine, bad) {
			return fmt.Errorf("%q is destructive, privilege-escalating, or network-fetching", cmdLine)
		}
	}
	return nil
}
