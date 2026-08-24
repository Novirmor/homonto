-- 001_initial.sql — runtime schema, version 1.
--
-- Conventions: identifiers are canonical UUIDv4 TEXT as produced by
-- internal/identity; timestamps are UTC RFC 3339 (RFC3339Nano) TEXT;
-- JSON payloads are TEXT. Later migrations may add columns; this file
-- only lays the minimum viable base.

-- Machine-readable key/value workspace metadata (workspace id, schema
-- hints, tool versions).
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,  -- machine key
  value TEXT NOT NULL      -- machine value
);

-- Work units (tasks, changes) the workspace tracks.
CREATE TABLE works (
  id         TEXT PRIMARY KEY, -- identity.WorkID
  kind       TEXT NOT NULL,    -- work kind (task, change, ...)
  title      TEXT NOT NULL,    -- human-readable title
  state      TEXT NOT NULL,    -- work lifecycle state
  created_at TEXT NOT NULL,    -- RFC 3339
  updated_at TEXT NOT NULL     -- RFC 3339
);

-- Member repositories of the workspace (control plane plus members).
CREATE TABLE members (
  id         TEXT PRIMARY KEY, -- identity.RepositoryID
  role       TEXT NOT NULL,    -- control | member
  path       TEXT NOT NULL UNIQUE, -- workspace-relative control path ("." for control)
  created_at TEXT NOT NULL,    -- RFC 3339
  updated_at TEXT NOT NULL     -- RFC 3339
);

-- Journal of operations: every state transition is committed immediately,
-- so a crash leaves a row whose state drives recovery (pending → nothing
-- applied yet; prepared → effects journalled, partially applied; finalized
-- / rolled_back → terminal).
CREATE TABLE operations (
  id         TEXT PRIMARY KEY, -- identity.OperationID
  kind       TEXT NOT NULL,    -- operation kind, dispatch key for factories
  state      TEXT NOT NULL CHECK (state IN ('pending','prepared','finalized','rolled_back')),
  work_id    TEXT,             -- identity.WorkID; NULL for workspace-level operations
  generation INTEGER NOT NULL DEFAULT 1, -- optimistic-concurrency generation
  policy     TEXT NOT NULL CHECK (policy IN ('roll_forward','roll_back')),
  payload    TEXT NOT NULL,    -- JSON operation parameters
  created_at TEXT NOT NULL,    -- RFC 3339
  updated_at TEXT NOT NULL     -- RFC 3339
);

CREATE INDEX operations_state_idx ON operations(state);

-- One journaled side effect per (operation, sequence). payload is the JSON
-- returned by the effect's Prepare; state records apply progress for
-- roll-forward / roll-back.
CREATE TABLE operation_effects (
  op_id   TEXT NOT NULL REFERENCES operations(id),
  seq     INTEGER NOT NULL,    -- 1-based apply order within the operation
  kind    TEXT NOT NULL,       -- effect kind, dispatch key for recovery
  state   TEXT NOT NULL CHECK (state IN ('pending','applied','reverted')),
  payload TEXT NOT NULL,       -- JSON payload produced by Prepare
  PRIMARY KEY (op_id, seq)
);

-- Guarded actions performed during operations.
CREATE TABLE actions (
  id         TEXT PRIMARY KEY, -- identity.ActionID
  work_id    TEXT NOT NULL,    -- owning identity.WorkID
  kind       TEXT NOT NULL,    -- action kind
  state      TEXT NOT NULL,    -- action lifecycle state
  created_at TEXT NOT NULL,    -- RFC 3339
  updated_at TEXT NOT NULL     -- RFC 3339
);

-- Ordering constraints between actions (action depends on depends_on).
CREATE TABLE action_dependencies (
  action_id  TEXT NOT NULL REFERENCES actions(id),
  depends_on TEXT NOT NULL REFERENCES actions(id),
  PRIMARY KEY (action_id, depends_on)
);

-- Generated reports attached to works.
CREATE TABLE reports (
  id         TEXT PRIMARY KEY, -- report id
  work_id    TEXT NOT NULL,    -- owning identity.WorkID
  kind       TEXT NOT NULL,    -- report kind
  created_at TEXT NOT NULL,    -- RFC 3339
  updated_at TEXT NOT NULL     -- RFC 3339
);

-- Verification checks run against works.
CREATE TABLE checks (
  id         TEXT PRIMARY KEY, -- check id
  work_id    TEXT NOT NULL,    -- owning identity.WorkID
  kind       TEXT NOT NULL,    -- check kind
  state      TEXT NOT NULL,    -- check outcome state
  created_at TEXT NOT NULL,    -- RFC 3339
  updated_at TEXT NOT NULL     -- RFC 3339
);

-- Individual findings produced by checks.
CREATE TABLE findings (
  id         TEXT PRIMARY KEY, -- finding id
  check_id   TEXT NOT NULL REFERENCES checks(id),
  state      TEXT NOT NULL,    -- finding state
  created_at TEXT NOT NULL,    -- RFC 3339
  updated_at TEXT NOT NULL     -- RFC 3339
);

-- Human decisions recorded against works.
CREATE TABLE decisions (
  id         TEXT PRIMARY KEY, -- decision id
  work_id    TEXT NOT NULL,    -- owning identity.WorkID
  summary    TEXT NOT NULL,    -- decision summary
  created_at TEXT NOT NULL,    -- RFC 3339
  updated_at TEXT NOT NULL     -- RFC 3339
);

-- Atomic facts asserted about workflow entities.
CREATE TABLE facts (
  id         TEXT PRIMARY KEY, -- fact id
  subject    TEXT NOT NULL,    -- subject entity id
  predicate  TEXT NOT NULL,    -- relation name
  object     TEXT NOT NULL,    -- object entity id or literal
  created_at TEXT NOT NULL     -- RFC 3339
);

-- Edges between facts (derivation, supersession).
CREATE TABLE fact_edges (
  from_fact TEXT NOT NULL REFERENCES facts(id),
  to_fact   TEXT NOT NULL REFERENCES facts(id),
  PRIMARY KEY (from_fact, to_fact)
);

-- Exclusive leases over workspace resources, held by token.
CREATE TABLE leases (
  id         TEXT PRIMARY KEY, -- lease id
  resource   TEXT NOT NULL UNIQUE, -- leased resource name
  holder     TEXT NOT NULL,    -- holding entity id
  token      TEXT NOT NULL,    -- identity.Token proving ownership
  expires_at TEXT NOT NULL     -- RFC 3339 expiry
);

-- Append-only record of state updates already folded into the workspace.
CREATE TABLE update_journal (
  seq        INTEGER PRIMARY KEY AUTOINCREMENT,
  source     TEXT NOT NULL,    -- update source descriptor
  digest     TEXT NOT NULL,    -- fingerprint.Digest of the update payload
  applied_at TEXT NOT NULL     -- RFC 3339
);
