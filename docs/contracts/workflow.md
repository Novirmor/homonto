# Workflow contract

A workflow owns the process around an engine: intake from a ticket or brief,
invoking an engine, review and delivery. The h workflows (`h-workflow-gh`,
`h-workflow-gl`, `h-workflow-bb`) are the builtins.

```toml
manifest_schema = 2
name = "my-workflow"
kind = "workflow"
version = "0.1.0"

[workflow]
coordinator = "shared"
entry = ["my-intake"]          # entry commands; each must be declared in [commands]

[requires]
harness = ["skills", "commands", "subagents", "question-dialog"]

[commands]
my-intake = "commands/my-intake.md"

[skills]
my-intake = "skills/my-intake"
```

## The coordinator

There is one coordinator, the shared `homonto` agent (ADR 0045). A workflow
runs under it by setting `coordinator = "shared"`. It may add process skills
and commands that the coordinator loads. It may not replace, fork or rename
the coordinator. Declaring the agent, its skill or its plugin yourself fails
at load ([ADR 0092](../adr/0092-publish-the-package-contract.md)).

## Invoking an engine

A workflow never names an engine (ADR 0067). Its process skill hands the
coordinator three things:

1. the brief: what to change and why, from the intake;
2. the requested **endpoint**: local commit, pushed branch, draft PR,
   review-ready PR, or merged (ADR 0069);
3. the repositories it may change.

The coordinator selects one installed engine for the task. It asks the user
when more than one could run it, and keeps the choice on resume. It then
creates the workspace task (`homonto workspace task create --engine ENGINE`)
and loads the engine's `entry` skill. A workflow that needs an engine when
none is installed stops and says so. It never falls back to doing the
engine's work itself.

## What a workflow may assume about the result

Ownership is fixed: the engine produces candidates and evidence, and the
coordinator owns commits, integration and publication.

| Result | Where it lives | Who owns it |
|---|---|---|
| Candidate commits and branches | the task's checkouts (`homonto workspace task inspect TASK`) | coordinator |
| Integration-check results | the task record (`results`), invalidated by later changes | coordinator |
| Verification evidence | native engines: their lifecycle record; stateless engines: the session/handoff (ADR 0071) | engine |
| Publication (draft, PR, comment) | the host, through a service's draft tools or CLI | coordinator, after the native approval dialog |
| Session handoff | `homonto_handoff` | coordinator |

A workflow may rely on four things:

- **Endpoint or blocker.** The task ends at the requested endpoint, or with a
  reported blocker. It never ends at a silently earlier endpoint.
- **Resume is idempotent.** Re-running `task create` with the same selection
  resumes the recorded task. A different engine, change or repository
  selection is refused.
- **Failure keeps state.** A failed check, a denied permission or a dismissed
  question leaves the task resumable. The coordinator reports the pending
  input instead of improvising consent.
- **Drafts are not gates.** A draft PR may be opened at any phase (ADR 0084).
  It records nothing and waives no gate.

A workflow must not read or write an engine's lifecycle records, run its
binary's mutating commands, or assume stage names. Stages belong to the
engine.
