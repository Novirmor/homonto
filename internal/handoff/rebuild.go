package handoff

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/noviopenworks/homonto/internal/checkpoint"
	"github.com/noviopenworks/homonto/internal/identity"
	"github.com/noviopenworks/homonto/internal/operation"
	"github.com/noviopenworks/homonto/internal/store"
	"github.com/noviopenworks/homonto/internal/workspacecfg"
)

// Meta keys written by the runtime rebuild.
const (
	// MetaWorkspaceID records the owning workspace in the runtime database.
	MetaWorkspaceID = "workspace_id"
	// MetaRuntimeKey holds the runtime HMAC key. Attach mints a fresh one:
	// every action and freshness token from before the handoff was signed
	// by a key this machine never had, so old tokens fail closed.
	MetaRuntimeKey = "runtime_key"
	// MetaEvidenceStale marks every recorded evidence row as needing
	// re-verification. Written only by a forced takeover (the checkpoint
	// was consumed elsewhere and possibly worked on); engines consult it
	// before trusting any fact and must re-verify before advancing.
	MetaEvidenceStale = "evidence_stale"
)

// freshnessDomain prefixes the HMAC message of a freshness token, keeping
// tokens namespaced away from every other keyed construction.
const freshnessDomain = "homonto.v1.freshness:"

