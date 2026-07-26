# Comet Design Handoff

- Change: optional-tooling-providers
- Phase: design
- Mode: compact
- Context hash: 895580432e8b1147185b550af4ac332b3eec1ef61dfe0cb4961da67cf00527d8

Generated-by: comet-handoff.sh

OpenSpec remains the canonical capability spec. This handoff is a deterministic, source-traceable context pack, not an agent-authored summary.

## openspec/changes/optional-tooling-providers/proposal.md

- Source: openspec/changes/optional-tooling-providers/proposal.md
- Lines: 1-71
- SHA256: 74a0a58c6c6fbcd5e9b5f7a6b19f5f9dc9e53ca138e1884e4ecd02dfa209070e

```md
## Why

The `onto` and `to` frameworks name two specific third-party tools in shipped
skill prose — `rtk` (a token-optimizing shell proxy) and `graphify`
(code-intelligence grounding) — across 11 catalog files. ADR 0008 downgraded
them from hard requirements to warn-never-halt, but the names stayed baked in:
a user who does not run them is warned on every dispatch, and a user who
grounds with a different tool has no way to declare it. Which tools a
developer uses is configuration, not shipped prose.

## What Changes

- New neutral `[tooling]` config table with two managed keys:
  - `shell_proxy` — `"rtk"` or `"none"`
  - `code_intel` — `"graphify"`, `"okf"`, or `"none"`
- **BREAKING (shipped skill behavior, not config load)**: when `[tooling]` is
  absent both keys default to `"none"`. No third-party tool is named or probed
  unless the user opts in. Grounding falls back to direct file reading, which
  is already the documented fallback in ADR 0008. Existing configs keep
  loading unchanged; only the rendered skill text differs.
- `homonto apply` generates a per-install `references/tooling.md` inside the
  `onto` and `to` dispatcher skills from the declared providers. Catalog
  `SKILL.md` files stay byte-stable verbatim artifacts and defer to it,
  following ADR 0006's reference-file pattern.
- The 11 hardcoded `rtk`/`graphify` mentions become provider-neutral pointers.
- `okf` selects okf-generator as the code-intelligence provider. homonto
  references it and never vendors, downloads, or installs it — ADR 0015 bars
  third-party content from the catalog. Installing the provider stays the
  user's job, exactly as it already is for `rtk` and `graphify`.
- The catalog content fingerprint gains the resolved tooling config as an
  input, so editing `[tooling]` re-renders the sidecar instead of being
  swallowed by the materialize gate.

## Non-Goals

- Installing, downloading, updating, or version-checking any provider.
- A provider plugin API. The three code-intelligence values and two
  shell-proxy values are a closed set; adding a provider is a catalog change.
- Changing ADR 0008's warn-never-halt rule. A declared-but-absent provider
  still warns and proceeds; only the `onto` binary itself halts.
- Per-phase or per-skill provider overrides. One provider pair per repository.
- Retiring `rtk` or `graphify` as recommended tools; both remain first-class
  selectable values.

## Capabilities

### New Capabilities

- `tooling-providers`: declaring optional shell-proxy and code-intelligence
  providers in configuration, resolving their defaults, and rendering the
  selected pair into framework dispatcher skills at materialize time.

### Modified Capabilities

None. The two existing specs (`agent-models`, `onto-evidence-gates`) describe
model projection and onto's evidence gates; neither states a requirement about
tooling preflight, so no delta spec is needed.

## Impact

- `internal/config` — schema, validation of the closed value sets, defaults.
- `internal/catalog` — sidecar rendering and fingerprint inputs.
- `internal/engine` — threading the resolved tooling config into materialize.
- Catalog content — `skills/onto`, `skills/onto-open`, `skills/onto-design`,
  `skills/onto-close`, `commands/onto.md`, `subagents/onto-explorer.md`,
  `subagents/to-explorer.md`, and the `to` dispatcher.
- Docs — `guides/configuration.md`, `guides/onto-workflow.md`,
  `guides/to-workflow.md`, release notes, and a new ADR recording
  provider-neutrality.
- Catalog and framework version bumps; both frameworks re-materialize on the
  next apply.

```

## openspec/changes/optional-tooling-providers/design.md

- Source: openspec/changes/optional-tooling-providers/design.md
- Lines: 1-169
- SHA256: 9cd34009d1ca77a72d5314c628cedc644cb3e56c0373fb8e12384fe9e83fd4c1

[TRUNCATED]

