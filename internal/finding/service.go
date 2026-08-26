package finding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
)

// RepairLimit is the number of consecutive failed repair rounds after
// which Homonto stops repairing and asks a human what to do. The spec sets
// it at three: a fourth attempt at the same problem is not diligence.
const RepairLimit = 3

// Clock is the service's time source; tests inject a fixed one.
type Clock func() time.Time

// Service persists findings, resolves them, and counts repair rounds.
type Service struct {
	db  *store.DB
	now Clock
}

// NewService binds a finding service to the runtime database.
func NewService(db *store.DB, now Clock) (*Service, error) {
	if db == nil {
		return nil, fmt.Errorf("finding: database must not be nil")
	}
	if now == nil {
		now = time.Now
	}
	return &Service{db: db, now: now}, nil
}

// Record persists the findings of one report. A finding the reporter has
// raised before updates the existing row rather than creating a duplicate,
// but it never resurrects a resolved one: once a human accepted a finding
// or an implementer fixed it, a later report repeating it does not silently
// reopen the gate. Reopening is the engine's decision, through Reopen.
func (s *Service) Record(ctx context.Context, findings []Finding) error {
	for i := range findings {
		if err := findings[i].Validate(); err != nil {
			return err
		}
	}
	now := formatTime(s.now().UTC())
	return s.db.Update(ctx, func(tx *store.Tx) error {
		for _, f := range findings {
			evidence, err := json.Marshal(f.Evidence)
			if err != nil {
				return fmt.Errorf("finding: encode evidence of %q: %w", f.ExternalID, err)
			}
			id, err := identity.NewUUID()
			if err != nil {
				return err
			}
			var actionID any
			if f.ActionID != "" {
				actionID = string(f.ActionID)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO findings (id, work_id, action_id, external_id, role, severity,
				                      summary, evidence, recommendation, state, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(work_id, external_id) DO UPDATE SET
				  action_id = excluded.action_id,
				  role = excluded.role,
				  severity = excluded.severity,
				  summary = excluded.summary,
				  evidence = excluded.evidence,
				  recommendation = excluded.recommendation,
				  updated_at = excluded.updated_at`,
				id, string(f.WorkID), actionID, f.ExternalID, string(f.Role), string(f.Severity),
				f.Summary, string(evidence), f.Recommendation, string(StateOpen), now, now); err != nil {
				return fmt.Errorf("finding: record %q: %w", f.ExternalID, err)
			}
		}
		return nil
	})
}

// RecordReport is Record over the findings a role report carries.
func (s *Service) RecordReport(ctx context.Context, workID identity.WorkID, actionID identity.ActionID, role protocol.Role, findings []protocol.Finding) error {
	converted, err := FromReport(workID, actionID, role, findings)
	if err != nil {
		return err
	}
	return s.Record(ctx, converted)
}

// Resolve closes one finding. The severity that decides how much ceremony
// the resolution needs is read from the STORED finding, not from the
// resolution — a caller cannot downgrade a blocker to wave it through.
func (s *Service) Resolve(ctx context.Context, resolution Resolution) error {
	return s.db.Update(ctx, func(tx *store.Tx) error {
		f, err := loadOne(ctx, tx, resolution.WorkID, resolution.ExternalID)
		if err != nil {
			return err
		}
		if f.State.Resolved() {
			return fmt.Errorf("finding %q is %s: %w", f.ExternalID, f.State, ErrAlreadyResolved)
		}
		if err := resolution.Validate(Blocking(f.Severity)); err != nil {
			return err
		}
		state, _ := resolution.Kind.state()
		now := formatTime(s.now().UTC())
		var decisionID any
		if resolution.DecisionID != "" {
			decisionID = string(resolution.DecisionID)
		}
		res, err := tx.ExecContext(ctx, `
			UPDATE findings SET state = ?, rationale = ?, decision_id = ?, resolved_at = ?, updated_at = ?
			 WHERE work_id = ? AND external_id = ? AND state = ?`,
			string(state), resolution.Rationale, decisionID, now, now,
			string(resolution.WorkID), resolution.ExternalID, string(StateOpen))
		if err != nil {
			return fmt.Errorf("finding: resolve %q: %w", resolution.ExternalID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("finding: resolve %q: %w", resolution.ExternalID, err)
		}
		if n == 0 {
			return fmt.Errorf("finding %q: %w", resolution.ExternalID, ErrAlreadyResolved)
		}
		return nil
	})
}

// All returns every finding of a work, oldest first.
func (s *Service) All(ctx context.Context, workID identity.WorkID) ([]Finding, error) {
	var out []Finding
	err := s.db.View(ctx, func(tx *store.Tx) error {
		var err error
		out, err = loadAll(ctx, tx, workID)
		return err
	})
	return out, err
}

// Blockers returns the findings that currently gate the workflow.
func (s *Service) Blockers(ctx context.Context, workID identity.WorkID) ([]Finding, error) {
	all, err := s.All(ctx, workID)
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, f := range all {
		if f.Blocking() {
			out = append(out, f)
		}
	}
	return out, nil
}

// Deviations returns the accepted blocking findings a record must carry.
func (s *Service) Deviations(ctx context.Context, workID identity.WorkID) ([]Deviation, error) {
	all, err := s.All(ctx, workID)
	if err != nil {
		return nil, err
	}
	return Deviations(all), nil
}

// FailRepair records one failed repair round and returns the new count and
// whether the limit is now reached. At the limit the engine must stop
// repairing and put the choice to a human.
func (s *Service) FailRepair(ctx context.Context, workID identity.WorkID) (int, bool, error) {
	if err := identity.ValidateUUID(string(workID)); err != nil {
		return 0, false, fmt.Errorf("finding: work_id: %w", err)
	}
	var rounds int
	now := formatTime(s.now().UTC())
	err := s.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO repair_rounds (work_id, rounds, updated_at) VALUES (?, 1, ?)
			ON CONFLICT(work_id) DO UPDATE SET rounds = rounds + 1, updated_at = excluded.updated_at`,
			string(workID), now); err != nil {
			return fmt.Errorf("finding: count repair round for %s: %w", workID, err)
		}
		return tx.QueryRowContext(ctx,
			`SELECT rounds FROM repair_rounds WHERE work_id = ?`, string(workID)).Scan(&rounds)
	})
	if err != nil {
		return 0, false, err
	}
	return rounds, rounds >= RepairLimit, nil
}

// RepairRounds returns how many consecutive repair rounds have failed.
func (s *Service) RepairRounds(ctx context.Context, workID identity.WorkID) (int, error) {
	var rounds int
	err := s.db.View(ctx, func(tx *store.Tx) error {
		err := tx.QueryRowContext(ctx,
			`SELECT rounds FROM repair_rounds WHERE work_id = ?`, string(workID)).Scan(&rounds)
		if errors.Is(err, sql.ErrNoRows) {
			rounds = 0
			return nil
		}
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("finding: read repair rounds for %s: %w", workID, err)
	}
	return rounds, nil
}

// ResetRepairs clears the counter. A repair round that SUCCEEDS resets it:
// the limit counts consecutive failures, not lifetime attempts.
func (s *Service) ResetRepairs(ctx context.Context, workID identity.WorkID) error {
	return s.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM repair_rounds WHERE work_id = ?`, string(workID)); err != nil {
			return fmt.Errorf("finding: reset repair rounds for %s: %w", workID, err)
		}
		return nil
	})
}

// loadOne reads one finding by its reporter id within a work.
func loadOne(ctx context.Context, tx *store.Tx, workID identity.WorkID, externalID string) (Finding, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, work_id, action_id, external_id, role, severity, summary, evidence,
		       recommendation, state, rationale, decision_id
		  FROM findings WHERE work_id = ? AND external_id = ?`, string(workID), externalID)
	f, err := scanFinding(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Finding{}, fmt.Errorf("finding %q in work %s: %w", externalID, workID, ErrUnknownFinding)
	}
	if err != nil {
		return Finding{}, fmt.Errorf("finding: read %q: %w", externalID, err)
	}
	return f, nil
}

// loadAll reads every finding of a work.
func loadAll(ctx context.Context, tx *store.Tx, workID identity.WorkID) ([]Finding, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, work_id, action_id, external_id, role, severity, summary, evidence,
		       recommendation, state, rationale, decision_id
		  FROM findings WHERE work_id = ? ORDER BY created_at, external_id`, string(workID))
	if err != nil {
		return nil, fmt.Errorf("finding: list findings of %s: %w", workID, err)
	}
	defer rows.Close()
	var out []Finding
	for rows.Next() {
		f, err := scanFinding(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("finding: read finding row: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("finding: list findings of %s: %w", workID, err)
	}
	return out, nil
}

// scanFinding decodes one finding row.
func scanFinding(scan func(...any) error) (Finding, error) {
	var (
		f          Finding
		actionID   sql.NullString
		evidence   string
		rationale  sql.NullString
		decisionID sql.NullString
	)
	if err := scan(&f.ID, &f.WorkID, &actionID, &f.ExternalID, &f.Role, &f.Severity,
		&f.Summary, &evidence, &f.Recommendation, &f.State, &rationale, &decisionID); err != nil {
		return Finding{}, err
	}
	if !f.State.known() {
		return Finding{}, fmt.Errorf("finding %q carries unknown state %q", f.ExternalID, f.State)
	}
	if err := json.Unmarshal([]byte(evidence), &f.Evidence); err != nil {
		return Finding{}, fmt.Errorf("decode evidence of %q: %w", f.ExternalID, err)
	}
	f.ActionID = identity.ActionID(actionID.String)
	f.Rationale = rationale.String
	f.DecisionID = identity.ActionID(decisionID.String)
	return f, nil
}

// formatTime is the package's single timestamp spelling.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
