package ontocli

import (
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/integrationrecord"
	"github.com/noviopenworks/homonto/internal/workspace"
	"github.com/spf13/cobra"
)

func completeIntegrationCmd() *cobra.Command {
	var (
		dir     string
		repo    string
		receipt string
		head    string
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
			unlockWs, err := lockOnto(dir)
			if err != nil {
				return err
			}
			defer unlockWs()
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
			if st.RepoMode == "legacy" || (st.RepoMode == "" && len(st.Repos) > 0) {
				if _, err := stateSourceDirs(dir, st); err != nil {
					return fmt.Errorf("onto complete-integration: %w", err)
				}
			}
			if repo == "" && st.RepoMode == "explicit" {
				if len(st.Repos) != 1 {
					return fmt.Errorf("onto complete-integration: --repo is required for a multi-repository change")
				}
				repo = st.Repos[0]
			}
			entry, err := findEntry(record, repo)
			if err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			if entry.Receipt == receipt && head != "" && head != entry.PublicationHead {
				return fmt.Errorf("onto complete-integration: publication head cannot replace an existing receipt")
			}
			if entry.Receipt == receipt && st.RepoMode != "explicit" {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: integration already complete for repository %s (%s)\n", name, entryDisplayName(repo), receipt)
				return nil
			}
			if entry.Receipt != "" && entry.Receipt != receipt {
				return fmt.Errorf("onto complete-integration: repository %s already completed with a different receipt", entryDisplayName(repo))
			}
			repoDir := dir
			if repo != "" {
				layout, scopeErr := workspace.LoadRoot(dir)
				if scopeErr != nil {
					return fmt.Errorf("onto complete-integration: %w", scopeErr)
				}
				repoDir = layout.Repos[repo]
				if repoDir == "" {
					return fmt.Errorf("repository %q is no longer declared", repo)
				}
				if st.RepoMode == "explicit" {
					identity, err := sourceIdentity(repoDir)
					if err != nil || identity != st.RepoBases[repo].GitCommonDir {
						return fmt.Errorf("repository %s: Git identity does not match state", repo)
					}
				}
			}
			// Publication is never inferred from a moving branch or a URL alone.
			if err := validateIntegrationSource(dir, repoDir, st, entry); err != nil {
				return err
			}
			if strings.HasPrefix(receipt, "unchanged:") {
				canonical, validateErr := validateUnchangedReceipt(repoDir, receipt, entry)
				if validateErr != nil {
					return fmt.Errorf("onto complete-integration: %w", validateErr)
				}
				receipt = canonical
			} else if record.Mode == "merge" {
				canonical, validateErr := validateMergeReceipt(dir, repoDir, receipt, entry)
				if validateErr != nil {
					return fmt.Errorf("onto complete-integration: %w", validateErr)
				}
				receipt = canonical
			} else if strings.HasPrefix(receipt, "pr:") {
				if head == "" {
					head = entry.PublicationHead
				}
				if st.RepoMode == "explicit" && head == "" && entry.Receipt == "" {
					return fmt.Errorf("onto complete-integration: PR receipt requires --head <canonical-commit> reported by the publication tool; inspect the pinned source with `onto state %s --json` before publishing", name)
				}
				if head != "" {
					if !fullCommitID.MatchString(head) {
						return fmt.Errorf("onto complete-integration: --head must be a canonical commit id")
					}
					if err := integrationCandidateIntact(dir, repoDir, entry, head); err != nil {
						return fmt.Errorf("onto complete-integration: PR head: %w", err)
					}
				}
				fmt.Fprintln(cmd.ErrOrStderr(), "PR URL/head recorded as an external claim; remote publication is not verified by onto")
			}
			if head != "" && !strings.HasPrefix(receipt, "pr:") {
				return fmt.Errorf("onto complete-integration: --head is only valid for a PR receipt")
			}
			completed, err := record.CompleteFor(repo, receipt)
			if err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			for i := range completed.Repositories {
				if completed.Repositories[i].Alias == repo && head != "" {
					completed.Repositories[i].PublicationHead = head
				}
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
	cmd.Flags().StringVar(&repo, "repo", "", "receipt repository (required for multiple explicit repos; legacy default: config)")
	cmd.Flags().StringVar(&receipt, "receipt", "", "merge:<commit-sha>, unchanged:<receiving-commit-sha>, or pr:<https-url>")
	cmd.Flags().StringVar(&head, "head", "", "canonical PR head reported by the publication tool (required for explicit sources; no network verification)")
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
