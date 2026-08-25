-- 004_artifact_grants.sql — the durable ledger of artifact edit grants.
--
-- One row per grant issued by artifact.Service.GrantEdit. The row is what
-- AcceptEdit trusts: the grant a host presents only identifies and
-- authenticates itself against this row, and every ownership and digest
-- check runs against the persisted fields. The freshness token itself is
-- never stored — only token_hash — so read access to the database cannot
-- mint an acceptance.
--
-- consumed_at is the single-use gate: acceptance updates the row only
-- while it is NULL, so a replayed acceptance can never apply twice.

CREATE TABLE artifact_grants (
  id            TEXT PRIMARY KEY, -- identity.ActionID naming the grant
  action_id     TEXT,             -- optional workflow action the grant serves
  work_id       TEXT NOT NULL,    -- owning identity.WorkID
  kind          TEXT NOT NULL,    -- artifact.Kind of the document
  path          TEXT NOT NULL,    -- control-root-relative document path
  owner         TEXT NOT NULL,    -- artifact.Owner the table resolved
  phase         TEXT NOT NULL,    -- artifact.Phase the grant was issued in
  regions       TEXT NOT NULL,    -- JSON array of granted region names
  meta_digest   TEXT NOT NULL,    -- digest of the immutable metadata block
  before_digest TEXT NOT NULL,    -- digest of every region outside the grant
  token_hash    TEXT NOT NULL,    -- digest of the freshness token
  issued_at     TEXT NOT NULL,    -- RFC 3339
  consumed_at   TEXT,             -- RFC 3339 once accepted; NULL while open
  result_digest TEXT              -- digest of the accepted document
);

CREATE INDEX artifact_grants_work_idx ON artifact_grants(work_id, path);
