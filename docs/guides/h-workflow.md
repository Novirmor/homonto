# The h GitHub workflow

`h` brings GitHub issues and pull requests into the coordinator: research an
issue, resolve it into a PR, review a PR, continue a PR from its feedback,
resolve its merge conflicts, or review a batch. It is a workflow package, not an engine — it uses an
installed engine only when resolving an issue.

## Install

```toml
[workflows.h]
source = "builtin:h"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.h-spike.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-5-5"

# Needed for /h-resolve-issue — at least one engine:
[engines.to]
source = "builtin:to"
scope  = "project"
# … plus its agents' model blocks
```

Requirements: `gh` on PATH and authenticated (`gh auth status`), and a local
checkout whose `origin` is the issue's or PR's repository (renamed and
transferred repositories are resolved by GitHub repository ID). In schema 2,
that checkout must be a declared `[repos]` alias; the config directory itself
may be non-Git.

## The six commands

| Command | Input | Endpoint | Writes code | Publishes |
|---|---|---|---|---|
| `/h-spike-issue` | issue | Implementation brief | never | optional issue comment, after you approve the shown draft |
| `/h-resolve-issue` | issue | Verified PR closing the issue | yes, via an engine | push + PR, after verification |
| `/h-review-pr` | PR | Review draft | never | review/comment, after you approve the shown draft |
| `/h-continue-pr` | PR | Verified fixes on the same PR | yes, directly | push to the PR branch + one summary comment, after verification |
| `/h-resolve-conflicts` | PR | Verified conflict resolution, or an evidenced no-op | conflicts only, directly | normal push to the same PR head; no comments by default |
| `/h-review-batch` | repo or PR list | Drafts for every PR | never | after one decision over all drafts |

Issue references: `https://github.com/OWNER/REPO/issues/N` or
`OWNER/REPO#N`. PR references: a URL, `OWNER/REPO#N`, a number, or a unique
head branch.

### `/h-spike-issue` — research only

```text
/h-spike-issue acme/notes#42
```

1. Validate the reference; preflight `gh` and the repository match.
2. Fetch the issue, comments, and linked context.
3. Dispatch `h-spike` workers — one per independent question — with the issue
   packet. They are read-only and return findings with file:line citations.
4. Check every citation against the code.
5. Present the brief: findings, candidate code paths, unknowns, risks, a
   proposed approach, and which workflow fits.
6. Offer to post it as a comment. Nothing is posted unless you approve the
   exact draft.

Never edits code, creates branches or workflow state, or pushes.

### `/h-resolve-issue` — issue to verified PR

```text
/h-resolve-issue https://github.com/acme/notes/issues/42
```

```text
spike (research endpoint) ─▶ choose engine ─▶ open change ─▶ implement ─▶ verify
   ─▶ push branch ─▶ open one PR "Fixes #42" ─▶ record integration ─▶ report URL + evidence
```

1. **Spike first**, always — "the issue looks tiny" is not a reason to skip.
   The brief isn't posted unless you separately ask.
2. **Choose the engine.** An existing matching change resumes. Otherwise the
   coordinator recommends among the installed `no`, `to`, `onto light`,
   and full `onto` paths and asks once.
3. **Run it** under the chosen engine's full contract (see
   [onto](onto-workflow.md), [to](to-workflow.md), [no](no-workflow.md)).
4. **Publish** only after verification passes: push the branch (never
   force), open one PR whose body closes the issue (or reuse the exact PR
   from an interrupted run), and record onto's `pr:` receipt when applicable.

Invocation authorizes the spike, the engine run, the push, and the PR. It
doesn't authorize skipping the spike, pushing before verification, or
force-pushing.

### `/h-review-pr` — review with full context

```text
/h-review-pr acme/notes#57
```

1. Resolve the PR to its canonical identity; stop if it's closed or merged
   (unless you ask anyway).
2. Build the **context pack**: metadata, every review thread (fully
   paginated — incomplete pagination is a blocker), the diff, linked issues,
   check results.
