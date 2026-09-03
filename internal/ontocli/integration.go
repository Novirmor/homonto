package ontocli

import (
	"fmt"

	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/spf13/cobra"
)

func completeIntegrationCmd() *cobra.Command {
	var (
		dir     string
		repo    string
		receipt string
	)
	cmd := &cobra.Command{
		Use:   "complete-integration <change>",
		Short: "Record the completed local merge or opened pull request for an archived change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ontoFramework.Gate(dir); err != nil {
				return err
			}
			if err := ontoFramework.ValidChangeName(name); err != nil {
				return err
			}
			if receipt == "" {
				return fmt.Errorf("onto complete-integration: --receipt is required")
			}
			// The load → validate → save sequence must be serialized against a
			// concurrent completion: two unlocked runs could both accept
			// different receipts and the last rename would win.
			lock, err := acquireSpecMergeLock(dir)
			if err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			defer lock.Release()
			archiveDir, st, err := locateArchive(dir, name)
			if err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			if !st.Archived {
				return fmt.Errorf("onto complete-integration: archive for %q is not marked archived; run `onto close %s` to recover it first", name, name)
			}
			record, ok, err := integrationrecord.Load(archiveDir, name)
			if err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			if !ok {
				return fmt.Errorf("onto complete-integration: archive has no integration record (legacy archives are already terminal)")
			}
			if err := validateIntegrationRecord(st, record); err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			entry, err := findEntry(record, repo)
			if err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			if entry.Receipt == receipt {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: integration already complete for repository %s (%s)\n", name, entryDisplayName(repo), receipt)
				return nil
			}
			if entry.Receipt != "" {
				return fmt.Errorf("onto complete-integration: repository %s already completed with a different receipt", entryDisplayName(repo))
			}
			repoDir := dir
			if repo != "" {
				_, dirs, scopeErr := scopeDirs(dir, []string{repo})
				if scopeErr != nil {
					return fmt.Errorf("onto complete-integration: %w", scopeErr)
				}
				repoDir = dirs[repo]
			}
			// The binary proves what local git can prove: a merge receipt is
			// validated against real history in that repository. A PR receipt
			// is external by nature — creation and review live outside onto —
			// so only its shape is checked here.
			if record.Mode == "merge" {
				canonical, validateErr := validateMergeReceipt(repoDir, receipt, entry)
				if validateErr != nil {
					return fmt.Errorf("onto complete-integration: %w", validateErr)
				}
				receipt = canonical
			}
			completed, err := record.CompleteFor(repo, receipt)
			if err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			if err := integrationrecord.Save(archiveDir, completed); err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			status := "pending"
			if completed.Status == integrationrecord.StatusComplete {
				status = "complete"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: integration %s for repository %s (%s)\n", name, status, entryDisplayName(repo), receipt)
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the archive")
	cmd.Flags().StringVar(&repo, "repo", "", "declared repository the receipt belongs to (default: the config repository)")
	cmd.Flags().StringVar(&receipt, "receipt", "", "merge:<commit-sha> or pr:<https-url>")
	_ = cmd.MarkFlagRequired("receipt")
	return cmd
}

func findEntry(record integrationrecord.Record, alias string) (integrationrecord.Entry, error) {
	for _, entry := range record.Repositories {
		if entry.Alias == alias {
			return entry, nil
		}
	}
	return integrationrecord.Entry{}, fmt.Errorf("integration record has no repository entry %q", entryDisplayName(alias))
}

func entryDisplayName(alias string) string {
	if alias == "" {
		return "config"
	}
	return alias
}
