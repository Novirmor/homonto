# Allow draft PRs at any phase

- **Status:** Accepted
- **Date:** 2026-10-29

## Context

A user asked for a way to "push the PR even though the skeptics disagree".
`/onto-bypass --to close` cannot do it: `onto close` still refuses without a
passing verify result, and `--to archive` opens no PR. The publication policy
only allowed a PR after verification, so work blocked on unresolved findings
had no visible PR at all.

## Decision

We will let the coordinator open a draft PR at any phase, whatever the verify
result or the skeptics say, instead of weakening a gate. The draft pushes
committed work on the change's own branch without force, lists the phase,
verify result and open findings in its body, and records nothing: no receipt,
no phase change, no gate waiver. At close the verified candidate is delivered
to the same branch, the draft is reused as the exact PR match and marked ready.
It is never marked ready before verification passes.

We rejected letting a bypass satisfy `onto close`: it would turn an audited
escape hatch into a merge route and break the rule that close needs a pass.

## Consequences

Work in progress becomes reviewable on GitHub early, and the skeptic gate stays
intact for the ready PR. Unverified code is now public on a draft branch, so a
draft must never be mistaken for delivery; its body says what is still open.
`h-resolve-issue` gains an explicit exception to "no push before verification"
for the draft. Engine-free `h-continue-pr` is unchanged: it pushes to an
existing PR only verified commits.
