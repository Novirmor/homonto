# Permit routine workflow evidence recording

- **Status:** Accepted
- **Date:** 2026-09-07

## Context

ADR 0047 denied every onto evidence setter to keep bypasses unreachable. Those
setters also record ordinary proposal, approach, verification, and close reviews
that the coordinator must complete. The denial made a normal onto workflow stop
at its first required token.

## Decision

The coordinator may run routine evidence setters after it has performed the
review they record. `onto bypass` and `to bypass` remain denied. Skills must
describe the evidence being recorded and never present a routine setter as a
user-approval substitute.

## Consequences

The coordinator can complete the documented workflow and records a reviewable
claim for each gate. The protection against command-level bypasses remains; the
control moves from impossible blanket denial to evidence-backed workflow rules.
