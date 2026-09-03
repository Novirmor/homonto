# Adversarial verification protocol

Self-verification shares every blind spot with the implementation that
produced it. Fresh-context skeptics exist to catch what in-session review
structurally cannot (v1 precedent: two dry-run agents found 11 real
defects self-review missed).

## When

- `verify.scale: full` → REQUIRED: **at least** two skeptics, dispatched in
  parallel, after the self-evidence table is drafted. Two is the floor, not
  the ceiling — see "More than two" below.
- `verify.scale: light` → one skeptic, optional; a skip is recorded in the
  report's Adversarial section with its reason.
- No subagent capability → record "adversarial pass skipped: no dispatch
  capability" in the report's Adversarial section (protocol-mandated
  skips live there, need no acceptor); verification may still pass with
  it recorded.

## The two mandatory skeptics

Both are the **`onto-skeptic`** subagent that ships with onto — the same
agent dispatched **twice, concurrently**, with a different **lens** named
in each dispatch (`conformance` and `robustness`). It denies edits and shell
commands so parallel skeptics cannot mutate the candidate. The coordinator
runs any exact probes they request and returns the literal output for a fresh
pass.

Both get: the delta spec(s), `design.md`, repo access, and the drafted
evidence table. Both are prompted to **REFUTE, never approve** — an
approving skeptic has failed its job; "I could not refute X because
<evidence>" is the only acceptable positive form.

1. **Conformance skeptic** — attack the claims: for each scenario verdict,
   try to demonstrate the evidence doesn't hold (challenge command provenance,
   propose a different probe of the same behavior, check the implementation actually does
   what the scenario says rather than something adjacent).
2. **Robustness skeptic** — attack the gaps: edge cases the scenarios
   don't cover, drift/recovery paths, failure modes, order-dependence,
   anything a hostile-but-honest reviewer would poke.

## More than two

Skeptics deny edits and shell commands, so additional lenses race nothing and cost only
tokens. Add them when the change earns it, dispatched in the same parallel
batch and named in the report's Adversarial section like the mandatory two:

- **abuse** — drive the shipped behavior as a hostile user would: untrusted
  input, secrets, path traversal, privilege, deletion and overwrite paths. Add
  for anything touching the projection engine, remote sources, or file removal.
- **data/migration** — existing state written by an older version, partial
  writes, crash points, rollback. Add for any schema or state-format change.
- **compatibility** — take a user's existing file or command and break it: a
  public API, config key, or CLI flag that changed shape without a stated
  migration. Add for anything a user's file could reference.

Redundant lenses waste tokens and bury real findings in agreement; a lens
that cannot name what it would look for is not a lens. Two skeptics with
distinct angles beat four that overlap.

### Skeptic lenses are not reviewer lenses

`onto-reviewer` in build also works "by lens" (correctness, security,
contract/scope, clarity) and the two sets must not be confused, because they
ask different questions of different material:

| | `onto-reviewer`, in build | `onto-skeptic`, in verify |
|---|---|---|
| Material | the diff, as written | the built system, running |
| Question | is this code right? | is the evidence true? |
| Output | findings on the change | refutations of a claim |

A reviewer's `security` lens reads the new code for defects. A skeptic's
`abuse` lens tries to make the finished thing misbehave. Running one does not
discharge the other, and a skeptic dispatched with a reviewer's lens name will
re-read the diff instead of attacking the claim — which is why the names here
are deliberately different words.

## Triage (coordinator, into the report)

| Finding | Action |
|---|---|
| Refuted claim (evidence doesn't hold) | That scenario's verdict → fail; failure gate applies |
| New defect, CRITICAL (broken behavior, data loss, security) | Must fix — back through the failure gate |
| New defect, non-critical | Fix now; ask only when accepting it as a deviation is justified by a user-owned constraint |
| Unverifiable speculation | Note and dismiss with the reason |

One verify round = self-evidence + skeptics together. `onto set verify-result
<name> fail` owns the single `observed.verify_rounds` increment. Findings that
force a fix start a new round.
