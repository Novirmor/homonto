package tocli

import (
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/destlock"
	"github.com/noviopenworks/homonto/internal/opid"
	"github.com/noviopenworks/homonto/internal/promote"
	"github.com/spf13/cobra"
)

// promoteCmd builds "to promote <name> [--as <name>] --yes": convert a growing
// `to` change into a full onto change (ADR 0028). The complete source
// workspace moves unchanged under docs/changes/<name>/imported-to/; a fresh
// proposal-only workspace starts at phase open. Promotion never installs both
// frameworks — the printed next steps are the homonto.toml swap, apply, and
// /onto. Locks: the `to` workspace lock, then the destination lock shared
// with `onto new`, in that fixed order, held across the final rename.
func promoteCmd() *cobra.Command {
	var (
		dir   string
		as    string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "promote <change-name>",
		Short: "Promote a `to` change into a full onto change (source preserved under imported-to/)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := toFramework.ValidChangeName(name); err != nil {
				return err
			}
			target := name
			if as != "" {
				if err := toFramework.ValidChangeName(as); err != nil {
					return fmt.Errorf("to promote: --as: %w", err)
				}
				target = as
			}
			if !force {
				return fmt.Errorf("to promote: pass --yes to promote (the source moves under docs/changes/%s/imported-to/ and the onto framework must replace `to` in homonto.toml)", target)
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

			created, err := promote.Run(dir, name, target, opid.New())
			if err != nil {
				return fmt.Errorf("to promote: %w", err)
			}
			cmd.Printf("promoted %s -> %s\n", name, created)
			cmd.Printf("  imported source (unchanged): %s\n", filepath.Join(created, "imported-to"))
			cmd.Printf("  fresh onto workspace (phase open): %s\n", created)
			cmd.Println("\nnext steps (onto and `to` are exclusive — swap, do not stack):")
			cmd.Println("  1. edit homonto.toml: replace [frameworks.to] with [frameworks.onto]")
			cmd.Println("  2. homonto apply --yes   # re-projects the onto framework")
			cmd.Println("  3. /onto                 # resume the promoted change at phase open")
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root")
	cmd.Flags().StringVar(&as, "as", "", "target onto change name (default: the source name)")
	cmd.Flags().BoolVar(&force, "yes", false, "confirm the promotion")
	return cmd
}
