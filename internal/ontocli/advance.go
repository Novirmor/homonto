package ontocli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// advanceCmd builds the "onto advance <change>" subcommand: it enforces
// ontoFramework.Gate(dir), validates the change name, and only then attempts a
// single gated phase transition on that change's onto-state.yaml. It writes
// nothing unless every precondition below passes.
func advanceCmd() *cobra.Command {
	var dir string

	cmd := &cobra.Command{
		Use:   "advance <change>",
		Short: "Advance a change to its next workflow phase, if all gates pass",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdvance(cmd, dir, args[0])
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// runAdvance enforces, in order: ontoFramework.Gate(root);
// ontoFramework.ValidChangeName(name); that
// docs/changes/<name>/onto-state.yaml loads; that its phase has a next
// phase; that every RequiredArtifacts(st.Phase) file — the current phase's
// cumulative deliverables — is present in the change directory; that, when
// leaving "build", tasks.md has no unchecked items; that the transition's
// evidence token is present — leaving "verify" requires verify.result==pass,
// and entering "build" requires isolation chosen (branch|worktree);
// and a worktree-dirty check that unconditionally blocks entering "close"
// (refusing when dirtiness can't even be determined) but only warns for
// every other transition. Only once all of these pass does it flip the
// phase and save onto-state.yaml.
func runAdvance(cmd *cobra.Command, root, name string) error {
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
		return fmt.Errorf("onto advance: loading %s: %w", statePath, err)
	}
	// Validate before any gate consults a state field: an unknown workflow,
	// isolation, or guides value must not bypass a downstream evidence gate
	// (close reads workflow to decide whether guides are required; the
	// build-entry check reads isolation). Load migrates but does not validate
	// (F9).
	if err := st.Validate(); err != nil {
		return fmt.Errorf("onto advance: %w", err)
	}

	if st.Abandoned {
		return fmt.Errorf("onto advance: %q is abandoned (a terminal state); nothing to advance", name)
	}

	next, ok := ontostate.NextPhase(st.Phase)
	if !ok {
		return fmt.Errorf("onto advance: %q is at terminal/unknown phase %q; nothing to advance", name, st.Phase)
	}

	for _, f := range ontostate.RequiredArtifacts(st.Phase, st.Workflow) {
		if _, statErr := os.Stat(filepath.Join(changeDir, f)); statErr != nil {
			return fmt.Errorf("onto advance: cannot leave %q: missing %s", st.Phase, f)
		}
	}

	if st.Phase == "build" {
		done, tasksErr := ontostate.TasksAllChecked(filepath.Join(changeDir, "tasks.md"))
		if tasksErr != nil {
			return tasksErr
		}
		if !done {
			return fmt.Errorf("onto advance: cannot leave build: tasks.md has unchecked items")
		}
	}

	// Phase-evidence gates: beyond artifact existence and checked tasks,
	// certain transitions require a recorded evidence token. Leaving verify
	// requires a passing verification; entering build requires a chosen
	// isolation so planning work is never committed unisolated. The judgment
	// gates (proposal approval, approach confirmation) require their tokens
	// for full-workflow changes — the binary checks presence, never content
	// (B1: an honest agent cannot skip the gate; it can still be asked lies).
	// Presets are exempt: their scope gate lives in the preset skill, and
	// blocking a preset on design-phase tokens would contradict its reason to
	// exist.
	full := st.Workflow == "" || st.Workflow == "full"
	if st.Phase == "open" && full && st.ProposalApproved == "" {
		return fmt.Errorf("onto advance: cannot leave open: proposal approval not recorded (answer the artifact-review gate, then run `onto set proposal-approved %s \"<evidence>\"`)", name)
	}
	if next == "build" && full && st.ApproachConfirmed == "" {
		return fmt.Errorf("onto advance: cannot enter build: approach confirmation not recorded (answer the approach gate, then run `onto set approach-confirmed %s \"<evidence>\"`)", name)
	}
	if st.Phase == "verify" && st.Verify.Result != "pass" {
		result := st.Verify.Result
		if result == "" {
			result = "unset"
		}
		return fmt.Errorf("onto advance: cannot leave verify: missing passing verification (verify.result=%s)", result)
	}
	if next == "build" {
		if st.Isolation == "" {
			return fmt.Errorf("onto advance: cannot enter build: missing isolation (set branch or worktree)")
		}
		// A change cannot enter build if it participates in a depends-on cycle —
		// no valid build order exists (F10). A cycle is a structural fact about the
		// recorded deps (B1: shape, not judgment). Reuses onto graph's detector.
		if _, edges, gErr := buildGraph(root); gErr != nil {
			return fmt.Errorf("onto advance: cannot enter build: reading change graph: %w", gErr)
		} else {
			for _, cyc := range detectDepCycles(edges) {
				for _, member := range cyc {
					if member == name {
						return fmt.Errorf("onto advance: cannot enter build: %q is in a dependency cycle: %s → %s",
							name, strings.Join(cyc, " → "), cyc[0])
					}
				}
			}
		}
	}

	dirt, determinable := worktreeDirt(root, name)
	if next == "close" {
		if !determinable {
			return fmt.Errorf("onto advance: cannot verify worktree is clean; refusing close")
		}
		// Only this change's own artifacts and source paths block; another
		// active change's uncommitted docs are its own close gate's problem —
		// parallel changes must not deadlock each other (see worktreeDirt).
		if blocking := blockingDirt(dirt); len(blocking) > 0 {
			return fmt.Errorf("onto advance: dirty worktree blocks close: %s", dirtGateError(blocking, len(dirt), name))
		}
	} else if len(dirt) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: worktree has %d uncommitted path(s) (run `onto dirt %s` to classify)\n", len(dirt), name)
	}

	old := st.Phase
	st.Phase = next
	if err := ontostate.Save(statePath, st); err != nil {
		return fmt.Errorf("onto advance: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s → %s\n", name, old, next)
	return nil
}
