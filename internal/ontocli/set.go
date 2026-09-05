package ontocli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// runTransition loads the change via LoadChange (so migration + dual-legacy
// conflict detection apply), lets apply validate+mutate the state, re-validates
// the whole state, and saves. It gates on ontoFramework.Gate(root) and
// ontoFramework.ValidChangeName, and writes nothing if any step fails.
func runTransition(cmd *cobra.Command, root, name string, apply func(*ontostate.State) error) error {
	if err := ontoFramework.Gate(root); err != nil {
		return err
	}
	if err := ontoFramework.ValidChangeName(name); err != nil {
		return err
	}
	unlockWs, err := lockOnto(root)
	if err != nil {
		return err
	}
	defer unlockWs()
	unlock := acquireStateLockBestEffort(root)
	defer unlock()
	changeDir := filepath.Join(changesDir(root), name)
	st, err := ontostate.LoadChange(changeDir)
	if err != nil {
		return fmt.Errorf("onto set: loading %s: %w", changeDir, err)
	}
	if st.Change != name {
		return fmt.Errorf("onto set: state change %q does not match directory %q", st.Change, name)
	}
	// Both terminal states are immutable: an abandoned change stays abandoned
	// (mutating it was the hole that let its evidence tokens be forged and its
	// deltas merged), and an archived one is history.
	if st.Abandoned {
		return fmt.Errorf("onto set: change %q is abandoned (terminal); its state is immutable", name)
	}
	if st.Archived {
		return fmt.Errorf("onto set: change %q is archived (terminal); its state is immutable", name)
	}
	if err := apply(&st); err != nil {
		return err
	}
	if err := st.Validate(); err != nil {
		return err
	}
	if err := ontostate.Save(filepath.Join(changeDir, "onto-state.yaml"), st); err != nil {
		return fmt.Errorf("onto set: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: updated\n", name)
	return nil
}

// enumSetterCmd builds a `set <field> <change> <value>` subcommand that accepts
// only members of allowed and applies set() to the loaded state.
func enumSetterCmd(field string, allowed []string, set func(*ontostate.State, string)) *cobra.Command {
	var dir string
	allowedSet := map[string]bool{}
	for _, v := range allowed {
		allowedSet[v] = true
	}
	cmd := &cobra.Command{
		Use:   field + " <change> <value>",
		Short: "Set the " + field + " field of a change",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, value := args[0], args[1]
			return runTransition(cmd, dir, name, func(st *ontostate.State) error {
				if !allowedSet[value] {
					return fmt.Errorf("onto set %s: %q is not one of %v", field, value, allowed)
				}
				set(st, value)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// setCmd builds the "onto set" parent with one semantic subcommand per gated
// field. Each subcommand owns its field's allowed set.
func setCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set a gated state field of an active change",
	}
	cmd.AddCommand(enumSetterCmd("isolation", []string{"branch", "worktree"},
		func(s *ontostate.State, v string) { s.Isolation = v }))
	cmd.AddCommand(enumSetterCmd("integration", []string{"merge", "pr"},
		func(s *ontostate.State, v string) { s.Integration = v }))
	cmd.AddCommand(enumSetterCmd("build-mode", []string{"direct", "subagent"},
		func(s *ontostate.State, v string) { s.BuildMode = v }))
	cmd.AddCommand(enumSetterCmd("tdd-mode", []string{"tdd", "direct"},
		func(s *ontostate.State, v string) { s.TDDMode = v }))
	cmd.AddCommand(enumSetterCmd("verify-scale", []string{"light", "full"},
		func(s *ontostate.State, v string) { s.Verify.Scale = v }))
	cmd.AddCommand(verifyResultCmd())
	cmd.AddCommand(enumSetterCmd("build-pause", []string{"plan-ready", "clear"},
		func(s *ontostate.State, v string) {
			if v == "clear" {
				s.BuildPause = ""
			} else {
				s.BuildPause = v
			}
		}))
	cmd.AddCommand(evidenceSetterCmd("proposal-approved",
		"Record the open phase's proposal review",
		func(s *ontostate.State, v string) { s.ProposalApproved = v }))
	cmd.AddCommand(evidenceSetterCmd("approach-confirmed",
		"Record the design phase's selected approach",
		func(s *ontostate.State, v string) { s.ApproachConfirmed = v }))
	cmd.AddCommand(evidenceSetterCmd("close-confirmed",
		"Record the close phase's validated close plan",
		func(s *ontostate.State, v string) { s.CloseConfirmed = v }))
	cmd.AddCommand(closeMergedCmd())
	cmd.AddCommand(directiveCmd())
	cmd.AddCommand(baseRefCmd())
	cmd.AddCommand(baseBranchCmd())
	cmd.AddCommand(workflowCmd())
	cmd.AddCommand(depsCmd())
	cmd.AddCommand(supersedesCmd())
	cmd.AddCommand(deviatesFromCmd())
	cmd.AddCommand(guidesCmd())
	return cmd
}

// verifyResultCmd records the verify outcome. A recorded pass also freezes
// each scoped repository's HEAD (verify.heads) so close can refuse archival
// when commits landed after the verified state; any other outcome clears the
// binding along with the close evidence it justified.
func verifyResultCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "verify-result <change> <pending|pass|fail>",
		Short: "Set the verify result of a change",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, value := args[0], args[1]
			return runTransition(cmd, dir, name, func(st *ontostate.State) error {
				if value != "pending" && value != "pass" && value != "fail" {
					return fmt.Errorf("onto set verify-result: %q is not one of [pending pass fail]", value)
				}
				st.Verify.Result = value
				st.Verify.Heads = nil
				if value != "pass" {
					st.Close.Merged = false
					st.CloseConfirmed = ""
				} else {
					// Binding policy: outside git there is nothing to bind
					// (legacy shape). Inside git, a capture failure — a
					// missing scoped repository, an unreadable config — is a
					// loud refusal, not a silently unbound pass.
					if !inGitRepository(dir) {
						return fmt.Errorf("onto set verify-result: %s is not a git repository; a pass cannot be bound to a commit here", dir)
					}
					heads, err := captureVerifyHeads(dir, *st)
					if err != nil {
						return fmt.Errorf("onto set verify-result: cannot bind the pass to the scoped repositories: %w", err)
					}
					st.Verify.Heads = heads
				}
				if value == "fail" {
					st.Observed.VerifyRounds++
				}
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// guidesCmd sets the guides obligation field. It cannot use enumSetterCmd
// because the "waived:<reason>" form is a prefix, not a fixed enum member.
func guidesCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "guides <change> <value>",
		Short: "Set a change's guides obligation: pending, updated, or waived:<reason>",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, value := args[0], args[1]
			return runTransition(cmd, dir, name, func(st *ontostate.State) error {
				if !ontostate.ValidGuides(value) || value == "" {
					return fmt.Errorf("onto set guides: %q is not one of pending|updated|waived:<reason>", value)
				}
				st.Guides = value
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// baseRefCmd records the change's base ref as a canonical commit id. The ref
// must resolve in the repository — base_ref is the immutable diff and
// verification anchor, and an unresolvable value would strand scale and close.
func baseRefCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "base-ref <change> <ref>",
		Short: "Record the base git commit a change branched from",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, ref := args[0], args[1]
			return runTransition(cmd, dir, name, func(st *ontostate.State) error {
				if strings.TrimSpace(ref) == "" {
					return fmt.Errorf("onto set base-ref: ref must not be empty")
				}
				canonical, err := resolveCommit(dir, ref)
				if err != nil {
					return fmt.Errorf("onto set base-ref: %w", err)
				}
				if st.BaseRef != "" && st.BaseRef != canonical {
					return fmt.Errorf("onto set base-ref: base_ref is immutable once recorded (currently %q)", st.BaseRef)
				}
				st.BaseRef = canonical
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// baseBranchCmd records the integration branch separately from BaseRef, which
// remains the immutable commit used for diffs and verification.
func baseBranchCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "base-branch <change> <branch>",
		Short: "Record the branch a change will integrate into",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, branch := args[0], args[1]
			return runTransition(cmd, dir, name, func(st *ontostate.State) error {
				if err := validateBranchName(dir, branch); err != nil {
					return fmt.Errorf("onto set base-branch: %w", err)
				}
				if st.BaseBranch != "" && st.BaseBranch != branch {
					return fmt.Errorf("onto set base-branch: base_branch is immutable once recorded (currently %q)", st.BaseBranch)
				}
				st.BaseBranch = branch
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// workflowCmd supports the one legal lifecycle conversion: a bounded preset
// may grow into the full workflow. Downgrades would discard obligations and
// are therefore rejected.
func workflowCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "workflow <change> full",
		Short: "Upgrade a fix or tweak preset to the full workflow",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, workflow := args[0], args[1]
			return runTransition(cmd, dir, name, func(st *ontostate.State) error {
				if workflow != "full" {
					return fmt.Errorf("onto set workflow: only an upgrade to full is supported")
				}
				if st.Workflow == "full" || st.Workflow == "" {
					return nil
				}
				if st.Workflow != "fix" && st.Workflow != "tweak" {
					return fmt.Errorf("onto set workflow: cannot upgrade workflow %q", st.Workflow)
				}
				st.Workflow = "full"
				st.Phase = "design"
				st.ApproachConfirmed = ""
				st.BuildMode = ""
				st.BuildPause = ""
				st.TDDMode = ""
				st.Verify = ontostate.Verify{Scale: "full", Result: "pending"}
				st.Close = ontostate.Close{}
				st.CloseConfirmed = ""
				st.Guides = "pending"
				st.Observed.PresetEscalated = true
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// depsCmd sets the change's dependency list from a repeatable --dep flag.
// --dep is used (not a comma-split positional) so dependency names carrying
// edge characters are never ambiguously parsed.
func depsCmd() *cobra.Command {
	var (
		dir  string
		deps []string
	)
	cmd := &cobra.Command{
		Use:   "deps <change> --dep <name> [--dep <name> ...]",
		Short: "Set a change's dependency list (repeat --dep per dependency)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTransition(cmd, dir, args[0], func(st *ontostate.State) error {
				// A dep is a change name and is matched literally against archive
				// directory names at close — validate it like one, so a typo'd or
				// metacharacter-carrying dep fails here instead of silently never
				// (or, with the old glob matching, always) resolving.
				for _, dep := range deps {
					if err := ontoFramework.ValidChangeName(dep); err != nil {
						return fmt.Errorf("onto set deps: --dep %q: %w", dep, err)
					}
				}
				st.Deps = deps
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().StringArrayVar(&deps, "dep", nil, "a dependency change name; repeat for several")
	return cmd
}

// supersedesCmd sets the change's supersedes list from a repeatable --change
// flag. Mirrors depsCmd: --change (not a comma-split positional) keeps names
// carrying edge characters unambiguous. Ungated — settable in any phase.
func supersedesCmd() *cobra.Command {
	var (
		dir        string
		supersedes []string
	)
	cmd := &cobra.Command{
		Use:   "supersedes <change> --change <name> [--change <name> ...]",
		Short: "Set the change names this change supersedes (repeat --change per name)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTransition(cmd, dir, args[0], func(st *ontostate.State) error {
				st.Supersedes = supersedes
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().StringArrayVar(&supersedes, "change", nil, "a superseded change name; repeat for several")
	return cmd
}

// deviatesFromCmd sets the change's deviates-from list from a repeatable --from
// flag. Mirrors supersedesCmd: --from (not a comma-split positional) keeps
// target names carrying edge characters unambiguous. Ungated — settable in any
// phase.
func deviatesFromCmd() *cobra.Command {
	var (
		dir     string
		targets []string
	)
	cmd := &cobra.Command{
		Use:   "deviates-from <change> --from <name> [--from <name> ...]",
		Short: "Set the targets this change knowingly deviates from (repeat --from per target)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTransition(cmd, dir, args[0], func(st *ontostate.State) error {
				st.DeviatesFrom = targets
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().StringArrayVar(&targets, "from", nil, "a target this change deviates from; repeat for several")
	return cmd
}

// closeMergedCmd delegates to the receipt-bound merge operation. It is kept as
// a compatibility spelling, but can no longer forge an unbound boolean. It
// is idempotent and needs no value,
// but tolerates an optional trailing "yes": the `onto gate --json` schema gives
// every gate option a value that skills append to the SetCommand mechanically,
// and this gate's option value is "yes" — rejecting it broke exactly the
// callers the schema exists for.
func closeMergedCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "close-merged <change> [yes]",
		Short: "Mark a change's close.merged flag true (idempotent)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 && args[1] != "yes" {
				return fmt.Errorf("onto set close-merged: %q is not a value; this setter takes no value (an optional literal \"yes\" is tolerated)", args[1])
			}
			return runMergeDeltas(cmd, dir, args[0])
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// directiveCmd stores a free-string directive verbatim; presence-only shape
// (empty rejected — a directive is a real pre-authorization, not a clear).
func directiveCmd() *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "directive <change> <text>",
		Short: "Record a verbatim pre-authorization directive on a change",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, text := args[0], args[1]
			return runTransition(cmd, dir, name, func(st *ontostate.State) error {
				if text == "" {
					return fmt.Errorf("onto set directive: text must not be empty")
				}
				st.Directive = text
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}

// evidenceSetterCmd builds a judgment-gate evidence setter: a free-form,
// non-empty value recording that the named gate was answered (convention:
// "YYYY-MM-DD <summary>"). It cannot use enumSetterCmd because the evidence
// is free text, not a fixed member — the binary checks presence, never
// content (B1).
func evidenceSetterCmd(field, short string, assign func(*ontostate.State, string)) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   field + " <change> <evidence>",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, value := args[0], args[1]
			return runTransition(cmd, dir, name, func(st *ontostate.State) error {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("onto set %s: evidence must not be empty", field)
				}
				assign(st, value)
				return nil
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	return cmd
}
