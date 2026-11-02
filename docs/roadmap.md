# Roadmap to 1.0 and beyond

**Status:** Product scope and priorities clarified with the maintainer;
implementation contracts still require design and ADRs.
**Baseline:** 2026-11-01, commit `72fb11e`, catalog/release line 0.36.1.

The primary promise is a ready-to-use process that carries work to its intended
endpoint. The first audience is the maintainer and existing users. Hosting
support comes first: GitLab.com, then Bitbucket Cloud, alongside GitHub.

The 1.0 blockers are Claude Code, complete BYO support, the two new hosts,
static/automatic model routing, reusable decision-model support, and stable
configuration/extension contracts with usable onboarding. Model routing and
decision APIs follow the plugin API milestone, before 1.0. Swarm follows 1.0.

## Product boundaries

- **Engine:** owns the implementation stages, transitions and repair loop.
- **Workflow:** owns the external process, such as ticket intake, invoking an
  engine, review and delivery. Ship concrete ready-made workflows.
- **Service integration:** a separate reusable plugin for a hosting or ticket
  service; a custom workflow can use it without installing a hosting workflow.
- **Harness adapter:** handles tool-specific execution and native formats.
  Engines and workflows must remain independent of the harness that uses them.
- **Configuration:** `homonto.toml` selects and configures plugins and can define
  processes through their settings. The selected plugin interprets those
  definitions; stages belong to the engine.
- **Model routing:** static assignments or automatic selection of an execution
  model for the task, engine/workflow and stage.
- **Decision models:** typed judgments available to engine/workflow plugins.
  Model routing is one consumer; classification, next-step selection and scoring
  are other uses. Support Jev/System One-compatible endpoints and native OpenAI
  Decisions through their respective protocol integrations.

Engines, workflows and service integrations are the required public plugin
surfaces. Builtins use a shared API, with exceptions permitted; the API must
still be sufficient for a real external engine/workflow/integration set.
Defining an external harness-adapter or model-selector plugin API is not a
requirement established by this interview. Providing plugins access to decision
models is required; the mechanism for adding new decision protocols remains a
design question.

## Starting point

| Area | Present in the repository | Remaining gap |
|---|---|---|
| Projection | Plan/apply, ownership, drift, snapshots, pinned remote sources, adapter registry | Only OpenCode is registered and accepted as a target |
| Engines and workflows | Separate declarations; `onto`, `to`, `no`, standalone GitHub workflow `h` | Workspace-task engine selection explicitly accepts only the three builtins |
| BYO packages | `local:` and `remote:` packages, manifests, dependencies, version constraints, package capabilities | A supported authoring and runtime contract for independently developed engines/workflows |
| Harness portability | Shared projection machinery; accepted architecture in ADR 0082 | Capability declarations, portable runtime, native Claude Code adapter and verified workflow execution |
| Model choice | Explicit static model/variant per builtin agent; external agents carry their own model | Per-package/per-stage policies, availability discovery and automatic selection |
| Parallel work | Multi-repository tasks, isolated worktrees, integration checks, one writer per checkout | Competing implementations of one task, comparison and prototype selection |

Grounding: direct inspection of code and existing test definitions, supplemented
by a refreshed CodeGraph index. After provisioning the missing CLI through mise,
`task index` succeeded; symbol queries confirmed builtin-only task selection and
OpenCode-only model routing. This is an implementation inventory, not a claim
that the current test suites or live harness integrations passed.

## Before 1.0

### M1 — Three hosting workflows, GitLab first

**Outcome:** the current GitHub process is available on the required hosts.

- Make the existing workflow the GitHub variant, `h-workflow-gh`.
- Add `h-workflow-gl` for GitLab.com, then `h-workflow-bb` for Bitbucket Cloud.
- Preserve the current process and delivery semantics, adapting them to each
  host. Cover the full existing scope: issue investigation and resolution,
  PR/MR review and continuation, conflicts, and batch review.
- Separate reusable service integrations from the workflow packages. Use these
  concrete integrations to inform the public plugin contract in M2.

**Done when:** all three demonstrate the equivalent supported processes on real
sandbox repositories. Provider differences are explicit. Self-managed GitLab
and Bitbucket Data Center are outside the agreed 1.0 host scope.

