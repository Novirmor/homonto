-- 007_change_state.sql — the Change engine's state.
--
-- change_preflights holds the LOCAL classification candidates. A candidate
-- is not a change: it has a work id so its read-only assignments can be
-- addressed, but no documents and no portable record. Confirming one
-- creates the change_states row and the documents; abandoning one leaves
-- the repository exactly as it was.
--
-- change_states holds the confirmed changes: which path, where in it, and
-- the baseline that position rests on. The work baseline inside it is the
-- immutable one the preset scope count is measured from — captured once,
-- at confirmation, and never moved by continuation, repair, or a later
-- path reconfirmation.

CREATE TABLE change_preflights (
  work_id    TEXT PRIMARY KEY, -- identity.WorkID
  name       TEXT NOT NULL,    -- the proposed work name
  request    TEXT NOT NULL,    -- the human's description of the work
  step       TEXT NOT NULL,    -- change.PreflightStep
  generation INTEGER NOT NULL,
  suggestion TEXT NOT NULL,    -- JSON change.Suggestion
  updated_at TEXT NOT NULL     -- RFC 3339
);

CREATE TABLE change_states (
  work_id       TEXT PRIMARY KEY, -- identity.WorkID
  name          TEXT NOT NULL,    -- normalized work name
  path          TEXT NOT NULL,    -- change.Path
  step          TEXT NOT NULL,    -- the path's own step vocabulary
  upgraded_from TEXT,             -- the preset a Full change was upgraded from
  generation    INTEGER NOT NULL,
  baseline      TEXT NOT NULL,    -- JSON change.Baseline
  updated_at    TEXT NOT NULL     -- RFC 3339
);

-- Names are indexed for lookup but NOT unique: a name is reusable once its
-- change is archived, which is why the archive resolves same-day
-- collisions with a suffix. "One ACTIVE change per name" is a rule about
-- steps, and only the engine knows which steps are terminal.
CREATE INDEX change_states_name_idx ON change_states(name);
