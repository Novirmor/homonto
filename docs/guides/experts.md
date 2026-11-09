# Experts — standalone knowledge and engine-assisted implementation

Experts provide domain knowledge and review. Engines (`onto`, `to`, `no`) own
implementation; workflows such as `h` own an external process. Install only the
pieces you need. An expert-only workspace can be an ordinary non-Git folder.

## Built-ins

| Package | Domain skill / analyst | Reviewer | Shortcut |
|---|---|---|---|
| `architecture` | `expert-architecture` | `expert-architecture-reviewer` | `/architecture` |
| `ui-ux` | `expert-ui-ux` | `expert-ui-ux-reviewer` | `/ui-design` |
| `database` | `expert-database` | `expert-database-reviewer` | `/database-design` |
| `security` | `expert-security` | `expert-security-reviewer` | `/security-review` |

Packages supply domain methods, reference outlines and two advisory workers.
Every expert installation also installs the shared `homonto` coordinator,
`homonto` and `expert` skills, and `/expert`. Shared resources install once and
must agree on scope with other packages that provide them.

## Install and use

Replace `provider/model` with an available model ID; each managed agent has its
own route, so they may use different models or variants.

```toml
[experts.architecture]
source = "builtin:architecture"
scope = "project"

[subagents.homonto.opencode]
model = "provider/model"
[subagents.expert-architecture.opencode]
model = "provider/model"
[subagents.expert-architecture-reviewer.opencode]
model = "provider/model"
```

Run `homonto plan`, then `homonto apply`. In OpenCode:

```text
/architecture propose the boundaries for our billing service; design only
/expert architecture review this proposal against the existing system
```

The coordinator resolves configured experts from a generated inventory, loads
the relevant domain knowledge and delegates bounded analysis or review. It
writes requested artifacts from advisory returns. Analysis/design does not
create lifecycle records or initialize Git. The requested endpoint controls
whether the work ends at a proposal or proceeds into implementation.

### UI implementation with no

```toml
[engines.no]
source = "builtin:no"
scope = "project"
[experts.ui-ux]
source = "builtin:ui-ux"
scope = "project"

[subagents.homonto.opencode]
model = "provider/model"
[subagents.no-implementer.opencode]
model = "provider/model"
[subagents.no-reviewer.opencode]
model = "provider/model"
[subagents.expert-ui-ux.opencode]
model = "provider/model"
[subagents.expert-ui-ux-reviewer.opencode]
model = "provider/model"
```

```text
/ui-design use no and the existing checkout to build the account settings page;
follow our design system and verify the rendered mobile and desktop layouts
```

The expert develops domain criteria; `no-implementer` receives the skill and
design handoff and writes the application. General review remains with
`no-reviewer`, supplemented by domain review. Corrections go through the writer.
Use the same expertise with `to`, `onto` or a GitHub workflow without installing
an engine-specific copy of the expert.

Browser/image tools are provided by the host or your integrations, not installed
by the expert. If rendering or inspection is unavailable, the result names that
gap. A passing build is not visual validation. Package and prompt tests do not
establish reasoning quality or host tool availability.

## Bring your own package

```toml
[experts.company]
source = "local:./experts/company"
scope = "project"

[subagents.homonto.opencode]
model = "provider/model"
[subagents.company-advisor.opencode]
model = "provider/model"
[subagents.company-reviewer.opencode]
model = "provider/model"
```

The local path is relative to `homonto.toml`. Its root contains `framework.toml`:

```toml
name = "company"                 # matches the experts table key
kind = "expert"
version = "0.1.0"
description = "Company design conventions and architecture constraints"

[expert]
skill = "company-design"
analyst = "company-advisor"
reviewer = "company-reviewer"

[skills]
company-design = "skills/design"
[subagents]
company-advisor = "agents/advisor.md"
company-reviewer = "agents/reviewer.md"
```

`skills/design/SKILL.md` contains `name: company-design` and a useful `description`
in frontmatter, followed by methods and links to bundled references/templates.
Both agent files use the neutral advisory contract:

```markdown
---
description: Analyze company-specific design constraints
mode: subagent
homonto:
  read_only: true
  bash: false
  network: true
  dialogs: false
  spawn: []
---

Load company-design. Inspect the assigned evidence and return recommendations,
assumptions, acceptance criteria and open questions to the parent. Do not edit,
publish or operate workflow state. Return execution requests to the parent.
```

Give the reviewer a distinct prompt challenging the candidate. The manifest
requires exactly two distinct agents and a declared skill containing `SKILL.md`.
Use plain identifiers for its skill and roles. Neutral worker headers accept
only `name`, `description`, `mode` and `homonto`; model tuning belongs in config.
Managed experts cannot be primary, writable, shell-enabled or delegating.
This contract grants declared workspace/skill read context to BYO experts without
granting it to arbitrary custom agents. Native tool rules are not a sandbox for
arbitrary integrations.

Additional skills and commands may be packaged. Expert packages carry no plugins
or package dependencies; install engines/workflows independently. Commands should
route to `homonto` and use the inventory through `expert`. The shared runtime is
installed automatically; do not copy its files into your package. Resource names
share the catalog namespace: conflicting content or scope is an error.

Pinned `remote:` packages use the same manifest and existing
[remote-source trust pipeline](remote-sources.md). A `sha256:` digest is required.
Local/remote content changes re-render on apply. Removing a declaration prunes
managed projections and dispatch access, leaving package source files alone.

## Reference an existing native expert

For an agent and skill you already manage directly in OpenCode:

```toml
[experts.company]
source = "external:team/company-advisor"
scope = "project"
description = "Company product and design expertise"
skill = "company-design"                 # optional native skill ID
reviewer = "team/company-reviewer"        # optional native agent ID

[subagents.homonto.opencode]
model = "provider/model"
```

This registers metadata and exact dispatch IDs. It does not install, adopt,
edit, tune or delete native agents/skills. Their models, permissions and directory
access remain user-managed. The coordinator checks native availability when
invoked; `plan`/`apply` cannot prove a host-loaded agent exists. A missing agent,
skill or permission is reported rather than replaced silently. Without a reviewer,
no independent expert review is claimed. MCP servers remain configured natively.

## Configuration fields

| Field | Managed package | External reference |
|---|---|---|
| `source` | `builtin:`, `local:`, or pinned `remote:` | `external:<native-agent-id>` |
| `scope` | Required: `project` or `user` | Required; controls shared coordinator/entrypoint installation |
| `targets` | Optional, only `opencode` | Same |
| `digest` | Required for remote only | Rejected |
| `description` | In manifest | Required, single line |
| `skill` | `[expert].skill` in manifest | Optional native skill ID |
| `reviewer` | `[expert].reviewer` in manifest | Optional native agent ID |
| `repo` | Not supported | Not supported |

A name cannot be declared in experts and engines/workflows. Package kinds are
checked for builtins and BYO manifests. See
[ADR 0089](../adr/0089-compose-experts-independently-of-engines.md).
