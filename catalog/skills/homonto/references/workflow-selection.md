# Choose a workflow for new work

Before creating a change, inspect both workflow inventories and the request.
Resume a unique matching active change in its recorded workflow; do not ask
again on resume, after compaction, at a phase boundary, or on automatic preset
escalation. Several plausible matches require an identity decision first.

For genuinely new work, an explicit user choice of `to`, `onto fix`, `onto
tweak`, or full `onto` (including `/to`, `/onto-fix`, `/onto-tweak`, and
`/onto-open`) selects that path. `/onto` selects the onto family, not its
preset: if the user has not chosen fix, tweak, or full, ask among those three.
An existing change or a binding repository policy may constrain the choice;
never silently replace an explicit choice with a different workflow. Explain
an incompatible choice and ask for a compliant path or a scope decision.

If the user has not chosen a path for new work, ask **once before `to new` or
`onto new`** using OpenCode's built-in `question` tool. Offer the viable choices with a short task-specific recommendation
and plain-language reasons:

- **`to`** — compact plan/do/done record for bounded solo work; verification is
  self-asserted, without onto's evidence-backed handoff.
- **`onto fix`** — broken behavior with a reproducible failing test; an
  evidence-gated fix/verify/close record, without a design phase.
- **`onto tweak`** — small non-bug edit (including a small feature) within the
  preset limits: at most five non-test files, no new capability or change to an
  existing spec requirement; evidence-gated verify/close without design. Apply
  the eligibility table below before recommending either preset.
- **Full `onto`** — work needing design, new capability, changed requirements,
  or a reviewable handoff/audit trail with full evidence gates.

Recommend based on the inspected scope and risk, not the shortest path by
default. A small bug may fit either `to` or `onto fix`; a small non-bug edit may
fit `to` or `onto tweak`. Show both where viable rather than silently choosing
`to`. Exclude unavailable or invalid options from the actual choice and name
why (missing framework, binding repository policy, or preset limit). Do not
install an absent framework or waive gates as a side effect of the answer. If
the selected preset later exceeds its limits, follow its normal upgrade to
full onto without another selection prompt; registered bindings may block
cross-framework conversion, so assess fit before allocating one.

This is a new-change routing decision, not permission to publish, skip
verification, abandon an existing change, or repeatedly approve phases.
After the answer, load the selected dispatcher and continue normally.

## Eligibility and escalation

This table owns preset eligibility at selection and during execution. Phase
skills use it rather than introducing additional thresholds.

| Path | Fits when | Escalate to full onto when |
|---|---|---|
| `onto fix` | Reproduces broken existing behavior with a failing test; bounded repair within one module and at most five non-test files | More than five non-test files, cross-module coordination, architecture/schema/dependency changes, or a new public API/capability |
| `onto tweak` | Non-bug edit within one module and at most five non-test files; no new capability or changed spec requirement | More than five non-test files, cross-module coordination, architecture/schema/dependency changes, or a new capability/changed requirement |
| Full `onto` | The user requests its record, or design/evidence obligations require it | Already the full path; never downgrade it automatically |
| `to` | Bounded work without a requirement for onto's design/spec/evidence gates | Reassess fit when those obligations emerge; use supported conversion, respecting binding restrictions |

Count source/content files, excluding tests and workflow bookkeeping. Test count
alone never forces escalation. A configuration key addition/removal is assessed
by its behavior and compatibility impact: a new public contract or migration
requires full onto; a private implementation detail need not. A small change
touching a security-sensitive surface needs full verification even if its preset
still fits; use `onto-verify`'s risk override, not an automatic workflow downgrade.

Assess actual dispatch capability before creation using
[execution policy](execution.md). Missing optional dispatch does not make a path
unavailable; a repository-required independent review remains a prerequisite.
Informational questions about workflows or code do not authorize creating a change.
