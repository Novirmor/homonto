package tocli

import (
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/convert"
	"github.com/noviopenworks/homonto/internal/destlock"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/workcli"
	"github.com/spf13/cobra"
)

// promoteCmd builds "to promote <name> [--as <name>] --yes": convert a growing
// `to` change into a full onto change (ADR 0028, engine: ADR 0042). The
// complete source workspace moves into the neutral control plane
// (.workflow/snapshots/) of a fresh proposal-only change at phase open;
// converting back with `onto demote` restores it byte-for-byte when nothing
// changed. It is one of the two bridges between the complementary
// frameworks, so its install gate accepts either applied framework. Locks,
// in the fixed global order: the `to` workspace lock, then the shared
// destination lock, held across the final rename.
func promoteCmd() *cobra.Command {
	var (
		dir   string
		as    string
		force bool
	)
	ontoAlt := workcli.Framework{Name: "onto", SkillsDir: "skills/onto", GatePrefix: "onto", NamePrefix: "onto new"}
	cmd := &cobra.Command{
		Use:   "promote <change-name>",
		Short: "Promote a `to` change into a full onto change (source preserved in .workflow/snapshots/)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := toFramework.GateAny(dir, ontoAlt); err != nil {
				return err
			}
			name := args[0]
			if err := toFramework.ValidChangeName(name); err != nil {
				return err
			}
			target := name
			if as != "" {
				// The target lives in onto's namespace: onto's rules govern it.
				if err := ontoAlt.ValidChangeName(as); err != nil {
					return fmt.Errorf("to promote: --as: %w", err)
				}
				target = as
			}
			if !force {
				return fmt.Errorf("to promote: pass --yes to promote (the source moves under <workflow-root>/changes/%s/.workflow/snapshots/ and the change restarts at phase open)", target)
			}
			if err := workcli.MarkWorkflowState(dir); err != nil {
				return fmt.Errorf("to promote: recording workflow root: %w", err)
			}

			unlockTo, err := lock(dir)
			if err != nil {
				return err
			}
			defer unlockTo()
			unlockDest, err := destlock.Acquire(dir)
			if err != nil {
				return err
			}
			defer unlockDest()

			created, err := convert.Run(convert.Promote, dir, name, target, opid.New())
			if err != nil {
				return fmt.Errorf("to promote: %w", err)
			}
			cmd.Printf("promoted %s -> %s\n", name, created)
			cmd.Printf("  source snapshot (unchanged): %s\n", filepath.Join(created, ".workflow", "snapshots"))
			cmd.Printf("  fresh onto workspace (phase open): %s\n", created)
			cmd.Println("\nnext steps (onto and `to` are complementary — pick per change):")
			cmd.Println("  1. declare [frameworks.onto] in homonto.toml (alongside `to`, if not")
			cmd.Println("     already) and homonto apply, if the onto framework is missing")
			cmd.Println("  2. /onto                 # resume the promoted change at phase open")
			cmd.Println("  3. onto demote <name> --yes converts back while nothing has changed")
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().StringVar(&as, "as", "", "target onto change name (default: the source name)")
	cmd.Flags().BoolVar(&force, "yes", false, "confirm the promotion")
	return cmd
}
