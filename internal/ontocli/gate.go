package ontocli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/noviopenworks/homonto/internal/ontostate"
	"github.com/spf13/cobra"
)

// gateOption is one choice for a pending gate. Recommended marks the default a
// dialog should preselect.
type gateOption struct {
	Value       string `json:"value"`
	Label       string `json:"label"`
	Recommended bool   `json:"recommended,omitempty"`
}

// gate is a structured, blocking decision derived from a change's phase and
// state — the schema a skill renders as an AskUserQuestion (Claude) or question
// (OpenCode) dialog, and records with SetCommand. Making the gate the binary's
// output (not free skill prose) keeps the same question asked the same way in
// both tools and makes "which gate is pending" machine-checkable (B1).
type pendingGate struct {
	ID         string       `json:"id"`
	Question   string       `json:"question"`
	Header     string       `json:"header"`
	Options    []gateOption `json:"options,omitempty"`
	SetCommand string       `json:"set_command"`
}

// pendingGates returns the decisions still unanswered for a change at its current
// phase, in the order the workflow needs them. A gate is pending only when its
// evidence token is missing, so an answered gate disappears from the list.
func pendingGates(name string, st ontostate.State) []pendingGate {
	set := func(field string) string {
		return fmt.Sprintf("onto set %s %s <value>", field, name)
	}
	full := st.Workflow == "" || st.Workflow == "full"
	var out []pendingGate
	switch st.Phase {
	case "open":
		if full && st.ProposalApproved == "" {
			out = append(out, pendingGate{
				ID: "proposal-approved", Header: "Proposal", SetCommand: fmt.Sprintf("onto set proposal-approved %s \"<evidence>\"", name),
				Question: "Has the user reviewed and approved the proposal (artifact-review gate)?",
			})
		}
	case "design":
		if full && st.ApproachConfirmed == "" {
			out = append(out, pendingGate{
				ID: "approach-confirmed", Header: "Approach", SetCommand: fmt.Sprintf("onto set approach-confirmed %s \"<evidence>\"", name),
				Question: "Has the user confirmed the design approach (approach gate)?",
			})
		}
		if st.Isolation == "" {
			out = append(out, pendingGate{
				ID: "isolation", Header: "Isolation", SetCommand: set("isolation"),
				Question: "How should this change be isolated for build?",
				Options: []gateOption{
					{Value: "branch", Label: "A branch off the base ref", Recommended: true},
					{Value: "worktree", Label: "A separate git worktree (parallel work / dirty tree)"},
				},
			})
		}
	case "build":
		if st.BuildMode == "" {
			out = append(out, pendingGate{
				ID: "build-mode", Header: "Build mode", SetCommand: set("build-mode"),
				Question: "How should the tasks be executed?",
				Options: []gateOption{
					{Value: "direct", Label: "Directly in this session", Recommended: true},
					{Value: "subagent", Label: "Dispatch onto-implementer per task (needs real dispatch)"},
				},
			})
		}
		if st.TDDMode == "" {
			out = append(out, pendingGate{
				ID: "tdd-mode", Header: "TDD mode", SetCommand: set("tdd-mode"),
				Question: "Test-driven or direct implementation?",
				Options: []gateOption{
					{Value: "tdd", Label: "Failing test first (anything with testable logic)", Recommended: true},
					{Value: "direct", Label: "Implement then verify (content/config/docs only)"},
				},
			})
		}
	case "verify":
		if st.Verify.Result != "pass" {
			out = append(out, pendingGate{
				ID: "verify-result", Header: "Verify result", SetCommand: set("verify-result"),
				Question: "What is the verification outcome?",
				Options: []gateOption{
					{Value: "pass", Label: "All scenarios verified with fresh evidence"},
					{Value: "fail", Label: "A scenario failed — decide fix or accept-deviation"},
				},
			})
		}
	case "close":
		if st.CloseConfirmed == "" {
			out = append(out, pendingGate{
				ID: "close-confirmed", Header: "Close plan", SetCommand: fmt.Sprintf("onto set close-confirmed %s \"<evidence>\"", name),
				Question: "Has the user confirmed the close plan (final-confirmation gate)? merge-deltas and close refuse without this.",
			})
		}
		if !st.Close.Merged {
			out = append(out, pendingGate{
				ID: "close-merged", Header: "Specs merged", SetCommand: fmt.Sprintf("onto set close-merged %s", name),
				Question: "Have the change's spec deltas been merged into the living specs?",
				Options:  []gateOption{{Value: "yes", Label: "Merged — mark close.merged"}},
			})
		}
		if (st.Workflow == "full" || st.Workflow == "") && !ontostate.GuidesResolved(st.Guides) {
			out = append(out, pendingGate{
				ID: "guides", Header: "Guides", SetCommand: set("guides"),
				Question: "How is the guides obligation resolved?",
				Options: []gateOption{
					{Value: "updated", Label: "The affected guides were written/updated"},
					{Value: "waived:<reason>", Label: "Waived with a recorded reason"},
				},
			})
		}
		if st.Integration == "" {
			out = append(out, pendingGate{
				ID: "integration", Header: "Integration", SetCommand: set("integration"),
				Question: "How should the branch be integrated at close?",
				Options: []gateOption{
					{Value: "merge", Label: "Merge the branch into its base ref"},
					{Value: "pr", Label: "Open a pull request and leave the branch for review"},
				},
			})
		}
	}
	return out
}

// gateCmd builds "onto gate <change> [--json]": a read-only report of the
// pending decisions for a change, with the exact `onto set` command to record
// each. Skills render these as dialogs; the binary owns the schema.
func gateCmd() *cobra.Command {
	var (
		dir    string
		asJSON bool
	)
	cmd := &cobra.Command{
		Use:   "gate <change>",
		Short: "Report the pending gate decisions for a change (read-only)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := ontoFramework.ValidChangeName(name); err != nil {
				return err
			}
			changeDir := filepath.Join(dir, "docs", "changes", name)
			st, err := ontostate.LoadChange(changeDir)
			if err != nil {
				return err
			}
			gates := pendingGates(name, st)
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