```md
## Context

The `onto` and `to` dispatcher skills run a tooling preflight before any phase
work. ADR 0005 introduced `rtk` and `graphify` as recommended tools; ADR 0008
downgraded them from halting requirements to warn-never-halt. Neither ADR
removed their names from the shipped prose, so today 11 catalog files name them
directly and every user is told about both regardless of what they run.

Two existing mechanisms make this tractable:

- **ADR 0006 reference-file skill architecture** — a skill loads a
  `references/*.md` file on demand instead of inlining a protocol. The onto
  skills already use this for the no-slop, subagent, TDD, and debugging
  protocols.
- **Config-driven render at materialize time** —
  `MaterializeSubagents(dstRoot, names, renderCtx)` in
  `internal/catalog/materialize.go` already renders per-tool agent variants
  from config rather than copying bytes verbatim.

Materialization is gated in `internal/engine/engine.go` by a composite
fingerprint:

```
fingerprint = subagentRenderFingerprint(renderCtx) + ":" + contentFP
```

recorded in `State.SubagentRenderFingerprint`, combined with the catalog
version and a presence check over every file a materialize would write.

## Goals / Non-Goals

**Goals**

- Provider choice becomes configuration, expressed once in `homonto.toml`.
- A user who declares nothing is told about nothing.
- `okf` becomes a first-class alternative to `graphify` without homonto
  taking on any responsibility for installing it.
- Provider prose lives in exactly one place and both frameworks share it.

**Non-Goals**

- Installing, updating, or version-checking providers.
- A provider plugin API. The value sets are closed.
- Changing the warn-never-halt rule from ADR 0008.
- Per-phase or per-skill provider overrides.

## Decisions

### D1. Generated sidecar, not a splice into SKILL.md

The chosen mechanism generates `references/tooling.md` inside each materialized
dispatcher skill. `SKILL.md` ships byte-stable and says only "run the tooling
preflight described in `references/tooling.md`".

*Why:* the shipped skill stays a verbatim catalog artifact, so
`ContentFingerprint` keeps its current meaning, every install has identical
`SKILL.md` bytes (supportable, diffable), and it reuses ADR 0006's established
pattern.

*Alternative rejected:* splicing a marked region inside `SKILL.md` at
materialize time. It removes one file read and lets absent providers vanish
entirely, but it turns the most-read file in the framework into per-install
generated content and entangles the content fingerprint with render inputs.

### D2. Provider prose lives in the catalog, not in Go

Each provider gets a fragment at `catalog/tooling/<provider>.md`. The Go layer
selects the two declared fragments, concatenates them under a generated
header, and writes the result. Go holds no provider prose.

*Why:* keeps content in the catalog where ADR 0015 says content belongs;
`ContentFingerprint` already digests catalog source bytes, so editing a
fragment re-materializes for free; prose changes review as prose.

*Alternative rejected:* provider descriptions as Go string constants. Prose in
code, invisible to the content fingerprint, and every wording fix becomes a
binary release.

### D3. Extend the composite fingerprint with a tooling component


```

Full source: openspec/changes/optional-tooling-providers/design.md

## openspec/changes/optional-tooling-providers/tasks.md

- Source: openspec/changes/optional-tooling-providers/tasks.md
- Lines: 1-73
- SHA256: 8afc13e58eee68ae705401866629ec0fa20a07c81c58d304b4247a082c727d28

```md
## 1. Config surface

- [ ] 1.1 `internal/config`: add the `Tooling` struct with `ShellProxy` and
      `CodeIntel`, decoded from a `[tooling]` table, with unknown keys rejected.
- [ ] 1.2 `internal/config/validate.go`: validate both keys against their
      closed value sets, failing with an error that names the offending key and
      lists the accepted values.
- [ ] 1.3 Resolve an absent table or an omitted key to `none`; add tests
      covering absent, partial, full, and invalid configs.

## 2. Provider content

- [ ] 2.1 Create `catalog/tooling/` fragments: `rtk.md`, `graphify.md`,
      `okf.md`, and the `none` fallback text.
- [ ] 2.2 Move the existing rtk and graphify preflight prose out of
      `catalog/skills/onto/SKILL.md` into the fragments verbatim, so no
      instruction is lost in the move.
- [ ] 2.3 Write the okf fragment (index/query/staleness guidance mirroring the
      graphify fragment's shape).
- [ ] 2.4 Wire `catalog/tooling/` into the embedded catalog FS and into
      `ContentFingerprint`'s digested set.

## 3. Sidecar generation

- [ ] 3.1 `internal/catalog`: add the renderer that selects the two declared
      fragments and writes `references/tooling.md` under a generated header.
- [ ] 3.2 Write the sidecar for dispatcher skills during `Materialize`,
      inside the existing stage-then-swap so a crash cannot leave it partial.
- [ ] 3.3 Golden-file tests: both-none, rtk+graphify, rtk+okf, none+okf —
      asserting the undeclared provider's name never appears.

## 4. Materialize gating

- [ ] 4.1 `internal/engine`: compute `toolingFP` and append it to the
      composite fingerprint.
- [ ] 4.2 Extend the presence check so a deleted `references/tooling.md`
      forces re-materialization.
- [ ] 4.3 Tests: editing `[tooling]` re-renders; an unchanged config is a
      no-op; deleting the sidecar restores it.

## 5. Neutralize shipped catalog prose

- [ ] 5.1 `catalog/skills/onto/SKILL.md`: replace the inline rtk and graphify
      preflight steps with a pointer to `references/tooling.md`.
- [ ] 5.2 `catalog/skills/onto-open/SKILL.md` and its `references/notes.md`
      and `references/proposal.md`: provider-neutral grounding wording.
- [ ] 5.3 `catalog/skills/onto-design/SKILL.md` and its
      `references/brainstorm-protocol.md` and `references/design.md`: same.
- [ ] 5.4 `catalog/skills/onto-close/references/lint-checklist.md`,
      `catalog/commands/onto.md`, `catalog/subagents/onto-explorer.md`,
      `catalog/subagents/to-explorer.md`: same.
- [ ] 5.5 The `to` dispatcher gains the same preflight pointer.
- [ ] 5.6 Add a test asserting no provider name appears in shipped catalog
      content outside `catalog/tooling/`.

## 6. Docs and versioning

- [ ] 6.1 `docs/guides/configuration.md`: document the `[tooling]` table, the
      closed value sets, and the `none` defaults.
- [ ] 6.2 `docs/guides/onto-workflow.md` and `docs/guides/to-workflow.md`:
      replace the named-tool preflight prose with the provider model.
- [ ] 6.3 New ADR recording provider-neutrality and the referenced-not-vendored
      rule, superseding the tool-naming parts of ADR 0005 and ADR 0008.
- [ ] 6.4 `docs/release-notes.md`: the behavior change plus the config snippet
      that restores rtk and graphify.
- [ ] 6.5 Bump `catalog/version.txt` and both framework versions.

## 7. Verification

- [ ] 7.1 `go build ./... && go vet ./...` clean; `go test ./...` green.
- [ ] 7.2 Extend the onto and to Docker E2E suites to assert the sidecar is
      written and matches the declared providers.
- [ ] 7.3 `./scripts/gate.sh` green.

```

## openspec/changes/optional-tooling-providers/specs/tooling-providers/spec.md

- Source: openspec/changes/optional-tooling-providers/specs/tooling-providers/spec.md
- Lines: 1-131
- SHA256: 8815d3b1784e960452932671d450d4a1f415a2852a8037b6484bdb9ff2c28426

[TRUNCATED]

```md
## ADDED Requirements

### Requirement: Tooling provider declaration

The configuration SHALL accept an optional `[tooling]` table with two managed
keys, `shell_proxy` and `code_intel`, each drawn from a closed set of provider
names. `shell_proxy` SHALL accept `rtk` or `none`. `code_intel` SHALL accept
`graphify`, `okf`, or `none`.

#### Scenario: A declared provider pair loads

- **WHEN** a config declares `shell_proxy = "rtk"` and `code_intel = "okf"`
- **THEN** the config loads and both providers resolve to those values

#### Scenario: An unknown provider name is rejected at load

- **WHEN** a config declares `code_intel = "ctags"`
- **THEN** loading fails with an error naming both the offending key and the
  accepted values, and no projection is planned

#### Scenario: An unknown key in the table is rejected

- **WHEN** a config declares a key other than `shell_proxy` or `code_intel`
  inside `[tooling]`
- **THEN** loading fails with an error naming the unknown key

### Requirement: Provider defaults resolve to none

An omitted key SHALL resolve to `none`, whether the `[tooling]` table is
absent entirely or present with only one key set. A config that predates this
capability SHALL continue to load without modification.

#### Scenario: Absent table defaults both keys

- **WHEN** a config declares no `[tooling]` table
- **THEN** `shell_proxy` and `code_intel` both resolve to `none`

#### Scenario: Partial table defaults only the omitted key

- **WHEN** a config declares `[tooling]` with `shell_proxy = "rtk"` only
- **THEN** `shell_proxy` resolves to `rtk` and `code_intel` resolves to `none`

### Requirement: Generated tooling reference

Materialization SHALL write a `references/tooling.md` file into each
materialized framework dispatcher skill, describing exactly the providers the
resolved configuration declares. The generated file SHALL be the single place
any provider is named.

#### Scenario: Both providers none

- **WHEN** both keys resolve to `none` and the framework materializes
- **THEN** the generated reference states that no providers are declared and
  that grounding falls back to direct file reading, and it names no provider

#### Scenario: A provider pair renders

- **WHEN** `shell_proxy = "rtk"` and `code_intel = "okf"` materialize
- **THEN** the generated reference documents the rtk probe and the okf
  grounding steps, and does not mention graphify

#### Scenario: Every enabled framework receives the reference

- **WHEN** a config enables the `onto` framework and materializes
- **THEN** the generated reference exists inside the materialized `onto`
  dispatcher skill directory

### Requirement: Shipped catalog content stays provider-neutral

Shipped catalog skills, commands, and subagents SHALL NOT name a specific
tooling provider in their prose. They SHALL defer to the generated reference
for provider-specific instructions.

#### Scenario: No provider name survives in shipped catalog prose

- **WHEN** the shipped catalog content is scanned for the provider names
- **THEN** no match occurs outside the provider descriptor source that feeds
  the generator

#### Scenario: The dispatcher points at the generated reference

```

Full source: openspec/changes/optional-tooling-providers/specs/tooling-providers/spec.md
