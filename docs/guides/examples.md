# Examples

Complete, copy-ready configurations and command sessions, from a single MCP
server to a multi-repository workspace. Every `homonto.toml` here loads with
the current release. Replace model IDs with ones your OpenCode providers
offer.

| # | Example | Uses |
|---|---|---|
| 1 | [One MCP server](#1-one-mcp-server) | `[mcps]` |
| 2 | [Personal global setup](#2-personal-global-setup-dotfiles) | user scope, settings, terminal |
| 3 | [Team repo with secrets](#3-team-repository-with-secrets) | project MCPs, `${ENV}`, `${pass:}` |
| 4 | [Your own skills, commands, agents](#4-your-own-skills-commands-and-agents) | `local:` |
| 5 | [Solo developer on `to`](#5-solo-developer-on-to) | `[engines.to]` |
| 6 | [Audited team on onto](#6-audited-team-on-onto) | `[engines.onto]`, variants, steps |
| 7 | [Everything: onto + to + no + h](#7-everything-onto--to--no--h) | all packages |
| 8 | [Just `no`](#8-just-no) | `[engines.no]` |
| 9 | [GitHub review only](#9-github-review-only) | `[workflows.h]` |
| 10 | [Cost-tuned models](#10-cost-tuned-models) | per-agent models |
| 11 | [Tooling and scratch](#11-tooling-and-a-scratch-directory) | `[tooling]`, `[tmp]` |
| 12 | [Sibling repositories (legacy)](#12-sibling-repositories-legacy-layout) | `[repos]`, `repo =` |
| 13 | [Monorepo-style combined schema 2](#13-schema-2-combined-layout) | schema 2 |
| 14 | [Control dir + managed records + worktrees](#14-schema-2-control-directory-managed-records-worktrees-integration-checks) | schema 2, `[integration]` |
| 15 | [A catalog agent on its own](#15-a-catalog-agent-on-its-own) | builtin alias |
| 16 | [A pinned remote agent](#16-a-pinned-remote-agent) | `remote:` |
| 17 | [Plugins](#17-plugins) | `[plugins.opencode]` |
| 18 | [Opting out of the workflow plugin](#18-opting-out-of-the-workflow-plugin) | `[integrations.opencode]` |
| 19 | [Pinning tools with mise](#19-pinning-tools-with-mise-in-the-project) | `mise.toml` |
| 20 | [CI drift check](#20-ci-drift-check) | `--exit-code` |
| 21 | [Undoable applies](#21-undoable-applies) | snapshots |
| 22 | [Prompts that drive the workflows](#22-prompts-that-drive-the-workflows) | slash commands |
| 23 | [Scripting workflow state](#23-scripting-workflow-state) | JSON + `jq` |
| 24 | [Cleaning up finished work](#24-cleaning-up-finished-work) | `onto cleanup`, name reuse |

---

## 1. One MCP server

The smallest useful config. No `scope` means user scope: every project gets
it.

```toml
[mcps.codegraph]
command = ["codegraph", "serve", "--mcp"]
```

```console
$ homonto plan
opencode:
  + mcp.codegraph = {"command":["codegraph","serve","--mcp"],"enabled":true,"type":"local"}
$ homonto apply --yes
```

## 2. Personal global setup (dotfiles)

Keep one config in a dotfiles repository and project it into your global
OpenCode config. Everything is `scope = "user"`.

```toml
# ~/dotfiles/ai/homonto.toml
[mcps.fetch]
command = ["uvx", "mcp-server-fetch"]

[skills.handoff]
source = "builtin:handoff"
scope  = "user"

[skills.grilling]
source = "builtin:grilling"
scope  = "user"

[commands.handoff]
source = "builtin:handoff"
scope  = "user"

[commands.grill]
source = "builtin:grill"
scope  = "user"

[settings.opencode]
model       = "anthropic/claude-sonnet-5-5"
small_model = "anthropic/claude-haiku-5-5"
autoupdate  = false

[tui.opencode]
theme  = { name = "gruvbox", mode = "system" }
scroll = { speed = 3, acceleration = false }

[tui.opencode.keybinds]
"app.exit" = ["ctrl+c", "<leader>q"]
```

```console
$ homonto apply --config ~/dotfiles/ai/homonto.toml
```

You now have `/handoff` and `/grill` in every project.

## 3. Team repository with secrets

Project-scoped servers live in the repository's `opencode.jsonc`. Secrets are
references; each developer supplies values.

```toml
[mcps.github]
command = ["github-mcp-server", "stdio"]
env     = { GITHUB_PERSONAL_ACCESS_TOKEN = "${GITHUB_TOKEN}" }
scope   = "project"

[mcps.postgres]
command = ["npx", "-y", "@modelcontextprotocol/server-postgres", "${DATABASE_URL}"]
scope   = "project"

[mcps.brave]
command = ["npx", "-y", "@modelcontextprotocol/server-brave-search"]
env     = { BRAVE_API_KEY = "${pass:team/brave}" }
scope   = "project"
```

```sh
# .env.example — committed
GITHUB_TOKEN=
DATABASE_URL=postgres://localhost/app_dev
```

```console
$ set -a; . ./.env; set +a
$ homonto apply
```

Because project-scoped servers write resolved values into `opencode.jsonc`,
keep that file out of Git (or use user scope for secret-bearing servers).

## 4. Your own skills, commands, and agents

```text
homonto/
├── skills/release/SKILL.md
├── commands/changelog.md
└── subagents/security-review.md
```

```toml
[skills.release]
source = "local:release"
scope  = "project"

[commands.changelog]
source = "local:changelog"
scope  = "project"

[subagents.security-review]
source = "local:security-review"
scope  = "project"
```

`homonto/skills/release/SKILL.md`:

```markdown
---
name: release
description: Use when cutting a release — bump the version, update CHANGELOG.md, tag, and push.
---

# Release

1. Run `task check`; stop on failure.
2. Move "Unreleased" entries in CHANGELOG.md under the new version heading.
3. Commit "release vX.Y.Z", tag `vX.Y.Z`, push the tag.
```

`homonto/commands/changelog.md`:

```markdown
---
description: Draft a CHANGELOG entry for the current branch.
---

Read `git log main..HEAD` and draft an "Unreleased" entry grouped by
Added / Changed / Fixed. $ARGUMENTS
```

`homonto/subagents/security-review.md` (projected verbatim — set its model
here, not in `homonto.toml`):

```markdown
---
name: security-review
description: Use to audit a diff for injection, authorization, and secret handling; returns ranked findings.
mode: subagent
model: anthropic/claude-opus-5-5
permission:
  edit: deny
  bash: deny
---

Review the supplied diff. For each finding give file:line, severity, and the
smallest fix. Do not edit files.
```

## 5. Solo developer on `to`

```toml
[engines.to]
source = "builtin:to"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

```console
$ homonto apply --yes && to init
```

```text
/to add a --json flag to `notes list`
```

## 6. Audited team on onto

Records live in `docs/`, committed with the code. Stronger models for the
roles that judge; a provider variant and a bigger budget for skeptics.

```toml
[engines.onto]
source = "builtin:onto"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
steps = 1500

[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"

[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"

[subagents.onto-reviewer.opencode]
model   = "anthropic/claude-opus-5-5"
variant = "high"

[subagents.onto-skeptic.opencode]
model   = "anthropic/claude-opus-5-5"
variant = "max"
steps   = 200
```

```console
$ homonto apply --yes && onto init
$ git add -A && git commit -m "Adopt onto"
```

```text
/onto-open add per-user rate limiting to the public API
/onto-fix login fails when the email has uppercase letters
/onto-tweak reword the 429 error message
```

## 7. Everything: onto + to + no + h

All engines installed; the coordinator asks once per new task which fits.

```toml
[engines.onto]
source = "builtin:onto"
scope  = "project"

[engines.to]
source = "builtin:to"
scope  = "project"

[engines.no]
source = "builtin:no"
scope  = "project"

[workflows.h]
source = "builtin:h"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"

[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"

[subagents.to-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"

[subagents.no-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.no-reviewer.opencode]
model = "anthropic/claude-opus-5-5"

[subagents.h-spike.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-5-5"
```

```console
$ homonto apply --yes && onto init && to init
```

## 8. Just `no`

Implementation and review with no records and no coordinator; `/no` runs in
your current agent.

```toml
[engines.no]
source = "builtin:no"
scope  = "project"

[subagents.no-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.no-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
```

```text
/no replace the hand-rolled retry loop in client.go with the backoff package we already vendor
```

## 9. GitHub review only

Spike issues and review or continue PRs without installing an engine.
(`/h-resolve-issue` additionally needs an engine.)

```toml
[workflows.h]
source = "builtin:h"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.h-spike.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.h-review.opencode]
model = "anthropic/claude-opus-5-5"
```

```text
/h-spike-issue acme/notes#42
/h-review-pr https://github.com/acme/notes/pull/57
/h-continue-pr 57
/h-review-batch https://github.com/acme/notes limit 15
```

## 10. Cost-tuned models

Mix providers by role: cheap and fast where an agent reads, strong where it
judges.

```toml
[engines.to]
source = "builtin:to"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-explorer.opencode]
model = "openai/gpt-5-mini"
steps = 80
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "openai/gpt-5"
variant = "high"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
variant = "high"
```

Changing a model and re-applying re-renders only the agents that use it.

## 11. Tooling and a scratch directory

```toml
[engines.onto]
source = "builtin:onto"
scope  = "project"

[tooling]
shell_proxy = "rtk"        # you install rtk; homonto only tells the skills
code_intel  = "graphify"   # or "okf"

[tmp]
dir = ".tmp"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

After apply, `.tmp/` exists and is gitignored, and the skills carry generated
`references/tooling.md` and `references/tmp.md`:

```console
$ cat .homonto/catalog/skills/onto/references/tooling.md
…
## Shell proxy — rtk

Run `rtk --version`. On success, all subsequent shell operations in every
workflow phase go through rtk …
```

## 12. Sibling repositories (legacy layout)

No `schema_version`: the config checkout is the implicit main source.

```toml
[repos]
api    = "../api"
shared = "../libs/shared"

[engines.onto]
source = "builtin:onto"
scope  = "project"

# Installed into ../api only
[skills.api-conventions]
source = "local:api-conventions"
scope  = "project"
repo   = "api"

[mcps.api-db]
command = ["pg-mcp", "--dsn", "${API_DEV_DSN}"]
scope   = "project"
repo    = "api"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

```console
$ homonto plan
repos (repo-tagged project resources project only into their named repo):
  api  /work/api
  shared  /work/libs/shared
opencode:
  …
opencode@api:
  + projmcp.api-db = …
  + skill.api-conventions = …
$ onto new add-pagination --repo api
```

## 13. Schema 2, combined layout

The config repository is also a source and holds the records.

```toml
schema_version = 2

[workflow]
root = "docs"
git  = "existing"

[repos]
app = "."
api = "../api"

[worktrees]
dir = "../execution"

[engines.to]
source = "builtin:to"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

```console
$ to new add-search --repo app --repo api --base api=main
$ homonto worktree create add-search --workflow to --repo app --base main --branch to/add-search --json
$ homonto worktree create add-search --workflow to --repo api --base main --branch to/add-search --json
```

## 14. Schema 2: control directory, managed records, worktrees, integration checks

```text
/work/control/   homonto.toml (not Git)
/work/records/   managed records repository
/work/api/  /work/web/   sources
/work/execution/ worktrees
```

```toml
# /work/control/homonto.toml
schema_version = 2

[workflow]
root = "../records"
git  = "managed"

[repos]
api = "../api"
web = "../web"

[worktrees]
dir = "../execution"

[integration.api-web-contract]
repos   = ["api", "web"]
repo    = "web"
cwd     = "tests/contract"
command = ["npm", "run", "contract"]

[integration.api-unit]
repos   = ["api"]
repo    = "api"
cwd     = "."
command = ["go", "test", "./..."]

[engines.onto]
source = "builtin:onto"
scope  = "project"

[engines.no]
source = "builtin:no"
scope  = "project"

[subagents.homonto.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.onto-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.onto-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.onto-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
[subagents.no-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.no-reviewer.opencode]
model = "anthropic/claude-opus-5-5"
```

A session from setup to cleanup:

```console
$ cd /work/control
$ homonto apply --yes
$ homonto workspace init --yes                       # creates /work/records with an initial commit
$ onto init

# An onto change spanning both repositories
$ onto new versioned-api --repo api --repo web --base api=main --base web=main
$ homonto worktree create versioned-api --workflow onto --repo api --base main --branch onto/versioned-api --json
$ homonto worktree create versioned-api --workflow onto --repo web --base main --branch onto/versioned-api --json
$ homonto workspace task create versioned-api --engine onto --change versioned-api --repo api --repo web --isolation worktree
#   … build in the returned worktree paths …
$ homonto workspace task check versioned-api --writers-paused
$ homonto workspace checkpoint --path changes/versioned-api/notes.md --message "Record decisions"

# A record-free no task in parallel, on another repository
$ homonto workspace task create tidy-logs --engine no --repo web --isolation existing
```

`api-web-contract` runs whenever `api` or `web` changed; `api-unit` only
when `api` changed. Each command reads `$HOMONTO_INTEGRATION_CONTEXT` for the
checkout paths.

## 15. A catalog agent on its own

Use a shipped agent without installing its engine — here the onto reviewer as
a general reviewer named `audit`:

```toml
[subagents.audit]
source = "builtin:onto-reviewer"
scope  = "project"

[subagents.audit.opencode]
model = "anthropic/claude-opus-5-5"
steps = 180
```

Not valid alongside `[engines.onto]`, which already installs
`onto-reviewer`; tune that one instead.

## 16. A pinned remote agent

```toml
[subagents.reviewer]
source = "remote:https://example.com/agents/reviewer.tar.gz"
digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
scope  = "project"
```

Apply fetches, validates, and checks the digest before writing anything; a
mismatch aborts. Roll back by restoring an earlier digest (served from
cache); reclaim cache with `homonto cache gc`. See
[remote sources](remote-sources.md).

## 17. Plugins

```toml
[plugins.opencode.opencode-quota]
source = "@slkiser/opencode-quota"

[plugins.opencode.permission-observer]
source = "permission-observer"           # bundled

[plugins.opencode.experimental]
source  = "@acme/opencode-experiment"
enabled = false
```

## 18. Opting out of the workflow plugin

Keep the engines but no OpenCode plugin (no panel, no injected context):

```toml
[engines.to]
source = "builtin:to"
scope  = "project"

[integrations.opencode]
workflow_context = false

[subagents.homonto.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-explorer.opencode]
model = "anthropic/claude-haiku-5-5"
[subagents.to-implementer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-reviewer.opencode]
model = "anthropic/claude-sonnet-5-5"
[subagents.to-skeptic.opencode]
model = "anthropic/claude-opus-5-5"
```

## 19. Pinning tools with mise in the project

Commit both files so every developer and CI run the same binaries and the
same projection:

```toml
# mise.toml
[tool_alias]
homonto = "github:Novirmor/homonto"
onto    = "github:Novirmor/homonto"

[tools]
homonto = { version = "0.34.0", matching_regex = "^homonto_" }
onto    = { version = "0.34.0", matching_regex = "^onto_" }
```

```console
$ mise install
$ homonto apply
```

Upgrading later:

```console
$ mise use homonto@latest onto@latest
$ homonto update
$ git commit -am "Upgrade homonto to latest"
```

## 20. CI drift check

Fail CI when someone edited `homonto.toml` without applying, or when the
committed projection doesn't match:

```yaml
# .github/workflows/homonto.yml
name: homonto
on: [pull_request]
jobs:
  plan:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: jdx/mise-action@v2
      - run: homonto plan --exit-code
      - run: onto doctor --quiet
```

`plan --exit-code` exits `2` when an apply is pending. Use `homonto status
--exit-code` on machines that have applied before (`2` pending, `3` drift).

## 21. Undoable applies

```console
$ homonto apply --snapshot --yes
$ homonto snapshot list
$ homonto snapshot undo <apply-id>
undo apply <apply-id>? [y/N] y
```

Undo refuses without changing anything if a managed value was edited after
the apply.

## 22. Prompts that drive the workflows

From OpenCode, with the packages installed:

```text
# Let the coordinator pick (it asks once, with a recommendation)
/onto add CSV export to the reports page

# Explicit paths
/onto-open redesign the permission model for shared notebooks
/onto-fix the importer drops the last row when the file has no trailing newline
/onto-tweak rename "Workspace" to "Project" in the UI copy
/to add a --dry-run flag to the sync command
/no bump the Go toolchain to 1.26.10 and fix any vet findings

# Endpoints and pauses
/onto add CSV export — stop after design so I can review it
/to-plan migrate the cron jobs to the scheduler; plan only

# Resume after a break or a context reset
/onto
/to

# GitHub
/h-resolve-issue acme/reports#112
/h-resolve-conflicts acme/reports#118
/h-review-batch https://github.com/acme/reports author bob

# Utilities
/grill the plan in docs/changes/csv-export/design.md
/handoff focus on the remaining verify findings
```

## 23. Scripting workflow state

```console
$ onto status
csv-export: build — skeleton ok

$ onto gate csv-export --json | jq -r '.[] | "\(.header): \(.set_command)"'
$ onto state csv-export --json | jq '{phase, derived_phase, pending_gates}'
$ to status --all --json | jq '.tasks[] | "\(.change) \(.phase)"'
$ homonto workflow snapshot --json | jq '.'
$ onto graph --json | jq '.cycles'
$ onto doctor --quiet && echo healthy
```

## 24. Cleaning up finished work

An abandoned onto change keeps its record in `changes/` and reserves its
name. Cleanup archives it and removes any clean, integrated worktrees; it
refuses while record files are uncommitted:

```console
$ onto abandon spike-x
spike-x: abandoned (was at phase open)
$ onto cleanup list
onto/spike-x	blocked	identity=13a7e345	changes/spike-x
  archive: changes/archive/2026-10-27-spike-x
  blocked: cleanup: workflow records have uncommitted or ignored content; preserve/checkpoint it before archiving: ?? docs/changes/spike-x/onto-state.yaml
?? docs/changes/spike-x/proposal.md
$ git add docs/changes && git commit -m "Abandon spike-x"
$ onto cleanup preview spike-x
onto/spike-x	cleanup-pending	identity=13a7e345	changes/spike-x
  archive: changes/archive/2026-10-27-spike-x
  apply: onto cleanup apply spike-x --identity 13a7e345 --yes
$ onto cleanup apply spike-x --identity 13a7e345 --yes
onto/spike-x	cleanup-complete	identity=13a7e345	changes/archive/2026-10-27-spike-x
$ onto new spike-x
created change "spike-x" at …/docs/changes/spike-x
```

Across both engines, from a control directory:

```sh
homonto workflow cleanup list --json --config /work/control/homonto.toml \
  | jq -r '.items[] | select(.status == "cleanup-pending") | "\(.workflow) \(.change) \(.identity)"'
```

The coordinator runs the same inventory after it delivers a change and asks
before `cleanup apply`. Full contract:
[cleaning up terminal workflows](workflow-cleanup.md).
