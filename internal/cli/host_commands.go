package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/noviopenworks/homonto/internal/app"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/host"
	"github.com/noviopenworks/homonto/internal/host/claude"
	"github.com/noviopenworks/homonto/internal/host/opencode"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/spf13/cobra"
)

// Environment variables a host passes back with a guarded operation.
//
// The ids and tokens are handed to the host when the assignment or the
// grant is ISSUED, and the host's hook returns them here. The environment
// is the transport because that is what a host already threads through to
// a subagent's process; nothing about the mechanism is secret, and a token
// that leaks is refused by the freshness check the moment its action is
// answered or invalidated.
const (
	envActionID   = "HOMONTO_ACTION_ID"
	envActionTok  = "HOMONTO_ACTION_TOKEN"
	envGrantID    = "HOMONTO_GRANT_ID"
	envGrantToken = "HOMONTO_GRANT_TOKEN"
)

// hostCmd groups the commands a host's generated wrappers invoke. They are
// separate from the protocol commands because a wrapper speaks a host's
// event shapes, and the protocol commands speak Homonto's.
func hostCmd(opener Opener) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Commands the generated host integrations invoke",
	}
	cmd.AddCommand(hostProbeCmd(), hostGuardCmd(opener), hostInstallCmd(opener))
	return cmd
}

// hostProbeCmd takes no opener: the probe resolves and opens the
// workspace itself, because it must distinguish "not a workspace" from
// "a workspace that has never run anything" and answer both rather than
// failing, and an opener that already opened something cannot tell them
// apart.
func hostProbeCmd() *cobra.Command {
	var hostName string
	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Report whether there is work to resume",
		Long: "Answer the read-only resume probe. It performs no write, no " +
			"migration, and no network access: a host runs it at the start of " +
			"every session, and starting a session must change nothing. A " +
			"directory that is not a Homonto workspace is answered, not refused.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := parseHost(hostName)
			if err != nil {
				return err
			}
			root, err := cmd.Flags().GetString("workspace")
			if err != nil {
				return fmt.Errorf("cli: read --workspace: %w", err)
			}
			// The probe never fails: a host runs it in every directory a
			// user opens, and "this is not a Homonto project" is an answer
			// rather than an error.
			return emitProbe(cmd, target, app.ProbeAt(cmd.Context(), root, target))
		},
	}
	cmd.Flags().StringVar(&hostName, "host", "", "the host tool asking (claude or opencode)")
	cmd.Flags().Bool("json", true, "emit the protocol payload (always on)")
	return cmd
}

// emitProbe writes the probe answer in the shape the asking host reads.
func emitProbe(cmd *cobra.Command, target protocol.Host, resp protocol.ProbeResponse) error {
	if target == protocol.HostClaude {
		body, err := claude.RenderProbe(resp)
		if err != nil {
			return err
		}
		if len(body) > 0 {
			cmd.OutOrStdout().Write(append(body, '\n'))
		}
		return nil
	}
	encoded, err := protocol.EncodeProbeResponse(resp)
	if err != nil {
		return err
	}
	cmd.OutOrStdout().Write(append(encoded, '\n'))
	return nil
}

func hostGuardCmd(opener Opener) *cobra.Command {
	var hostName string
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Decide a host's presented write",
		Long: "Read a host's own hook event from stdin, normalize it, and answer " +
			"it in that host's response shape. This is a process gate for a " +
			"cooperating host, not a sandbox: a shell command can walk past it, " +
			"which is why Homonto validates the resulting diff independently " +
			"before accepting any report.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			target, err := parseHost(hostName)
			if err != nil {
				return err
			}
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()

			wire, err := normalizeEvent(target, cmd, a.Root())
			if err != nil {
				return err
			}
			decision, err := a.Authorize(cmd.Context(), guard.Request{
				Wire:       wire,
				ActionID:   identity.ActionID(os.Getenv(envActionID)),
				Token:      identity.Token(os.Getenv(envActionTok)),
				GrantID:    identity.ActionID(os.Getenv(envGrantID)),
				GrantToken: identity.Token(os.Getenv(envGrantToken)),
			})
			if err != nil {
				return err
			}
			if err := emitDecision(cmd, target, decision); err != nil {
				return err
			}
			if !decision.Allow {
				return errRefused{decision}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&hostName, "host", "", "the host tool asking (claude or opencode)")
	return cmd
}

// normalizeEvent reads the asking host's event shape.
func normalizeEvent(target protocol.Host, cmd *cobra.Command, root string) (protocol.GuardRequest, error) {
	if target == protocol.HostClaude {
		return claude.NormalizeEvent(cmd.InOrStdin(), root)
	}
	return opencode.NormalizeEvent(cmd.InOrStdin(), root)
}

// emitDecision writes the verdict in the asking host's response shape.
func emitDecision(cmd *cobra.Command, target protocol.Host, d protocol.GuardDecision) error {
	if target == protocol.HostClaude {
		body, err := claude.RenderDecision(d)
		if err != nil {
			return err
		}
		cmd.OutOrStdout().Write(append(body, '\n'))
		return nil
	}
	encoded, err := protocol.EncodeGuardDecision(d)
	if err != nil {
		return err
	}
	cmd.OutOrStdout().Write(append(encoded, '\n'))
	return nil
}

// parseHost resolves the --host flag.
func parseHost(name string) (protocol.Host, error) {
	switch protocol.Host(strings.ToLower(strings.TrimSpace(name))) {
	case protocol.HostClaude:
		return protocol.HostClaude, nil
	case protocol.HostOpenCode:
		return protocol.HostOpenCode, nil
	}
	return "", fmt.Errorf("cli: --host must be %q or %q, got %q",
		protocol.HostClaude, protocol.HostOpenCode, name)
}

func hostInstallCmd(opener Opener) *cobra.Command {
	var (
		tools  []string
		adopt  bool
		commit bool
		dryRun bool
		binary string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the thin host integrations",
		Long: "Install the command, skill, and hooks that let a host drive this " +
			"workspace's workflow. Generated files are project-local and " +
			"gitignored by default; --commit opts into committing them. A file " +
			"you have edited is refused rather than overwritten; --adopt " +
			"replaces it.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			plans, err := a.PlanHostInstall(cmd.Context(), app.HostInstallOptions{
				Tools: tools, Adopt: adopt, Commit: commit, Binary: binary,
			})
			if err != nil {
				return err
			}
			for _, plan := range plans {
				renderHostPlan(cmd, plan)
			}
			if dryRun {
				return nil
			}
			return a.ApplyHostInstall(cmd.Context(), plans)
		},
	}
	cmd.Flags().StringSliceVar(&tools, "tool", nil, "install for these tools (default: the ones detected)")
	cmd.Flags().BoolVar(&adopt, "adopt", false, "replace generated files that were edited by hand")
	cmd.Flags().BoolVar(&commit, "commit", false, "commit the generated files instead of ignoring them")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change and write nothing")
	cmd.Flags().StringVar(&binary, "binary", "", "how the wrappers invoke homonto (default: homonto)")
	return cmd
}

// renderHostPlan prints one tool's install plan.
func renderHostPlan(cmd *cobra.Command, plan host.Plan) {
	cmd.Printf("%s (%s)\n", plan.Target.Tool, plan.Target.Dir)
	for _, f := range plan.Files {
		if f.Reason == "" {
			cmd.Printf("  %-9s %s\n", f.Action, f.Path)
			continue
		}
		cmd.Printf("  %-9s %s — %s\n", f.Action, f.Path, f.Reason)
	}
	for _, entry := range plan.Ignore {
		cmd.Printf("  ignore    %s\n", entry)
	}
}
