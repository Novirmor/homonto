-- 003_assignment.sql — issued assignment actions and their submissions.
--
-- Additive only: existing columns and rows are untouched.
--
-- actions gains what the assignment store needs to issue and answer an
-- action: the role it addresses, the parallel group it was released in,
-- the JSON protocol.Action spec handed to the host, the generation of the
-- inputs the spec was captured at, and when it was answered. No freshness
-- token is stored: tokens are HMAC-SHA256 over the runtime key and the
-- action id (see handoff.IssueFreshnessToken), so they are re-derived on
-- demand and every token minted before an attach fails closed under the
-- newly minted key.
--
-- reports gains the submission linkage: which action a report answers, the
-- answering role, the raw report payload, and the input generation the
-- action was issued at. The unique index on reports.action_id is what
-- refuses a duplicate submission — one action is answered at most once.
--
-- decisions gains the same linkage for human decision gates.

ALTER TABLE actions ADD COLUMN role TEXT;
ALTER TABLE actions ADD COLUMN group_id TEXT;
ALTER TABLE actions ADD COLUMN payload TEXT;
ALTER TABLE actions ADD COLUMN generation INTEGER NOT NULL DEFAULT 1;
ALTER TABLE actions ADD COLUMN submitted_at TEXT;

CREATE INDEX actions_work_state_idx ON actions(work_id, state);
CREATE INDEX actions_work_group_idx ON actions(work_id, group_id);

ALTER TABLE reports ADD COLUMN action_id TEXT;
ALTER TABLE reports ADD COLUMN role TEXT;
ALTER TABLE reports ADD COLUMN payload TEXT;
ALTER TABLE reports ADD COLUMN inputs_generation INTEGER;

CREATE UNIQUE INDEX reports_action_id_idx ON reports(action_id);

ALTER TABLE decisions ADD COLUMN action_id TEXT;
ALTER TABLE decisions ADD COLUMN choice TEXT;
ALTER TABLE decisions ADD COLUMN rationale TEXT;
ALTER TABLE decisions ADD COLUMN answer TEXT;
ALTER TABLE decisions ADD COLUMN inputs_generation INTEGER;

CREATE UNIQUE INDEX decisions_action_id_idx ON decisions(action_id);
