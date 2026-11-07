# Swarms

A swarm builds two or three competing implementations of one task, each in
its own worktree from the same base, runs the same checks on all of them,
starts a running preview of each, and lets you choose one. The chosen one is
delivered the normal way; the rest are removed.

It is a bounded experiment, not a scheduler. The binary records the
candidates, their checkouts, check results, previews and your choice. The
agent you are talking to dispatches the candidates. Rationale:
[ADR 0113](../adr/0113-run-swarm-candidates-as-stateless-workspace-tasks.md),
[ADR 0114](../adr/0114-select-only-a-verified-swarm-candidate.md),
[ADR 0115](../adr/0115-preview-candidates-in-docker-under-fixed-budgets.md).

## Set up

A swarm runs on [workspace tasks](workspaces.md#workspace-tasks), so it needs
schema 2, declared repositories, `worktrees.dir`, and the stateless `no`
engine. Onto and to bind one checkout per change and cannot hold candidates.

Each candidate is an installed agent, and the model that agent is routed to is
the candidate's fixed model. To compare models, give each its own agent:

```toml
schema_version = 2
harnesses = ["claude"]

[repos]
web = "../web"

[worktrees]
dir = "../worktrees"

[engines.no]
source = "builtin:no"
scope = "project"

[skills.swarm]
source = "builtin:swarm"
scope = "project"

[commands.swarm]
source = "builtin:swarm"
scope = "project"

[routing.default.claude]                  # no-implementer runs on sonnet
model = "sonnet"

[subagents.impl-opus]                     # homonto/subagents/impl-opus.md
source = "local:impl-opus"
scope = "project"
[subagents.impl-opus.claude]              # a second implementer on opus
model = "opus"

[integration.web-tests]                   # the checks every candidate faces
repos   = ["web"]
repo    = "web"
cwd     = "."
command = ["npm", "test"]

[preview.site]                            # optional: a running prototype each
repo       = "web"
dockerfile = "Dockerfile"
port       = 3000
```

The second implementer needs a `homonto:` block so its model can be
rendered ([subagents](subagents.md#the-neutral-homonto-block)):

```markdown
---
name: impl-opus
description: Implements an assigned change in one checkout and runs its verification.
mode: subagent
homonto:
  read_only: false
  bash: true
  dialogs: false
  spawn: []
---

Implement the assignment you are given, only in the checkout you are given,
and run its verification. Return your edits and the evidence.
```

Check what each agent runs on with `homonto explain routing <agent>`.
Preview fields and budgets: [configuration](configuration.md#swarm-previews--previewname).

## Run one

In the harness, `/swarm <task>` drives the whole flow. The commands it runs:

```sh
homonto workspace swarm create landing --repo web --brief brief.md \
  --candidate a=no-implementer --candidate b=impl-opus
# … the agent dispatches a and b in parallel, then commits each …
homonto workspace swarm check landing --writers-paused
homonto workspace swarm preview start landing
homonto workspace swarm compare landing --html
homonto workspace swarm select landing --candidate a
# … the winner is reviewed and delivered: merged, or opened as a PR …
homonto workspace swarm cleanup landing --yes
```

- **create** pins one base for every candidate and allocates a worktree per
  candidate under `worktrees.dir/<alias>/workspace-<swarm>--<candidate>`, on
  branch `work-<swarm>--<candidate>/<alias>`. The brief is copied into the
  record, so every candidate gets the same text. Rerunning the same command
  resumes an interrupted creation.
- **check** runs every matching `[integration.*]` check on each candidate,
  one at a time. A failing candidate is a result, not a failed command.
- **compare** runs nothing; it shows the recorded evidence side by side:

  ```console
  $ homonto workspace swarm compare landing
  candidate        a                                 b
  agent            no-implementer                    impl-opus
  model claude     sonnet                            opus
  web              9b42c9370e26 1c 1f +1 -0          536c1322648b 1c 1f +1 -0
  check web-tests  passed                            failed
  previews         http://127.0.0.1:33704/page.html  http://127.0.0.1:33705/page.html
  verified         yes                               no
  b: web-tests failed
  ```

  Each repository cell is the candidate's revision, then commits, files,
  insertions and deletions against the base.

  `--html` also writes `.homonto/swarms/<name>/compare.html`, with each
  running preview embedded next to its evidence. `--json` prints everything.

## Choosing

Only a verified candidate can be selected: each changed repository has
committed work beyond the base, nothing uncommitted, and every matching check
passed on the current commits. New commits make results stale; run check
again. With no matching check, verify the candidate yourself and select with
`--fallback-verified`.

Selection records every candidate's revisions. That is what cleanup compares
against when it removes the alternatives.

## Previews

`swarm preview start` builds the Dockerfile from each candidate's checkout
(or runs the stock `image` with the checkout mounted read-only at
`/workspace`) and publishes the container port on a free loopback port. Every
container runs under its budget: CPUs, memory with swap capped at the same
value, a process limit, and a time limit.

A preview is torn down when it fails to build, start or answer within its
startup budget, or when the command is interrupted. Past its time limit, a
detached watchdog removes it; if the watchdog is gone, the next swarm command
does. Containers carry the swarm's labels, so anything an interrupted start
left behind is found and removed too. `swarm preview stop` tears them down on
demand. A swarm that never started a preview never needs Docker.

## Cleaning up

`swarm cleanup --yes` runs after the winner is delivered:

1. Every preview is stopped. If Docker is unreachable while a preview is
   recorded, cleanup stops here and keeps the worktrees.
2. Each rejected candidate's worktree is removed. Its branch is deleted only
   if it still points at the revision you rejected; a branch with newer
   commits is kept and reported. `--keep-rejected` keeps them all.
3. The winner's worktree is removed like any workspace task's: its branch is
   released once integrated into the base branch, and kept while a PR is open.

`--abandon` ends a swarm without a winner and keeps every branch. Cleanup
never removes a dirty worktree; it reports it.

## Limits

- Two or three candidates, one stateless engine, worktrees only.
- The fixed model is the routed model at creation; re-apply after changing
  routes, then create a new swarm.
- Checks run one candidate at a time.
- Image builds are bounded by the startup budget, not by CPU or memory
  limits.
