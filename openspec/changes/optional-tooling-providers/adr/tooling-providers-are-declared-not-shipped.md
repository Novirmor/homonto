# Declare tooling providers; stop shipping their names

- **Status:** Proposed
- **Date:** 2026-07-26
- **Change:** optional-tooling-providers

## Context

ADR 0005 adopted `rtk` and `graphify` as onto's recommended tools. ADR 0008
downgraded them from halting requirements to warn-never-halt after a missing
tool proved able to block a whole workflow. Neither ADR removed their names
from the shipped prose, so the frameworks kept naming two specific third-party
tools across 11 catalog files.

That left two problems. A user who runs neither tool was warned about both on
every dispatch — noise ADR 0008 reduced but could not remove, because the
warning comes from prose that assumes the tools are wanted. And a user who
grounds with something else had no way to say so: the prose named `graphify`,
so any other provider was invisible to the workflow.

Tool choice is configuration. It was living in shipped content.

## Decision

We will declare tooling providers in config and render them at projection time.

A `[tooling]` table names a shell-proxy provider and a code-intelligence
provider, each from a closed set. `homonto apply` generates a
`references/tooling.md` inside each framework's dispatcher skill describing
exactly the declared pair; shipped `SKILL.md` files name no provider and defer
to that file, following ADR 0006's reference-file pattern.

We will default both keys to `none`. Declaring nothing means the preflight
names nothing and grounding falls back to direct file reading — already
ADR 0008's documented fallback. Naming a tool becomes opt-in, not opt-out.

We will reference providers, never vendor them. homonto does not download,
install, update, version-check, or execute a provider. ADR 0015 bars
third-party content from the catalog; the provider fragments are
homonto-authored prose describing how to use a tool the user installs
themselves. That is what makes adding `okf`
([okf-generator](https://github.com/UmairBaig8/okf-generator)) a catalog change
rather than a supply-chain commitment.

We will keep the value sets closed. `shell_proxy` accepts `rtk` or `none`;
`code_intel` accepts `graphify`, `okf`, or `none`. There is no provider plugin
API — adding a provider is a deliberate change to the closed set plus a
fragment.

This supersedes the tool-naming parts of ADR 0005 and ADR 0008. Both remain in
force otherwise; in particular ADR 0008's warn-never-halt rule is unchanged and
now applies to whichever providers the user declared.

## Consequences

- Existing configs keep loading unchanged, but the rendered preflight changes:
  anyone relying on the implicit `rtk` + `graphify` recommendation must declare
  it. The release notes carry the two-line snippet.
- Provider prose has one home (`catalog/tooling/`), shared by both frameworks,
  so onto and `to` cannot drift apart in what they say about a tool.
- The materialize gate needs a tooling component in its fingerprint, since
  editing `[tooling]` changes no catalog version and no resource byte. Without
  it the gate would serve a stale reference forever — the defect class the
  content fingerprint closed in v0.2.1.
- A test asserts no provider name appears in shipped catalog content outside
  `catalog/tooling/`, so the coupling cannot silently return.
- `doctor` gains a warning for a declared-but-undetected provider, probing
  `PATH` and index directories only so it can never hang on an external tool.
