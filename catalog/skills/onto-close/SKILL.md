---
name: onto-close
description: Finish an onto change with current passing verification. Validate the close plan, merge spec deltas, promote ADRs, resolve guides, archive, and complete the recorded source integration.
---

# onto-close — Close

## Purpose and entry

Land the verified change's durable knowledge, archive its record, and complete
source integration. Follow [autonomy](../homonto/references/autonomy.md),
[workspace policy](../homonto/references/workspace-policy.md), and
[verified source publication](../homonto/references/publication.md).
All workflow calls that accept it retain `--dir "<configRoot>"`.

Entry requires recorded close and a current `verification.md` with exactly one
canonical passing `Result:` line, optionally `pass (N accepted deviations)`.
Read state and notes. Otherwise return to the dispatcher. An in-scope defect
before archive follows [active repair](../onto/references/recovery.md#active-repairs-versus-new-work),
including fix/tweak's own build steps; do not ask merely whether to repair it.

## Required inputs

- Current report/receipts, proposal, task list, and full-workflow design/plan.
- Any delta specs and ADR drafts; current living specs, ADR numbers and guides.
- Recorded source candidates, per-repo targets, integration mode and authorization.
- Validated execution/receiver roots and existing dirty-work decisions.

## Ordered actions

### 1. Prepare

Run [lint-checklist.md](references/lint-checklist.md) sections 0–2. Findings block
close; presets use their reduced proposal and inline task contracts. Missing
deltas need an explicit no-spec justification or the missing contract, never silence.

Execute only non-runtime `DEFERRED to close:` tasks, recording evidence and
rewriting each to `- [x] N.N (deferred, done at close YYYY-MM-DD): <desc> [trace #K]`.
Runtime changes require active repair and verification before returning here.

Resolve guides for full changes: update affected guides/README and record
`onto set guides <name> updated`, or obtain an explicit waiver through the built-in
`question` tool before recording `waived: <reason>`. Presets have no new guides
obligation but must resolve a carried pending value.

Honor recorded integration or intake intent. Otherwise use `pr` when repository
policy requires remote review, and local `merge` by default. Record
`onto set integration <name> merge|pr` while active. Validate each immutable
`repo_bases` target (legacy scalar `base_branch` when applicable); a commit-valued
diff base is not an integration branch. Never retarget state at close.

Identify receivers before archive and record their identities/paths under the
shared workspace policy. Prefer preallocation when needed; existing clean target
checkouts need none. Terminal recovery uses the supported identity-checked API.

### 2. Validate the close plan

List each delta → living spec, each ADR → next number/slug, guide outcome,
deferred task and source delivery target. Check against the verified workspace
and targets, present the result, then record:

```sh
onto set close-confirmed <name> "YYYY-MM-DD <validated close-plan summary>"
```

This records a performed review, not personal user approval. No spec/ADR mutation
precedes it. Shared workspace policy owns Markdown checkpoints before binary calls.

### 3. Land knowledge and archive

1. Run `onto merge-deltas <name>`. It owns spec merging and its receipt; never
   hand-merge living specs. Operations are RENAMED → MODIFIED → REMOVED → ADDED.
   A receipt mismatch blocks; inspect the recorded pre/post-images and deltas
   rather than clearing `close.merged` or replaying over newer content.
2. Promote each draft ADR to `<workflow-root>/adr/NNNN-<slug>.md` and mark Accepted;
   update superseded ADR links. Allocate from the highest existing number plus
   one, re-scan immediately before every move and never overwrite. Preserve already
   assigned numbers on resume. Rewrite design/notes links to the final paths.
   Managed mode uses filesystem moves plus named checkpoints, not `git mv`;
   existing mode uses named moves/commits in the records owner.
3. Run lint sections 3–4. Edit only new explanatory prose for clarity; preserve
   requirements, markers and literal evidence. No style receipt is required.
4. Record close preparation in the records owner. In managed mode checkpoint
   touched manual Markdown before each following mutation; binary writes checkpoint
   themselves. Existing mode stages only named touched records, inspects the index
   and commits preparation. Recover pending managed history before further writes.
5. Run `onto close <name>`. It validates gates, verification, merge receipt, anchors,
   dependencies and clean roots, then archives with pending integration. Managed
   mode checkpoints the move; existing mode commits only old/new workspace paths
   together. Preparation and archive are separate records operations. Archives
   become immutable except sanctioned `ship.md` and the one-way integration receipt.

### 4. Integrate

Read and follow [source integration](references/integration.md) for the recorded
merge/PR route, unchanged sources, receipt commands and retry behavior. There is
no implicit config entry in schema 2. In an existing combined checkout only a
proven records-only archive descendant may substitute for the verified candidate;
otherwise never use a records archive/checkpoint SHA as source.
Managed receipt writes checkpoint automatically; source delivery is separate.

## Completion evidence and next route

- Close lint passes; spec receipts match; ADR moves and required guides are complete.
- Archive exists with `archived: true` and its move is recorded.
- Every source has a proven unchanged receipt, real merge receipt, or independently
  observed exact PR receipt. `onto state <name> --json` derives done only then.
- Report the source integration and durable record locations. If publication is
  unavailable, preserve pending integration and report the concrete blocker.

## Recovery

Read state before repeating any step. `close.merged` proves spec merging only;
reconcile ADR promotion, guides and source integration independently. A directory
already moved with `archived: false` resumes with `onto close <name>`. An archived
pending change resumes integration only. If close refuses a newer source candidate,
reverify it; never bypass the refusal. See the dispatcher recovery reference for
active repairs, scope changes and malformed state.
