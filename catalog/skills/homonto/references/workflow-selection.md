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
`onto new`**. Offer the viable choices with a short task-specific recommendation
and plain-language reasons:

- **`to`** — compact plan/do/done record for bounded solo work; verification is
  self-asserted, without onto's evidence-backed handoff.
- **`onto fix`** — broken behavior with a reproducible failing test; an
  evidence-gated fix/verify/close record, without a design phase.
- **`onto tweak`** — small non-bug edit (including a small feature) within the
  preset limits: at most five non-test files, no new capability or change to an
  existing spec requirement; evidence-gated verify/close without design.
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
