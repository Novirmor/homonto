package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Novirmor/homonto/internal/buildinfo"
	"github.com/Novirmor/homonto/internal/hostruntime"
	"github.com/Novirmor/homonto/internal/workflowstatus"
	"github.com/spf13/cobra"
)

// runtimeCmd serves the workflow runtime core (ADR 0090, M4.1). Harnesses
// start these commands from projected configuration; they are not meant to be
// run by hand.
func runtimeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runtime",
		Short: "Workflow runtime services for harness integrations (internal)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Serve the runtime core to a plugin shim over stdio JSON-RPC (started by the OpenCode workflow plugin)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return hostruntime.NewServer(cmd.InOrStdin(), cmd.OutOrStdout(), buildinfo.Resolve(Version, buildinfo.DevVersion)).Serve()
		},
	})
	cmd.AddCommand(runtimeMCPCmd(), runtimeHookCmd())
	return cmd
}

// claudeSessionEnv is where Claude Code gives its MCP servers the session id
// its hooks report (verified on Claude Code 2.1.318).
const claudeSessionEnv = "CLAUDE_CODE_SESSION_ID"

func runtimeMCPCmd() *cobra.Command {
	var configPath, harness string
	var hosts []string
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the runtime core directly to a harness as an MCP stdio server (started by Claude Code)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if harness != "claude" {
				return errors.New("runtime mcp: --harness must be \"claude\"")
			}
			if !filepath.IsAbs(configPath) {
				return errors.New("runtime mcp: --config must be the absolute selected config path")
			}
			session := os.Getenv(claudeSessionEnv)
			srv, err := hostruntime.NewDirectServer(cmd.InOrStdin(), cmd.OutOrStdout(), hostruntime.DirectOptions{
				ConfigPath: configPath, Hosts: hosts, SessionID: session, Dialect: hostruntime.DialectClaude,
				Version: buildinfo.Resolve(Version, buildinfo.DevVersion)})
			if err != nil {
				return err
			}
			l, err := hostruntime.Listen(configPath, session)
			if err != nil {
				return err
			}
			return srv.Serve(l)
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "absolute path of the selected homonto config")
	c.Flags().StringVar(&harness, "harness", "claude", "harness that launched the server")
	c.Flags().StringArrayVar(&hosts, "host", nil, "hosting provider to serve (github, gitlab, bitbucket); repeatable")
	return c
}

func runtimeHookCmd() *cobra.Command {
	var configPath string
	c := &cobra.Command{
		Use:   "hook",
		Short: "Handle one Claude Code hook event (started by Claude Code hooks)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return hostruntime.RunClaudeHook(cmd.InOrStdin(), cmd.OutOrStdout(), configPath, hostruntime.HookDeps{
				Snapshot: func(path string) (string, error) {
					b, err := json.Marshal(workflowstatus.ReadConfig(path))
					return string(b), err
				},
				Relay: hostruntime.RelaySocket,
			})
		},
	}
	c.Flags().StringVar(&configPath, "config", "", "absolute path of the selected homonto config")
	return c
}
