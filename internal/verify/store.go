package verify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/fingerprint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/store"
)

// ErrNoEvidence reports a work with no recorded verification pass.
var ErrNoEvidence = errors.New("verify: no verification evidence for that work")

// Store persists verification evidence. It is the ONLY door raw check
// output goes through: the redacted streams land here, in the local
// runtime database, and every other consumer reads the portable summary.
type Store struct {
	db  *store.DB
	now Clock
}

// NewStore binds an evidence store to the runtime database.
func NewStore(db *store.DB, now Clock) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("verify: database must not be nil")
	}
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}, nil
}

// Record persists a whole verification pass for a work, replacing any
// earlier pass. A pass supersedes rather than accumulates: stale evidence
// that is still readable is evidence someone will eventually trust.
func (s *Store) Record(ctx context.Context, workID identity.WorkID, set Set) error {
	if err := identity.ValidateUUID(string(workID)); err != nil {
		return fmt.Errorf("verify: work_id: %w", err)
	}
	if err := set.Inputs.Validate(); err != nil {
		return err
	}
	inputsJSON, err := MarshalInputs(set.Inputs)
	if err != nil {
		return err
	}
	inputsDigest, err := set.Inputs.Digest()
	if err != nil {
		return err
	}
	now := formatTime(s.now().UTC())
	return s.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM checks WHERE work_id = ?`, string(workID)); err != nil {
			return fmt.Errorf("verify: clear previous evidence for %s: %w", workID, err)
		}
		for _, r := range set.Results {
			command, err := json.Marshal(r.Spec.Command)
			if err != nil {
				return fmt.Errorf("verify: encode command of %q: %w", r.Spec.Name, err)
			}
			envNames, err := json.Marshal(r.Spec.Environment)
			if err != nil {
				return fmt.Errorf("verify: encode environment names of %q: %w", r.Spec.Name, err)
			}
			summary, err := json.Marshal(r.Summary)
			if err != nil {
				return fmt.Errorf("verify: encode summary of %q: %w", r.Spec.Name, err)
			}
			id, err := identity.NewUUID()
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO checks (id, work_id, kind, state, name, repository_id, command,
				                    working_dir, env_names, timeout_ms, exit_code, duration_ms,
				                    started_at, spec_pin, inputs, inputs_digest, summary,
				                    error, stdout, stderr, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, string(workID), "verification", string(r.Outcome), r.Spec.Name,
				string(set.Inputs.Repository), string(command), r.Spec.WorkingDir, string(envNames),
				r.Spec.Timeout.Milliseconds(), r.ExitCode, r.Duration.Milliseconds(),
				formatTime(r.StartedAt), string(r.SpecPin), string(inputsJSON), string(inputsDigest),
				string(summary), r.Error, r.Stdout, r.Stderr, now, now); err != nil {
				return fmt.Errorf("verify: record check %q: %w", r.Spec.Name, err)
			}
		}
		return nil
	})
}

// Latest returns the recorded verification pass for a work, raw streams
// included — the local view. Callers that ship evidence anywhere must call
// Set.Portable first.
func (s *Store) Latest(ctx context.Context, workID identity.WorkID) (Set, error) {
	var set Set
	err := s.db.View(ctx, func(tx *store.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT name, command, working_dir, env_names, timeout_ms, state, exit_code,
			       duration_ms, started_at, spec_pin, inputs, summary, error, stdout, stderr,
			       created_at
			  FROM checks WHERE work_id = ? ORDER BY started_at, id`, string(workID))
		if err != nil {
			return fmt.Errorf("verify: read evidence for %s: %w", workID, err)
		}
		defer rows.Close()
		var inputsJSON string
		for rows.Next() {
			var (
				r          Result
				command    string
				envNames   string
				timeoutMS  int64
				durationMS int64
				startedAt  string
				summary    string
				createdAt  string
			)
			if err := rows.Scan(&r.Spec.Name, &command, &r.Spec.WorkingDir, &envNames, &timeoutMS,
				&r.Outcome, &r.ExitCode, &durationMS, &startedAt, &r.SpecPin, &inputsJSON,
				&summary, &r.Error, &r.Stdout, &r.Stderr, &createdAt); err != nil {
				return fmt.Errorf("verify: read evidence row: %w", err)
			}
			if err := json.Unmarshal([]byte(command), &r.Spec.Command); err != nil {
				return fmt.Errorf("verify: decode command of %q: %w", r.Spec.Name, err)
			}
			if err := json.Unmarshal([]byte(envNames), &r.Spec.Environment); err != nil {
				return fmt.Errorf("verify: decode environment names of %q: %w", r.Spec.Name, err)
			}
			if err := json.Unmarshal([]byte(summary), &r.Summary); err != nil {
				return fmt.Errorf("verify: decode summary of %q: %w", r.Spec.Name, err)
			}
			r.Spec.Timeout = time.Duration(timeoutMS) * time.Millisecond
			r.Duration = time.Duration(durationMS) * time.Millisecond
			if r.StartedAt, err = parseTime(startedAt); err != nil {
				return fmt.Errorf("verify: decode start time of %q: %w", r.Spec.Name, err)
			}
			if set.At.IsZero() {
				if set.At, err = parseTime(createdAt); err != nil {
					return fmt.Errorf("verify: decode record time: %w", err)
				}
			}
			set.Results = append(set.Results, r)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("verify: read evidence for %s: %w", workID, err)
		}
		if len(set.Results) == 0 {
			return fmt.Errorf("verify: work %s: %w", workID, ErrNoEvidence)
		}
		set.Inputs, err = UnmarshalInputs([]byte(inputsJSON))
		return err
	})
	if err != nil {
		return Set{}, err
	}
	return set, nil
}

// Digests returns the digest of the recorded pass without loading its raw
// streams — the cheap identity check a checkpoint or a record needs.
func (s *Store) Digests(ctx context.Context, workID identity.WorkID) (fingerprint.Digest, error) {
	set, err := s.Latest(ctx, workID)
	if err != nil {
		return "", err
	}
	return set.Digest()
}

// Clear drops a work's recorded evidence. Repair invalidates checks, and
// invalid evidence must be gone rather than merely marked.
func (s *Store) Clear(ctx context.Context, workID identity.WorkID) error {
	return s.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM checks WHERE work_id = ?`, string(workID)); err != nil {
			return fmt.Errorf("verify: clear evidence for %s: %w", workID, err)
		}
		return nil
	})
}

// formatTime and parseTime are the package's single timestamp spelling.
func formatTime(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
