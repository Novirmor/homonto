---
description: Build two or three competing implementations of one task, compare them side by side and deliver the one I choose.
---

# /swarm

Run a bounded swarm for this task. If the `swarm` skill is installed, follow
it; otherwise apply the same contract below.

Write one brief, then `homonto workspace swarm create` two or three candidates,
each an installed agent whose routed model is its fixed model. Dispatch the
candidates in parallel, each in its own worktree with the identical brief;
commit each, run `homonto workspace swarm check --writers-paused`, start
previews when `[preview.*]` is configured, and show
`homonto workspace swarm compare --html`. Ask me, through the native question
dialog, which verified candidate to keep. Record it with
`homonto workspace swarm select`, deliver it through the normal workflow, then
run `homonto workspace swarm cleanup --yes`.

`$ARGUMENTS`, if present, is the task and any candidates (`NAME=AGENT`).