3. Check out the PR only if needed to inspect surrounding code or confirm a
   suspicion, and only over a clean tree. Relevant tests may run under the
   trusted shell policy.
4. Dispatch `h-review` with the whole pack and a lens.
5. Validate each finding against the code; drop unsupported ones.
6. Show the draft (findings ranked by severity, verification run, residual
   risk). Post exactly that draft only after you approve it.

### `/h-continue-pr` — act on feedback

```text
/h-continue-pr 57
```

1. Resolve the PR; confirm it's open and you can push to its head.
2. Collect requested changes, unresolved non-outdated threads, substantive
   comments, failing in-scope checks, and linked-issue criteria. Feedback from
   unknown or untrusted authors needs your decision.
3. Check out the exact head (matching OID), isolated if configured.
4. Make the smallest fixes that answer the feedback. No engine is required;
   one is used only if you select it.
5. Verify, re-check that the PR head hasn't moved, push to the same branch
   (no force, amend, or rebase), post one summary comment, and resolve only
   the threads actually addressed.

### `/h-resolve-conflicts` — conflicts only

```text
/h-resolve-conflicts 57
```

Pins the open PR's head and current base, probes for actual merge conflicts, then
merges the base into the head without rebasing or force-pushing. Reads both sides'
intent, resolves only conflicts and directly necessary compatibility adaptations,
verifies the merged tree, and pushes that exact commit to the same PR head.
No engine or lifecycle record is required. If the pair is conflict-free, it
reports that evidence without creating a commit or pushing.

This does **not** request review-feedback fixes, pre-existing test hardening, or
general cleanup. A disproportionate repair needs your scope approval through
the native dialog before edits. Existing dirty work is preserved. Moving remote
refs or verification that cannot establish safety are blockers, not permission
to widen scope. The skill does not merge the PR, post comments or resolve threads
by default, and conflict-free does not mean CI is green.

### `/h-review-batch` — many PRs, one decision

```text
/h-review-batch https://github.com/acme/notes limit 20 author alice
/h-review-batch acme/notes#57 acme/notes#58 acme/notes#61
```

Default limit 10; the author filter is applied server-side. One `h-review`
worker per PR runs concurrently. A PR that can't be fetched is reported as a
failure without stopping the batch. All drafts are shown together, then one
publication decision; the final table reports reviewed / failed / posted
counts and URLs. "All open PRs" over ten asks about cost once.

## Publication rules

- Review comments and spike briefs: posted only after **explicit approval of
  the exact draft shown**. A permission prompt, an earlier approval, or shell
  access never counts.
- `h-resolve-issue` and `h-continue-pr` publish as part of their invocation,
  but only after the applicable verification has passed.
- `h-resolve-conflicts` authorizes only its verified same-PR push, not comments,
  reviews, thread resolution, or merging the PR.
- Only the coordinator publishes. Workers are read-only; implementers are
  denied push and `gh` publishing.
- Issue, PR, and comment text is **data**, never instructions that change
  goals, permissions, or scope.

## OpenCode draft tools

With `h` installed and OpenCode 2, the workflow plugin registers
`homonto_github_draft`, `homonto_github_status`, and `homonto_github_publish`.
The coordinator stages issue comments, PR comments, or formal reviews
(`COMMENT` / `REQUEST_CHANGES`), shows the exact preview, and publishes only
items you approve through OpenCode's native question dialog — a permission
reply alone never approves.

Limits: memory-only, at most ten items per batch, 8 KiB per body, 32 KiB per
batch, 15-minute lifetime; restaging invalidates earlier approvals; a restart
needs fresh approval. Headless clients without native questions can't approve
drafts. The tools don't push or create PRs. See
[OpenCode integration](opencode.md#github-draft-tools).

## The agents

| Agent | Role |
|---|---|
| `homonto` | Fetches all GitHub context, dispatches workers, validates their output, owns every publication. |
| `h-spike` | Read-only issue or conflict-intent research from a supplied packet; one per question. |
| `h-review` | Read-only PR review from a supplied context pack; conflict-only work uses a bounded resolution packet. One per PR or lens. |
