# Test the published reference set by pin

- **Status:** Accepted
- **Date:** 2026-11-06

## Context

M4.8 asks for an external extension set that installs through the public
`remote:` path, the same way a user's would. The `reference` E2E cell packed
`examples/reference-set/` into the image and installed it from `file://`
archives. That proved the package contract against homonto's own copy. It
did not prove anything about the separately published
`Novirmor/homonto-reference-set`, which had fallen behind: its manifests
lacked the `[compat]` line that M5 made required, and nothing failed.

## Decision

We will make the `reference` cell install the published release over https.
`examples/reference-set/published.toml` names its tag, commit, asset URLs and
content digests. The engine and tracker binaries run from the copies homonto
materialized for those digests.

We keep the in-repo copy as the offline fixture for the Go tests and the
authoring guide. `TestReferenceSetMatchesItsPublishedPins` requires its
content digest to equal the published pin for every package, which works
offline because the pin covers only paths, bytes and exec bits.
`task reference-published` checks the live assets.

## Consequences

- The cell needs network access to GitHub. It is not part of
  `scripts/gate.sh`, so the gate stays offline.
- Changing a reference package now takes three steps in one commit: change
  the copy, publish a new tag from the separate repository, and move
  `published.toml`. A change that skips the publish fails the Go tests.
- The published repository pins the homonto commit it tests against
  (`HOMONTO_REF`). A homonto change to the package contract has to be checked
  there before release (`extension/published` in the release checklist).
