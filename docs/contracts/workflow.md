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

A workflow never names an engine. It asks the coordinator to select one
installed engine for the task, which loads that engine's entry skill. Engine
selection is described in the [engine contract](engine.md).
