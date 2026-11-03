# Publish the package contract

- **Status:** Accepted
- **Date:** 2026-11-03

## Context

Engines and workflows are packages with a `framework.toml`, and `local:` and
`remote:` packages already load. Nothing said what a package must provide.
`kind` was optional, a misspelled key was ignored, and the shared `homonto`
coordinator (ADR 0045) reached every package because each manifest declared
the same catalog path. An external package could not get the coordinator
without copying its bytes, and nothing stopped it from shipping a different
agent under that name.

## Decision

`manifest_schema = 2` is the public package contract, documented in
[`docs/contracts/`](../contracts/README.md):

- `kind` is required and is `engine`, `workflow` or `service`. Each kind has
  its own table (`[engine]`, `[workflow]`, `[service]`), and a manifest may
  carry only its own. Schema 2 manifests decode strictly, so an unknown key
  fails and names itself.
- An engine declares `lifecycle` (`stateless` or `native`; `native` names its
  `binary`), its `entry` skills, the task `operations` it supports (`start`
  and `resume` required) and its named `stages`.
- A workflow declares its `entry` commands. It invokes an engine only through
  the coordinator's engine selection. It never names an engine (ADR 0067).
- There is exactly one coordinator. A package asks for it with
  `coordinator = "shared"`, and the catalog adds the agent, skill and plugin
  from the base catalog. Declaring `homonto` (agent or skill) or
  `homonto-workflow` (plugin) yourself is rejected. An external workflow
  extends the coordinator only through its own skills and commands. It cannot
  replace or fork the coordinator.
- The contract version is `manifest_schema`. A newer schema fails closed
  (`upgrade homonto`). Schema 0/1 manifests still load as resource bundles
  without a contract. Nothing contract-driven accepts them: engine selection,
  services and settings.

Every builtin package uses schema 2 with no exception to the shape. The
builtin exceptions to individual rules are listed where they apply (ADRs 0093,
0094).

We rejected keeping the coordinator as an ordinary shared resource: the
byte-for-byte rule worked for bundled packages, but for an external package
it either forces copying a moving file or allows a second coordinator under
the shared name.

## Consequences

An external author has a written target and a clear error for every rule. Builtin manifests no longer
list the coordinator. A legacy unversioned package keeps projecting its
resources but cannot be selected as an engine or contribute services. Moving
it to schema 2 means adding `kind`, its kind's table and fixing any key strict
decoding now rejects.
