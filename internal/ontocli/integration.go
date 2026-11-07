package ontocli

import (
	"fmt"
	"strings"

	"github.com/Novirmor/homonto/internal/integrationrecord"
	"github.com/Novirmor/homonto/internal/ontostate"
	"github.com/Novirmor/homonto/internal/workspace"
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
			archiveDir, st, record, err := loadIntegrationTarget(dir, name)
			if err != nil {
				return err
			}
			if repo, err = defaultIntegrationRepo(repo, st); err != nil {
				return err
			}
			entry, err := findEntry(record, repo)
			if err != nil {
				return fmt.Errorf("onto complete-integration: %w", err)
			}
			if done, err := receiptReplay(entry, repo, receipt, head, st); err != nil || done {
				if done {
					fmt.Fprintf(cmd.OutOrStdout(), "%s: integration already complete for repository %s (%s)\n", name, entryDisplayName(repo), receipt)
				}
				return err
			}
			repoDir, err := integrationRepoDir(dir, repo, st)
			if err != nil {
				return err
			}
			// Publication is never inferred from a moving branch or a URL alone.
			if err := validateIntegrationSource(dir, repoDir, st, entry); err != nil {
				return err
			}
			if receipt, head, err = canonicalReceipt(cmd, dir, repoDir, name, receipt, head, record, entry, st); err != nil {
				return err
			}
			if head != "" && !strings.HasPrefix(receipt, "pr:") {
				return fmt.Errorf("onto complete-integration: --head is only valid for a PR receipt")
			}
			completed, err := saveCompletion(archiveDir, record, repo, receipt, head)
			if err != nil {
				return err
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

// defaultIntegrationRepo picks the only repository of an explicit
// single-repository change when --repo is omitted.
func defaultIntegrationRepo(repo string, st ontostate.State) (string, error) {
	if repo != "" || st.RepoMode != "explicit" {
		return repo, nil
	}
	if len(st.Repos) != 1 {
		return "", fmt.Errorf("onto complete-integration: --repo is required for a multi-repository change")
	}
	return st.Repos[0], nil
}

// receiptReplay decides a repeated completion: done when a legacy entry
// already holds this receipt, an error when it would replace a recorded head
// or a different receipt. An explicit source revalidates a repeat instead.
func receiptReplay(entry integrationrecord.Entry, repo, receipt, head string, st ontostate.State) (bool, error) {
	if entry.Receipt == receipt && head != "" && head != entry.PublicationHead {
		return false, fmt.Errorf("onto complete-integration: publication head cannot replace an existing receipt")
	}
	if entry.Receipt == receipt && st.RepoMode != "explicit" {
		return true, nil
	}
	if entry.Receipt != "" && entry.Receipt != receipt {
		return false, fmt.Errorf("onto complete-integration: repository %s already completed with a different receipt", entryDisplayName(repo))
	}
	return false, nil
}

// saveCompletion records repo's receipt, and a PR's head, in the archive.
func saveCompletion(archiveDir string, record integrationrecord.Record, repo, receipt, head string) (integrationrecord.Record, error) {
	completed, err := record.CompleteFor(repo, receipt)
	if err != nil {
		return completed, fmt.Errorf("onto complete-integration: %w", err)
	}
	for i := range completed.Repositories {
		if completed.Repositories[i].Alias == repo && head != "" {
			completed.Repositories[i].PublicationHead = head
		}
	}
	if err := integrationrecord.Save(archiveDir, completed); err != nil {
		return completed, fmt.Errorf("onto complete-integration: %w", err)
	}
	return completed, nil
}

// loadIntegrationTarget loads an archived change and its integration record,
// both validated, and checks that legacy sources still resolve.
func loadIntegrationTarget(dir, name string) (string, ontostate.State, integrationrecord.Record, error) {
	archiveDir, st, err := locateArchive(dir, name)
	if err != nil {
		return "", st, integrationrecord.Record{}, fmt.Errorf("onto complete-integration: %w", err)
	}
	if !st.Archived {
		return "", st, integrationrecord.Record{}, fmt.Errorf("onto complete-integration: archive for %q is not marked archived; run `onto close %s` to recover it first", name, name)
	}
	record, ok, err := integrationrecord.Load(archiveDir, name)
	if err != nil {
		return "", st, integrationrecord.Record{}, fmt.Errorf("onto complete-integration: %w", err)
	}
	if !ok {
		return "", st, integrationrecord.Record{}, fmt.Errorf("onto complete-integration: archive has no integration record (legacy archives are already terminal)")
	}
	if err := validateIntegrationRecord(st, record); err != nil {
		return "", st, integrationrecord.Record{}, fmt.Errorf("onto complete-integration: %w", err)
	}
	if st.RepoMode == "legacy" || (st.RepoMode == "" && len(st.Repos) > 0) {
		if _, err := stateSourceDirs(dir, st); err != nil {
			return "", st, integrationrecord.Record{}, fmt.Errorf("onto complete-integration: %w", err)
		}
	}
	return archiveDir, st, record, nil
}

// integrationRepoDir is the checkout the receipt belongs to: the config root,
// or a declared repository whose Git identity still matches the state.
func integrationRepoDir(dir, repo string, st ontostate.State) (string, error) {
	if repo == "" {
		return dir, nil
	}
	layout, err := workspace.LoadRoot(dir)
	if err != nil {
		return "", fmt.Errorf("onto complete-integration: %w", err)
	}
	repoDir := layout.Repos[repo]
	if repoDir == "" {
		return "", fmt.Errorf("repository %q is no longer declared", repo)
	}
	if st.RepoMode == "explicit" {
		identity, err := sourceIdentity(repoDir)
		if err != nil || identity != st.RepoBases[repo].GitCommonDir {
			return "", fmt.Errorf("repository %s: Git identity does not match state", repo)
		}
	}
	return repoDir, nil
}

// canonicalReceipt validates the receipt against the source and returns its
// canonical form and the PR head to record. A PR is an external claim: its
// head is checked locally, its remote publication is not.
func canonicalReceipt(cmd *cobra.Command, dir, repoDir, name, receipt, head string, record integrationrecord.Record, entry integrationrecord.Entry, st ontostate.State) (string, string, error) {
	switch {
	case strings.HasPrefix(receipt, "unchanged:"):
		canonical, err := validateUnchangedReceipt(repoDir, receipt, entry)
		if err != nil {
			return "", "", fmt.Errorf("onto complete-integration: %w", err)
		}
		return canonical, head, nil
	case record.Mode == "merge":
		canonical, err := validateMergeReceipt(dir, repoDir, receipt, entry)
		if err != nil {
			return "", "", fmt.Errorf("onto complete-integration: %w", err)
		}
		return canonical, head, nil
	case strings.HasPrefix(receipt, "pr:"):
		if head == "" {
			head = entry.PublicationHead
		}
		if st.RepoMode == "explicit" && head == "" && entry.Receipt == "" {
			return "", "", fmt.Errorf("onto complete-integration: PR receipt requires --head <canonical-commit> reported by the publication tool; inspect the pinned source with `onto state %s --json` before publishing", name)
		}
		if head != "" {
			if !fullCommitID.MatchString(head) {
				return "", "", fmt.Errorf("onto complete-integration: --head must be a canonical commit id")
			}
			if err := integrationCandidateIntact(dir, repoDir, entry, head); err != nil {
				return "", "", fmt.Errorf("onto complete-integration: PR head: %w", err)
			}
		}
		fmt.Fprintln(cmd.ErrOrStderr(), "PR URL/head recorded as an external claim; remote publication is not verified by onto")
	}
	return receipt, head, nil
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
