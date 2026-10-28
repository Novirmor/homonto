# Require approval for disproportionate repairs

- **Status:** Accepted
- **Date:** 2026-10-27

## Context

Skeptic-driven repair loops can turn a conflict-resolution-only request into PR
test hardening. Product-contract checks alone are too broad: a real defect in the
same PR does not necessarily belong to the current assignment.

## Decision

Judge repairs, including their cumulative scope, against the current user request
before creating or dispatching tasks. Continue necessary proportionate in-scope
repairs, but obtain explicit scope approval for materially broader work. For
conflict-only work, preserve intended behavior and fix resolution-induced
regressions; leave unrelated test improvements as follow-ups. A non-waivable
finding can block delivery without authorizing an out-of-scope repair.

Reject both unconditional fix-until-green and asking for every routine repair.

## Consequences

Broader repairs may pause for a user decision, and unrelated issues may remain
reported rather than fixed. Both engines share the same proportionality policy;
prompt tests protect its delivery but do not prove live model compliance.
