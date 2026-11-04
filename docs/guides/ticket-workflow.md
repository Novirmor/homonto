# Building a ticket-driven workflow

This guide builds a complete external extension set — a service for your
company's ticket tracker, an engine that owns its own lifecycle, and a
workflow that carries a ticket to delivery — and runs it on OpenCode and
Claude Code. Every file in it exists in
[`examples/reference-set/`](../../examples/reference-set/); follow along there.
Jira appears only as an illustration: the tracker is a stand-in script, and a
real client slots in behind the same commands. homonto does not ship or
maintain a Jira integration.

You need homonto, git, and either harness. Nothing here changes homonto's
source.

## 1. Lay out the three packages

```text
reference-set/
  ticket-service/    kind = "service"   the tracker, behind ticket-cli
  ticket-engine/     kind = "engine"    plan → implement → review, its own binary
  ticket-workflow/   kind = "workflow"  intake → engine → review → delivery
```

One package per responsibility (ADR 0094): a workflow that needs the tracker
names the service; it does not reach the tracker itself, and another workflow
can use the same service.

## 2. The service: your tracker

[`ticket-service/framework.toml`](../../examples/reference-set/ticket-service/framework.toml)
declares the service `id` a workflow will ask for, the transport command, and
how a session proves it may use it:

```toml
[service]
id = "tickets"
operations = ["read", "publish"]
transport = "ticket-cli"
credentials = [
  { kind = "env", name = "TICKET_TOKEN" },
  { kind = "cli", check = "ticket-cli auth status" },
]

[permissions.coordinator]
bash_ask = ["ticket-cli comment*", "ticket-cli transition*"]

[permissions.workers]
bash_deny = ["ticket-cli *"]
```

The shell rules only tighten: the coordinator is asked before it writes to
the tracker, and workers can never touch it. homonto renders these into every
agent on every harness — OpenCode permission maps, Claude Code agent
policies — without the service knowing which harness runs.

For Jira, `ticket-cli` becomes a thin wrapper over the Jira REST API or
`jira-cli` that keeps the four commands: `auth status`, `view KEY` (JSON with
`key`, `type`, `title`, `body`, `state`), `comment KEY --body-file FILE`, and
`transition KEY STATE`. The reference script stores tickets as JSON files in
`$TICKET_DIR`, so you can try the flow without a tracker:

```sh
mkdir -p /tmp/tickets
printf '{"key": "ABC-12", "type": "bug", "title": "Login fails on Safari", "body": "Steps…", "state": "todo"}\n' > /tmp/tickets/ABC-12.json
export TICKET_DIR=/tmp/tickets TICKET_TOKEN=dev
ticket-cli view ABC-12
```

## 3. The engine: work that survives interruption

[`ticket-engine/framework.toml`](../../examples/reference-set/ticket-engine/framework.toml)
is a **native** engine: it owns a durable record per change, written by its
own binary, so a change resumes after a crash, a new session or compaction.

```toml
[engine]
lifecycle = "native"
binary = "ticket-engine"
entry = ["ticket-engine"]
operations = ["start", "resume", "integration-check", "release"]
stages = ["plan", "implement", "review"]

[engine.agent_stages]
ticket-implementer = ["implement"]
ticket-reviewer = ["review"]
```

The binary writes
`<workflow root>/engines/ticket-engine/<change>/ticket-engine-state.yaml` in
the schema homonto reads ([engine contract](../contracts/engine.md),
ADR 0106). homonto never writes it; it binds source checkouts against it and
checks the recorded generation on every resume:

```sh
ticket-engine start abc-12 --root <workflow root> --repo api --base <commit> --branch main
homonto worktree create abc-12 --workflow ticket-engine --repo api --base main --branch ticket/abc-12 --json
homonto workspace task create abc-12 --engine ticket-engine --change abc-12 --repo api --isolation existing
ticket-engine phase abc-12 --root <workflow root> implement
```

Its agents declare intent, not harness permissions:

```yaml
homonto:
  read_only: false
  bash_allow: ["git status*", "git diff*", "git add *", "git commit *", "go test *"]
  bash_deny: ["git push*", "ticket-engine *", "homonto *"]
  dialogs: false
  spawn: []
```

homonto renders that natively per harness. On Claude Code the reviewer gets
`disallowedTools: Edit, Write, …, Bash` and the implementer a `homonto_policy`
that homonto's hook enforces on each command it runs.

## 4. The workflow: ticket to delivery

[`ticket-workflow/framework.toml`](../../examples/reference-set/ticket-workflow/framework.toml)
uses the shared coordinator, requires the tracker, and lets the config define
one process per ticket type:

```toml
[workflow]
coordinator = "shared"
entry = ["ticket"]

[requires.services]
tickets = "ticket-service"

[processes.fields.steps]
type = "list"
required = true
```

Its skill
([`ticket-to-delivery`](../../examples/reference-set/ticket-workflow/skills/ticket-to-delivery/SKILL.md))
reads the ticket, picks the configured process for its type (asking through
the native question dialog when none fits), asks the coordinator to select an
engine — it never names one — moves the ticket as the work progresses, and
delivers through the installed hosting workflow's draft and publication flow.

## 5. Check the contract

```sh
homonto package check examples/reference-set/ticket-engine examples/reference-set/ticket-workflow examples/reference-set/ticket-service
```

This is the same conformance every builtin passes: manifest schema, kind
table, declared resources, skill frontmatter, agent rendering, and harness
capabilities.

## 6. Publish and pin

Pack each package as a `.tar.gz` with `framework.toml` at its root, publish
it, and pin its digest ([remote sources](remote-sources.md)). A changed
archive under the same URL fails closed until the pin moves.

## 7. Install on both harnesses

```toml
schema_version = 2
harnesses = ["opencode", "claude"]

[repos]
api = "../api"

[engines.ticket-engine]
source = "remote:https://example.com/ticket-engine-0.1.0.tar.gz"
digest = "sha256:<64 hex>"
scope  = "project"

[workflows.ticket-workflow]
source = "remote:https://example.com/ticket-workflow-0.1.0.tar.gz"
digest = "sha256:<64 hex>"
scope  = "project"

[services.ticket-service]
source = "remote:https://example.com/ticket-service-0.1.0.tar.gz"
digest = "sha256:<64 hex>"
scope  = "project"

[workflows.ticket-workflow.processes.bug]
steps = ["reproduce", "fix", "review"]

[routing.default.opencode]
model = "anthropic/claude-sonnet-5-5"

[routing.default.claude]
model = "sonnet"

[routing.engines.ticket-engine.stages.review.claude]
model = "opus"
```

`homonto plan` previews both harnesses; `homonto apply` writes `.opencode/`
and `.claude/` side by side. `homonto doctor` reports each harness's tier and
the Claude Code limitations accepted in ADR 0105.

## 8. Run it

OpenCode: start `opencode` in the config root and run `/ticket ABC-12`.
Claude Code: start `claude` there — the session runs as the coordinator — and
run `/ticket ABC-12`. On both, the coordinator reads the ticket, starts the
engine's change, dispatches the implementer and reviewer, and delivers.
Interrupt it and start a new session: the injected workflow context shows the
`ticket-engine` change and its phase, and the coordinator resumes from there.

## What you did not have to write

No harness code, no permission files, no hooks, no MCP configuration. The
service, engine and workflow say what they need; homonto renders it for each
harness, enforces it where the harness can, and says plainly where it cannot.
