# Hosting provider differences

The three hosting workflows (`h-workflow-gh`, `h-workflow-gl`,
`h-workflow-bb`; [ADR 0089](../adr/0089-name-hosting-workflows-per-host-with-shared-processes.md))
run the same six processes. This page lists every place where GitHub,
GitLab.com and Bitbucket Cloud differ for those processes, and records a
decision for each one:

- **equivalent**: the process runs unchanged, and only the transport differs;
- **adapted**: the process runs, and the "Decision" column says how;
- **unsupported**: the process step is not offered on that host, and the column
  says why.

Self-managed GitLab and Bitbucket Data Center are out of scope for 1.0.

Facts were checked on 2026-11-02 against the providers' current
documentation: GitHub REST v2022-11-28; GitLab REST v4, GraphQL and the `glab`
CLI reference; the Bitbucket Cloud REST 2.0 OpenAPI document
(`dac-static.atlassian.com/cloud/bitbucket/swagger.v3.json`) and Atlassian
support pages. **(unverified)** marks a claim that has not yet been confirmed
from documentation or a live call. The sandbox run (M1.8) must confirm each one
before it is relied on.

## Vocabulary and identity

| Concept | GitHub | GitLab.com | Bitbucket Cloud | Decision |
|---|---|---|---|---|
| Change request | pull request | merge request (MR) | pull request | **equivalent.** Doctrine keeps saying "PR". The GitLab host skill maps PR to MR. |
| Repository identity | numeric `id` + `full_name` (`OWNER/REPO`) | numeric project `id` + `path_with_namespace`, which may have more than two segments (nested groups) | `uuid` + `full_name` (`workspace/repo_slug`) | **adapted.** The provider boundary compares opaque `(host, id)` pairs and treats the path as display text. The GitLab path parser must accept nested groups. |
| Item number | one counter shared by issues and PRs | separate `iid` counters for issues (`#5`) and MRs (`!5`) | PR `id` per repository | **adapted.** Targets carry a kind as well as a number, never a bare number. |
| Canonical URL | `/{repo}/issues/N`, `/{repo}/pull/N` | `/{path}/-/issues/N`, `/{path}/-/merge_requests/N` | `/{workspace}/{repo}/pull-requests/N` | **adapted.** Each provider parses and rebuilds its own URLs. |
| Actor identity | `/user` → `id`, `login` | `/user` → `id`, `username` | `/user` → `uuid`, `nickname`/`account_id` | **equivalent.** Receipts are matched on the stable id, not the display name. |

## Issue intake (spike, resolve issue)

