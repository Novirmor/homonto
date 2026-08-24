-- 002_effect_failed.sql — terminal failed state for effect rows, version 2.
--
-- An Apply that returns an error is terminal for that effect: Run journals
-- the row 'failed' and switches the operation to roll_back, so crash
-- recovery never re-applies it. SQLite cannot alter a CHECK constraint in
-- place, so the table is rebuilt with the widened state set and the
-- existing rows are carried over unchanged.

CREATE TABLE operation_effects_v2 (
  op_id   TEXT NOT NULL REFERENCES operations(id),
  seq     INTEGER NOT NULL,    -- 1-based apply order within the operation
  kind    TEXT NOT NULL,       -- effect kind, dispatch key for recovery
  state   TEXT NOT NULL CHECK (state IN ('pending','applied','reverted','failed')),
  payload TEXT NOT NULL,       -- JSON payload produced by Prepare
  PRIMARY KEY (op_id, seq)
);

INSERT INTO operation_effects_v2 (op_id, seq, kind, state, payload)
  SELECT op_id, seq, kind, state, payload FROM operation_effects;

DROP TABLE operation_effects;

ALTER TABLE operation_effects_v2 RENAME TO operation_effects;
