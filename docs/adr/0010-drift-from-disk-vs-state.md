# Compute drift from disk-vs-state, not from the desired plan

- **Status:** Superseded by 0023
- **Date:** 2026-07-05
- **Change:** state-source-of-truth

## Context

`engine.Drift` reuses `Plan()` and treats an `update`/`create` on a
state-recorded key as drift. But `Plan()` decides non-secret actions by
comparing disk against the current **desired** value (`homonto.toml`), not
against the last-applied `Entry.Applied` hash. So editing `homonto.toml`
surfaces a key as "drifted" even when its on-disk value is unchanged since the
last apply. `status` conflates un-applied config edits with real out-of-band
disk drift. (The secret branch already compares against `Entry.Applied`; only
non-secret keys are leaky.)

Reusing the desired-centric plan is the root cause. The disk values needed for
a true disk-vs-state comparison include resolved secret plaintext, which should
not leave the adapter in raw form.

Alternatives considered: threading a `Change.DiskHash` through every Change and
keeping drift Plan-derived (leaves drift structurally coupled to Plan, and
de-declared/orphan keys have no clean disk hash); a full adapter-owned status
report (duplicates the drift-vs-pending policy per adapter).

## Decision

We will add a narrow, secret-safe adapter method
`ObserveHashes(st) (map[string]string, error)` returning
`key -> sha256(canonical(on-disk value))` for each state-recorded key still
present on disk (recorded-but-absent keys are omitted). Only hashes leave the
adapter. `engine.Status()` compares each hash to `Entry.Applied`: mismatch →
drifted, absent → missing. It reports **pending** separately as the count of
`Plan()` visible changes whose key is not itself drifted — i.e. config edits
whose disk still matches the last apply. `homonto status` prints drift lines
plus, when pending > 0, `N config change(s) awaiting apply`.

## Consequences

- `status` becomes a true disk-vs-last-applied comparator — closing NEXT_AGENT
  gap #2 — while still surfacing un-applied config edits, now labelled as
  pending rather than drift.
- Drift is decoupled from `Plan()`; the two questions ("what does apply want to
  do" vs "did disk change under us") no longer share one comparison.
- Disk values (incl. resolved secrets) stay inside the adapter; the engine sees
  only hashes, consistent with ADR 0002.
- Two disk reads on `status` (Plan + ObserveHashes); acceptable — status is not
  a hot path.
- A new `Engine.Status()` supersedes the old `Engine.Drift()` shape; the
  `Drift` name is retained internally or replaced (build decides).
