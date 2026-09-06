---
name: h-spike-issue
description: Use when a GitHub issue needs research before anyone implements it — the user invokes /h-spike-issue, or asks to spike, scope, or investigate an issue without code changes yet.
---

# h-spike-issue

Research one GitHub issue and hand back an implementation brief. No
implementation happens here; the brief feeds `h-resolve-issue` (or the user).

Invoking `/h-spike-issue <issue>` is consent to fetch the issue read-only
and dispatch `h-spike` workers. It is not consent to edit code, create
branches or workflow state, commit, push, or post anything to GitHub.

## Required order

1. **Validate input.** Exactly one issue reference: a GitHub issue URL or
   `OWNER/REPO#NUMBER`. A missing argument, several references, or a pull
   request URL is a stop — ask for
   `/h-spike-issue https://github.com/OWNER/REPO/issues/NUMBER`.
2. **Preflight.** `command -v gh`, `gh auth status`, and the repository
   match: the local `origin` must resolve to the issue's `OWNER/REPO`
   (SSH/HTTPS equivalence counts). A missing or unauthenticated `gh` is a
   blocker — unlike tooling preflight, there is no degraded path without
   GitHub. A repo mismatch is a blocker; rerun from the matching repository.
3. **Fetch the issue.**

   ```bash
   gh issue view NUMBER --repo OWNER/REPO --json number,title,body,state,author,labels,assignees,comments,url
   ```

   If the issue is closed, ask whether to continue; do not decide.
4. **Dispatch `h-spike`.** Hand the worker the full packet — title, body,
   the comments that carry substance — and the question. One worker for one
   issue; add more only for genuinely independent sub-questions (workers are
   read-only, so concurrent dispatch is safe). The worker cannot fetch; you
   supply everything external.
5. **Validate the brief.** Spot-check the cited paths against the repository
   before reporting. Resolve the worker's `Questions:` yourself where
   repository evidence answers them; only genuinely missing product intent
   goes to the user.
6. **Report the brief** in conversation: findings, candidate code paths,
   unknowns, risks, approach sketch, and the worker's `to`-vs-onto fit
   assessment with its deciding facts. Do not write files unless the user
   asks.

## Common mistakes

- Do not implement, however obvious the fix looks — "spike and also fix it"
  is a scope change the user did not request; name it in the report instead.
- Do not skip the worker and summarize the issue yourself; the value is a
  fresh-context read of the code against the packet.
- Do not fetch from inside the worker; it has no network and must not.
- Do not report unverified citations — a brief grounded in paths you never
  opened is worse than a shorter one.
