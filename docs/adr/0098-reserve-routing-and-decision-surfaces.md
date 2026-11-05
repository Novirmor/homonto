# Reserve routing and decision surfaces

- **Status:** Partially superseded by 0099 and 0101 (the reserved tables are now defined)
- **Date:** 2026-11-03

## Context

M3 adds static and automatic model routing and reusable typed decision
calls. Today the only route is `[subagents.<name>.opencode]`, stamped into
an agent file at apply. Stages were not an addressable surface, so a policy
had nothing stable to target. M3 must not have to change the M2 contracts.

## Decision

- Engines name their stages in `[engine].stages` (ADR 0092). Those
  identifiers are the routing addresses.
- `[routing]` and `[decisions]` are reserved config tables. Load rejects
  them by name until M3 defines their fields. The selectors are fixed now
  (`default`, `workflows.<w>`, `engines.<e>`, `engines.<e>.stages.<s>`), and
  so is the precedence: the most specific selector wins, and an explicit
  per-agent `[subagents.<name>.<tool>]` block wins over every policy.
- Selectors are harness-independent. Policy fields are not specified here.
  They will be native per harness (ADR 0082), so the reservation hard-codes
  no OpenCode field.
- Typed decisions have a fixed plugin-facing shape in `internal/decision`:
  `choice`, `yes-no` (probability of yes) and `score` (on a declared
  scale), addressed by `<package>.<point>`, validated on both request and
  result, behind a `Decider` interface. No provider exists. Provider
  protocols are #55–#58.

We rejected defining policy fields now. The provider work decides what a
policy needs, and a guessed schema would be a breaking change once real
providers exist.

## Consequences

#32 can build on fixed addresses and shapes. A user who writes `[routing]`
today gets a precise "not yet" instead of a generic unknown-key error. The
cost is that renaming a builtin stage is now a contract change.
