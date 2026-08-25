// Package assignment owns issued workflow actions: their durable state,
// the maximal parallel groups the scheduler releases, and the submissions
// that answer them. It is the freshness authority the protocol package
// deliberately is not — protocol validates the SHAPE of a submission, this
// package decides whether the action it answers is still live.
//
// # Freshness
//
// An action's freshness token is HMAC-SHA256 over the runtime key and the
// action id (handoff.IssueFreshnessToken), so no token is ever stored: it
// is re-derived on demand, and every token minted before an attach fails
// closed because attach mints a new runtime key. Staleness is therefore
// carried by STATE, not by the token — an invalidated action keeps a
// derivable token but refuses every submission, and re-issuing work after
// invalidation mints new action ids, so an old id can never be answered.
//
// # Groups
//
// The scheduler releases the MAXIMAL set of ready actions at once: every
// pending action whose dependencies are all answered goes out in one
// parallel group, so hosts run assignments concurrently instead of being
// drip-fed. Human decisions are the exception — a ready decision blocks
// the workflow and is released alone, because the protocol's blocked state
// carries exactly one action and silence is never approval. Actions that
// are not yet ready are deferred to a later group; re-asking for the ready
// group while one is outstanding returns that same group with the same
// action ids and tokens, so `homonto next` is safe to repeat.
package assignment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/noviopenworks/homonto/internal/handoff"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/protocol"
	"github.com/noviopenworks/homonto/internal/store"
)

// State is an action's lifecycle state.
type State string

const (
	// StatePending: created, not yet released to any host.
	StatePending State = "pending"
	// StateIssued: released in a parallel group and awaiting an answer.
	StateIssued State = "issued"
	// StateSubmitted: answered by a report or a decision.
	StateSubmitted State = "submitted"
	// StateInvalidated: superseded — its inputs changed under it. An
	// invalidated action refuses every submission and is never re-issued;
	// the engine mints a fresh action instead.
	StateInvalidated State = "invalidated"
)

// known reports whether s is a persisted state.
func (s State) known() bool {
	switch s {
	case StatePending, StateIssued, StateSubmitted, StateInvalidated:
		return true
	}
	return false
}

// Typed errors. Callers branch with errors.Is.
var (
	// ErrUnknownAction: no action with that id exists.
	ErrUnknownAction = errors.New("assignment: no such action")
	// ErrNotIssued: the action is not awaiting an answer — it is still
	// pending, already answered, or invalidated.
	ErrNotIssued = errors.New("assignment: action is not awaiting an answer")
	// ErrStaleToken: the freshness token was not issued for that action
	// under this runtime key.
	ErrStaleToken = errors.New("assignment: freshness token is stale")
	// ErrWrongRole: the submission's role is not the role the assignment
	// addresses.
	ErrWrongRole = errors.New("assignment: submission role does not match the assignment")
	// ErrDuplicateSubmission: the action was already answered.
	ErrDuplicateSubmission = errors.New("assignment: action was already answered")
	// ErrUnsatisfiable: pending actions remain but none can ever become
	// ready — a dependency cycle or a dependency on an invalidated action.
	ErrUnsatisfiable = errors.New("assignment: no pending action can become ready")
	// ErrKindMismatch: a report answered a decision, or a decision
	// answered an assignment.
	ErrKindMismatch = errors.New("assignment: submission kind does not match the action")
)

// Clock is the store's time source; tests inject a fixed one.
type Clock func() time.Time

// Action is one persisted action: its identity, the work it belongs to,
// its lifecycle state, the generation of the inputs it was specified at,
// and the wire spec handed to hosts.
type Action struct {
	ID           identity.ActionID
	WorkID       identity.WorkID
	Kind         protocol.ActionKind
	Role         protocol.Role
	State        State
	GroupID      identity.ParallelGroupID
	Step         string
	Generation   int64
	Dependencies []identity.ActionID
	Spec         protocol.Action
	CreatedAt    time.Time
	UpdatedAt    time.Time
	SubmittedAt  time.Time
}

// Spec describes an action to create: which work it serves, the generation
// of the inputs it was derived from, which actions must be answered first,
// and the wire action itself. Template's ID, FreshnessToken, GroupID, and
// Dependencies are assigned by the store and must be left zero.
type Spec struct {
	WorkID identity.WorkID
	// Step is the engine step the action was issued for. The engine uses
	// it to ask "is this step's work answered" without re-deriving the
	// answer from roles and timestamps.
	Step         string
	Generation   int64
	Dependencies []identity.ActionID
	Template     protocol.Action
}