// RuntimeKey returns the runtime database's HMAC key, minting and storing
// one when absent. Attach's rebuild always mints a fresh key instead; this
// accessor serves engines and recovery tooling on an initialized runtime.
func RuntimeKey(ctx context.Context, db *store.DB) (identity.Token, error) {
	var value string
	err := db.View(ctx, func(tx *store.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key=?`, MetaRuntimeKey).Scan(&value)
	})
	if err == nil {
		token := identity.Token(value)
		if err := identity.ValidateToken(string(token)); err != nil {
			return "", fmt.Errorf("handoff: runtime key: %w", err)
		}
		return token, nil
	}
	if err.Error() == "sql: no rows in result set" {
		token := mustNewToken()
		if err := db.Update(ctx, func(tx *store.Tx) error {
			return tx.SetMeta(ctx, MetaRuntimeKey, string(token))
		}); err != nil {
			return "", fmt.Errorf("handoff: mint runtime key: %w", err)
		}
		return token, nil
	}
	return "", fmt.Errorf("handoff: read runtime key: %w", err)
}

// IssueFreshnessToken derives the freshness token of one action under key:
// HMAC-SHA256 over the freshness domain and the action id. Deterministic
// per (key, action), so re-derivation replaces storage.
func IssueFreshnessToken(key identity.Token, actionID identity.ActionID) identity.Token {
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "%s%s", freshnessDomain, actionID)
	return identity.Token(base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

// VerifyFreshnessToken reports whether token was issued for actionID under
// key, in constant time.
func VerifyFreshnessToken(key identity.Token, actionID identity.ActionID, token identity.Token) bool {
	want := IssueFreshnessToken(key, actionID)
	return hmac.Equal([]byte(want), []byte(token))
}

// RebuildRuntime recreates the local runtime projection from portable
// inputs only: the works row (state projected from the checkpoint's active
// work), members rows for every checkpoint member, source-fingerprint
// facts recorded as unverified (engines must re-verify — a fingerprint
// carried across machines is evidence of the old machine's observation,
// never of this one's), the phase fact, and a freshly minted runtime key.
//
// mappings must cover every checkpoint member including the control
// repository (Attach constructs that entry from its ControlRoot; standalone
// callers must pass it themselves). Action, report, and decision rows are
// deliberately not recreated: after attach the runtime issues fresh action
// identities and freshness tokens under the new key.
//
// The writes are idempotent upserts and inserts, so both the standalone
// call and the journaled handoff.rebuild effect converge under re-apply.
func RebuildRuntime(ctx context.Context, cfg workspacecfg.Config, cp checkpoint.Checkpoint, mappings []ConfirmedMapping) error {
	return rebuildAt(ctx, nil, cfg, cp, mappings, false)
}

// rebuildAt executes the runtime rebuild against db (opened at the control
// mapping's runtime database when db is nil). force adds the
// forced-takeover record (a decisions row whose summary encodes kind and
// rationale — the schema's decisions table carries no kind column — plus
// the evidence_stale meta key). The runtime database lives under the
// control repository, which the mappings locate (the control member's
// confirmed path).
func rebuildAt(ctx context.Context, db *store.DB, cfg workspacecfg.Config, cp checkpoint.Checkpoint, mappings []ConfirmedMapping, force bool) error {
	effective := effectiveMappings(cp, cfg, mappings)
	ownsDB := db == nil
	if ownsDB {
		controlRoot, ok := effective[cfg.Control.ID]
		if !ok {
			return fmt.Errorf("handoff: rebuild: no control mapping for %s: %w", cfg.Control.ID, ErrMappingIncomplete)
		}
		var err error
		db, err = openRuntime(ctx, controlRoot)
		if err != nil {
			return err
		}
		defer func() { _ = db.Close() }()
	}
	payload, err := buildRebuildPayload(cfg, cp, effective, force)
	if err != nil {
		return err
	}
	return applyRebuildPayload(ctx, db, &payload)
}

// rebuildPayload is the journalled identity of the runtime rebuild: every
// row it writes. Identities (ids, the runtime key) are minted once at
// prepare so recovery replays the same values; timestamps are written by
// apply, so a re-applied rebuild records fresh ones.
type rebuildPayload struct {
	Work     workRow      `json:"work"`
	Members  []memberRow  `json:"members"`
	Facts    []factRow    `json:"facts"`
	Meta     []metaEntry  `json:"meta"`
	Decision *decisionRow `json:"decision,omitempty"`
}

// workRow is the works row the rebuild projects.
type workRow struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	State string `json:"state"`
}

// memberRow is one members row the rebuild recreates.
type memberRow struct {
	ID   string `json:"id"`
	Role string `json:"role"`
	Path string `json:"path"`
}

// factRow is one facts row the rebuild asserts.
type factRow struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
}

// metaEntry is one meta row the rebuild writes.
type metaEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// decisionRow is the forced-takeover decision record.
type decisionRow struct {
	ID      string `json:"id"`
	WorkID  string `json:"work_id"`
	Summary string `json:"summary"`
}

// rebuildEffect applies the runtime rebuild inside the attach operation.
// The registered recovery prototype carries the database.
type rebuildEffect struct {
	payload rebuildPayload
	db      *store.DB
}

func (e *rebuildEffect) Kind() string { return kindRebuild }

func (e *rebuildEffect) Prepare(ctx context.Context) (any, error) { return e.payload, nil }

func (e *rebuildEffect) Apply(ctx context.Context, rec operation.EffectRecord) error {
	var p rebuildPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode rebuild payload of effect %d: %w", rec.Seq, err)
	}
	return applyRebuildPayload(ctx, e.db, &p)
}

func (e *rebuildEffect) Revert(ctx context.Context, rec operation.EffectRecord) error {
	var p rebuildPayload
	if err := json.Unmarshal(rec.Payload, &p); err != nil {
		return fmt.Errorf("handoff: decode rebuild payload of effect %d: %w", rec.Seq, err)
	}
	return revertRebuildPayload(ctx, e.db, &p)
}

// buildRebuildPayload derives every row the rebuild writes.
func buildRebuildPayload(cfg workspacecfg.Config, cp checkpoint.Checkpoint, mappings map[identity.RepositoryID]string, force bool) (rebuildPayload, error) {
	payload := rebuildPayload{
		Work: workRow{
			ID:    string(cp.Work.ID),
			Kind:  string(cp.Work.Workflow),
			Title: cp.Work.Name,
			State: "active",
		},
	}
	controlRoot := mappings[cfg.Control.ID]
	for _, m := range sortedCheckpointMembers(cp) {
		root, ok := mappings[m.ID]
		if !ok {
			return rebuildPayload{}, fmt.Errorf("handoff: rebuild: member %s has no confirmed mapping: %w", m.ID, ErrMappingIncomplete)
		}
		role := "member"
		if m.ID == cfg.Control.ID {
			role = "control"
		}
		payload.Members = append(payload.Members, memberRow{
			ID: string(m.ID), Role: role, Path: memberDisplayPath(root, controlRoot),
		})
		payload.Facts = append(payload.Facts,
			factRow{mustUUID(), string(m.ID), "source_fingerprint", string(m.SourceFingerprint)},
			factRow{mustUUID(), string(m.ID), "source_fingerprint_state", "unverified"},
		)
	}
	payload.Facts = append(payload.Facts,
		factRow{mustUUID(), string(cp.Work.ID), "phase", cp.Work.Phase},
	)
	payload.Meta = []metaEntry{
		{Key: MetaWorkspaceID, Value: string(cp.WorkspaceID)},
		{Key: MetaRuntimeKey, Value: string(mustNewToken())},
	}
	if force {
		// The decisions table has no kind or rationale columns, so the
		// summary encodes both: "forced_takeover: force-attach".
		payload.Decision = &decisionRow{
			ID:      mustUUID(),
			WorkID:  string(cp.Work.ID),
			Summary: "forced_takeover: force-attach",
		}
		payload.Meta = append(payload.Meta, metaEntry{Key: MetaEvidenceStale, Value: "1"})
	}
	return payload, nil
}

// applyRebuildPayload writes every row of the payload in one transaction,
// idempotently (upserts and insert-or-ignore keyed by the journalled ids).
func applyRebuildPayload(ctx context.Context, db *store.DB, p *rebuildPayload) error {
	return db.Update(ctx, func(tx *store.Tx) error {
		if err := tx.UpsertWork(ctx, store.WorkRecord{
			ID: identity.WorkID(p.Work.ID), Kind: p.Work.Kind, Title: p.Work.Title,
			State: p.Work.State, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
		}); err != nil {
			return err
		}
		for _, m := range p.Members {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO members (id, role, path, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT(id) DO UPDATE SET role=excluded.role, path=excluded.path, updated_at=excluded.updated_at`,
				m.ID, m.Role, m.Path, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("store: upsert member %s: %w", m.ID, err)
			}
		}
		for _, f := range p.Facts {
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO facts (id, subject, predicate, object, created_at)
				VALUES (?, ?, ?, ?, ?)`,
				f.ID, f.Subject, f.Predicate, f.Object, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("store: insert fact %s: %w", f.ID, err)
			}
		}
		for _, m := range p.Meta {
			if err := tx.SetMeta(ctx, m.Key, m.Value); err != nil {
				return err
			}
		}
		if p.Decision != nil {
			if _, err := tx.ExecContext(ctx, `
				INSERT OR IGNORE INTO decisions (id, work_id, summary, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?)`,
				p.Decision.ID, p.Decision.WorkID, p.Decision.Summary,
				time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				return fmt.Errorf("store: insert decision %s: %w", p.Decision.ID, err)
			}
		}
		return nil
	})
}

// revertRebuildPayload deletes exactly the rows the payload wrote, leaving
// any pre-existing rows other runs contributed untouched (facts are keyed
// by journalled id; members and works by the payload's explicit ids).
func revertRebuildPayload(ctx context.Context, db *store.DB, p *rebuildPayload) error {
	return db.Update(ctx, func(tx *store.Tx) error {
		for _, f := range p.Facts {
			if _, err := tx.ExecContext(ctx, `DELETE FROM facts WHERE id=?`, f.ID); err != nil {
				return fmt.Errorf("store: delete fact %s: %w", f.ID, err)
			}
		}
		for _, m := range p.Members {
			if _, err := tx.ExecContext(ctx, `DELETE FROM members WHERE id=? AND path=?`, m.ID, m.Path); err != nil {
				return fmt.Errorf("store: delete member %s: %w", m.ID, err)
			}
		}
		for _, m := range p.Meta {
			if _, err := tx.ExecContext(ctx, `DELETE FROM meta WHERE key=? AND value=?`, m.Key, m.Value); err != nil {
				return fmt.Errorf("store: delete meta %s: %w", m.Key, err)
			}
		}
		if p.Decision != nil {
			if _, err := tx.ExecContext(ctx, `DELETE FROM decisions WHERE id=?`, p.Decision.ID); err != nil {
				return fmt.Errorf("store: delete decision %s: %w", p.Decision.ID, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM works WHERE id=?`, p.Work.ID); err != nil {
			return fmt.Errorf("store: delete work %s: %w", p.Work.ID, err)
		}
		return nil
	})
}