These are the intended package names; exact configuration keys, command names
and the transition from today's `h` declaration still need an implementation
decision. The first release's internal plugin boundary may evolve while M2
settles the public API.

### M2 — Establish the plugin and configuration contracts

**Outcome:** users know how to add and configure new behavior.

- Define public contracts for engines, workflows and service integrations,
  building on existing package manifests, dependency checks and remote sources.
- Establish the `homonto.toml` API for selecting plugins, configuring their
  settings and defining plugin-interpreted processes, with actionable validation.
- Keep engine/workflow logic harness-independent. Define capability requirements
  and adapter behavior separately from service APIs and package dependencies.
- Specify how external engines integrate with coordinator selection, task state,
  resume and optional lifecycle operations. A custom engine can own its CLI and
  lifecycle; stateless engines remain possible.
- Define native rendering/model configuration for external agents; today they
  are projected verbatim and reject TOML model overrides.
- Include model-routing policies and reusable typed-decision calls in the
  configuration and plugin contracts, ready for M3.

**Done when:** the public API has examples and contract checks, and the hosting
workflows/integrations use the settled boundaries. Record architectural choices
and any builtin exceptions in ADRs before treating the API as stable.

### M3 — Static/auto model routing and reusable decision models

**Outcome:** users can select execution models automatically, and plugins can
use specialized decision models for their own bounded judgments.

- Extend per-agent static settings with explicit per-engine/workflow and stage
  policies. Define precedence and how a selected route reaches the harness.
- Add automatic selection from available execution models using task needs and
  configured constraints. Keep an explicit static mode and fallback behavior.
- Expose typed choices, yes/no probabilities and scores to plugins. The first
  two protocol integrations are configurable Jev/System One-compatible endpoints
  and direct OpenAI Decisions API access.
- Keep provider protocol differences in the integrations. Engines/workflows
  consume the decision capability independently of their harness; a decision
  model is configured separately from the chat model executing the work.
- Let plugins use decisions for routing, classification, next-step selection or
  evaluation. Define handling of unavailable models, refusals and uncertain
  answers, and record the selected route and returned decision signals.
- Evaluate routing against static assignments on representative tasks, including
  completion quality, verification results, cost and latency. Evaluate decision
  behavior separately for each provider and supported question type.

**Done when:** static and auto routing work on OpenCode, both decision protocols
are exercised, and an external plugin uses typed decisions for a purpose beyond
model routing. M4 verifies these capabilities on Claude Code as well. Revisit
ADR 0016 deliberately: it removed hidden tiers that coupled configuration
validity to internal model-role names.

#### Researched API surfaces (2026-11-01)

| Surface | Documented model and request shape |
|---|---|
| Jev-compatible System One | `POST /v1/systemone`; `model`, `state`, named `questions`; `choice`, `noul`, `score` and model-dependent extensions |
| Native OpenAI Decisions | `POST https://api.openai.com/v1/decisions`; model `gpt-6-luna`; `input` and a `questions` array; `choice`, `predicate`, `score` |

The question types overlap, but the native request/response formats differ.
Compatibility must cover the protocol and supported capabilities, not just a
model name. Provider-specific extensions need explicit capability checks.

