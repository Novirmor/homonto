-- 005_verification.sql — verification evidence and workflow findings.
--
-- checks gains the full evidence of one executed check: exactly what ran,
-- where, which environment NAMES were forwarded (never their values), the
-- exit status and duration, the inputs it was taken against, and both
-- layers of its output. stdout/stderr hold the redacted raw streams and
-- are LOCAL-ONLY — they never enter a checkpoint, a record, or a protocol
-- payload; summary holds the content-free portable counts and digest that
-- may travel.
--
-- findings is recreated rather than extended. The 001 shape tied every
-- finding to a check row, but the findings that actually gate a workflow
-- come from reviewer and skeptic REPORTS, which are not checks. Nothing
-- has ever written the old table, so recreating it costs no data.
--
-- repair_rounds counts consecutive failed repair rounds per work. The
-- third failure is what forces a human choice, so the counter is durable
-- state rather than something recomputed from history.

ALTER TABLE checks ADD COLUMN name TEXT;
ALTER TABLE checks ADD COLUMN repository_id TEXT;
ALTER TABLE checks ADD COLUMN command TEXT;        -- JSON argv, never a shell string
ALTER TABLE checks ADD COLUMN working_dir TEXT;    -- member-relative
ALTER TABLE checks ADD COLUMN env_names TEXT;      -- JSON array of NAMES only
ALTER TABLE checks ADD COLUMN timeout_ms INTEGER;
ALTER TABLE checks ADD COLUMN exit_code INTEGER;
ALTER TABLE checks ADD COLUMN duration_ms INTEGER;
ALTER TABLE checks ADD COLUMN started_at TEXT;     -- RFC 3339
ALTER TABLE checks ADD COLUMN spec_pin TEXT;       -- digest of the spec that ran
ALTER TABLE checks ADD COLUMN inputs TEXT;         -- JSON verify.Inputs
ALTER TABLE checks ADD COLUMN inputs_digest TEXT;
ALTER TABLE checks ADD COLUMN summary TEXT;        -- JSON portable summary
ALTER TABLE checks ADD COLUMN error TEXT;
ALTER TABLE checks ADD COLUMN stdout BLOB;         -- redacted, local-only
ALTER TABLE checks ADD COLUMN stderr BLOB;         -- redacted, local-only

CREATE INDEX checks_work_started_idx ON checks(work_id, started_at);

DROP TABLE findings;

CREATE TABLE findings (
  id             TEXT PRIMARY KEY, -- internal finding id
  work_id        TEXT NOT NULL,    -- owning identity.WorkID
  action_id      TEXT,             -- the assignment that reported it
  external_id    TEXT NOT NULL,    -- the reporter's own finding id
  role           TEXT NOT NULL,    -- reviewer or skeptic
  severity       TEXT NOT NULL,    -- critical | high | medium | low
  summary        TEXT NOT NULL,
  evidence       TEXT NOT NULL,    -- JSON array of evidence strings
  recommendation TEXT NOT NULL,
  state          TEXT NOT NULL,    -- open | fixed | accepted | withdrawn
  rationale      TEXT,             -- required to accept a blocking finding
  decision_id    TEXT,             -- the decision action that accepted it
  resolved_at    TEXT,             -- RFC 3339
  created_at     TEXT NOT NULL,    -- RFC 3339
  updated_at     TEXT NOT NULL     -- RFC 3339
);

-- One reporter id is one finding within a work: a re-reported finding
-- updates the row rather than accumulating duplicates.
CREATE UNIQUE INDEX findings_work_external_idx ON findings(work_id, external_id);
CREATE INDEX findings_work_state_idx ON findings(work_id, state);

CREATE TABLE repair_rounds (
  work_id    TEXT PRIMARY KEY, -- owning identity.WorkID
  rounds     INTEGER NOT NULL, -- consecutive failed repair rounds
  updated_at TEXT NOT NULL     -- RFC 3339
);
