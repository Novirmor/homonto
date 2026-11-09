# Migration and recovery outline

- Engine/version, affected data and workload assumptions
- Current and desired schema or invariant
- Compatibility window and deploy ordering
- Preparatory checks and prerequisites
- DDL/backfill sequence, batching and restartability
- Expected locks, resource use and operational impact
- Integrity checks and old/new application verification
- Monitoring, stop conditions and partial-failure handling
- Rollback or forward-recovery procedure and data-loss limits
- Evidence gathered, untested claims and required target authorization

Keep the plan proportional to the change. Explain engine-specific behavior and
cite its source rather than applying one vendor's guarantees to another.
