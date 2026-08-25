package cli

import (
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/app"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/spf13/cobra"
)

// handoffCmd makes the active work portable.
func handoffCmd(opener Opener) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "handoff [name-or-id]",
		Short: "Make the active work portable so another machine can pick it up",
		Long: "Mark the active work's checkpoint transferable, commit it to the " +
			"control repository, and release this machine's leases. Nothing " +
			"local is destroyed: the work simply stops being anchored here, so " +
			"a clone of the control repository can attach it.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			id, err := a.ResolveWork(cmd.Context(), selector)
			if err != nil {
				return err
			}
			if err := a.PreparePortable(cmd.Context(), id); err != nil {
				return err
			}
			cmd.Printf("handed off %s; the checkpoint is committed and transferable\n", id)
			cmd.Println("clone or pull the control repository elsewhere and run `homonto attach`")
			return nil
		},
	}
	return cmd
}

// attachCmd picks up a transferable checkpoint on this machine.
func attachCmd(opener Opener) *cobra.Command {
	var (
		mappings []string
		force    bool
		propose  bool
	)
	cmd := &cobra.Command{
		Use:   "attach",
		Short: "Pick up work handed off from another machine",
		Long: "Consume a transferable checkpoint in this cloned control repository: " +
			"claim every member's registration, take the leases, and rebuild the " +
			"local runtime from the portable record. Member locations are " +
			"CONFIRMED, not guessed — run with --propose to see the proposals.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			if propose {
				proposals, err := a.ProposeAttachMappings(cmd.Context())
				if err != nil {
					return err
				}
				if len(proposals) == 0 {
					cmd.Println("no members to map")
					return nil
				}
				for _, p := range proposals {
					cmd.Printf("  %-10s %s\n", p.Status, p.RepositoryID)
					for _, candidate := range p.Candidates {
						cmd.Printf("             %s\n", candidate)
					}
					for _, reason := range p.Reasons {
						cmd.Printf("             (%s)\n", reason)
					}
				}
				cmd.Println("\nconfirm each with --member <repository-id>=<path>")
				return nil
			}
			confirmed, err := parseMappings(mappings)
			if err != nil {
				return err
			}
			if err := a.Attach(cmd.Context(), confirmed, force); err != nil {
				return err
			}
			cmd.Println("attached; the work is now anchored on this machine")
			if force {
				cmd.Println("this was a forced takeover: every recorded fact is marked stale " +
					"and must be re-verified before anything advances")
			}
			return nil
		},
	}
	cmd.Flags().StringSliceVar(&mappings, "member", nil,
		"confirm a member's location here, as <repository-id>=<path>")
	cmd.Flags().BoolVar(&force, "force", false,
		"take over a checkpoint another machine already consumed")
	cmd.Flags().BoolVar(&propose, "propose", false, "show the proposed member locations and stop")
	return cmd
}

// parseMappings reads the --member flags.
func parseMappings(raw []string) ([]app.AttachMapping, error) {
	out := make([]app.AttachMapping, 0, len(raw))
	for _, entry := range raw {
		id, path, found := splitOnce(entry, '=')
		if !found {
			return nil, fmt.Errorf("cli: --member %q must be <repository-id>=<path>", entry)
		}
		if err := identity.ValidateUUID(id); err != nil {
			return nil, fmt.Errorf("cli: --member %q: %w", entry, err)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("cli: --member %q: %w", entry, err)
		}
		out = append(out, app.AttachMapping{
			RepositoryID: identity.RepositoryID(id), Path: abs,
		})
	}
	return out, nil
}

// splitOnce splits on the first occurrence of sep.
func splitOnce(s string, sep byte) (string, string, bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
