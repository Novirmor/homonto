# Hosting sandbox verification

`scripts/hosting-sandbox.sh` checks the hosting workflows (`h-workflow-gh`,
`h-workflow-gl`, `h-workflow-bb`) against real sandbox repositories on
GitHub, GitLab.com and Bitbucket Cloud. It is a local release check
([release checklist](../../../docs/release-checklist.md) § Live host checks).
It is not run in CI, because it needs sandbox credentials and writes to real
repositories.

```sh
cp test/e2e/hosting/sandbox.env.example test/e2e/hosting/sandbox.env   # fill in
export GH_TOKEN=… GITLAB_TOKEN=… HOMONTO_BITBUCKET_TOKEN=…            # sandbox accounts only
scripts/hosting-sandbox.sh                  # every host with a sandbox
scripts/hosting-sandbox.sh gitlab --write   # one host, including write processes
```

Each host runs `test/e2e/suites/hosting.sh` in an image layered on the
real-tool E2E image, adding pinned `gh`, `glab` and Node. Credentials are
passed by environment variable name at run time and never written into the
image. OpenCode's auth file is mounted read-only for the process layer.

## Layers

1. **Runtime** ([`runtime.mjs`](runtime.mjs)). The projected workflow
   plugin's shim drives `homonto runtime serve` against the host. It reads
   the fixture PR's OIDs as the host defines them, stages a uniquely marked
   PR comment, checks that an unapproved publish writes nothing, approves
   through the native question events, publishes, re-publishes (which must
   reconcile without sending again), and deletes the comment. This layer is
   deterministic and does not use a model.
2. **Processes**. Each `/h-*` command runs through real OpenCode with a free
   model. Read-only processes (`/h-spike-issue`, `/h-review-pr`,
   `/h-review-batch`) must reach their endpoint, and the fixture PR's comment
   count must not change: a headless run has no native question dialog, so
   nothing may be approved. With `--write`, `/h-resolve-conflicts` and
   `/h-continue-pr` must push to their fixture branches, which are then
   force-reset to the pinned heads, and `/h-resolve-issue` must open a PR,
   which is then closed.

## Fixtures

Per host, create one throwaway repository with:

- an open issue (GitHub and GitLab only; Bitbucket Cloud has no tracker, so
  its spike and resolve take `HOSTING_BB_BRIEF`);
- an open PR/MR **from a fork**, with at least one unresolved review thread.
  Pin its head commit as `HOSTING_<H>_PR_HEAD`;
- an open PR/MR whose head conflicts with its base. Pin its head as
  `HOSTING_<H>_CONFLICT_HEAD`.

The tokens need comment and push rights on the sandbox, plus PR creation for
`--write`. Use scoped, short-lived tokens for sandbox-only accounts.
