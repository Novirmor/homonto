---
name: h-review-batch
description: Use when the user asks to review many pull requests at once — invoked by /h-review-batch with a repository or a list of PR references; also covers "review all open PRs".
---

# h-review-batch

Review a repository's open pull requests in one pass: one read-only
`h-review` worker per PR, drafts collected, one publication decision.

Invoking `/h-review-batch` is consent to enumerate and fetch the selected
PRs read-only and dispatch workers. It is not consent to post anything —
one explicit approval after the drafts covers publication — and never to
let a worker touch GitHub.

**Success endpoint:** every selected PR accounted for by a validated draft or
an explicit failure, one publication decision over the shown drafts, and a
final table with reviewed/failed/posted counts and any posted URLs. While
approval is pending, leave all drafts unpublished. Continue to this endpoint
in the same invocation unless the user names an earlier endpoint or asks to
pause. Follow
[h workflow autonomy](../h-resolve-issue/references/autonomy.md) for failure
recovery, trusted workspace execution, and permission boundaries; it never
replaces explicit approval of the shown drafts.

## Required order

1. **Parse input.** Either a repository URL (`https://github.com/OWNER/REPO`)
    optionally followed by `limit N` (default 10) and `author USER`, or one
   or more unambiguous PR references (PR URLs, `OWNER/REPO#NUMBER`, numbers,
   or unique head branches resolved against the repository). Resolve each to
   its canonical PR URL and number with `gh pr view`; never ask for a
   syntax-only rerun. Ask for missing or ambiguous identity, and reject issue
   URLs rather than treating their numbers as PRs.
2. **Preflight.** `command -v gh`, `gh auth status`, and canonical repository
    identity per the shared context-pack contract. Resolve old origin names
    through GitHub before rejecting a renamed repository. Verify each PR's
    declared source independently; a non-Git control root is not a mismatch.
3. **Enumerate.**

   ```bash
   gh pr list --repo HOST/OWNER/REPO --state open --limit N --json number,title,author,headRefName,updatedAt,reviewDecision
   ```

   If author is given, include `--author "$AUTHOR"` in the server query before
   applying `--limit N`; never fetch N unfiltered PRs then filter locally, which
   underfills or selects the wrong batch. Deduplicate canonical PR identities.
   Paginated all-open enumeration uses the same server-side author filter.

   ```bash
   gh pr list --repo HOST/OWNER/REPO --state open --author "$AUTHOR" --limit N --json number,title,author,headRefName,updatedAt,reviewDecision
   ```

   An explicit count or limit above ten, or an explicit list of more than ten
   PRs, already authorizes that batch size: do not ask for duplicate count
   approval. For an unbounded request such as "all open PRs", establish the
   full count with pagination; ask about cost only if it exceeds ten and the
   user has not already authorized that count. Otherwise use the default ten
   and report the selected scope, never silently truncate an "all" request.
4. **Build one context pack per PR** — each independent, exactly per
   [`h-review-pr/references/context-pack.md`](../h-review-pr/references/context-pack.md).
    Materialize each pack in its own worker-readable directory and validate it
    before dispatch. Recover transient in-scope failures with bounded retries
    from the shared contract: defer HTTP 5xx diffs while other PRs proceed,
    retry within the same three-attempt budget, then try the pinned local-diff
    fallback only if its identity, objects, and inventory can be verified.
    A PR whose pack
   still cannot be completed (fetch failure, broken pagination) is
   marked failed and skipped; the others proceed. Partial success is the
   design, not an error.
5. **Dispatch in waves.** One `h-review` worker per PR, a few at a time
   (three to five concurrent keeps results tractable); each gets its own
    complete pack's absolute manifest/component paths and pinned refs. Only
    ready packs enter a wave; earlier coordinator tool output is not a handoff.
    Workers have no shell: the coordinator supplies `git show`/other probe output
    and re-dispatches missing evidence. Workers are read-only, so waves are safe; the boundary
   exists for attention and rate limits, not safety.
6. **Validate and draft per PR** — confirm cited file:line against each
   fetched diff, fold findings already covered by that PR's unresolved
    threads, require the worker's Context used report, and shape each draft per
   [`h-review-pr/references/draft-format.md`](../h-review-pr/references/draft-format.md).
7. **Present the batch**: one table (PR, title, findings by severity, draft
    ready / failed with reason) followed by the drafts. Distinguish unresolved
    identity, missing handoff files, and exhausted diff retries from a completed
    review. Include attempts and fallback provenance; retain retryable failures
    with the exact next action. Never count a Questions-only return as reviewed.
8. **One publication decision.** A single dialog after all drafts: post all,
   post selected (name them), or post none. Approval covers exactly the
   shown drafts; any later finding change reopens it.
9. **Post the approved drafts**, one comment per PR via a body file
    (`gh pr comment NUMBER --repo HOST/OWNER/REPO --body-file COMMENT_FILE` —
   draft and comment files are transient: keep them under the workspace tmp
   directory when the shared `homonto` skill's generated
   `references/tmp.md` declares one, otherwise `mktemp`);
    revalidate identity/state and base/head OIDs for each PR before posting,
    following the shared publication-freshness contract. Changed refs require
    a new review and approval for that PR; unaffected approved drafts proceed.
    Include reviewed OIDs in every body; report each URL and each failure exactly.
10. **Report.** Counts (reviewed, failed, posted), the table, posted comment
    URLs, and residual risks.

## Common mistakes

- Do not exceed the authorized batch size or run unbounded parallel fetches;
  use bounded waves without re-asking for an explicitly requested count.
- Do not abort the batch on one PR's failure; preserve the successful
  drafts and report the gap.
- Do not post per-PR approvals or treat the invocation as approval — one
  decision, after every draft is visible.
- Do not let workers fetch GitHub context or post; the coordinator owns GitHub.
