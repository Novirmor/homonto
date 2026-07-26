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

- **WHEN** the `onto` dispatcher skill runs its tooling preflight
- **THEN** it directs the reader to the generated reference rather than
  listing providers inline

### Requirement: A tooling config change re-renders the reference

The content fingerprint that gates materialization SHALL include the resolved
tooling configuration, so that editing `[tooling]` re-renders the generated
reference on the next apply.

#### Scenario: Editing a provider re-renders

- **WHEN** apply runs, `code_intel` changes from `graphify` to `okf`, and
  apply runs again
- **THEN** the generated reference content changes to describe okf

#### Scenario: An unchanged config does not churn

- **WHEN** apply runs twice with no configuration change
- **THEN** the second run reports no change to the generated reference

#### Scenario: A state file written by an earlier schema re-renders once

- **WHEN** a state file recorded under the previous schema version is loaded
- **THEN** it loads without error and the next apply re-renders the generated
  reference exactly once, rather than failing or serving stale content

### Requirement: Providers are referenced, never installed

homonto SHALL NOT download, install, update, version-check, or execute a
tooling provider. Declaring a provider SHALL only affect rendered instructions.

#### Scenario: A declared but absent provider still applies cleanly

- **WHEN** `code_intel = "okf"` is declared and okf is not installed
- **THEN** apply succeeds and projects normally

#### Scenario: An absent provider warns at preflight and never halts

- **WHEN** the workflow runs its tooling preflight and the declared provider
  is absent from the environment
- **THEN** the preflight warns and the workflow proceeds, preserving the
  warn-never-halt rule

#### Scenario: Health reporting flags a declared but absent provider

- **WHEN** health reporting runs against a config declaring a provider that is
  not present in the environment
- **THEN** it emits a warning-level finding naming that provider, and the
  finding never blocks a projection
