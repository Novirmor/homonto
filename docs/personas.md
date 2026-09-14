# Which workflow? — homonto, onto, to, and the h skill bundle

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
(`open → design → build → verify → close`). Normal advance and close commands
refuse missing required artifacts or malformed state. onto has
a **hard dependency on the compiled binary** (it is not markdown-only), and
the markdown skills invoke the binary rather than editing state by hand.

**to is homonto's native minimal coding framework** — the lightweight
complement to onto (`plan → do → done`, one bookkeeper binary, no evidence
gates). onto and to are complementary: declare either or both and pick per
change; see [the to workflow](guides/to-workflow.md).

**h is the GitHub skill bundle over both lifecycle workflows.** It ships
`h-spike-issue`, `h-resolve-issue`, `h-review-pr`, `h-continue-pr`, and
`h-review-batch`, with matching `/h-*` commands and the read-only `h-spike`
and `h-review` workers. `[frameworks.h]` with `source = "builtin:h"` remains
the stable generic package key and installs onto and to as dependencies.
The shared `homonto` coordinator handles `/onto`, `/to`, and `/h-*`; the
request or matching change determines which lifecycle dispatcher it loads
([ADR 0045](adr/0045-one-homonto-coordinator-for-both-workflows.md)).

**Comet, OpenSpec, and Superpowers are unenforced alternative workflows.**
They drive the same spec-driven shape (propose → design → build → verify →
archive) through skills and prose, without a binary gate: more flexible and
portable, but nothing mechanically prevents skipping a step. **homonto no
longer ships them**: the catalog carries onto and to, the `h` GitHub skill
bundle, and loose framework-agnostic skills. This repository no longer uses them
either: development is direct — branches, tests, and ADRs
([ADR 0023](adr/0023-develop-directly-without-comet.md)).

## What onto enforces (and what it doesn't)

onto's normal transition commands enforce the *presence and shape* of required
state, artifacts, and evidence, plus applicable Git checks. Explicit operator
bypasses are separate commands that record skipped checks and a reason; they
do not certify verification. The binary does **not** re-derive judgment (it does
not read your design and decide it is good, or run your tests for you). Its threat
model is **T-honest**: it defends against a forgetful or sloppy agent, not
a malicious one forging an audit trail on your own repo. The *projection
engine* is different — it consumes remote content and deletes files, so it
is hardened against a real adversary.

`to done --verified` requires phase `do` and applicable source-cleanliness
checks, but records verification as a self-asserted claim. Its skills require
real tests and review. Both doctors provide read-only diagnostics; the bundled
OpenCode bridge observes state and reports findings. Neither makes session
completion non-skippable (see [enforcement](guides/enforcement.md)).

## Choosing

| You want… | Use |
|---|---|
| To project one config into OpenCode | **homonto** (the projector — always) |
| A spec-driven change lifecycle with artifact/evidence checks on normal transitions | **onto** (needs the binary) |
| A lightweight coding flow with one plan artifact and skill-led verification | **to** (needs its binary; can coexist with onto) |
| GitHub issue investigation/resolution, PR review/continuation, or batch review | **h** GitHub skills (installs onto and to dependencies) |
| A flexible, portable, prose-driven workflow with no binary gate | **Comet** / OpenSpec / Superpowers (external — not shipped by homonto) |

## Why onto is shipped but not self-used

Honesty matters here: **this repository is developed with no workflow stack
at all — not onto, not an external one.** onto is a
shipped-but-not-self-used product. That is a deliberate trade-off. The
maintainers work directly on branches — commits, tests, ADRs
([ADR 0023](adr/0023-develop-directly-without-comet.md)) — while onto's
mechanical checks serve teams who want evidence-backed transitions and a
reviewable handoff. Dogfooding onto here is
deferred to v1 ([ROADMAP](ROADMAP.md)).

The cost of not eating our own dog food is that onto misses the feedback
loop that hardened the projector. We offset it two ways, both intentional.
onto's correctness comes from a **full-lifecycle conformance test suite**
(it asserts the gates reject bad work, since no human catches it in daily
use), and from this page telling you plainly where onto fits, so "the
maintainers don't use this?" is an answered question, not a surprise.
