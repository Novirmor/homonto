# Delta spec — canonical template

One file per affected capability: `docs/changes/<name>/specs/<capability>.md`.
Deltas are living documents during build; onto-close lints them, then
merges into `docs/specs/<capability>.md`.

## Template

```markdown
# Delta Spec: <capability> (<change-name>)

## ADDED Requirements

### Requirement: <name>

Requirement-ID: <stable id, e.g. REQ-<change>-<n> — minted once at design,
never renumbered; heading rewording keeps the ID>

<First line MUST contain SHALL or MUST.> <single-behavior statement>

#### Scenario: <name>

Scenario-ID: <stable id, e.g. SC-<change>-<slug> — minted once, kept across
rewording; `onto evidence record` and `onto trace` key on it>

- **GIVEN** <precondition>
- **WHEN** <action>
- **THEN** <observable outcome>

## MODIFIED Requirements

### Requirement: <exact existing name>

Requirement-ID: <carry the existing ID forward unchanged when present>

<First normative line MUST contain SHALL or MUST. MODIFIED replaces the body,
not the identity.>

#### Scenario: <name>

Scenario-ID: <carry the existing ID forward unchanged when present>

- **GIVEN** <precondition>
- **WHEN** <action>
- **THEN** <observable outcome>

## REMOVED Requirements

### Requirement: <exact existing name>

<one line: why it no longer holds>

## RENAMED Requirements

- FROM: <exact existing name>
  TO: <new name>
```

## Rules (lint-enforced at close)

- Section headings: only `## ADDED|MODIFIED|REMOVED|RENAMED Requirements`;
  omit empty sections.
- Every ADDED/MODIFIED requirement's **first normative line after optional
  `Requirement-ID:` metadata** contains SHALL or MUST (REMOVED bodies are
  removal rationales, RENAMED entries have no bodies — neither is subject to
  this rule).
- **Every** `#### Scenario:` block has WHEN and THEN bullets (GIVEN
  optional), and each ADDED/MODIFIED requirement has ≥1 — scenarios are
  what verify demands evidence for; an unverifiable requirement is a lint
  finding.
- MODIFIED/REMOVED/RENAMED names must match the living spec exactly — a
  MODIFIED name may instead match the TO name of a RENAMED entry in the
  same delta (rename applies first at merge).
- RENAMED preserves the body unless a MODIFIED block also targets the new
  name.
- IDs are optional but load-bearing once present: a Requirement-ID or
  Scenario-ID line inside its block (`onto evidence record`, `onto trace`,
  and `onto doctor` key on these; a duplicate ID is a doctor finding). Mint
  an ID once at design and never renumber — heading rewording keeps its ID.
