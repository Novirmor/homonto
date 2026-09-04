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
	var (
		dir string
		to  string
	)

	cmd := &cobra.Command{
		Use:   "advance <change> [--to build]",
		Short: "Advance a change to its next workflow phase, if all gates pass",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if to == "" {
				return runAdvance(cmd, dir, args[0])
			}
			return runAdvanceTo(cmd, dir, args[0], to)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().StringVar(&to, "to", "", "preset-only: walk the gated advances up to this phase (only \"build\")")
	return cmd
}

// runAdvanceTo walks single gated advances until the change reaches the
// target phase. It exists so a preset (which skips design) reaches build in
// one call instead of scripting two ceremonial advances — sugar, never a
// bypass: every hop runs runAdvance's full gate set, and a failing hop's
// error propagates unchanged. Full workflows are refused (they advance one
// gate at a time because each phase has distinct evidence), as is any target
// other than build (later phases require work between transitions).
func runAdvanceTo(cmd *cobra.Command, root, name, target string) error {
	// Same entry invariant as every other command: the framework gate and the
	// change-name validation run before ANY path is built from the name — a
	// traversal-carrying name must never cause an out-of-workspace read.
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}
	if err := ontoFramework.ValidChangeName(name); err != nil {
		return err
	}
	if target != "build" {
		return fmt.Errorf("onto advance --to: only \"build\" is a valid target (got %q) — phases past build are never advanced mechanically", target)
	}
	changeDir := filepath.Join(changesDir(root), name)
	st, err := ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
	if err != nil {
		return fmt.Errorf("onto advance: loading state: %w", err)
	}
	if err := st.Validate(); err != nil {
		return fmt.Errorf("onto advance: %w", err)
	}
	if st.Change != name {
		return fmt.Errorf("onto advance: state change %q does not match directory %q", st.Change, name)
	}
	if st.Workflow != "fix" && st.Workflow != "tweak" {
		return fmt.Errorf("onto advance --to build: workflow %q advances one gate at a time; --to is preset-only (fix/tweak skip design)", st.Workflow)
	}
	// Only a change BEFORE build may walk to build — the loop must never carry
	// a change past its declared target (a preset at verify running --to build
	// would otherwise advance verify→close and fail beyond it, mutating state
	// it was never asked to touch).
	if st.Phase != "open" && st.Phase != "design" {
		return fmt.Errorf("onto advance --to build: %q is at phase %q, not before build; nothing to walk", name, st.Phase)
	}
	for st.Phase != "build" {
		if err := runAdvance(cmd, root, name); err != nil {
			return err
		}
		st, err = ontostate.Load(filepath.Join(changeDir, "onto-state.yaml"))
		if err != nil {
			return fmt.Errorf("onto advance: reloading state: %w", err)
		}
	}
	return nil
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

	changeDir := filepath.Join(changesDir(root), name)
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
	if st.Change != name {
		return fmt.Errorf("onto advance: state change %q does not match directory %q", st.Change, name)
	}

	if st.Abandoned {
		return fmt.Errorf("onto advance: %q is abandoned (a terminal state); nothing to advance", name)
	}
	if st.Archived {
		return fmt.Errorf("onto advance: %q is archived (a terminal state); nothing to advance", name)
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
	// review gates (proposal approval, approach confirmation) require their
	// tokens for full-workflow changes. The binary checks presence, never
	// content: the token proves a review was recorded, not who made it.
	// Presets are exempt: their scope gate lives in the preset skill, and
	// blocking a preset on design-phase tokens would contradict its reason to
	// exist.
	full := st.Workflow == "" || st.Workflow == "full"
	if st.Phase == "open" && full && st.ProposalApproved == "" {
		return fmt.Errorf("onto advance: cannot leave open: proposal review not recorded (review the proposal, then run `onto set proposal-approved %s \"<evidence>\"`)", name)
	}
	if next == "build" && full && st.ApproachConfirmed == "" {
		return fmt.Errorf("onto advance: cannot enter build: approach selection not recorded (select the approach, then run `onto set approach-confirmed %s \"<evidence>\"`)", name)
	}
	if st.Phase == "verify" && st.Verify.Result != "pass" {
		result := st.Verify.Result
		if result == "" {
			result = "unset"
		}
		return fmt.Errorf("onto advance: cannot leave verify: missing passing verification (verify.result=%s)", result)
	}
	// The state's pass is self-asserted; the report is the evidence. Both must
	// agree before verify closes — a `Result: fail` (or absent Result line) in
	// verification.md beside verify.result=pass is a contradiction to fix, not
	// a technicality to advance past. Prefix match tolerates the accepted-
	// deviations suffix (`Result: pass (N accepted deviations)`).
	if st.Phase == "verify" {
		line, ok := ontostate.VerificationResultLine(filepath.Join(changeDir, "verification.md"))
		if !ok {
			return fmt.Errorf("onto advance: cannot leave verify: verification.md must contain exactly one canonical \"Result:\" line — write the report before leaving verify")
		}
		if !ontostate.ResultLineIsPass(line) {
			return fmt.Errorf("onto advance: cannot leave verify: verification.md says %q but verify.result=pass — the report and the state must agree", line)
		}
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
		if unresolved := ontostate.DepsResolved(root, st.Deps); len(unresolved) > 0 {
			return fmt.Errorf("onto advance: cannot enter build: unresolved dependencies: %v", unresolved)
		}
	}

	if next == "close" {
		dirt, err := scopedWorktreeDirt(root, name, st.Repos)
		if err != nil {
			return fmt.Errorf("onto advance: cannot verify scoped worktrees; refusing close: %w", err)
		}
		if msg := scopedDirtGateError(dirt, name); msg != "" {
			return fmt.Errorf("onto advance: dirty worktree blocks close: %s", msg)
		}
	} else if dirt, determinable := worktreeDirt(root, name); determinable && len(dirt) > 0 {
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
