package cli

import (
	"strings"

	"github.com/noviopenworks/homonto/internal/initws"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
	"github.com/spf13/cobra"
)

// initCmd creates a workspace.
func initCmd() *cobra.Command {
	var (
		workflow string
		members  []string
		discover bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a Homonto workspace here",
		Long: "Turn this directory into a Homonto workspace: a control repository " +
			"that holds the records, a manifest, and the document tree. Members " +
			"are the repositories the work happens in, and you name them: a scan " +
			"proposes, it never decides.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := cmd.Flags().GetString("workspace")
			if err != nil {
				return err
			}
			if discover {
				found, err := initws.Discover(cmd.Context(), root, members, nil)
				if err != nil {
					return err
				}
				if len(found.Candidates) == 0 {
					cmd.Println("no member candidates found")
					return nil
				}
				cmd.Printf("candidates under %s (confirm with --member):\n", found.Root)
				for _, c := range found.Candidates {
					if c.Manifest != "" {
						cmd.Printf("  %-8s %s (%s)\n", c.Kind, c.Path, c.Manifest)
						continue
					}
					cmd.Printf("  %-8s %s\n", c.Kind, c.Path)
				}
				return nil
			}
			cfg, err := initws.Init(cmd.Context(), initws.InitInput{
				Root: root, Workflow: workspacecfg.Workflow(workflow), Members: members,
			})
			if err != nil {
				return err
			}
			cmd.Printf("created a %s workspace with %d member(s)\n",
				cfg.Workspace.Workflow, len(cfg.Members))
			cmd.Printf("next: `homonto host install`, then `homonto %s start <name>`\n",
				cfg.Workspace.Workflow)
			return nil
		},
	}
	cmd.Flags().StringVar(&workflow, "workflow", string(workspacecfg.WorkflowTask),
		"which workflow this workspace runs (task or change)")
	cmd.Flags().StringSliceVar(&members, "member", nil, "a repository to include as a member")
	cmd.Flags().BoolVar(&discover, "discover", false, "propose members and write nothing")
	return cmd
}

// statusCmd shows the workspace at a glance.
func statusCmd(opener Opener) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the workspace, its work, and its integrations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			works, err := a.Works(cmd.Context())
			if err != nil {
				return err
			}
			hosts, err := a.Hosts(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd, map[string]any{
					"root":     a.Root(),
					"workflow": a.Config().Workspace.Workflow,
					"works":    works,
					"hosts":    hosts,
				})
			}
			cmd.Printf("%s (%s workflow, %d member(s))\n",
				a.Root(), a.Config().Workspace.Workflow, len(a.Config().Members))
			if len(works) == 0 {
				cmd.Println("no work recorded")
			}
			for _, w := range works {
				cmd.Printf("  %-9s %-24s %s\n", w.Kind, w.Name, w.Step)
			}
			for _, h := range hosts {
				line := []string{}
				if h.Installed > 0 {
					line = append(line, itoa(h.Installed)+" installed")
				}
				if h.Modified > 0 {
					line = append(line, itoa(h.Modified)+" edited by hand")
				}
				if h.Missing > 0 {
					line = append(line, itoa(h.Missing)+" missing")
				}
				cmd.Printf("  host %-9s %s\n", h.Tool, strings.Join(line, ", "))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine-readable form")
	return cmd
}

// doctorCmd reports what is wrong.
func doctorCmd(opener Opener) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check the workspace and report what needs attention",
		Long: "Check the workspace: its members, its host integrations, its active " +
			"work, and whether a self-update was interrupted. Doctor reports and " +
			"never repairs — everything it finds is something you might have done " +
			"deliberately.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			report, err := a.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				if err := writeJSON(cmd, report); err != nil {
					return err
				}
			} else {
				if len(report.Diagnostics) == 0 {
					cmd.Println("nothing to report")
				}
				for _, d := range report.Diagnostics {
					cmd.Printf("%-8s %s\n", d.Severity, d.Summary)
					if d.Remedy != "" {
						cmd.Printf("         → %s\n", d.Remedy)
					}
				}
			}
			if !report.Healthy() {
				setExitCode(3)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the machine-readable form")
	return cmd
}

// itoa renders a small count.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
