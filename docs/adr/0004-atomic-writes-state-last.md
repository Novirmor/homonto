# Write files atomically and persist state after successful adapter writes

- **Status:** Superseded by 0023
- **Date:** 2026-07-03
- **Change:** homonto-v1-core

## Context

An apply interrupted mid-write (crash, ctrl-C, power loss) must never leave
a tool config half-written or homonto's state claiming work that did not
happen.

## Decision

We will write every file atomically (temp file + fsync + rename in the same
directory) and persist `.homonto/state.json` only after the tool files it records
have succeeded. State is saved after each successful adapter, so a later adapter
failure does not lose the earlier adapter's applied records.

## Consequences

- An interrupted apply leaves each tool file either old or new — never torn.
- Worst case after interruption: state may be stale for the adapter that failed
  mid-apply, while earlier successful adapters keep their records. Re-apply is
  idempotent and converges.
- Every writer must go through the shared atomic-write helper; direct
  `os.WriteFile` on managed files is a defect.