// Store persists actions and their submissions in the runtime database.
type Store struct {
	db  *store.DB
	key identity.Token
	now Clock
}

// NewStore binds an assignment store to the runtime database, loading the
// runtime HMAC key that every freshness token derives from (minting one on
// a database that has none yet).
func NewStore(ctx context.Context, db *store.DB, now Clock) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("assignment: database must not be nil")
	}
	key, err := handoff.RuntimeKey(ctx, db)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, key: key, now: now}, nil
}

// Token returns the freshness token of an action under this runtime's key.
func (s *Store) Token(id identity.ActionID) identity.Token {
	return handoff.IssueFreshnessToken(s.key, id)
}

// Create records a new pending action and returns it with its assigned id
// and freshness token. The wire spec is validated before it is stored, so
// no unissuable action ever reaches the database.
func (s *Store) Create(ctx context.Context, spec Spec) (Action, error) {
	if err := identity.ValidateUUID(string(spec.WorkID)); err != nil {
		return Action{}, fmt.Errorf("assignment: work_id: %w", err)
	}
	if spec.Generation < 1 {
		return Action{}, fmt.Errorf("assignment: generation %d must be at least 1", spec.Generation)
	}
	if spec.Template.ID != "" || spec.Template.FreshnessToken != "" ||
		spec.Template.ParallelGroupID != "" || len(spec.Template.Dependencies) != 0 {
		return Action{}, fmt.Errorf("assignment: template must leave id, token, group, and dependencies unset")
	}
	id, err := identity.NewActionID()
	if err != nil {
		return Action{}, err
	}
	wire := spec.Template
	wire.ID = id
	wire.FreshnessToken = s.Token(id)
	wire.Dependencies = append([]identity.ActionID(nil), spec.Dependencies...)
	if err := wire.Validate(); err != nil {
		return Action{}, fmt.Errorf("assignment: action spec: %w", err)
	}

	now := s.now().UTC()
	stored := wire
	stored.FreshnessToken = "" // derived, never persisted
	payload, err := json.Marshal(stored)
	if err != nil {
		return Action{}, fmt.Errorf("assignment: encode action spec: %w", err)
	}
	var role any
	if wire.Role != "" {
		role = string(wire.Role)
	}
	err = s.db.Update(ctx, func(tx *store.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO actions (id, work_id, kind, role, state, step, generation, payload, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			string(id), string(spec.WorkID), string(wire.Kind), role, string(StatePending),
			spec.Step, spec.Generation, string(payload), formatTime(now), formatTime(now)); err != nil {
			return fmt.Errorf("assignment: insert action %s: %w", id, err)
		}
		for _, dep := range spec.Dependencies {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO action_dependencies (action_id, depends_on) VALUES (?, ?)`,
				string(id), string(dep)); err != nil {
				return fmt.Errorf("assignment: link action %s to %s: %w", id, dep, err)
			}
		}
		return nil
	})
	if err != nil {
		return Action{}, err
	}
	return Action{
		ID:           id,
		WorkID:       spec.WorkID,
		Kind:         wire.Kind,
		Role:         wire.Role,
		State:        StatePending,
		Step:         spec.Step,
		Generation:   spec.Generation,
		Dependencies: wire.Dependencies,
		Spec:         wire,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

// Action returns one action by id.
func (s *Store) Action(ctx context.Context, id identity.ActionID) (Action, error) {
	var act Action
	err := s.db.View(ctx, func(tx *store.Tx) error {
		var err error
		act, err = loadAction(ctx, tx, id)
		return err
	})
	if err != nil {
		return Action{}, err
	}
	act.Spec.FreshnessToken = s.Token(act.ID)
	return act, nil
}

// Actions returns every action of a work, oldest first.
func (s *Store) Actions(ctx context.Context, workID identity.WorkID) ([]Action, error) {
	var out []Action
	err := s.db.View(ctx, func(tx *store.Tx) error {
		var err error
		out, err = loadActions(ctx, tx, workID)
		return err
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Spec.FreshnessToken = s.Token(out[i].ID)
	}
	return out, nil
}

// Invalidate marks actions superseded. Only pending and issued actions can
// be invalidated: an answered action's report is durable evidence, and
// re-invalidating an invalidated one is a no-op, so Invalidate converges
// under re-application.
func (s *Store) Invalidate(ctx context.Context, ids ...identity.ActionID) error {
	if len(ids) == 0 {
		return nil
	}
	now := formatTime(s.now().UTC())
	return s.db.Update(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, `
				UPDATE actions SET state = ?, updated_at = ?
				 WHERE id = ? AND state IN (?, ?)`,
				string(StateInvalidated), now, string(id),
				string(StatePending), string(StateIssued)); err != nil {
				return fmt.Errorf("assignment: invalidate action %s: %w", id, err)
			}
		}
		return nil
	})
}

// loadAction reads one action row and its dependencies.
func loadAction(ctx context.Context, tx *store.Tx, id identity.ActionID) (Action, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT id, work_id, kind, role, state, group_id, step, generation, payload,
		       created_at, updated_at, submitted_at
		  FROM actions WHERE id = ?`, string(id))
	act, err := scanAction(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return Action{}, fmt.Errorf("assignment: action %s: %w", id, ErrUnknownAction)
	}
	if err != nil {
		return Action{}, fmt.Errorf("assignment: read action %s: %w", id, err)
	}
	act.Dependencies, err = loadDependencies(ctx, tx, id)
	if err != nil {
		return Action{}, err
	}
	act.Spec.Dependencies = act.Dependencies
	return act, nil
}

// loadActions reads every action of a work with its dependencies.
func loadActions(ctx context.Context, tx *store.Tx, workID identity.WorkID) ([]Action, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, work_id, kind, role, state, group_id, step, generation, payload,
		       created_at, updated_at, submitted_at
		  FROM actions WHERE work_id = ? ORDER BY created_at, id`, string(workID))
	if err != nil {
		return nil, fmt.Errorf("assignment: list actions of %s: %w", workID, err)
	}
	defer rows.Close()
	var out []Action
	for rows.Next() {
		act, err := scanAction(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("assignment: read action row: %w", err)
		}
		out = append(out, act)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("assignment: list actions of %s: %w", workID, err)
	}
	for i := range out {
		deps, err := loadDependencies(ctx, tx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Dependencies = deps
		out[i].Spec.Dependencies = deps
	}
	return out, nil
}

// scanAction decodes one action row through the given Scan function.
func scanAction(scan func(...any) error) (Action, error) {
	var (
		act         Action
		role        sql.NullString
		groupID     sql.NullString
		step        sql.NullString
		payload     sql.NullString
		createdAt   string
		updatedAt   string
		submittedAt sql.NullString
	)
	if err := scan(&act.ID, &act.WorkID, &act.Kind, &role, &act.State, &groupID, &step,
		&act.Generation, &payload, &createdAt, &updatedAt, &submittedAt); err != nil {
		return Action{}, err
	}
	if !act.State.known() {
		return Action{}, fmt.Errorf("action %s carries unknown state %q", act.ID, act.State)
	}
	act.Role = protocol.Role(role.String)
	act.GroupID = identity.ParallelGroupID(groupID.String)
	act.Step = step.String
	if payload.Valid {
		if err := json.Unmarshal([]byte(payload.String), &act.Spec); err != nil {
			return Action{}, fmt.Errorf("decode action %s spec: %w", act.ID, err)
		}
	}
	if act.Spec.ID != act.ID {
		return Action{}, fmt.Errorf("action %s carries a spec for %s", act.ID, act.Spec.ID)
	}
	act.Spec.ParallelGroupID = groupID.String
	var err error
	if act.CreatedAt, err = parseTime(createdAt); err != nil {
		return Action{}, fmt.Errorf("action %s created_at: %w", act.ID, err)
	}
	if act.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Action{}, fmt.Errorf("action %s updated_at: %w", act.ID, err)
	}
	if submittedAt.Valid {
		if act.SubmittedAt, err = parseTime(submittedAt.String); err != nil {
			return Action{}, fmt.Errorf("action %s submitted_at: %w", act.ID, err)
		}
	}
	return act, nil
}

// loadDependencies reads the actions one action waits on.
func loadDependencies(ctx context.Context, tx *store.Tx, id identity.ActionID) ([]identity.ActionID, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT depends_on FROM action_dependencies WHERE action_id = ? ORDER BY depends_on`, string(id))
	if err != nil {
		return nil, fmt.Errorf("assignment: read dependencies of %s: %w", id, err)
	}
	defer rows.Close()
	var deps []identity.ActionID
	for rows.Next() {
		var dep identity.ActionID
		if err := rows.Scan(&dep); err != nil {
			return nil, fmt.Errorf("assignment: read dependency of %s: %w", id, err)
		}
		deps = append(deps, dep)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("assignment: read dependencies of %s: %w", id, err)
	}
	return deps, nil
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
