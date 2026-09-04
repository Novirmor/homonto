package ontocli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// gateOption is one choice for a pending workflow decision. Recommended marks
// the safe default when repository evidence does not select another option.
type gateOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Recommended bool   `json:"recommended,omitempty"`
}

// pendingGate is a structured, blocking decision derived from a change's phase
// and state. The coordinator resolves it from repository evidence or asks the
// user when intent is genuinely missing, then records it with SetCommand.
type pendingGate struct {
	ID         string       `json:"id"`
	Question   string       `json:"question"`
	Header     string       `json:"header"`
	Options    []gateOption `json:"options,omitempty"`
	SetCommand string       `json:"set_command"`
	// SetArgv is SetCommand as an argv template (no shell parsing): recovery
	// packs carry it instead of the shell string, and value placeholders stay
	// literal "<value>" tokens.
	SetArgv []string `json:"set_argv,omitempty"`
}

// pendingGates returns the decisions still unanswered for a change at its current
// phase, in the order the workflow needs them. A gate is pending only when its
// evidence token is missing, so an answered gate disappears from the list.
func pendingGates(name string, st ontostate.State) []pendingGate {
	set := func(field string) (string, []string) {
		return fmt.Sprintf("onto set %s %s <value>", field, name),
			[]string{"onto", "set", field, name, "<value>"}
	}
	full := st.Workflow == "" || st.Workflow == "full"
	var out []pendingGate
	switch st.Phase {
	case "open":
		if full && st.ProposalApproved == "" {
			out = append(out, pendingGate{
				ID: "proposal-approved", Header: "Proposal",
				SetCommand: fmt.Sprintf("onto set proposal-approved %s \"<evidence>\"", name),
				SetArgv:    []string{"onto", "set", "proposal-approved", name, "<value>"},
				Question:   "Has the proposal been reviewed against the request and grounding?",
			})
		}
	case "design":
		if full && st.ApproachConfirmed == "" {
			out = append(out, pendingGate{
				ID: "approach-confirmed", Header: "Approach",
				SetCommand: fmt.Sprintf("onto set approach-confirmed %s \"<evidence>\"", name),
				SetArgv:    []string{"onto", "set", "approach-confirmed", name, "<value>"},
				Question:   "Has the design approach been selected and its basis recorded?",
			})
		}
		if st.Isolation == "" {
			cmd, argv := set("isolation")
			out = append(out, pendingGate{
				ID: "isolation", Header: "Isolation", SetCommand: cmd, SetArgv: argv,
				Question: "How should this change be isolated for build?",
				Options: []gateOption{
					{Value: "branch", Label: "A branch off the base ref", Recommended: true},
					{Value: "worktree", Label: "A separate git worktree (parallel work / dirty tree)"},
				},
			})
		}
	case "build":
		if st.BuildMode == "" {
			cmd, argv := set("build-mode")
			out = append(out, pendingGate{
				ID: "build-mode", Header: "Build mode", SetCommand: cmd, SetArgv: argv,
				Question: "How should the tasks be executed?",
				Options: []gateOption{
					{Value: "direct", Label: "Directly in this session"},
					{Value: "subagent", Label: "Dispatch onto-implementer per task (needs real dispatch)", Recommended: true},
				},
			})
		}
		if st.TDDMode == "" {
			cmd, argv := set("tdd-mode")
			out = append(out, pendingGate{
				ID: "tdd-mode", Header: "TDD mode", SetCommand: cmd, SetArgv: argv,
				Question: "Test-driven or direct implementation?",
				Options: []gateOption{
					{Value: "tdd", Label: "Failing test first (anything with testable logic)", Recommended: true},
					{Value: "direct", Label: "Implement then verify (content/config/docs only)"},
				},
			})
		}
	case "verify":
		if st.Verify.Result != "pass" {
			cmd, argv := set("verify-result")
			out = append(out, pendingGate{
				ID: "verify-result", Header: "Verify result", SetCommand: cmd, SetArgv: argv,
				Question: "What is the verification outcome?",
				Options: []gateOption{
					{Value: "pass", Label: "All scenarios verified with fresh evidence"},
					{Value: "fail", Label: "A scenario failed — fix by default; acceptance needs authorization"},
				},
			})
		}
	case "close":
		if st.Verify.Result != "pass" {
			cmd, argv := set("verify-result")
			out = append(out, pendingGate{
				ID: "verify-result", Header: "Verify result", SetCommand: cmd, SetArgv: argv,
				Question: "What is the verification outcome?",
				Options: []gateOption{
					{Value: "pass", Label: "All scenarios verified with fresh evidence"},
					{Value: "fail", Label: "A scenario failed — fix by default; acceptance needs authorization"},
				},
			})
		}
		if (st.Workflow == "full" || st.Workflow == "") && !ontostate.GuidesResolved(st.Guides) {
			cmd, argv := set("guides")
			out = append(out, pendingGate{
				ID: "guides", Header: "Guides", SetCommand: cmd, SetArgv: argv,
				Question: "How is the guides obligation resolved?",
				Options: []gateOption{
					{Value: "updated", Label: "The affected guides were written/updated"},
					{Value: "waived:<reason>", Label: "Waived with a recorded reason"},
				},
			})
		}
		if st.BaseRef == "" {
			cmd, argv := set("base-ref")
			out = append(out, pendingGate{
				ID: "base-ref", Header: "Diff base", SetCommand: cmd, SetArgv: argv,
				Question: "Which immutable commit anchors this change's diff and verification?",
			})
		}
		if st.BaseBranch == "" {
			cmd, argv := set("base-branch")
			out = append(out, pendingGate{
				ID: "base-branch", Header: "Base branch", SetCommand: cmd, SetArgv: argv,
				Question: "Which branch should receive this change at integration?",
			})
		}
		if st.Integration == "" {
			cmd, argv := set("integration")
			out = append(out, pendingGate{
				ID: "integration", Header: "Integration", SetCommand: cmd, SetArgv: argv,
				Question: "How should the branch be integrated at close?",
				Options: []gateOption{
					{Value: "merge", Label: "Merge the branch into its base branch", Recommended: true},
					{Value: "pr", Label: "Open a pull request and leave the branch for review"},
				},
			})
		}
		if st.CloseConfirmed == "" {
			out = append(out, pendingGate{
				ID: "close-confirmed", Header: "Close plan",
				SetCommand: fmt.Sprintf("onto set close-confirmed %s \"<evidence>\"", name),
				SetArgv:    []string{"onto", "set", "close-confirmed", name, "<value>"},
				Question:   "Has the close plan been validated against the verified workspace? merge-deltas and close refuse without this.",
			})
		}
		if !st.Close.Merged {
			out = append(out, closeMergeGate(name))
		}
	}
	return out
}

