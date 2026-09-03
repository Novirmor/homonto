package ontocli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/noviopenworks/homonto/internal/applylock"
	"github.com/noviopenworks/homonto/internal/deltamerge"
	"github.com/noviopenworks/homonto/internal/fsutil"
	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// mergeDeltasCmd builds "onto merge-deltas <change>": deterministically merge the
// change's delta specs into the living specs (RENAMED → MODIFIED → REMOVED →
// ADDED), lint the result, and mark close.merged. This replaces the by-hand spec
// merge that was the riskiest step of onto-close. It is transactional (nothing is
// written unless every delta merges and lints clean) and idempotent (a change
// already close.merged is a no-op).
func mergeDeltasCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "merge-deltas <change>",
		Short: "Merge a change's delta specs into the living specs (deterministic)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runMergeDeltas(cmd, dir, args[0])
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

func runMergeDeltas(cmd *cobra.Command, root, name string) error {
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}
	if err := ontoFramework.ValidChangeName(name); err != nil {
		return err
	}
	lock, err := acquireSpecMergeLock(root)
	if err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}
	defer lock.Release()
	changeDir := filepath.Join(root, "docs", "changes", name)
	statePath := filepath.Join(changeDir, "onto-state.yaml")
	st, err := ontostate.Load(statePath)
	if err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}
	// Validate before consulting Abandoned/Close.Merged: a malformed state must
	// not reach the merge logic. Load migrates but does not validate (F9).
	if err := st.Validate(); err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}
	if st.Change != name {
		return fmt.Errorf("onto merge-deltas: state change %q does not match directory %q", st.Change, name)
	}
	// An abandoned change is the unsuccessful terminal state: its deltas were
	// never accepted, so they must never mutate the living specs.
	if st.Abandoned {
		return fmt.Errorf("onto merge-deltas: change %q is abandoned; an abandoned change's deltas are never merged into the living specs", name)
	}
	// Spec deltas are accepted only at close — an open/design/build/verify change
	// has not yet been verified, so its deltas must not mutate the living specs
	// (F7). Idempotent re-runs at close are covered by the Close.Merged check
	// below.
	if st.Phase != "close" {
		return fmt.Errorf("onto merge-deltas: change %q is at phase %q; merge-deltas runs only at close", name, st.Phase)
	}
	if err := passingVerificationEvidence(changeDir, st); err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}
	inputs, err := deltaInputs(root, changeDir)
	if err != nil {
		return fmt.Errorf("onto merge-deltas: listing delta specs: %w", err)
	}
	if st.Close.Merged {
		if err := validateCompletedMergeReceipt(root, changeDir, name); err != nil {
			return fmt.Errorf("onto merge-deltas: recorded merge is stale or unbound: %w; invalidate verification and reconcile explicitly rather than replaying over living specs", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s: already merged (receipt verified)\n", name)
		return nil
	}
	// The close-plan review must be recorded before the first global mutation:
	// merging deltas rewrites the living specs. Checked AFTER the idempotent
	// no-op above because an interrupted close has nothing left to validate.
	if st.CloseConfirmed == "" {
		return fmt.Errorf("onto merge-deltas: close plan not validated: review it, then run `onto set close-confirmed %s \"<evidence>\"`", name)
	}

	receipt, recovering, err := loadMergeReceipt(changeDir, name)
	if err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}
	if recovering {
		if err := validateReceiptManifest(receipt, inputs); err != nil {
			// close.merged is false here (a true marker returned above), so
			// this receipt is from an interrupted run OR a round whose
			// verification was invalidated and whose deltas then changed.
			// Crash recovery always has an unchanged manifest, so a mismatch
			// means the invalidated round: discard the stale receipt and
			// recompute from the current pre-images instead of dead-ending.
			receipt = mergeReceipt{Change: name, Entries: make([]mergeReceiptEntry, 0, len(inputs))}
			recovering = false
		}
	} else {
		receipt = mergeReceipt{Change: name, Entries: make([]mergeReceiptEntry, 0, len(inputs))}
	}

	// Compute every merge first; write nothing until all succeed and lint clean.
	type result struct {
		capability, target, merged string
	}
	var results []result
	specsDir := filepath.Join(root, "docs", "specs")
	if err := fsutil.RequireRealParents(root, specsDir); err != nil {
		return fmt.Errorf("onto merge-deltas: unsafe living-spec directory: %w", err)
	}
	for i, input := range inputs {
		exists, currentDigest, livingBytes, err := fileImage(input.target)
		if err != nil {
			return fmt.Errorf("onto merge-deltas: reading %s: %w", input.target, err)
		}
		if recovering {
			entry := receipt.Entries[i]
			if exists && currentDigest == entry.AfterSHA256 {
				continue
			}
			matchesBefore := exists == entry.BeforeExists && ((!exists && entry.BeforeSHA256 == "") || currentDigest == entry.BeforeSHA256)
			if !matchesBefore {
				return fmt.Errorf("onto merge-deltas: %s matches neither the recorded pre-image nor post-image; refusing to overwrite newer content", input.targetRel)
			}
		}
		merged, err := deltamerge.Merge(input.capability, string(livingBytes), string(input.data))
		if err != nil {
			// A prior round (verification invalidated) or receipt-less
			// partial write may already have applied this delta. Recognize
			// exactly that — the ADDED conflict with the post-state present —
			// and bind the fresh receipt to the current image. Every other
			// failure (typo'd REMOVED, missing MODIFIED target, conflicting
			// content) stays loud.
			if !recovering && exists && errors.Is(err, deltamerge.ErrAlreadyExists) &&
				deltamerge.Applied(input.capability, string(livingBytes), string(input.data)) {
				receipt.Entries = append(receipt.Entries, mergeReceiptEntry{
					Delta: input.delta, DeltaSHA256: input.digest, Target: input.targetRel,
					BeforeExists: true, BeforeSHA256: currentDigest, AfterSHA256: currentDigest,
				})
				continue
			}
			return fmt.Errorf("onto merge-deltas: %w", err)
		}
		if findings := deltamerge.Lint(merged); len(findings) > 0 {
			return fmt.Errorf("onto merge-deltas: %s would produce an invalid living spec: %s", input.capability, strings.Join(findings, "; "))
		}
		afterDigest := digestBytes([]byte(merged))
		if recovering {
			if afterDigest != receipt.Entries[i].AfterSHA256 {
				return fmt.Errorf("onto merge-deltas: recomputed post-image for %s does not match its receipt", input.targetRel)
			}
		} else {
			receipt.Entries = append(receipt.Entries, mergeReceiptEntry{
				Delta: input.delta, DeltaSHA256: input.digest, Target: input.targetRel,
				BeforeExists: exists, BeforeSHA256: currentDigest, AfterSHA256: afterDigest,
			})
		}
		results = append(results, result{input.capability, input.target, merged})
	}
	if !recovering {
		if err := saveMergeReceipt(changeDir, receipt); err != nil {
			return fmt.Errorf("onto merge-deltas: %w", err)
		}
	}

	// Commit: write the merged living specs, then record close.merged.
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}
	for _, r := range results {
		if err := fsutil.WriteControlPlaneWithin(root, r.target, []byte(r.merged), 0o644); err != nil {
			return fmt.Errorf("onto merge-deltas: writing %s: %w", r.target, err)
		}
	}
	st.Close.Merged = true
	if err := ontostate.Save(statePath, st); err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}

	if len(inputs) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: no delta specs; marked close.merged\n", name)
		return nil
	}
	for _, r := range results {
		fmt.Fprintf(cmd.OutOrStdout(), "  merged %s → docs/specs/%s.md\n", r.capability, r.capability)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %d delta spec(s) merged; marked close.merged\n", name, len(inputs))
	return nil
}

