# Choose a workflow for new work

Before creating a task, inspect the request and any recorded workspace task.
Resume a matching task in its recorded engine; ignore other installed engines
after selection. Only inspect a native inventory when selecting or resuming
that lifecycle engine. No never invokes onto/to. Do not ask
again on resume, after compaction, at a phase boundary, or on automatic preset
escalation. Several plausible matches require an identity decision first.

For genuinely new work, an explicit user choice of `no`, `to`, `onto light`, or
full `onto` (including `/to`, `/onto-light`, and `/onto-open`) selects that path.
`/onto` selects the onto family, not its preset: if the user has not chosen light
or full, ask between them.
An existing change or a binding repository policy may constrain the choice;
never silently replace an explicit choice with a different workflow. Explain
an incompatible choice and ask for a compliant path or a scope decision.

If the user has not chosen a path for new work, ask **once before task creation**
(including no work and `to new` or `onto new`) using OpenCode's built-in `question`
tool. Offer the viable choices with a short task-specific recommendation
and plain-language reasons:

- **`no`** — model-directed implementation and review, without a dedicated binary
  or lifecycle records; shared checkout and integration bookkeeping is allowed.
- **`to`** — compact plan/do/done record for bounded work; verification is
  self-asserted, without onto's evidence-backed handoff.
- **`onto light`** — a bounded bug fix (failing reproduction test first) or a
  small non-bug edit within the preset limits; an evidence-gated
  build/verify/close record without design and **without skeptics**. Apply the
  eligibility table below before recommending it.
- **Full `onto`** — work needing design, new capability, changed requirements,
  a security-sensitive surface, independent skeptic review, or a reviewable
  handoff/audit trail with full evidence gates.

Recommend based on the inspected scope and risk, not the shortest path by
default. Small work may fit either `to` or `onto light`. Show both where viable rather
than silently choosing `to`. Exclude unavailable or invalid options from the actual choice and name
why (missing framework, binding repository policy, or preset limit). Do not
install an absent framework or waive gates as a side effect of the answer. If
the selected preset later exceeds its limits, follow its normal upgrade to
full onto without another selection prompt. Engine selection is one-way for
this task: do not promote/demote into another engine. If new obligations cannot
be met in the selected engine, report the conflict and ask for a scope decision.

This is a new-change routing decision, not permission to publish, skip
verification, abandon an existing change, or repeatedly approve phases.
After the answer, load the selected dispatcher and continue normally.

## Eligibility and escalation

This table owns preset eligibility at selection and during execution. Phase
skills use it rather than introducing additional thresholds.

| Path | Fits when | Escalate to full onto when |
|---|---|---|
| `onto light` | A bug fix reproduced by a failing test, or a non-bug edit, within one module and at most five non-test files; no new capability, changed spec requirement, or security-sensitive surface | More than five non-test files (or `onto scale` measures `full`), cross-module coordination, architecture/schema/dependency changes, a new public API/capability or changed requirement, or a security-sensitive surface |
| Full `onto` | The user requests its record, or design/evidence obligations require it | Already the full path; never downgrade it automatically |
| `to` | Bounded work without a requirement for onto's design/spec/evidence gates | Ask about incompatible scope; keep this task's selected engine |
| `no` | Implementation and review without lifecycle records | Ask about incompatible scope; keep this task's selected engine |

Count source/content files, excluding tests and workflow bookkeeping. Test count
alone never forces escalation. A configuration key addition/removal is assessed
by its behavior and compatibility impact: a new public contract or migration
requires full onto; a private implementation detail need not. A small change
touching a security-sensitive surface (secret resolution, remote fetch/verify,
file deletion/pruning, permission/ownership) needs skeptic review, so it is not
light: offer full at selection, and escalate a light change that reaches one.

Assess actual dispatch capability before creation using
[execution policy](execution.md). Missing optional dispatch does not make a path
unavailable; a repository-required independent review remains a prerequisite.
Informational questions about workflows or code do not authorize creating a change.
