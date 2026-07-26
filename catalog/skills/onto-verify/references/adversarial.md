# Adversarial verification protocol

Self-verification shares every blind spot with the implementation that
produced it. Fresh-context skeptics exist to catch what in-session review
structurally cannot (v1 precedent: two dry-run agents found 11 real
defects self-review missed).

## When

- `verify.mode: full` → REQUIRED: **at least** two skeptics, dispatched in
  parallel, after the self-evidence table is drafted. Two is the floor, not
  the ceiling — see "More than two" below.
- `verify.mode: light` → one skeptic, optional; a skip is recorded in the
  report's Adversarial section with its reason.
- No subagent capability → record "adversarial pass skipped: no dispatch
  capability" in the report's Adversarial section (protocol-mandated
  skips live there, need no acceptor); verification may still pass with
  it recorded.

## The two mandatory skeptics

Both are the **`onto-skeptic`** subagent that ships with onto — the same
agent dispatched **twice, concurrently**, with a different **lens** named
in each dispatch (`conformance` and `robustness`). It is read-only and
keeps bash on purpose: a skeptic must re-run the evidence itself, and must
never fix what it finds (that would contaminate the independent context
that makes it worth dispatching).

Both get: the delta spec(s), `design.md`, repo access, and the drafted
evidence table. Both are prompted to **REFUTE, never approve** — an
approving skeptic has failed its job; "I could not refute X because
<evidence>" is the only acceptable positive form.

1. **Conformance skeptic** — attack the claims: for each scenario verdict,
   try to demonstrate the evidence doesn't hold (re-run commands, probe
   the same behavior differently, check the implementation actually does
   what the scenario says rather than something adjacent).
2. **Robustness skeptic** — attack the gaps: edge cases the scenarios
   don't cover, drift/recovery paths, failure modes, order-dependence,
   anything a hostile-but-honest reviewer would poke.

## More than two

Skeptics are read-only, so additional lenses race nothing and cost only
tokens. Add them when the change earns it, dispatched in the same parallel
batch and named in the report's Adversarial section like the mandatory two:

- **security** — untrusted input, secrets, path traversal, privilege, deletion
  and overwrite paths. Add for anything touching the projection engine,
  remote sources, or file removal.
- **data/migration** — existing state written by an older version, partial
  writes, crash points, rollback. Add for any schema or state-format change.
- **contract** — a public API, config key, or CLI flag that changed shape
  without a stated migration. Add for anything a user's file could reference.

Redundant lenses waste tokens and bury real findings in agreement; a lens
that cannot name what it would look for is not a lens. Two skeptics with
distinct angles beat four that overlap.

## Triage (coordinator, into the report)

| Finding | Action |
|---|---|
| Refuted claim (evidence doesn't hold) | That scenario's verdict → fail; failure gate applies |
| New defect, CRITICAL (broken behavior, data loss, security) | Must fix — back through the failure gate |
| New defect, non-critical | Fix now or record as accepted deviation (user's call at the gate) |
| Unverifiable speculation | Note and dismiss with the reason |

One verify round = self-evidence + skeptics together. The verify skill's
exit checklist owns the single `metrics.verify_rounds` increment — do not
increment it here as well. Findings that force a fix start a new round.
