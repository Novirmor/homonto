package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/guard"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/spf13/cobra"
)

// The protocol commands are the whole surface a host tool speaks to.
// Every one of them reads or writes the versioned JSON contract in package
// protocol and nothing else: the host never inspects the database, never
// edits the record, and never decides what happens next.

func nextCmd(opener Opener) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "next [name-or-id]",
		Short: "Ask what to do now",
		Long: "Return the actions a host may execute now: a parallel group of " +
			"assignments, one blocking human decision, one document edit, or " +
			"nothing when the work is finished. Asking again while a group is " +
			"outstanding returns the same group, so this is safe to repeat.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			selector := ""
			if len(args) == 1 {
				selector = args[0]
			}
			id, err := a.ResolveWork(cmd.Context(), selector)
			if err != nil {
				return err
			}
			resp, err := a.NextFor(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !asJSON {
				return renderNext(cmd, resp)
			}
			encoded, err := protocol.EncodeNextResponse(resp)
			if err != nil {
				return err
			}
			cmd.OutOrStdout().Write(append(encoded, '\n'))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the protocol payload")
	return cmd
}

// renderNext prints the human-readable form of a next response.
func renderNext(cmd *cobra.Command, resp protocol.NextResponse) error {
	switch resp.State {
	case protocol.NextComplete:
		cmd.Println("complete: nothing further to do")
		return nil
	case protocol.NextBlocked:
		act := resp.Actions[0]
		cmd.Printf("blocked on a human decision (%s)\n", act.ID)
		cmd.Printf("  %s\n", act.Decision.Prompt)
		for _, c := range act.Decision.Choices {
			suffix := ""
			if c.RequiresRationale {
				suffix = " (needs a rationale)"
			}
			cmd.Printf("  - %s: %s%s\n", c.Value, c.Label, suffix)
		}
		return nil
	}
	cmd.Printf("ready: %d action(s)\n", len(resp.Actions))
	for _, act := range resp.Actions {
		switch act.Kind {
		case protocol.KindEdit:
			cmd.Printf("  edit %s (%s) regions %v\n", act.Edit.Document, act.ID, act.Edit.Regions)
		default:
			cmd.Printf("  %s %s in %s\n", act.Role, act.ID, act.WorkingDirectory)
		}
	}
	return nil
}

func reportCmd(opener Opener) *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "report",
		Short: "Submit a role report",
		Long: "Submit a host's answer to an assignment, read as protocol JSON " +
			"from stdin or --file. For a writable assignment Homonto validates " +
			"what actually changed on disk BEFORE recording anything: a report " +
			"backed by changes the assignment was not issued to make is refused.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			r, closer, err := inputReader(cmd, file)
			if err != nil {
				return err
			}
			defer closer()
			sub, err := protocol.DecodeSubmission(r)
			if err != nil {
				return err
			}
			st, err := a.SubmitReport(cmd.Context(), sub)
			if err != nil {
				return err
			}
			cmd.Printf("recorded the %s report for %s; %s %s is at %s\n",
				sub.Role, sub.ActionID, st.Kind, st.Name, st.Step)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "read the submission from this file instead of stdin")
	return cmd
}

func decideCmd(opener Opener) *cobra.Command {
	var (
		file      string
		actionID  string
		token     string
		choice    string
		rationale string
		answer    string
	)
	cmd := &cobra.Command{
		Use:   "decide",
		Short: "Answer a human decision gate",
		Long: "Record a human's answer to a blocking decision, either as protocol " +
			"JSON on stdin or through the flags. An empty choice is refused: " +
			"silence is never approval.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			var sub decision.Submission
			if actionID == "" {
				r, closer, err := inputReader(cmd, file)
				if err != nil {
					return err
				}
				defer closer()
				wire, err := protocol.DecodeDecisionSubmission(r)
				if err != nil {
					return err
				}
				sub = decision.Submission{
					ActionID:       wire.ActionID,
					FreshnessToken: wire.FreshnessToken,
					Choice:         wire.Choice,
					Rationale:      wire.Rationale,
					Answer:         wire.Answer,
				}
			} else {
				sub = decision.Submission{
					ActionID:       identity.ActionID(actionID),
					FreshnessToken: identity.Token(token),
					Choice:         choice,
					Rationale:      rationale,
					Answer:         answer,
				}
			}
			st, err := a.Decide(cmd.Context(), sub)
			if err != nil {
				return err
			}
			cmd.Printf("recorded %q for %s; %s %s is at %s\n",
				sub.Choice, sub.ActionID, st.Kind, st.Name, st.Step)
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "read the submission from this file instead of stdin")
	cmd.Flags().StringVar(&actionID, "action", "", "the decision action being answered")
	cmd.Flags().StringVar(&token, "token", "", "the action's freshness token")
	cmd.Flags().StringVar(&choice, "choice", "", "the chosen option")
	cmd.Flags().StringVar(&rationale, "rationale", "", "why, for choices that require it")
	cmd.Flags().StringVar(&answer, "answer", "", "the free answer, for question gates")
	return cmd
}

