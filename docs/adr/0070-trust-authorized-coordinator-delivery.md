# Trust authorized coordinator delivery

- **Status:** Accepted
- **Date:** 2026-10-26

## Context

A workspace accumulated 56 saved approvals while its coordinator carried 321
shell ask rules. Raw GitHub reads repeatedly prompted, encouraging saved patterns
that also cover mutations. Ordinary shell execution was already trusted. The user
chose trusted task-authorized delivery over repeated publication tool prompts.

## Decision

We will allow ordinary pushes, raw GitHub API queries/mutations, routine PR/issue
publication and CI reruns for the coordinator. Keep asks for recognized forced or
deleting pushes, PR merge, administrative operations, non-Git destruction,
privilege escalation and workflow recovery/bypass. Local Git stays allowed.
Workers retain publication/API denies and read-only roles retain shell/edit denies.
Apply the same generated role restrictions to the new no workers, whose profiles
were missing the publication and coordinator-tool denies.

Task authority, verified candidates, exact destinations and required shown-draft
approval remain binding. Do not classify API semantics with shell globs or import
saved approvals into generated policy. OpenCode saved approvals can satisfy asks,
but cannot override configured denies; leave those user-owned approvals intact.

Worker record-edit rules target V2 location-relative internal paths and absolute
external paths. Do not combine legacy Git-relative paths: overlapping strings can
deny unrelated files in nested layouts. Existing frontmatter remains loadable in
V2; this does not promise V1 runtime permission-path compatibility.

## Consequences

Authorized delivery proceeds with fewer interruptions. Raw API calls and scripts
can perform broader effects, so this is execution trust, not an enforced API
sandbox. Retained asks are convenience confirmations, not unoverrideable gates.
Rendered-rule and projection tests cover role separation and path collisions;
they do not prove model compliance or parse arbitrary shell/API payloads.
