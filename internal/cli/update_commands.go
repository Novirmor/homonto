package cli

import (
	"encoding/json"
	"fmt"

	"github.com/noviopenworks/homonto/internal/update"
	"github.com/noviopenworks/homonto/internal/update/trust"
	"github.com/spf13/cobra"
)

// workflowUpdateCmd groups the self-update commands.
func workflowUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update Homonto itself",
		Long: "Fetch, verify, and activate a new Homonto binary. This is the only " +
			"command that touches the network, it runs only when you ask, and " +
			"Homonto never checks for updates on its own.",
	}
	cmd.AddCommand(candidateMetadataCmd(), updateTrustCmd())
	return cmd
}

// candidateMetadataCmd answers what this binary is.
//
// It is hidden because it exists for one binary to ask another what it is
// — the running Homonto runs a staged candidate with this command and
// reads the answer — rather than for a human to read. It performs no
// network access and opens no workspace, so a candidate can be
// interrogated safely before anything is replaced.
func candidateMetadataCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "candidate-metadata",
		Short:  "Report this binary's version, protocol, and schema",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			encoded, err := json.Marshal(update.LocalMetadata())
			if err != nil {
				return fmt.Errorf("cli: encode candidate metadata: %w", err)
			}
			cmd.OutOrStdout().Write(append(encoded, '\n'))
			return nil
		},
	}
	cmd.Flags().Bool("json", true, "emit the metadata document (always on)")
	return cmd
}

// updateTrustCmd shows what this build will accept a release from.
func updateTrustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trust",
		Short: "Show the signing roots this build trusts",
		Long: "Show which signing roots this binary will accept a release from. A " +
			"build carrying none verifies nothing and cannot update itself — " +
			"which is the safe default for a build you compiled yourself.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store := trust.Compiled()
			if store.Empty() {
				cmd.Println("This build carries no signing root, so `homonto update` is unavailable.")
				cmd.Println("That is expected for a locally built binary.")
				return nil
			}
			cmd.Printf("%d signing root(s), %d signature(s) required:\n",
				len(store.Roots), max(store.Threshold, 1))
			for _, root := range store.Roots {
				cmd.Printf("  %s\n", root.ID)
			}
			return nil
		},
	}
}