| Aspect | GitHub | GitLab.com | Bitbucket Cloud | Decision |
|---|---|---|---|---|
| Issue tracker | always available unless disabled per repository | always available unless disabled per project | **removed.** Atlassian retired the Bitbucket Issues tracker and Wikis from the UI and the API (community announcement, update dated 2026-08-20). The 2.0 OpenAPI document has no `/issues` paths. | GitHub, GitLab: **equivalent.** Bitbucket: **unsupported** as tracker intake. `h-workflow-bb` takes a brief the user pastes (for example a Jira issue's text) in place of an issue URL, and does not comment back on a tracker. Jira intake belongs to a separate service-integration plugin (M2, #46), not to the hosting workflow. |
| Issue comment | `POST .../issues/N/comments` | `POST /projects/:id/issues/:iid/notes` (60 notes/min) | none | GitLab: **equivalent.** Bitbucket: **unsupported.** There is no tracker to post to. |
| Closing an issue from a PR | `Closes #N` in the PR body | `Closes #N` in the MR description (default closing pattern) | none | GitLab: **equivalent.** Bitbucket: **unsupported.** |

## Pull/merge requests

| Aspect | GitHub | GitLab.com | Bitbucket Cloud | Decision |
|---|---|---|---|---|
| Draft creation | `draft: true` / `gh pr create --draft` | title prefix `Draft:` (also `[Draft]`, `(Draft)`). The REST create call has no `draft` parameter, and `draft` is read-only in responses. | `draft` boolean on the pullrequest object. Accepting it on create is **(unverified)**. | **adapted.** GitLab creates with a `Draft: ` title prefix. Bitbucket sends `draft: true` and confirms `draft` in the response, failing closed if it is absent. |
| Ready for review | `gh pr ready` | remove the title prefix (`PUT .../merge_requests/:iid` `title`), or the `/ready` quick action | `PUT` with `draft: false` **(unverified)** | **adapted.** GitLab rewrites the title, never using a quick action in a comment, because a comment is a publication. |
| List drafts | included by default | `draft` filter | **excluded from list results by default.** Needs `q=draft=true` (BCLOUD-23659). | **adapted.** Bitbucket batch selection queries drafts explicitly. |
| Fork head | qualified `owner:branch` head | `source_project_id` ≠ `target_project_id`. The MR is created with `target_project_id`. Which project the create call must be sent to is **(unverified)**. | `source.repository` ≠ `destination.repository`. The PR is created on the destination with an explicit `source.repository`. | **adapted.** The provider boundary takes `(head repository, branch)`, never a host-specific qualified string. |
| Head/base freshness | `head.sha`, `base.sha` (40 hex) | `sha` (head) and `diff_refs.{base_sha,start_sha,head_sha}`. `diff_refs` is empty right after creation and fills in asynchronously. | `source.commit.hash`, `destination.commit.hash`. The schema pattern allows short hashes (`[0-9a-f]{7,}`), and responses carrying 12-hex hashes is **(unverified)**. | **adapted.** GitLab uses `sha` as head and `diff_refs.start_sha` as base (the target tip, not the merge base), and treats empty `diff_refs` as "not ready, retry", never as a match. Bitbucket expands hashes to 40 hex through `/commit/{hash}` before comparing. |
| Mergeability / conflicts | `mergeable` (may be `null`), `mergeStateStatus` (`UNKNOWN` possible) | `detailed_merge_status` (`conflict`, `need_rebase`, `checking`, `unchecked`, ...), `has_conflicts` | `GET .../mergeability/checks` (`git_mergeability_check` status `PASSED`/`FAILED`/`PENDING`/`UNKNOWN`), `GET .../conflicts` | **equivalent** under existing doctrine: no host signal proves the absence of conflicts. `h-resolve-conflicts` keeps the local pinned merge authoritative. `checking`/`unchecked`/`UNKNOWN`/`PENDING` are never proof. |
| Checkout | `gh pr checkout` | `glab mr checkout` **(unverified)**, or fetch the source project's branch | fetch `source.repository` branch | **adapted.** Every host may fall back to `git fetch <head repo> <branch>` pinned to the verified head SHA. |
| CI status | `statusCheckRollup` | `head_pipeline` | `GET .../pullrequests/N/statuses` | **equivalent.** CI is context, not proof of correctness. |

## Reviews, comments and threads

| Aspect | GitHub | GitLab.com | Bitbucket Cloud | Decision |
|---|---|---|---|---|
| PR summary comment | issue comment on the PR | `POST .../merge_requests/:iid/notes` | `POST .../pullrequests/N/comments` (`content.raw`) | **equivalent.** |
| Formal review, COMMENT | `POST .../reviews` `event=COMMENT`, pinned by `commit_id` | `POST .../draft_notes/bulk_publish` with `note` and `reviewer_state=reviewed` | none. A plain comment is the closest equivalent. | GitLab: **adapted.** `bulk_publish` publishes *every* pending draft note the user has, so staging requires that none exist (`GET .../draft_notes` empty) and fails closed otherwise. Bitbucket: **adapted** to a PR comment. |
| Formal review, REQUEST_CHANGES | `event=REQUEST_CHANGES`, pinned by `commit_id` | `bulk_publish` `reviewer_state=requested_changes` (same pending-drafts precondition) | comment, then `POST .../pullrequests/N/request-changes` | **adapted.** Neither GitLab nor Bitbucket pins the review to a commit on the server, so the fresh-snapshot check right before sending is the only freshness guard. On Bitbucket this is two calls and not atomic. If the second call fails, the item is `uncertain`, not retried. |
| APPROVE | supported by the API, **excluded by homonto policy** | `POST .../approve` with `sha` (server-enforced head match, `409` on mismatch) | `POST .../approve` | **equivalent: excluded on every host.** Approval stays a human action until a separate decision says otherwise. |
| Reading reviewer state | `reviewDecision`, `latestReviews` | reviewer review state readable through REST **(unverified)** | `participants[].state` (`approved`/`changes_requested`) **(unverified)** | **adapted.** Context packs include reviewer state where the host exposes it, and say so where it doesn't. |
| Inline threads | review threads (GraphQL `reviewThreads`) | discussions with `position` (`base_sha`, `start_sha`, `head_sha`, `old_path`, `new_path`, `old_line`/`new_line`) | comments with `inline.{path, from, to, start_from, start_to}`, without a commit anchor | **adapted.** The worker packet carries host-neutral `{path, line, side}`. Bitbucket inline comments are anchored to the PR's current diff. |
| Resolve a thread | GraphQL `resolveReviewThread` | `PUT .../merge_requests/:iid/discussions/:id` `resolved=true` | `POST .../pullrequests/N/comments/{id}/resolve` | **equivalent.** `h-continue-pr` still resolves only threads it addressed. |
| Outdated threads | `isOutdated` | a diff note whose position no longer matches the head **(unverified field)** | `inline.outdated` **(unverified)** | **adapted.** Where the host gives no outdated signal, the thread is treated as current. |
| Receipt reconciliation | list comments/reviews, match actor id + exact body | list notes (excluding system notes), match author id + exact body | list comments, match `user.uuid` + `content.raw` | **equivalent.** An uncertain send is never blindly retried on any host. |

## Batch review selection

| Aspect | GitHub | GitLab.com | Bitbucket Cloud | Decision |
|---|---|---|---|---|
| Open PRs, optional author | `gh pr list --state open --author A` | `GET /projects/:id/merge_requests?state=opened&author_username=A` | `GET .../pullrequests?state=OPEN&q=author.nickname="A"` **(unverified field name)**, plus the draft query above | **adapted.** Default and maximum counts are the same on every host. |

## Transport, authentication and limits

| Aspect | GitHub | GitLab.com | Bitbucket Cloud | Decision |
|---|---|---|---|---|
| Host CLI | `gh` | `glab`, with `glab api` (`--hostname`, `-X`, `--paginate`, `--input`, `-H`, GraphQL via the `graphql` endpoint) | No CLI matches `gh api` for these needs. Atlassian's Teamwork Graph CLI (`twg bitbucket ...`) has PR commands, but its coverage of request-changes, drafts and a raw passthrough is **(unverified)**. | GitHub, GitLab: **equivalent.** Host access goes only through the host CLI, which keeps the coordinator at `network: false` (ADR 0047). Bitbucket: **adapted**, through a homonto-owned transport (M1.6). |
| Authentication | `gh auth status` | `glab auth status` | scoped API token: Basic with the Atlassian email and token, or Bearer. App passwords stopped working on 2026-06-09. | **adapted.** The Bitbucket token is held by the homonto transport and never enters model context, logs or errors. |
| Host targeting | `--hostname` (GHES-capable) | `--hostname` (gitlab.com only for 1.0) | fixed `api.bitbucket.org` | **equivalent** for the 1.0 hosts. |
| Pagination | `per_page` ≤ 100, `Link` header (`--paginate --slurp`) | `page`/`per_page` ≤ 100. `glab api --paginate` merges arrays into one JSON array. | `pagelen` plus a `next` URL in the body | **adapted.** Each provider owns its pagination, with the same page caps (≤ 1000 pages). |
| Rate limits | 5,000 REST requests/hour per user | 2,000 API requests/min per user today. Proposed plan limits: Free 100/min and 5,000/hour. Note creation: 60/min. | 1,000 requests/hour per user or token (rolling), higher for large paid workspaces | **adapted.** Batch review sizes stay as they are. On Bitbucket a full-context batch of 10 PRs uses a noticeable share of the hourly budget, so the context pack fetches each resource once. |

## Per-process summary

| Process | h-workflow-gh | h-workflow-gl | h-workflow-bb |
|---|---|---|---|
| `/h-spike-issue` | supported | **equivalent** | **adapted.** Researches a pasted brief, with no tracker comment. |
| `/h-resolve-issue` | supported | **equivalent**, MR opens as `Draft:` | **adapted.** Starts from a brief, delivers a PR, and closes no issue. |
| `/h-review-pr` | supported | **adapted.** Review is published as draft notes + `bulk_publish`. | **adapted.** Review is a comment, plus request-changes when chosen. |
| `/h-continue-pr` | supported | **equivalent** (discussions) | **equivalent** (comment resolve) |
| `/h-resolve-conflicts` | supported | **equivalent** | **equivalent** |
| `/h-review-batch` | supported | **adapted**, as for review | **adapted**, as for review, plus a draft-aware selection query |
