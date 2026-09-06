---
name: h-review-batch
description: Use when the user asks to review many pull requests at once — invoked by /h-review-batch with a repository or a list of PR URLs; also covers "review all open PRs".
---

# h-review-batch

Review a repository's open pull requests in one pass: one read-only
`h-review` worker per PR, drafts collected, one publication decision.

Invoking `/h-review-batch` is consent to enumerate and fetch the selected
PRs read-only and dispatch workers. It is not consent to post anything —
one explicit approval after the drafts covers publication — and never to
let a worker touch GitHub.

## Required order

1. **Parse input.** Either a repository URL (`https://github.com/OWNER/REPO`)
    optionally followed by `limit N` (default 10) and `author USER`, or one
   or more explicit PR URLs. Anything unparseable is a stop with the grammar.
2. **Preflight.** `command -v gh`, `gh auth status`, repository match.
3. **Enumerate.**

   ```bash
   gh pr list --repo OWNER/REPO --state open --limit N --json number,title,author,headRefName,updatedAt,reviewDecision
   ```

   Apply the author filter if given. Confirm the resulting list with the
   user before dispatching when it exceeds ten PRs — cost is a product
   decision at that scale.
4. **Build one context pack per PR** — each independent, exactly per
   [`h-review-pr/references/context-pack.md`](../h-review-pr/references/context-pack.md).
   A PR whose pack cannot be completed (fetch failure, broken pagination) is
   marked failed and skipped; the others proceed. Partial success is the
   design, not an error.
5. **Dispatch in waves.** One `h-review` worker per PR, a few at a time
   (three to five concurrent keeps results tractable); each gets its own
   complete pack. Workers are read-only, so waves are safe; the boundary
   exists for attention and rate limits, not safety.
6. **Validate and draft per PR** — confirm cited file:line against each
   fetched diff, fold findings already covered by that PR's unresolved
   threads, and shape each draft per
   [`h-review-pr/references/draft-format.md`](../h-review-pr/references/draft-format.md).
7. **Present the batch**: one table (PR, title, findings by severity, draft
   ready / failed with reason) followed by the drafts.
8. **One publication decision.** A single dialog after all drafts: post all,
   post selected (name them), or post none. Approval covers exactly the
   shown drafts; any later finding change reopens it.
9. **Post the approved drafts**, one comment per PR via a body file
   (`gh pr comment NUMBER --repo OWNER/REPO --body-file COMMENT_FILE` —
   draft and comment files are transient: keep them under the workspace tmp
   directory when the shared `homonto` skill's generated
   `references/tmp.md` declares one, otherwise `mktemp`);
   report each URL and each failure exactly.
10. **Report.** Counts (reviewed, failed, posted), the table, posted comment
    URLs, and residual risks.

## Common mistakes

- Do not fan out one worker per PR before enumeration is confirmed at
  scale, and do not run unbounded parallel fetches — waves, not a stampede.
- Do not abort the batch on one PR's failure; preserve the successful
  drafts and report the gap.
- Do not post per-PR approvals or treat the invocation as approval — one
  decision, after every draft is visible.
- Do not let workers fetch or post; the coordinator owns GitHub.
