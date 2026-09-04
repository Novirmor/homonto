# <workflow-root>/specs/README.md — canonical template

Bootstrap writes this into a repo at `<workflow-root>/specs/README.md`. It records the
living-spec format and the delta-merge semantics onto-close applies at
close, so the repo documents its own spec contract.

## Template

```markdown
# Specs

Living capability specifications — each `<workflow-root>/specs/<capability>.md` states
what the system does **now**, as present-tense truth. No change-log
language; history lives in `<workflow-root>/changes/archive/` and `<workflow-root>/adr/`.

## Requirement format

Each spec is `## Requirements` followed by:

### Requirement: <name>

<first non-empty line contains SHALL or MUST> <single-behavior statement>

#### Scenario: <name>

- **GIVEN** <precondition> / **WHEN** <action> / **THEN** <outcome>

Every requirement carries ≥1 scenario with WHEN and THEN (GIVEN optional).

## How changes land (onto-close merge semantics)

A change proposes edits as a delta at `<workflow-root>/changes/<name>/specs/<cap>.md`
using `## ADDED | MODIFIED | REMOVED | RENAMED Requirements` sections.
Close merges each delta into the living spec, applying sections in a fixed
order so cross-references resolve:

1. **RENAMED** — rename the heading per each `FROM:`/`TO:` pair, body kept.
2. **MODIFIED** — replace the whole requirement of that name entirely
   (never append beside the old block).
3. **REMOVED** — delete the named requirement.
4. **ADDED** — append the new requirement blocks.

A delta for a capability with no living spec creates the file with plain
`## Requirements` (the ADDED wrapper is stripped). After merge the spec
carries no delta-only section headings and no duplicated requirement name.
```
