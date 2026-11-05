# Keep 1.x compatible within the major

- **Status:** Accepted
- **Date:** 2026-11-05
- **Partially supersedes:** [0081](0081-support-only-the-live-version.md)
  (within 1.x, "live" means every format a 1.x release wrote)

## Context

ADR 0081 supports only what the current binaries write. Every 0.x release
could change a format and tell users to finish in-flight work with the old
release. A 1.0 label promises more than that, but saying "stable" without
naming what is stable invites an implicit migration promise that no test
backs. The surfaces differ: a user's `homonto.toml`, persisted workflow
state, package manifests, the native files homonto projects, and the
harness versions it runs on each need their own answer.

## Decision

For every 1.x release, the promise covers 1.0 and every later 1.x release
before it:

| Surface | 1.x promise | Not promised |
|---|---|---|
| **Config** (`homonto.toml`, `schema_version` 0/1/2) | A config that loads under 1.N loads under 1.M (M > N) with the same meaning. New keys are optional additions. | An older binary reading a newer config. Strict decoding refuses it. |
| **Persisted state** (`.homonto/state*.json`, `onto-state.yaml`, `to-state.yaml`, workflow records, snapshots, handoff and evidence files) | A 1.x binary reads what any earlier 1.x binary wrote, including in-flight changes. A format change within 1.x adds a version that the newer binary reads beside the old one, and it may rewrite the file to the new version on its next write. | Reading a newer version with an older binary. It fails closed with "upgrade". Reading 0.x formats ADR 0081 already retired. |
| **Package contracts** (`manifest_schema`, `[compat].contract`, ADR 0108) | Contract majors stay at 1. Contract minors only add. A 1.0 package loads and conforms under every 1.x release. | A package that declares a minor newer than the binary. |
| **CLI and machine output** (command names, flags, `--output json` fields, exit codes) | Additions only. A field or flag is removed or changes meaning only in 2.0. | Human-readable text, which may change. |
| **Projected native files** (agent renders, hook commands, `opencode.jsonc`/`settings.json` keys homonto owns) | The ownership boundary: homonto changes only keys and files it owns, and plan shows every change. | Their content. Each release renders what it renders. |
| **Harness versions** | Each release names the harness versions it was verified on in its compatibility matrix (release checklist). | Any harness version the matrix does not name. A release may drop an old harness version, and the release notes say so. |

**Deprecation.** A key, flag, value or output field that 1.x intends to
retire is deprecated first. It keeps working, and `plan`/`doctor` warn and
name its replacement for at least one minor release. It is removed only in
2.0.

**No migrations across a major.** 2.0 may drop 1.x formats. When a format
is not supported, homonto fails at load naming the file, the version, and
the documented manual recovery. It never guesses. Within 1.x, a newer
binary reading an older format is a reader, not a migration tool: there is
no separate migrate command and no journal.

**Enforcement.** Frozen fixtures for each surface live in
`testdata/compat/<version>/`. `internal/compat` loads them through the
binary's own loaders and checks their meaning. A test failure there means
the change breaks 1.x, and the fixtures are never edited to make it pass.
Each 1.x release adds a fixture directory for any format version it
introduces.

## Consequences

Within 1.x, an upgrade no longer means finishing in-flight work first. The
cost is that every format change after 1.0 keeps an old reader alive until
2.0: the kind of code ADR 0081 deleted. Any 1.x change that needs a
breaking format has to wait for 2.0 or be redesigned as additive.

Pre-1.0 formats get nothing new. 0.x state that 0.36 could no longer read
stays unreadable, and in-flight 0.x work is still finished with its own
release. The policy starts at 1.0.

Harness drift is the weakest promise: OpenCode and Claude Code move on their
own schedules, and only the real-harness matrix tells us that a version still
works.
