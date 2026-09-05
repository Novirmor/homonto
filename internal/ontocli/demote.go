package ontocli

import (
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/convert"
	"github.com/noviopenworks/homonto/internal/destlock"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/spf13/cobra"
)

// demoteCmd builds "onto demote <name> [--as <name>] --yes": convert an onto
// change into a `to` change (ADR 0042), the mirror of `to promote`. The
// complete source workspace moves into the neutral control plane
// (.workflow/snapshots/) of a fresh change; open/design restart at phase
// plan, build/verify continue at phase do when the tasks translate into a
// contract-clean `to` plan. Converting back with `to promote` restores the
// source byte-for-byte while nothing changed. Locks, in the fixed global
// order (to workspace, shared destination, onto workspace) held across the
// final rename — the onto lock excludes concurrent onto mutations of the
// source.
func demoteCmd() *cobra.Command {
	var (
		dir   string
		as    string
		force bool
	)
	toAlt := workcli.Framework{Name: "to", SkillsDir: "skills/to", GatePrefix: "to", NamePrefix: "to", ReservedNames: []string{"archive"}}
	cmd := &cobra.Command{
		Use:   "demote <change-name>",
		Short: "Demote an onto change into a `to` change (source preserved in .workflow/snapshots/)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := ontoFramework.GateAny(dir, toAlt); err != nil {
				return err
			}
			name := args[0]
			if err := ontoFramework.ValidChangeName(name); err != nil {
				return err
			}
			target := name
			if as != "" {
				// The target lives in to's namespace: to's rules govern it
				// (including the reserved "archive").
				if err := toAlt.ValidChangeName(as); err != nil {
					return fmt.Errorf("onto demote: --as: %w", err)
				}
				target = as
			}
			if !force {
				return fmt.Errorf("onto demote: pass --yes to demote (the source moves under <workflow-root>/tasks/%s/.workflow/snapshots/ and the change continues under to's no-gates workflow)", target)
			}
			if err := workcli.MarkWorkflowState(dir); err != nil {
				return fmt.Errorf("onto demote: recording workflow root: %w", err)
			}

			unlockTo, err := lockToWorkspace(dir)
			if err != nil {
				return err
			}
			defer unlockTo()
			unlockDest, err := destlock.Acquire(dir)
			if err != nil {
				return err
			}
			defer unlockDest()
			unlockOnto, err := lockOnto(dir)
			if err != nil {
				return err
			}
			defer unlockOnto()

			created, err := convert.Run(convert.Demote, dir, name, target, opid.New())
			if err != nil {
				return fmt.Errorf("onto demote: %w", err)
			}
			cmd.Printf("demoted %s -> %s\n", name, created)
			cmd.Printf("  source snapshot (unchanged): %s\n", filepath.Join(created, ".workflow", "snapshots"))
			cmd.Printf("  fresh to workspace: %s\n", created)
			cmd.Println("\nnext steps (onto and `to` are complementary — pick per change):")
			cmd.Println("  1. /to   # resume the demoted change (phase plan, or do with carried tasks)")
			cmd.Println("  2. declare [frameworks.to] in homonto.toml (alongside onto, if not")
			cmd.Println("     already) and homonto apply, if the to framework is missing")
			cmd.Println("  3. to promote <name> --yes converts back while nothing has changed")
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().StringVar(&as, "as", "", "target to change name (default: the source name)")
	cmd.Flags().BoolVar(&force, "yes", false, "confirm the demotion")
	return cmd
}
