# Which workflow? — homonto, onto, and the alternatives

This project ships more than one way to drive spec-driven work, which is
confusing until you see the hierarchy. This page is the selection matrix (it
exists because the 2026-07-13 review found no persona/selection guidance —
F21).

## The hierarchy

**homonto is the product.** It is a declarative configuration projector for
AI coding tools: one TOML config is planned, confirmed, and atomically
projected into each tool's native files, with state tracking ownership and
drift, and fail-closed remote sources. If you use nothing else here, you use
homonto.

**onto is homonto's native, binary-enforced spec-driven workflow.** The
`onto` binary owns one versioned state schema and gates the change lifecycle
(`open → design → build → verify → close`). You cannot advance or close on
missing artifacts or a malformed state, because the binary refuses. onto has
a **hard dependency on the compiled binary** (it is not markdown-only), and
the markdown skills invoke the binary rather than editing state by hand.

**to is homonto's native minimal coding framework** — the lightweight
complement to onto (`plan → do → done`, one bookkeeper binary, no evidence
gates). onto and to are complementary: declare either or both and pick per
change; see [the to workflow](guides/to-workflow.md).

**Comet, OpenSpec, and Superpowers are unenforced alternative workflows.**
They drive the same spec-driven shape (propose → design → build → verify →
archive) through skills and prose, without a binary gate: more flexible and
portable, but nothing mechanically prevents skipping a step. **homonto no
longer ships them**: the catalog carries onto and to plus the loose
framework-agnostic skills, nothing else. This repository no longer uses them
either: development is direct — branches, tests, and ADRs
([ADR 0023](adr/0023-develop-directly-without-comet.md)).

## What onto enforces (and what it doesn't)

onto's binary guarantee is **B1: an honest agent cannot skip a step.** It
enforces the *presence and shape* of the state and evidence the workflow
produces; a phase advances only when the required artifacts exist and the
state is well-formed. It does **not** re-derive judgment (it does not read
your design and decide it is good, or run your tests for you). Its threat
model is **T-honest**: it defends against a forgetful or sloppy agent, not
a malicious one forging an audit trail on your own repo. The *projection
engine* is different — it consumes remote content and deletes files, so it
is hardened against a real adversary.

## Choosing

| You want… | Use |
|---|---|
| To project one config into OpenCode | **homonto** (the projector — always) |
| A spec-driven change lifecycle with mechanical gates you can't skip | **onto** (needs the binary) |
| The lightest structured coding flow, no gates, one plan artifact | **to** (needs its binary; excludes onto) |
| A flexible, portable, prose-driven workflow with no binary gate | **Comet** / OpenSpec / Superpowers (external — not shipped by homonto) |

## Why onto is shipped but not self-used

Honesty matters here: **this repository is developed with no workflow stack
at all — not onto, not an external one.** onto is a
shipped-but-not-self-used product. That is a deliberate trade-off. The
maintainers work directly on branches — commits, tests, ADRs
([ADR 0023](adr/0023-develop-directly-without-comet.md)) — while onto's
mechanical enforcement is the product we believe teams who *want* a
can't-skip-a-step guarantee should reach for. Dogfooding onto here is
deferred to v1 ([ROADMAP](ROADMAP.md)).

The cost of not eating our own dog food is that onto misses the feedback
loop that hardened the projector. We offset it two ways, both intentional.
onto's correctness comes from a **full-lifecycle conformance test suite**
(it asserts the gates reject bad work, since no human catches it in daily
use), and from this page telling you plainly where onto fits, so "the
maintainers don't use this?" is an answered question, not a surprise.
