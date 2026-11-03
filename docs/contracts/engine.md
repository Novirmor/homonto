# Engine contract

An engine owns the implementation stages of a change: how work is planned,
built, verified and repaired. A workflow or the user selects one installed
engine per task (ADR 0067). It runs the same on every harness that provides
what it requires.

```toml
manifest_schema = 2
name = "my-engine"
kind = "engine"
version = "0.1.0"

[engine]
lifecycle = "stateless"          # stateless | native
# binary = "my-engine"           # required for native: the CLI that owns the lifecycle
entry = ["my-engine"]            # entry skills; each must be declared in [skills]
operations = ["start", "resume", "integration-check", "release"]
stages = ["plan", "build", "review"]
coordinator = "shared"           # optional: run under the shared homonto coordinator

[engine.agent_stages]            # optional: which stages each of your agents serves
my-reviewer = ["review"]

[requires]
harness = ["skills", "commands", "subagents", "question-dialog"]

[skills]
my-engine = "skills/my-engine"
```

| Field | Rule |
|---|---|
| `lifecycle` | `stateless`: the engine keeps no durable state; a workspace task records only its name. `native`: the engine owns a lifecycle record through `binary` and binds source checkouts through homonto's worktree registry (see Task integration). |
| `binary` | Plain command name. Required for `native`, forbidden for `stateless`. |
| `entry` | At least one skill the coordinator or a command loads to start the engine. |
| `operations` | Subset of `start`, `resume`, `integration-check`, `release`. `start` and `resume` are required. |
| `stages` | At least one identifier (`[a-z][a-z0-9-]*`), unique. Stages are the addressable units model-routing policies target. |
| `coordinator` | Empty or `shared`. An engine never ships its own coordinator. |
| `agent_stages` | Maps each of the engine's own `[subagents]` to the declared stages it serves. A stage routing policy reaches an agent only through this binding ([routing.md](routing.md), [ADR 0099](../adr/0099-route-models-by-policy.md)). An unbound agent follows the engine-level policy. |

## Task integration

A coordinator runs an engine's work as a workspace task
(`homonto workspace task create NAME --engine ENGINE --repo ALIAS ...`). The
task reads the engine's contract, never its name
([ADR 0093](../adr/0093-describe-engines-instead-of-naming-them.md)):

| Operation | Command | Without it |
|---|---|---|
| `start` | `task create` | required |
| `resume` | every later load, including a repeated `create` | required |
| `integration-check` | `task check` | refused |
| `release` | `task cleanup` | refused |

- **Stateless** engines take no `--change`. Writable repositories get
  task-owned worktrees (or the existing checkout), and the task keeps only the
  engine name and lifecycle.
- **Native** engines require `--change` naming their active lifecycle record.
  They bind writable checkouts through homonto's worktree registry under the
  engine name (`homonto worktree create CHANGE --workflow ENGINE ...`). That
  binding is how the engine reports state to the coordinator: the task
  validates its recorded generation and bindings against it on every resume.
  Only builtin native engines can run tasks today. External lifecycle
  reporting through the same registry is #67.
- A task records the lifecycle it was created with. If the installed engine
  later declares a different one, resume fails and names both.

## Builtins

| Engine | Lifecycle | Binary | Stages |
|---|---|---|---|
| `onto` | native | `onto` | open, design, build, verify, close |
| `to` | native | `to` | plan, do, done |
| `no` | stateless | — | implement, review |

`no` has no coordinator (ADR 0067). It ships one entry skill and two
subagents.
