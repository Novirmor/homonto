---
name: swarm
description: Run two or three candidate implementations of one brief in parallel worktrees, verify and preview them side by side, let the user choose one, deliver it and clean up the rest. Use when the user asks for alternatives, a swarm, a bake-off or competing prototypes of one task.
---

# swarm

When installed, follow the shared [workspace and dirty-work policy](../homonto/references/workspace-policy.md)
and its [parallel repository assignments](../homonto/references/execution.md#parallel-repository-assignments).
A swarm is a bounded experiment, not a scheduler: the binary records the
candidates, their checkouts, checks, previews and the user's choice; you
dispatch the agents. Every command below takes `--config "<configRoot>/homonto.toml"`.

Use the harness's native question dialog for every question to the user, in
standalone mode too. If it is unavailable, denied or dismissed, keep the swarm
as recorded and report the pending question; never infer a choice.

## Preconditions

- Schema 2 config with declared `[repos]`, `worktrees.dir`, and a stateless
  engine installed (`no` by default). Lifecycle engines (onto, to) cannot run
  a swarm. Missing pieces are a blocker to report, not to work around.
- Two or three candidates, each an installed agent. The agent's routed model
  is the candidate's fixed model (`homonto explain routing <agent>`). Use the
  candidates the request names; otherwise ask once, recommending agents routed
  to different models. Two agents on the same model compare prompts, not
  models; say so if the user picks that.

## Run

1. Write the brief once, to a file in the declared tmp directory (or the
   swarm's caller-provided path): goal, acceptance criteria, constraints and the
   verification each candidate must run. Then:

   ```sh
   homonto workspace swarm create <name> --repo <alias> --brief <file> \
     --candidate a=<agent> --candidate b=<agent> [--candidate c=<agent>]
   ```

   It pins one base for every candidate and allocates a worktree per
   candidate and repository. Rerunning the same command resumes an interrupted
   creation. Read the JSON: each candidate's task name, agent and models.
2. Dispatch every candidate in parallel: the recorded agent, never another and
   never with a model override, one worker per candidate and repository. Give
   each worker the brief file's exact text, its own absolute checkout paths
   from `homonto workspace swarm inspect <name>`, and read access to the
   declared repositories. A worker writes only its own checkout and never sees
   another candidate's work. Questions come back to you, not the user.
3. Commit each candidate in its own checkout (the parent owns commits). An
   unchanged or uncommitted candidate cannot be selected.
4. With every candidate writer paused, run the same checks on all of them:

   ```sh
   homonto workspace swarm check <name> --writers-paused
   ```

   A failing candidate is a result, not an error. If no configured check
   matches, run the brief's verification in each candidate yourself and keep
   the evidence.
5. When `[preview.<name>]` matches the changed repositories, start the
   previews: `homonto workspace swarm preview start <name>`. Each candidate runs
   in its own container under the configured CPU, memory, process, port and
   time budgets, on a loopback URL. A failed preview is torn down and reported.
6. Compare: `homonto workspace swarm compare <name> --html`. Present the
   side-by-side table (model, change size, checks, previews, verified) and the
   page path, which embeds the running previews next to each other.
7. Ask the user to choose among the verified candidates, or to stop. Name
   what selection means: the winner is delivered and the rejected candidates'
   worktrees, previews and branches are removed at cleanup (unless they ask to
   keep the branches). Only verified candidates can be selected; offer a
   repair round for an unverified one instead (same agent, same checkout,
   commit, recheck).
8. Record the choice: `homonto workspace swarm select <name> --candidate <c>`
   (add `--fallback-verified` only when no configured check matches and your
   verification passed).
9. Deliver the winner through the normal workflow for the caller's endpoint:
   review it with the engine's reviewer, then integrate its branch
   (`work-<name>--<c>/<alias>`) into the recorded base branch, or publish it
   for review with the user's authority. Publication still needs that
   authority.
10. Clean up after delivery, without asking again:

    ```sh
    homonto workspace swarm cleanup <name> --yes [--keep-rejected]
    ```

    It stops every preview first, removes every candidate worktree, deletes a
    rejected branch only while it still points at the revision the user saw,
    and keeps the winner's branch until it is integrated. To stop without a
    winner, `--abandon` releases every candidate and keeps all branches.
    Report a refusal; never force-remove or prune around it.

## Resume

`homonto workspace swarm inspect <name>` shows the recorded candidates,
selection and previews; perform the first missing step. Every swarm command
also removes previews past their time budget and anything an interrupted
preview start left behind. Never recreate a swarm to change its brief or
candidates; abandon it and create a new one.

Standalone fallback (the shared homonto skill is optional): use the host cwd's
`homonto.toml`, keep evidence in conversation, and preserve dirty work; never
automatically stash, reset, delete, commit user changes, or copy `.env` or
secrets into a candidate.
