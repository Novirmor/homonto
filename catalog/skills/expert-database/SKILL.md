---
name: expert-database
description: Design data models and analyze database queries, indexes, transactions, migrations and recovery using engine-specific evidence. Use for schema changes, query performance, concurrency or database reliability questions and implementation reviews.
---

# Database expertise

Use the [expert coordination contract](../expert/SKILL.md) for scope, handoffs
and engine composition.

Establish the database engine/version, deployment, schema, workload, data size
and availability constraints before relying on vendor-specific behavior. Read
relevant migrations, queries, execution plans and operational evidence. Name
missing evidence and distinguish hypotheses from measured performance problems.

## Design and investigate

Model entities, ownership, relationships, lifecycle and integrity constraints
from actual access patterns. Explain keys, nullability, uniqueness and retention.
Reason about transaction boundaries, isolation, concurrent writers and retries.
Evaluate index selectivity and ordering alongside write, storage and maintenance
costs; an index is not automatically beneficial because a column is filtered.

For tuning, connect the query and plan to cardinality, data distribution, joins,
sorting, statistics and observed latency or resource use. Propose a measurable
comparison. Do not claim an improvement without measurements on a representative
workload. Verify important engine/version-specific claims against authoritative
documentation when local evidence is insufficient.

## Plan change and recovery

Use [the migration checklist](references/migration.md) for schema or data changes.
Consider compatibility between old and new application versions, locks,
transaction duration, backfill load, replication and validation. Identify what
cannot be reversed without data loss. A backup's existence is not proof of
restorability and a down migration is not necessarily a lossless rollback.

An analysis request does not authorize connecting to a live database or running
a migration. Analysts/reviewers request execution evidence from the parent.
EXPLAIN ANALYZE may execute the query; identify the required target and impact
before asking for evidence. Keep credentials and raw sensitive records out of
shared reports.

## Implementation handoff and review

Provide the engine's writer with concrete schema/query changes, invariants,
compatibility requirements and verification cases. Review the actual migration
and application behavior, including concurrent execution and partial completion.
Return prioritized findings with evidence, failure scenarios and repairs.
