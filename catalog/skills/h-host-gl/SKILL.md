---
name: h-host-gl
description: GitLab.com host operations for the shared h processes (h-workflow-gl) — load when an h-* process targets a gitlab.com issue or merge request.
---

# h-host-gl

GitLab.com host operations for the shared h processes (`/h-spike-issue`,
`/h-resolve-issue`, `/h-review-pr`, `/h-continue-pr`, `/h-resolve-conflicts`,
`/h-review-batch`). The process skills spell host operations in their GitHub
form. On GitLab.com, run the same operation with the form below and keep every
other rule of the process: approval, pinning, freshness, workspace policy and
the success endpoint. Self-managed GitLab is not supported.

This skill maps operations only. The process skill's policies still apply in
full: [workspace and dirty-work policy](../homonto/references/workspace-policy.md),
[autonomy](../homonto/references/autonomy.md) and
[publication](../homonto/references/publication.md).

Each provider difference and its decision is listed in the homonto docs
(`docs/guides/hosting-differences.md`). Forms marked **(unverified)** must
be checked on first use. If one fails, report the exact failure and stop that
step rather than improvising a different write.

## Vocabulary

- "PR" in the process skills means a merge request (MR). A PR URL is
  `https://gitlab.com/GROUP[/SUBGROUP...]/PROJECT/-/merge_requests/IID` and an
  issue URL is `.../-/issues/IID`. Issues (`#5`) and MRs (`!5`) number
  separately, so never treat a bare number as both.
- `HOST/OWNER/REPO` means `gitlab.com/GROUP/.../PROJECT`. Paths may have
  more than two segments. `PROJECT_ID` is the numeric project id. API
  calls that take a path need it URL-encoded: `group%2Fsub%2Fproject`.
- `baseOID` is `diff_refs.start_sha` and `headOID` is `diff_refs.head_sha`
  (equal to `sha`). An MR whose `diff_refs` is empty is not ready: wait and
  re-read it. Never guess OIDs.

## Operations

All reads and writes go through `glab api --hostname gitlab.com`, never
`curl`. Read lists with `--paginate`. Writes take a JSON body on stdin
(`--input -`, **(unverified)**) or fields (`-f key=value`).

| Operation | GitLab.com |
|---|---|
| Host CLI and auth | `glab` on PATH; `glab auth status --hostname gitlab.com` |
| Repository identity | `glab api --hostname gitlab.com projects/GROUP%2FPROJECT` → `id`, `path_with_namespace`, `web_url` |
| Issue view | `glab api --hostname gitlab.com projects/PROJECT_ID/issues/IID`; notes: `.../issues/IID/notes?per_page=100&sort=asc` with `--paginate` |
| MR view | `glab api --hostname gitlab.com projects/PROJECT_ID/merge_requests/IID` → `state` (`opened`/`closed`/`merged`/`locked`), `draft`, `diff_refs`, `source_project_id`, `target_project_id`, `source_branch`, `target_branch`, `detailed_merge_status`, `has_conflicts`, `head_pipeline` |
| Review threads | `.../merge_requests/IID/discussions?per_page=100` with `--paginate`; a thread is a discussion with `resolvable: true`, resolved when every note is `resolved` |
| Diff | `git fetch origin refs/merge-requests/IID/head` (works for fork MRs), then a local `git diff START_SHA...HEAD_SHA` pinned to `diff_refs` |
| Checkout | fetch `refs/merge-requests/IID/head` into a new local branch and verify it is `HEAD_SHA`. Push fixes to the *source* project's `source_branch`; a fork MR pushes to the fork. |
| Checks | `head_pipeline` on the MR, or `.../merge_requests/IID/pipelines`; context, never proof |
| Conflicts | `detailed_merge_status` `conflict`/`need_rebase` and `has_conflicts` are hints only. `checking`/`unchecked` prove nothing; the local pinned merge decides. |
| Open MRs (batch) | `.../projects/PROJECT_ID/merge_requests?state=opened&per_page=N`, plus `&author_username=USER` for an author filter |
| MR for a branch | `.../merge_requests?state=opened&source_branch=BRANCH&target_branch=BASE` |
| Create a draft MR | `glab api --hostname gitlab.com --method POST projects/SOURCE_PROJECT_ID/merge_requests` with `source_branch`, `target_branch`, `title` prefixed `Draft: ` and `description`. A fork MR also sends `target_project_id` (which project the call goes to is **(unverified)**). |
| Mark ready | `--method PUT .../merge_requests/IID` with the `title` minus its `Draft: ` prefix. Never post `/ready` as a comment: a comment is a publication. |
| Update the MR body | `--method PUT .../merge_requests/IID` with `description` |
| Close an issue on merge | `Closes #IID` in the MR description |
| Resolve a thread | `--method PUT .../merge_requests/IID/discussions/DISCUSSION_ID` with `resolved=true` |
| Draft tools | `homonto_gitlab_draft`, `homonto_gitlab_status`, `homonto_gitlab_publish` (read permission `homonto_gitlab_read`) |

## Publication through the draft tools

Item kinds are the same as on GitHub: `issue_comment` (issue note),
`pr_comment` (MR note) and `pr_review` with `COMMENT` or `REQUEST_CHANGES`.
A formal review is published with GitLab's draft-notes `bulk_publish`, which
sets the reviewer state to `reviewed` or `requested_changes`. `bulk_publish`
also publishes every pending draft note you have on that MR, so the runtime
refuses a review while any exist. Do not create draft notes yourself.

A formal `APPROVE` is outside the draft tools, as on GitHub. Use it only when
explicitly selected and with no critical or major findings:
`glab api --hostname gitlab.com --method POST
projects/PROJECT_ID/merge_requests/IID/approve -f sha=HEAD_OID`. GitLab
refuses the approval (409) if the head moved, which makes it commit-bound.
