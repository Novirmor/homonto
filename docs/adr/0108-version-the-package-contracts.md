# Version the package contracts

- **Status:** Accepted
- **Date:** 2026-11-05

## Context

ADR 0092 made `manifest_schema = 2` the public package contract. That
number versions the manifest file format. It cannot tell an external author,
or a binary, which engine, workflow or service contract a package was
written against. A package's own `version` is the author's release number,
and `[compat].homonto` is a range of homonto releases, so it changes with
every release, not with the contract. To freeze the contracts for 1.0, each
kind needs a version that an author declares and a binary can refuse.

## Decision

- **One contract version per kind**, `MAJOR.MINOR`: engine, workflow and
  service are each at **1.0**. `catalog.Contracts` holds them.
  `homonto package contracts` and `docs/contracts/README.md` publish them.
- **Declared, never inferred.** A `manifest_schema = 2` package must set
  `[compat].contract`. A missing value fails at load and names the version
  to declare. homonto does not assume that a silent package means 1.0.
- **Fail closed on what the binary does not implement.** A newer minor, or
  a newer major, fails with "upgrade homonto". An older major fails with
  "update the package". Within a major, a lower minor loads unchanged: a
  minor only adds to the contract.
- **What a bump means.** A minor bump is an addition an existing package can
  ignore: a new optional manifest field, operation, capability or setting
  type. Anything that changes or removes the meaning of something a package
  already declares is a major bump.
- **The conformance suite is pinned.** `ConformanceSuiteContracts` names the
  versions `homonto package check` certifies. A test fails when it differs
  from `Contracts`, so the published version cannot move without the suite.
- **Four numbers, four questions.** Config `schema_version` covers the
  user's config, `manifest_schema` the manifest format, `[compat].contract`
  the kind's contract, and `[compat].homonto` the author's supported
  releases. None of them stands in for another.

## Consequences

Every schema 2 manifest needs one more line. That includes the shipped
packages, the examples, the in-repo reference set and the published
`homonto-reference-set`, which loads only once it declares the line too. The
change breaks packages written before it. That is acceptable before 1.0 and
is the reason to make it now.

The cost is discipline: a contract change must decide whether it is a minor
or a major, and update the contract docs, `Contracts` and the suite together.
A major bump strands every package written for the previous major until its
author updates it. The 1.x policy (ADR 0109) therefore allows no contract
major bump within 1.x.
