-- 008_work_units.sql — one table for both engines' unit records.
--
-- The Task engine recorded how it split a step's work in task_partitions.
-- The Change engine needs exactly the same record for exactly the same
-- reason — once Homonto checks an item off, the "unchecked items" that
-- produced the split no longer exist — so the table is renamed rather than
-- duplicated. A second table of identical shape would drift.

ALTER TABLE task_partitions RENAME TO work_units;

DROP INDEX IF EXISTS task_partitions_work_step_idx;

CREATE INDEX work_units_work_step_idx ON work_units(work_id, step);