Sources: [OpenAI Decisions guide](https://developers.openai.com/api/docs/guides/decisions),
[OpenRouter Jev/System One guide](https://openrouter.ai/docs/guides/community/jev),
and [OpenRouter Decisions schema](https://openrouter.ai/docs/api/api-reference/alphadecisions/submit-a-decisions-request).
OpenRouter documents `openai/gpt-6-luna-decisions` through its shared Decisions
format, but that model's detail page returned 404 during research. Availability
through that gateway was not verified; direct OpenAI support does not depend on
it. No paid inference calls were made for this research.

### M4 — Prove complete BYO and native Claude Code execution

**Outcome:** builtins and a genuine external extension set execute through the
same platform on both harnesses.

- Implement the portable runtime and adapter work described by ADR 0082:
  reusable runtime behavior, native integration shims and harness tool mappings.
  Preserve existing OpenCode behavior while extracting the shared core.
- Add native Claude Code configuration, agents, permissions and hooks. Support
  all ready-made processes, including the three hosting workflows.
- Verify M3's static/auto execution-model routing and plugin decision calls on
  Claude Code while keeping their policies independent of the harness.
- Make external engines full participants in coordinator dispatch and supported
  task operations, replacing builtin-only selection where required.
- Supply and exercise an external engine + workflow + service integration on
  OpenCode and Claude Code without changes to homonto's source.
- Document authoring through a company-ticket → engine → review → delivery
  example. Jira is an illustrative BYO use case, not a required maintained Jira
  integration for 1.0.

**Done when:** builtins and the external set complete their declared processes
on both real harnesses, including relevant resume and recovery scenarios.
Claude Code and BYO implementation can progress together; final validation of
the external set requires both harnesses.

The system may offer an explicitly limited mode when an adapter lacks a
capability. Whether a particular Claude Code limitation is acceptable for 1.0
must be assessed against the actual missing guarantee. Neither blanket parity
nor blanket acceptance of weaker guarantees was agreed. Reconcile this policy
with ADR 0082 during design.

### M5 — Stabilize and release 1.0

**Outcome:** existing users can start quickly or compose their own setup, and
extension authors have a dependable contract.

- Provide both a ready-made starting configuration and documented manual TOML
  configuration, including plugin settings, examples and precise diagnostics.
- Stabilize the configuration and extension API using the M1–M4 examples.
- Decide the 1.x compatibility and upgrade policy explicitly. ADR 0081 currently
  supports only live formats; a 1.0 label alone does not change that policy.
- Run the full release gate and real harness/host checks; document the supported
  combinations and any specifically accepted Claude Code limitations.

**1.0 gate:** the three complete hosting workflows, native Claude Code support,
public engine/workflow/service plugins, plugin-interpreted process configuration
in TOML, static/auto execution-model routing, reusable Jev-compatible and native
OpenAI decision support, the working external extension set, and both onboarding
paths.

During design, also reconcile the proposed runtime MCP registration with
ADR 0085: user MCP servers, skills and commands remain bring-your-own. The older
harness design still discusses managed MCP declarations. This interview did not
change that ownership boundary.

## After the foundation is stable

### M6 — Swarm and visual prototype comparison

**Outcome:** users compare working alternatives for one task.

- Start with a bounded experiment: two or three implementations from the same
  base, each in its own worktree, using fixed models and a shared brief.
- Apply common verification and compare results before integrating the chosen
  candidate. Reuse workspace isolation, integration checks and cleanup.
- Add local Docker/web previews, a side-by-side presentation and user selection;
  define resource budgets and teardown for the preview environments.

**Done when:** a user can inspect three running prototypes, choose one, deliver
the verified winner and clean up the alternatives. Static routes are sufficient
for the first experiment; automatic model selection can be integrated later.

Codex and Antigravity remain follow-on harnesses under ADR 0082, prioritized by
demand and maintainable verification coverage after Claude Code.

## Sequence and evidence

```text
M1: h-workflow-gh / -gl / -bb (GitLab is the first new host)
  → M2: public plugin and homonto.toml contracts
  → M3: static/auto model routing + reusable decision-model APIs
  → M4: complete BYO + portable runtime + Claude Code
  → M5: onboarding, compatibility, verification → 1.0
  → M6: swarm and visual prototype comparison
```

Key implementation anchors:

- [`internal/adapter/registry/registry.go`](../internal/adapter/registry/registry.go)
  and [`internal/config/validate.go`](../internal/config/validate.go): active target boundary.
- [`internal/catalog/catalog.go`](../internal/catalog/catalog.go),
  [`localframework_test.go`](../internal/catalog/localframework_test.go) and
  [`framework_source_test.go`](../internal/config/framework_source_test.go): existing BYO foundations.
- [`internal/worktask/task.go`](../internal/worktask/task.go): builtin-only task selection.
- [`catalog/plugins/homonto-workflow/`](../catalog/plugins/homonto-workflow/): current host integration and GitHub runtime.
- ADRs [0067](adr/0067-separate-workflows-from-engines.md),
  [0068](adr/0068-coordinate-parallel-work-by-repository.md),
  [0082](adr/0082-support-harnesses-natively-in-tiers.md),
  [0085](adr/0085-bring-your-own-mcp-servers-and-skills.md),
  [0016](adr/0016-remove-model-tiers.md) and
  [0081](adr/0081-support-only-the-live-version.md): existing constraints and rationale.
- [`harness-architecture.md`](harness-architecture.md) and
  [`release-checklist.md`](release-checklist.md): planned portability and release evidence.