func closeMergeGate(name string) pendingGate {
	return pendingGate{
		ID: "close-merged", Header: "Specs merged",
		SetCommand: fmt.Sprintf("onto merge-deltas %s", name),
		SetArgv:    []string{"onto", "merge-deltas", name},
		Question:   "Have the change's spec deltas been merged into the living specs?",
	}
}

func addInvalidReceiptGate(root, changeDir, name string, st ontostate.State, gates []pendingGate) []pendingGate {
	if st.Phase == "close" && st.Close.Merged {
		if err := validateCompletedMergeReceipt(root, changeDir, name); err != nil {
			return append(gates, closeMergeGate(name))
		}
	}
	return gates
}

// gateCmd builds "onto gate <change> [--json]": a read-only report of the
// pending decisions for a change, with the exact `onto set` command to record
// each. The binary owns the schema; skills decide whether evidence settles the
// decision or user intent is needed.
func gateCmd() *cobra.Command {
	var (
		dir    string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "gate <change>",
		Short: "Report the pending workflow decisions for a change (read-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ontoFramework.ValidChangeName(name); err != nil {
				return err
			}
			changeDir := filepath.Join(changesDir(dir), name)
			st, err := ontostate.LoadChange(changeDir)
			if err != nil {
				return err
			}
			if err := st.Validate(); err != nil {
				return fmt.Errorf("onto gate: %w", err)
			}
			if st.Change != name {
				return fmt.Errorf("onto gate: state change %q does not match directory %q", st.Change, name)
			}
			gates := addInvalidReceiptGate(dir, changeDir, name, st, pendingGates(name, st))
			if asJSON {
				b, err := json.MarshalIndent(gates, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(b))
				return nil
			}
			if len(gates) == 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: no pending gate at phase %s\n", name, st.Phase)
				return nil
			}
			for _, g := range gates {
				fmt.Fprintf(cmd.OutOrStdout(), "[%s] %s\n", g.Header, g.Question)
				for _, o := range g.Options {
					star := " "
					if o.Recommended {
						star = "*"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  %s %s — %s\n", star, o.Value, o.Label)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  → %s\n", g.SetCommand)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&dir, "dir", ".", "workspace root containing the change")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the pending gates as JSON")
	return cmd
}
