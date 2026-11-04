---
name: ticket-engine
description: Reference engine entry — plan, implement and review one change in a workspace task, with the lifecycle recorded by the ticket-engine binary so the change survives interruption and compaction.
---

# ticket-engine

The coordinator runs this engine. Workers never run `ticket-engine` or
`homonto workspace` commands.

## Start or resume

1. Read `homonto workspace inspect --json` for `workflowRoot` and the
   declared repositories. Pick the one alias the change writes.
2. If `ticket-engine status CHANGE --root <workflowRoot>` prints a record,
   resume it: its `phase` says where to continue. Otherwise start it from the
   chosen base:

   ```sh
   ticket-engine start CHANGE --root <workflowRoot> --repo ALIAS --base <commit> --branch <base branch>
   homonto worktree create CHANGE --workflow ticket-engine --repo ALIAS --base <base branch> --branch ticket/CHANGE --json
   homonto workspace task create CHANGE --engine ticket-engine --change CHANGE --repo ALIAS --isolation existing
   ```

   A repeated `task create` resumes the same task; it never starts another.

## Stages

1. **plan** — write the plan for the change in the task's checkout notes and
   confirm scope with the user through the native question dialog when the
   ticket leaves it open. Then `ticket-engine phase CHANGE --root <root> implement`.
2. **implement** — dispatch `ticket-implementer` with the plan and the
   checkout path from `homonto workspace task inspect CHANGE`. When it returns,
   run the integration check: `homonto workspace task check CHANGE --writers-paused`.
   Then `ticket-engine phase CHANGE --root <root> review`.
3. **review** — read `homonto explain settings ticket-engine --json` for
   `review`. When it is `always`, dispatch `ticket-reviewer` with the diff. A
   blocking finding returns the change to implement; record why.

Finish with `ticket-engine finish CHANGE --root <root>` and report the
candidate branch. The workflow that called the engine owns delivery.
