package assignment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/noviopenworks/homonto/internal/decision"
	"github.com/noviopenworks/homonto/internal/handoff"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
)

// Receipt is the durable acknowledgement of an answered action: which
// action was answered, by which role, at which input generation, and when.
// The generation is the submitter's evidence anchor — a later invalidation
// compares against it to decide whether this answer still stands.
type Receipt struct {
	ActionID   identity.ActionID
	WorkID     identity.WorkID
	Kind       protocol.ActionKind
	Role       protocol.Role
	Generation int64
	At         string
}

// Submit records a host's report against the assignment it answers. It is
// the freshness authority: the action must exist, still be awaiting an
// answer, carry a token derivable for it under this runtime key, and
// address exactly the submitted role. Every one of those is checked inside
// the same transaction that writes the report, and the unique index on the
// report's action id is what makes a duplicate answer impossible even
// under a concurrent retry.
func (s *Store) Submit(ctx context.Context, sub protocol.ReportSubmission) (Receipt, error) {
	var receipt Receipt
	err := s.db.Update(ctx, func(tx *store.Tx) error {
		act, err := loadAction(ctx, tx, sub.ActionID)
		if err != nil {
			return err
		}
		if act.Kind != protocol.KindAssignment {
			return fmt.Errorf("assignment: action %s is a %s: %w", act.ID, act.Kind, ErrKindMismatch)
		}
		if err := s.checkLive(act); err != nil {
			return err
		}
		if !handoff.VerifyFreshnessToken(s.key, act.ID, sub.FreshnessToken) {
			return fmt.Errorf("assignment: action %s: %w", act.ID, ErrStaleToken)
		}
		if sub.Role != act.Role {
			return fmt.Errorf("assignment: action %s addresses the %s, not the %s: %w",
				act.ID, act.Role, sub.Role, ErrWrongRole)
		}
		wire := act.Spec
		wire.FreshnessToken = s.Token(act.ID)
		if err := protocol.ValidateSubmission(wire, sub); err != nil {
			return err
		}
		if _, err := protocol.DecodeReport(wire, sub.Report); err != nil {
			return err
		}
		payload, err := json.Marshal(sub)
		if err != nil {
			return fmt.Errorf("assignment: encode submission: %w", err)
		}
		reportID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		now := formatTime(s.now().UTC())
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO reports (id, work_id, kind, action_id, role, payload,
			                     inputs_generation, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			reportID, string(act.WorkID), string(act.Role), string(act.ID), string(sub.Role),
			string(payload), act.Generation, now, now); err != nil {
			return duplicateOr(err, act.ID, "record report")
		}
		if err := markSubmitted(ctx, tx, act.ID, now); err != nil {
			return err
		}
		receipt = Receipt{
			ActionID: act.ID, WorkID: act.WorkID, Kind: act.Kind,
			Role: act.Role, Generation: act.Generation, At: now,
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// Decide records a human's answer to a decision action. Silence is never
// approval: an empty choice is refused by decision.Validate before
// anything is written, and a decision that is not the outstanding one is
// refused as not awaiting an answer.
func (s *Store) Decide(ctx context.Context, sub decision.Submission) (Receipt, error) {
	var receipt Receipt
	err := s.db.Update(ctx, func(tx *store.Tx) error {
		act, err := loadAction(ctx, tx, sub.ActionID)
		if err != nil {
			return err
		}
		if act.Kind != protocol.KindDecision {
			return fmt.Errorf("assignment: action %s is a %s: %w", act.ID, act.Kind, ErrKindMismatch)
		}
		if err := s.checkLive(act); err != nil {
			return err
		}
		if !handoff.VerifyFreshnessToken(s.key, act.ID, sub.FreshnessToken) {
			return fmt.Errorf("assignment: action %s: %w", act.ID, ErrStaleToken)
		}
		if act.Spec.Decision == nil {
			return fmt.Errorf("assignment: action %s carries no decision schema", act.ID)
		}
		schema, err := toDecisionSchema(*act.Spec.Decision)
		if err != nil {
			return err
		}
		if err := decision.Validate(schema, sub); err != nil {
			return err
		}
		decisionID, err := identity.NewUUID()
		if err != nil {
			return err
		}
		now := formatTime(s.now().UTC())
		summary := fmt.Sprintf("%s: %s", schema.Kind, sub.Choice)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO decisions (id, work_id, summary, action_id, choice, rationale,
			                       answer, inputs_generation, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			decisionID, string(act.WorkID), summary, string(act.ID), sub.Choice,
			sub.Rationale, sub.Answer, act.Generation, now, now); err != nil {
			return duplicateOr(err, act.ID, "record decision")
		}
		if err := markSubmitted(ctx, tx, act.ID, now); err != nil {
			return err
		}
		receipt = Receipt{
			ActionID: act.ID, WorkID: act.WorkID, Kind: act.Kind,
			Generation: act.Generation, At: now,
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// checkLive refuses an action that is not awaiting an answer. An already
// answered action is a duplicate; a pending or invalidated one is stale.
func (s *Store) checkLive(act Action) error {
	switch act.State {
	case StateIssued:
		return nil
	case StateSubmitted:
		return fmt.Errorf("assignment: action %s: %w", act.ID, ErrDuplicateSubmission)
	default:
		return fmt.Errorf("assignment: action %s is %s: %w", act.ID, act.State, ErrNotIssued)
	}
}

// markSubmitted advances an issued action to answered. The conditional
// UPDATE is the second duplicate gate: it matches only a still-issued row.
func markSubmitted(ctx context.Context, tx *store.Tx, id identity.ActionID, now string) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE actions SET state = ?, submitted_at = ?, updated_at = ?
		 WHERE id = ? AND state = ?`,
		string(StateSubmitted), now, now, string(id), string(StateIssued))
	if err != nil {
		return fmt.Errorf("assignment: answer action %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("assignment: answer action %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("assignment: action %s: %w", id, ErrDuplicateSubmission)
	}
	return nil
}

// duplicateOr maps a unique-constraint violation on the submission link to
// ErrDuplicateSubmission and passes every other failure through.
func duplicateOr(err error, id identity.ActionID, what string) error {
	if isUniqueViolation(err) {
		return fmt.Errorf("assignment: action %s: %w", id, ErrDuplicateSubmission)
	}
	return fmt.Errorf("assignment: %s for %s: %w", what, id, err)
}

// toDecisionSchema converts the wire schema an action carries into the
// persisted twin this package validates against, refusing a schema that is
// not structurally sound in the first place.
func toDecisionSchema(in protocol.DecisionSchema) (decision.Schema, error) {
	out := decision.Schema{
		Kind:       decision.Kind(in.Kind),
		Prompt:     in.Prompt,
		FindingID:  in.FindingID,
		QuestionID: in.QuestionID,
	}
	for _, c := range in.Choices {
		out.Choices = append(out.Choices, decision.Choice{
			Value:             c.Value,
			Label:             c.Label,
			RequiresRationale: c.RequiresRationale,
		})
	}
	if err := decision.ValidateSchema(out); err != nil {
		return decision.Schema{}, err
	}
	return out, nil
}

// isUniqueViolation reports whether err is SQLite's unique-constraint
// failure. The driver exposes no typed sentinel for it, so the message is
// the only available signal; a false negative merely surfaces the raw
// error instead of the typed one, and the conditional UPDATE in
// markSubmitted refuses the duplicate regardless.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// CompleteEdit answers a host edit action. An edit is not reported and not
// decided: it is finished by the artifact service accepting the edit under
// the grant the action carried, and this records that the action is done.
// The caller must have accepted the edit first — this records the fact,
// it does not verify it, because only the artifact service can.
func (s *Store) CompleteEdit(ctx context.Context, id identity.ActionID, token identity.Token) (Receipt, error) {
	var receipt Receipt
	err := s.db.Update(ctx, func(tx *store.Tx) error {
		act, err := loadAction(ctx, tx, id)
		if err != nil {
			return err
		}
		if act.Kind != protocol.KindEdit {
			return fmt.Errorf("assignment: action %s is a %s: %w", act.ID, act.Kind, ErrKindMismatch)
		}
		if err := s.checkLive(act); err != nil {
			return err
		}
		if !handoff.VerifyFreshnessToken(s.key, act.ID, token) {
			return fmt.Errorf("assignment: action %s: %w", act.ID, ErrStaleToken)
		}
		now := formatTime(s.now().UTC())
		if err := markSubmitted(ctx, tx, act.ID, now); err != nil {
			return err
		}
		receipt = Receipt{
			ActionID: act.ID, WorkID: act.WorkID, Kind: act.Kind,
			Generation: act.Generation, At: now,
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// Report returns the submission that answered one assignment.
func (s *Store) Report(ctx context.Context, id identity.ActionID) (protocol.ReportSubmission, bool, error) {
	var (
		payload string
		found   bool
	)
	err := s.db.View(ctx, func(tx *store.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT payload FROM reports WHERE action_id = ?`, string(id)).Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return protocol.ReportSubmission{}, false, fmt.Errorf("assignment: read report of %s: %w", id, err)
	}
	if !found {
		return protocol.ReportSubmission{}, false, nil
	}
	var sub protocol.ReportSubmission
	if err := json.Unmarshal([]byte(payload), &sub); err != nil {
		return protocol.ReportSubmission{}, false, fmt.Errorf("assignment: decode report of %s: %w", id, err)
	}
	return sub, true, nil
}

// Decision returns the recorded answer to one decision action.
func (s *Store) Decision(ctx context.Context, id identity.ActionID) (decision.Submission, bool, error) {
	var (
		sub       decision.Submission
		rationale sql.NullString
		answer    sql.NullString
		found     bool
	)
	err := s.db.View(ctx, func(tx *store.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT choice, rationale, answer FROM decisions WHERE action_id = ?`,
			string(id)).Scan(&sub.Choice, &rationale, &answer)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return decision.Submission{}, false, fmt.Errorf("assignment: read decision of %s: %w", id, err)
	}
	if !found {
		return decision.Submission{}, false, nil
	}
	sub.ActionID = id
	sub.Rationale = rationale.String
	sub.Answer = answer.String
	return sub, true, nil
}
