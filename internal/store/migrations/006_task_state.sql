-- 006_task_state.sql — the Task engine's own state.
--
-- One row per Task: where it is, which generation of its inputs the
-- current position rests on, and the baseline fingerprints Reconcile
-- compares against. The recorded step is only ever trusted together with
-- that baseline, which is why they live in the same row.
--
-- actions gains the step it was issued for, so the engine can ask "are the
-- explorers done" without re-deriving it from roles and timestamps.

CREATE TABLE task_states (
  work_id    TEXT PRIMARY KEY, -- identity.WorkID
  name       TEXT NOT NULL,    -- normalized work name
  step       TEXT NOT NULL,    -- task.Step
  generation INTEGER NOT NULL, -- bumped whenever inputs are re-baselined
  baseline   TEXT NOT NULL,    -- JSON task.Baseline
  updated_at TEXT NOT NULL     -- RFC 3339
);

ALTER TABLE actions ADD COLUMN step TEXT;

CREATE INDEX actions_work_step_idx ON actions(work_id, step);

-- task_partitions records how the engine split one step's work: which
-- checklist items an assignment was issued for, in which isolation area,
-- with which scope. It is kept because the mapping cannot be re-derived
-- afterwards — once Homonto checks an item off, the "unchecked items"
-- that produced the partition are gone.

CREATE TABLE task_partitions (
  action_id TEXT PRIMARY KEY, -- the assignment issued for this partition
  work_id   TEXT NOT NULL,    -- owning identity.WorkID
  step      TEXT NOT NULL,    -- task.Step the partition was issued for
  partition TEXT NOT NULL     -- JSON task.Partition
);

CREATE INDEX task_partitions_work_step_idx ON task_partitions(work_id, step);