func acceptEditCmd(opener Opener) *cobra.Command {
	var (
		actionID string
		token    string
	)
	cmd := &cobra.Command{
		Use:   "accept-edit",
		Short: "Finish a document edit",
		Long: "Tell Homonto the host has finished the document edit an edit " +
			"action opened. Homonto looks up what that grant actually opened " +
			"and refuses any change outside it — the grant is not re-described " +
			"by the host, only named.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			st, err := a.AcceptEdit(cmd.Context(), identity.ActionID(actionID), identity.Token(token))
			if err != nil {
				return err
			}
			cmd.Printf("accepted the edit for %s; %s %s is at %s\n",
				actionID, st.Kind, st.Name, st.Step)
			return nil
		},
	}
	cmd.Flags().StringVar(&actionID, "action", "", "the edit action being finished")
	cmd.Flags().StringVar(&token, "token", "", "the grant token the action carried")
	_ = cmd.MarkFlagRequired("action")
	_ = cmd.MarkFlagRequired("token")
	return cmd
}

func guardCmd(opener Opener) *cobra.Command {
	var (
		file     string
		actionID string
		token    string
		grantID  string
		grantTok string
	)
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Decide whether a presented write is allowed",
		Long: "Answer a cooperating host's write hook. This is a process gate, " +
			"not an operating-system sandbox: it blocks what a host presents, " +
			"and a shell command can walk past it. Homonto validates the " +
			"resulting diff independently before accepting any report.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := open(cmd, opener)
			if err != nil {
				return err
			}
			defer a.Close()
			r, closer, err := inputReader(cmd, file)
			if err != nil {
				return err
			}
			defer closer()
			wire, err := protocol.DecodeGuardRequest(r)
			if err != nil {
				return err
			}
			d, err := a.Authorize(cmd.Context(), guard.Request{
				Wire:       wire,
				ActionID:   identity.ActionID(actionID),
				Token:      identity.Token(token),
				GrantID:    identity.ActionID(grantID),
				GrantToken: identity.Token(grantTok),
			})
			if err != nil {
				return err
			}
			encoded, err := protocol.EncodeGuardDecision(d)
			if err != nil {
				return err
			}
			cmd.OutOrStdout().Write(append(encoded, '\n'))
			if !d.Allow {
				return errRefused{d}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "read the request from this file instead of stdin")
	cmd.Flags().StringVar(&actionID, "action", "", "the assignment the session is executing")
	cmd.Flags().StringVar(&token, "token", "", "that assignment's freshness token")
	cmd.Flags().StringVar(&grantID, "grant", "", "the edit grant the session is writing under")
	cmd.Flags().StringVar(&grantTok, "grant-token", "", "that grant's token")
	cmd.Flags().Bool("json", true, "emit the protocol payload (always on)")
	return cmd
}

// errRefused makes a refusal a non-zero exit without losing the decision,
// which has already been written to stdout for the hook to read.
type errRefused struct{ d protocol.GuardDecision }

func (e errRefused) Error() string { return fmt.Sprintf("%s: %s", e.d.Code, e.d.Reason) }

// inputReader returns the submission source: a named file or stdin.
func inputReader(cmd *cobra.Command, file string) (io.Reader, func(), error) {
	if file == "" {
		return cmd.InOrStdin(), func() {}, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, nil, fmt.Errorf("cli: open %s: %w", file, err)
	}
	return f, func() { _ = f.Close() }, nil
}

// writeJSON emits a value as indented JSON on the command's stdout.
func writeJSON(cmd *cobra.Command, v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("cli: encode JSON: %w", err)
	}
	cmd.OutOrStdout().Write(append(encoded, '\n'))
	return nil
}
