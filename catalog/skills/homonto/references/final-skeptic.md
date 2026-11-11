# Final skeptic policy

Read the policy from `homonto final-skeptic <engine> --config CONFIG`
for the selected engine (`onto` or `no`). If the command is unavailable,
use `two` and record the fallback. An invalid configuration or another command
failure is not unavailability; fix it or report a blocker.

- `off`: zero passes, intentionally disabled.
- `one`: one completed independent pass; additional distinct lenses may be used.
- `two`: two independent, fresh-context passes, with conformance and robustness
  lenses, dispatched concurrently where possible.
- `auto`: two when the full diff against the frozen base touches a
  security-sensitive surface, public API, schema or dependencies, or spans
  multiple source repositories. Otherwise zero only for documentation or prose
  with no executable instructions, configuration or behavior change; otherwise
  one. When the diff or risk cannot be determined, choose two. Higher-risk
  criteria take precedence over prose-only.

Explicit user or applicable policy requirements for independent review override
a lower count, including `off`. This setting never skips testing, verification,
or separate reviews. Supply each skeptic a complete readable pack including
candidate and base OIDs, diff, task and scope, verification commands, status and
full output, supported scenarios, non-goals, and prior findings. Confirm pack
consumption. Only literal `Status: complete` counts. Blocked or failed workers
are not unavailable; resolve evidence requests and retry. If dispatch or the
worker is unavailable, follow the shared capability policy: record the gap and
perform direct checks without claiming a completed independent pass. Explicit
independent-review requirements still block.

Record the policy, chosen and completed counts, candidate identity, each outcome,
and auto rationale or fallback in the engine's final verification record (for
`no`, the session evidence and handoff). Distinguish intentionally disabled,
completed, and unavailable. A changed candidate invalidates its verdicts: rerun
verification, reclassify `auto` on the complete new diff, and refresh every
required pass, including after integration changes the tree.