// acquireStateLockBestEffort serializes whole-state writers (`onto set`,
// `onto close`) against merge-deltas and complete-integration on the same
// repository, closing the lost-update window where a close saves a snapshot
// clobbering a concurrent set. Workspaces outside git have no shared lock
// anchor and proceed unlocked (the same best-effort as before).
func acquireStateLockBestEffort(root string) func() {
	lock, err := acquireSpecMergeLock(root)
	if err != nil {
		return func() {}
	}
	return func() { _ = lock.Release() }
}

func acquireSpecMergeLock(root string) (*applylock.Lock, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCmdTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("cannot locate the repository git directory: %s", strings.TrimSpace(string(out)))
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}
	lock, err := applylock.AcquireProcess(filepath.Join(filepath.Clean(gitDir), "homonto-onto-merge"))
	if err != nil {
		return nil, fmt.Errorf("spec merge lock: %w", err)
	}
	return lock, nil
}

func deltaSpecPaths(deltaDir string) ([]string, error) {
	entries, err := os.ReadDir(deltaDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") || strings.EqualFold(entry.Name(), "README.md") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("delta spec %s must not be a symlink", entry.Name())
		}
		paths = append(paths, filepath.Join(deltaDir, entry.Name()))
	}
	sort.Strings(paths)
	return paths, nil
}
