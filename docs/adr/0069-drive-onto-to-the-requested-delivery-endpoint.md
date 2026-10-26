# Drive onto to the requested delivery endpoint

- **Status:** Accepted
- **Date:** 2026-10-26

## Context

Continuation instructions already required repairing skeptic findings, but real
sessions still ended with executable repairs or verification pending. Reviews
also expanded local-development acceptance into unsupported scenarios. Native
archive/integration completion cannot establish remote PR readiness.

## Decision

We will give onto one coordinator-owned delivery loop across full and preset
phases: implement, verify, triage, repair or obtain evidence, close, publish, and
confirm the requested endpoint. Check delivery prerequisites before final verify.
For a review-ready PR, observe the intended open non-draft PR, delivered verified
head, passing required checks and no confirmed in-scope blocking findings.

The coordinator determines scope and severity from supported scenarios,
requirements and evidence. Skeptics receive complete packs and bounded rechecks
after repairs. Real data-loss and credential-exposure defects remain blocking;
reviewer labels alone cannot expand acceptance. Preserve explicit earlier
endpoints, authorization and immutable archives; post-archive source repairs use
linked follow-ups. Retain the nonblocking observer from ADR 0064, rather than
adding idle-triggered execution or making local receipts assert remote CI success.

## Consequences

Delivery can take longer than local verification and needs remote observations.
Concrete missing decisions, permissions and external waits remain valid stops.
Prompt/materialization tests protect the shipped contract and CLI tests prove
recovery sequences; neither proves model obedience or live GitHub readiness.
