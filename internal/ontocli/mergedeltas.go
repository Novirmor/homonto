package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
	if st.Close.Merged {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: already merged (close.merged=true)\n", name)
		return nil
	}
	// The final-confirmation gate must be answered before the first global
	// mutation: merging deltas rewrites the living specs, and a declined gate
	// must leave the repo untouched. Checked AFTER the idempotent no-op above —
	// an already-merged change re-running an interrupted close has nothing
	// left to confirm.
	if st.CloseConfirmed == "" {
		return fmt.Errorf("onto merge-deltas: close not confirmed: answer the final-confirmation gate, then run `onto set close-confirmed %s \"<evidence>\"`", name)
	}

	deltaDir := filepath.Join(changeDir, "specs")
	entries, _ := filepath.Glob(filepath.Join(deltaDir, "*.md"))
	sort.Strings(entries)

	// Compute every merge first; write nothing until all succeed and lint clean.
	type result struct {
		capability, target, merged string
	}
	var results []result
	specsDir := filepath.Join(root, "docs", "specs")
	for _, delta := range entries {
		capability := strings.TrimSuffix(filepath.Base(delta), ".md")
		if strings.EqualFold(capability, "README") {
			continue
		}
		deltaBytes, err := os.ReadFile(delta)
		if err != nil {
			return fmt.Errorf("onto merge-deltas: reading %s: %w", delta, err)
		}
		target := filepath.Join(specsDir, capability+".md")
		living := ""
		if b, err := os.ReadFile(target); err == nil {
			living = string(b)
		}
		merged, err := deltamerge.Merge(capability, living, string(deltaBytes))
		if err != nil {
			// Crash recovery: writes below are one atomic file at a time, so a
			// prior run that died mid-commit leaves each spec either untouched
			// (Merge still applies) or fully merged (its post-state holds). A
			// fully-merged spec is skipped rather than poisoning every re-run;
			// anything else (a typo'd name, a hand-edited spec) still fails.
			if deltamerge.Applied(capability, living, string(deltaBytes)) {
				continue
			}
			return fmt.Errorf("onto merge-deltas: %w", err)
		}
		if findings := deltamerge.Lint(merged); len(findings) > 0 {
			return fmt.Errorf("onto merge-deltas: %s would produce an invalid living spec: %s", capability, strings.Join(findings, "; "))
		}
		results = append(results, result{capability, target, merged})
	}

	// Commit: write the merged living specs, then record close.merged.
	if err := os.MkdirAll(specsDir, 0o755); err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}
	for _, r := range results {
		if err := fsutil.WriteAtomic(r.target, []byte(r.merged)); err != nil {
			return fmt.Errorf("onto merge-deltas: writing %s: %w", r.target, err)
		}
	}
	st.Close.Merged = true
	if err := ontostate.Save(statePath, st); err != nil {
		return fmt.Errorf("onto merge-deltas: %w", err)
	}

	if len(results) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "%s: no delta specs; marked close.merged\n", name)
		return nil
	}
	for _, r := range results {
		fmt.Fprintf(cmd.OutOrStdout(), "  merged %s → docs/specs/%s.md\n", r.capability, r.capability)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %d delta spec(s) merged; marked close.merged\n", name, len(results))
	return nil
}
