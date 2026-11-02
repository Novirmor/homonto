---
name: h-host-gh
description: GitHub host operations for the shared h processes (h-workflow-gh) — load when an h-* process targets github.com or a GitHub Enterprise host.
---

# h-host-gh

GitHub host operations for the shared h processes (`/h-spike-issue`,
`/h-resolve-issue`, `/h-review-pr`, `/h-continue-pr`, `/h-resolve-conflicts`,
`/h-review-batch`). The process skills spell every host operation in its
GitHub form, so on GitHub use them exactly as written.

This skill maps operations only. The process skill's policies still apply in
full: [workspace and dirty-work policy](../homonto/references/workspace-policy.md),
[autonomy](../homonto/references/autonomy.md) and
[publication](../homonto/references/publication.md).

| Operation | GitHub |
|---|---|
| Host CLI and auth | `gh` on PATH; `gh auth status` |
| Draft tools | `homonto_github_draft`, `homonto_github_status`, `homonto_github_publish` (read permission `homonto_github_read`) |
| Item kinds | `issue_comment`, `pr_comment`, `pr_review` (`COMMENT` or `REQUEST_CHANGES`) |
| Everything else | the inline `gh` commands in each process skill and in `homonto/references/publication.md` |

A formal `APPROVE` uses the commit-bound review path in the publication
reference (`commit_id` = reviewed head), only when explicitly selected and with
no critical or major findings.