// effectiveMappings indexes the confirmed mappings by repository id,
// validating coverage of every checkpoint member (the control repository
// maps to the attach's control root; Attach adds it internally and rejects
// a human mapping that names a different path).
func effectiveMappings(cp checkpoint.Checkpoint, cfg workspacecfg.Config, mappings []ConfirmedMapping) map[identity.RepositoryID]string {
	out := make(map[identity.RepositoryID]string, len(mappings))
	for _, m := range mappings {
		out[m.RepositoryID] = m.Path
	}
	return out
}

// sortedCheckpointMembers returns the checkpoint's members sorted by id.
func sortedCheckpointMembers(cp checkpoint.Checkpoint) []checkpoint.Member {
	out := append([]checkpoint.Member(nil), cp.Members...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// memberDisplayPath renders a member's recorded location: relative to the
// control root when inside it, absolute otherwise. The members table's path
// column is UNIQUE, and distinct member roots keep it so either way.
func memberDisplayPath(memberRoot, controlRoot string) string {
	rel, err := filepath.Rel(controlRoot, memberRoot)
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	return memberRoot
}

// mustUUID mints a canonical UUID string.
func mustUUID() string {
	id, err := identity.NewUUID()
	if err != nil {
		panic(fmt.Sprintf("handoff: mint uuid: %v", err))
	}
	return id
}
